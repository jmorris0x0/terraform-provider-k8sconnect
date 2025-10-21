package wait

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
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
			fmt.Sprintf("Failed to wait for %s: %s", formatObjectRef(wc.ObjectRef), err.Error()),
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
		// Resource was deleted outside Terraform
		tflog.Warn(ctx, "Resource no longer exists", map[string]interface{}{
			"error": err.Error(),
		})
		resp.State.RemoveResource(ctx)
		return
	}

	// For field waits, refresh result from current state (drift detection)
	// Condition/rollout waits have null result per ADR-008
	// Only refresh if connection is ready (all values known, not during bootstrap)
	if !wc.WaitConfig.Field.IsNull() && wc.WaitConfig.Field.ValueString() != "" {
		if r.isConnectionReady(data.ClusterConnection) {
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
			fmt.Sprintf("Failed to wait for %s: %s", formatObjectRef(wc.ObjectRef), err.Error()),
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
		Data:              data,
		Client:            client,
		GVR:               gvr,
		ObjectRef:         objRef,
		WaitConfig:        waitConfig,
		ClusterConnection: connModel,
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
func (r *waitResource) isConnectionReady(obj types.Object) bool {
	return auth.IsConnectionReady(obj)
}
