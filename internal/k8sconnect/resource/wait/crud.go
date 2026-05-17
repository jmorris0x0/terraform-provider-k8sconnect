package wait

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
)

// waitContext holds all the data needed for a wait operation
type waitContext struct {
	Data       *waitResourceModel
	Client     k8sclient.K8sClient
	GVR        schema.GroupVersionResource
	ObjectRef  objectRefModel
	WaitConfig waitForModel
	Cluster    auth.ClusterModel
}

func (r *waitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data waitResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Generate unique ID
	data.ID = types.StringValue(common.GenerateID())

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
			err.Error(),
		)
		return
	}

	tflog.Info(ctx, "Wait operation completed successfully")

	// Populate result if configured in wait_for (following ADR-008)
	if err := r.updateStatus(ctx, wc); err != nil {
		tflog.Warn(ctx, "Failed to populate result after wait", map[string]interface{}{"error": err.Error()})
		// Don't fail the entire operation - result is optional
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
		if errors.IsNotFound(err) {
			// Resource was deleted outside Terraform
			tflog.Warn(ctx, "Resource no longer exists", map[string]interface{}{
				"error": err.Error(),
			})
			resp.State.RemoveResource(ctx)
			return
		}
		// ADR-023: Degrade auth errors to warnings during Read
		if k8serrors.IsAuthError(err) {
			resp.Diagnostics.AddWarning(
				"Read: Using Prior State — Authentication Failed",
				fmt.Sprintf("Could not verify resource existence: authentication failed. "+
					"Using prior state. This typically means the stored token has expired "+
					"between Terraform runs. Details: %v", err),
			)
		} else {
			resp.Diagnostics.AddError(
				"Read: Failed to verify resource",
				fmt.Sprintf("Could not read resource from cluster: %v", err),
			)
		}
		return
	}

	// For field waits, refresh result from current state (drift detection)
	// Condition/rollout waits have null result per ADR-008
	// Only refresh if connection is ready (all values known, not during bootstrap)
	if !wc.WaitConfig.Field.IsNull() && wc.WaitConfig.Field.ValueString() != "" {
		if r.isConnectionReady(data.Cluster) {
			if err := r.updateStatus(ctx, wc); err != nil {
				tflog.Warn(ctx, "Failed to update result during Read", map[string]interface{}{
					"error": err.Error(),
				})
				// Don't fail - keep existing result on transient errors
			}
			tflog.Debug(ctx, "Refreshed result for field wait", map[string]interface{}{
				"field": wc.WaitConfig.Field.ValueString(),
			})
		} else {
			tflog.Debug(ctx, "Skipping result refresh - connection has unknown values (bootstrap)")
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
			err.Error(),
		)
		return
	}

	tflog.Info(ctx, "Wait operation completed successfully")

	// Populate result if configured in wait_for (following ADR-008)
	if err := r.updateStatus(ctx, wc); err != nil {
		tflog.Warn(ctx, "Failed to populate result after wait", map[string]interface{}{"error": err.Error()})
		// Don't fail the entire operation - result is optional
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

	// Parse cluster
	var connModel auth.ClusterModel
	diagsConn := data.Cluster.As(ctx, &connModel, basetypes.ObjectAsOptions{})
	diags.Append(diagsConn...)
	if diags.HasError() {
		return nil, diags
	}

	// Get K8s client
	client, err := r.clientGetter(connModel)
	if err != nil {
		diags.AddError(
			"Failed to Create Kubernetes Client",
			fmt.Sprintf("Could not create Kubernetes client for %s: %s", formatObjectRef(objRef), err.Error()),
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

	// Construct GVR from object_ref using discovery
	gvr, err := r.constructGVR(ctx, client, objRef)
	if err != nil {
		diags.AddError(
			"Failed to Discover GVR",
			fmt.Sprintf("Could not discover resource type for %s (apiVersion: %s): %s",
				formatObjectRef(objRef), objRef.APIVersion.ValueString(), err.Error()),
		)
		return nil, diags
	}

	return &waitContext{
		Data:       data,
		Client:     client,
		GVR:        gvr,
		ObjectRef:  objRef,
		WaitConfig: waitConfig,
		Cluster:    connModel,
	}, diags
}

// constructGVR builds a GroupVersionResource from object_ref using discovery
func (r *waitResource) constructGVR(ctx context.Context, client k8sclient.K8sClient, objRef objectRefModel) (schema.GroupVersionResource, error) {
	apiVersion := objRef.APIVersion.ValueString()
	kind := objRef.Kind.ValueString()

	// Use discovery to get the GVR - this works for all resources including CRDs with custom plurals
	gvr, err := client.DiscoverGVR(ctx, apiVersion, kind)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to discover resource type: %w", err)
	}

	return gvr, nil
}

// performWait executes the wait operation.
//
// If the object doesn't exist yet, performWait polls for its creation, bounded
// by the configured wait_for.timeout. This supports the common pattern of
// waiting on resources that are created lazily by operators (e.g., Stackgres
// creating Services, cert-manager creating Secrets, ALB controller creating
// ALBs). See issue #171.
func (r *waitResource) performWait(ctx context.Context, wc *waitContext) error {
	obj, err := r.waitForExistence(ctx, wc)
	if err != nil {
		return err
	}

	// Execute wait logic based on wait_for configuration
	return r.waitForResource(ctx, wc.Client, wc.GVR, obj, wc.WaitConfig)
}

// waitForExistence polls for the configured object to exist, bounded by the
// wait_for.timeout. Returns the object once it appears. Returns an error if
// the object never appears within the timeout, or if Get returns a non-NotFound
// error.
func (r *waitResource) waitForExistence(ctx context.Context, wc *waitContext) (*unstructured.Unstructured, error) {
	namespace := wc.ObjectRef.Namespace.ValueString()
	name := wc.ObjectRef.Name.ValueString()

	// Fast path: object already exists
	obj, err := wc.Client.Get(ctx, wc.GVR, namespace, name)
	if err == nil {
		return obj, nil
	}
	if !errors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get resource: %w", err)
	}

	// Object doesn't exist yet. Poll for its creation, bounded by the wait timeout.
	timeout := parseTimeout(wc.WaitConfig.Timeout)

	tflog.Info(ctx, "Resource not found, polling for existence", map[string]interface{}{
		"kind":      wc.ObjectRef.Kind.ValueString(),
		"name":      name,
		"namespace": namespace,
		"timeout":   timeout.String(),
	})

	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	const pollInterval = 2 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			// Distinguish wait timeout from caller cancellation
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			resourceDesc := fmt.Sprintf("%s %q", wc.ObjectRef.Kind.ValueString(), name)
			if namespace != "" {
				resourceDesc = fmt.Sprintf("%s (namespace: %q)", resourceDesc, namespace)
			}
			return nil, fmt.Errorf("%s did not appear within %s.\n\n"+
				"k8sconnect_wait polls for the referenced resource to be created, but it never appeared.\n\n"+
				"Possible causes:\n"+
				"1. The resource was never created (typo in name/namespace, or the operator that creates it never ran)\n"+
				"2. The resource creation is slow; increase wait_for.timeout\n"+
				"3. The cluster connection or permissions are wrong",
				resourceDesc, timeout)

		case <-ticker.C:
			obj, err := wc.Client.Get(ctx, wc.GVR, namespace, name)
			if err == nil {
				tflog.Info(ctx, "Resource appeared, proceeding with wait", map[string]interface{}{
					"kind":      wc.ObjectRef.Kind.ValueString(),
					"name":      name,
					"namespace": namespace,
				})
				return obj, nil
			}
			if !errors.IsNotFound(err) {
				return nil, fmt.Errorf("failed to get resource while polling for existence: %w", err)
			}
			// Still not found, keep polling
		}
	}
}

// parseTimeout reads wait_for.timeout, falling back to 10m if unset or invalid.
// Mirrors the parsing in waitForResource so existence polling honors the same
// timeout the user configured for the wait condition.
func parseTimeout(t types.String) time.Duration {
	const defaultTimeout = 10 * time.Minute
	if t.IsNull() || t.ValueString() == "" {
		return defaultTimeout
	}
	d, err := time.ParseDuration(t.ValueString())
	if err != nil {
		return defaultTimeout
	}
	return d
}

// waitForResource is implemented in wait_logic.go

// isConnectionReady checks if the connection has all values known (not unknown)
// This determines if we can attempt to contact the cluster for status refresh.
func (r *waitResource) isConnectionReady(obj types.Object) bool {
	return auth.IsConnectionReady(obj)
}
