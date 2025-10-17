// internal/k8sconnect/resource/wait/crud.go
package wait

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

// waitContext holds all the data needed for a wait operation
type waitContext struct {
	Data              *waitResourceModel
	Client            k8sclient.K8sClient
	GVR               schema.GroupVersionResource
	ObjectRef         objectRefModel
	WaitConfig        waitForModel
	ClusterConnection auth.ClusterConnectionModel
}

func (r *waitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data waitResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Generate unique ID
	data.ID = types.StringValue(uuid.New().String())

	// Build wait context
	wc, diags := r.buildWaitContext(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "Starting wait operation", map[string]interface{}{
		"api_version": wc.ObjectRef.APIVersion.ValueString(),
		"kind":        wc.ObjectRef.Kind.ValueString(),
		"name":        wc.ObjectRef.Name.ValueString(),
		"namespace":   wc.ObjectRef.Namespace.ValueString(),
	})

	// Perform the wait operation
	if err := r.performWait(ctx, wc); err != nil {
		resp.Diagnostics.AddError(
			"Wait Operation Failed",
			fmt.Sprintf("Failed to wait for resource: %s", err.Error()),
		)
		return
	}

	tflog.Info(ctx, "Wait operation completed successfully")

	// Populate status if configured in wait_for (following ADR-008)
	if err := r.updateStatus(ctx, wc); err != nil {
		tflog.Warn(ctx, "Failed to populate status after wait", map[string]interface{}{"error": err.Error()})
		// Don't fail the entire operation - status is optional
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *waitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data waitResourceModel

	// Read Terraform state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Build wait context
	wc, diags := r.buildWaitContext(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Verify the resource still exists
	_, err := wc.Client.Get(ctx, wc.GVR, wc.ObjectRef.Namespace.ValueString(), wc.ObjectRef.Name.ValueString())
	if err != nil {
		// Resource was deleted outside Terraform
		tflog.Warn(ctx, "Resource no longer exists", map[string]interface{}{
			"error": err.Error(),
		})
		resp.State.RemoveResource(ctx)
		return
	}

	// For field waits, refresh status from current state (drift detection)
	// Condition/rollout waits have null status per ADR-008
	// Only refresh if connection is ready (all values known, not during bootstrap)
	if !wc.WaitConfig.Field.IsNull() && wc.WaitConfig.Field.ValueString() != "" {
		if r.isConnectionReady(data.ClusterConnection) {
			if err := r.updateStatus(ctx, wc); err != nil {
				tflog.Warn(ctx, "Failed to update status during Read", map[string]interface{}{
					"error": err.Error(),
				})
				// Don't fail - keep existing status on transient errors
			}
			tflog.Debug(ctx, "Refreshed status for field wait", map[string]interface{}{
				"field": wc.WaitConfig.Field.ValueString(),
			})
		} else {
			tflog.Debug(ctx, "Skipping status refresh - connection has unknown values (bootstrap)")
		}
	}

	// Save potentially updated state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *waitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data waitResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Build wait context
	wc, diags := r.buildWaitContext(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "Re-performing wait operation after configuration change")

	// Re-perform the wait operation with new configuration
	if err := r.performWait(ctx, wc); err != nil {
		resp.Diagnostics.AddError(
			"Wait Operation Failed",
			fmt.Sprintf("Failed to wait for resource: %s", err.Error()),
		)
		return
	}

	tflog.Info(ctx, "Wait operation completed successfully")

	// Populate status if configured in wait_for (following ADR-008)
	if err := r.updateStatus(ctx, wc); err != nil {
		tflog.Warn(ctx, "Failed to populate status after wait", map[string]interface{}{"error": err.Error()})
		// Don't fail the entire operation - status is optional
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// buildWaitContext constructs a waitContext from the resource model
func (r *waitResource) buildWaitContext(ctx context.Context, data *waitResourceModel) (*waitContext, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Parse object_ref
	var objRef objectRefModel
	diagsObjRef := data.ObjectRef.As(ctx, &objRef, basetypes.ObjectAsOptions{})
	diags.Append(diagsObjRef...)
	if diags.HasError() {
		return nil, diags
	}

	// Parse cluster_connection
	var connModel auth.ClusterConnectionModel
	diagsConn := data.ClusterConnection.As(ctx, &connModel, basetypes.ObjectAsOptions{})
	diags.Append(diagsConn...)
	if diags.HasError() {
		return nil, diags
	}

	// Get K8s client
	client, err := r.clientGetter(connModel)
	if err != nil {
		diags.AddError(
			"Failed to Create Kubernetes Client",
			fmt.Sprintf("Could not create Kubernetes client: %s", err.Error()),
		)
		return nil, diags
	}

	// Parse wait_for configuration
	var waitConfig waitForModel
	diagsWait := data.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
	diags.Append(diagsWait...)
	if diags.HasError() {
		return nil, diags
	}

	// Construct GVR from object_ref
	gvr, err := r.constructGVR(ctx, objRef)
	if err != nil {
		diags.AddError(
			"Failed to Construct GVR",
			fmt.Sprintf("Could not determine resource type: %s", err.Error()),
		)
		return nil, diags
	}

	return &waitContext{
		Data:              data,
		Client:            client,
		GVR:               gvr,
		ObjectRef:         objRef,
		WaitConfig:        waitConfig,
		ClusterConnection: connModel,
	}, diags
}

// constructGVR builds a GroupVersionResource from object_ref
func (r *waitResource) constructGVR(ctx context.Context, objRef objectRefModel) (schema.GroupVersionResource, error) {
	apiVersion := objRef.APIVersion.ValueString()
	kind := objRef.Kind.ValueString()

	// Parse group and version from apiVersion
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("invalid api_version %q: %w", apiVersion, err)
	}

	// Convert kind to resource name (lowercase, pluralized)
	resource := r.kindToResource(kind)

	return schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resource,
	}, nil
}

// kindToResource converts a Kubernetes Kind to its resource name
// This is a simplified version - a proper implementation would use RESTMapper
func (r *waitResource) kindToResource(kind string) string {
	// Common Kubernetes resource mappings
	resourceMap := map[string]string{
		"Namespace":             "namespaces",
		"Pod":                   "pods",
		"Service":               "services",
		"Deployment":            "deployments",
		"StatefulSet":           "statefulsets",
		"DaemonSet":             "daemonsets",
		"Job":                   "jobs",
		"CronJob":               "cronjobs",
		"ConfigMap":             "configmaps",
		"Secret":                "secrets",
		"ServiceAccount":        "serviceaccounts",
		"PersistentVolume":      "persistentvolumes",
		"PersistentVolumeClaim": "persistentvolumeclaims",
		"Ingress":               "ingresses",
		"NetworkPolicy":         "networkpolicies",
	}

	if resource, ok := resourceMap[kind]; ok {
		return resource
	}

	// Default: lowercase + 's'
	return strings.ToLower(kind) + "s"
}

// performWait executes the wait operation
func (r *waitResource) performWait(ctx context.Context, wc *waitContext) error {
	// Get the current object to verify it exists
	obj, err := wc.Client.Get(ctx, wc.GVR, wc.ObjectRef.Namespace.ValueString(), wc.ObjectRef.Name.ValueString())
	if err != nil {
		return fmt.Errorf("failed to get resource: %w", err)
	}

	// Execute wait logic based on wait_for configuration
	return r.waitForResource(ctx, wc.Client, wc.GVR, obj, wc.WaitConfig)
}

// waitForResource is implemented in wait_logic.go

// isConnectionReady checks if the connection has all values known (not unknown)
// This determines if we can attempt to contact the cluster for status refresh.
// Null values are OK (means not using that auth method), but unknown values
// (like "known after apply" during bootstrap) mean we cannot connect yet.
func (r *waitResource) isConnectionReady(obj types.Object) bool {
	// First check if the object itself is null/unknown
	if obj.IsNull() || obj.IsUnknown() {
		return false
	}

	// Convert to connection model to check individual fields
	conn, err := auth.ObjectToConnectionModel(context.Background(), obj)
	if err != nil {
		return false
	}

	// Check all string fields - null is OK, unknown is not
	if conn.Host.IsUnknown() ||
		conn.ClusterCACertificate.IsUnknown() ||
		conn.Kubeconfig.IsUnknown() ||
		conn.Context.IsUnknown() ||
		conn.Token.IsUnknown() ||
		conn.ClientCertificate.IsUnknown() ||
		conn.ClientKey.IsUnknown() ||
		conn.ProxyURL.IsUnknown() {
		return false
	}

	// Check bool field
	if conn.Insecure.IsUnknown() {
		return false
	}

	// Check exec auth if present
	if conn.Exec != nil {
		if conn.Exec.APIVersion.IsUnknown() ||
			conn.Exec.Command.IsUnknown() {
			return false
		}

		// Check args array
		for _, arg := range conn.Exec.Args {
			if arg.IsUnknown() {
				return false
			}
		}

		// Check env vars map
		if conn.Exec.Env != nil {
			for _, value := range conn.Exec.Env {
				if value.IsUnknown() {
					return false
				}
			}
		}
	}

	// All fields are known (or null) - connection is ready
	return true
}
