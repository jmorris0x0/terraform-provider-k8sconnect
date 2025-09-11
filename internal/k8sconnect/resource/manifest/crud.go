// internal/k8sconnect/resource/manifest/crud.go
package manifest

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data manifestResourceModel

	diags := req.Plan.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Use pipeline for common setup
	pipeline := NewOperationPipeline(r)
	rc, err := pipeline.PrepareContext(ctx, &data, true)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// Check if resource already exists
	existing, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err == nil && existing != nil {
		resp.Diagnostics.AddError(
			"Resource Already Exists",
			fmt.Sprintf("%s %s/%s already exists in namespace %s",
				rc.Object.GetKind(),
				rc.Object.GetAPIVersion(),
				rc.Object.GetName(),
				rc.Object.GetNamespace()),
		)
		return
	}

	// Generate ID and set ownership
	data.ID = types.StringValue(r.generateID())
	r.setOwnershipAnnotation(rc.Object, data.ID.ValueString())

	// Apply with Force=false
	err = rc.Client.Apply(ctx, rc.Object, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        false,
	})
	if err != nil {
		resp.Diagnostics.AddError("Creation Failed",
			fmt.Sprintf("Failed to create %s: %s", rc.Object.GetKind(), err))
		return
	}

	tflog.Info(ctx, "Resource created", map[string]interface{}{
		"id":        data.ID.ValueString(),
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})

	// Handle wait conditions
	waited := false
	if err := pipeline.ExecuteWait(rc); err != nil {
		resp.Diagnostics.AddWarning("Wait Failed",
			fmt.Sprintf("Resource created but wait failed: %s", err))
		waited = true
	} else if !rc.Data.WaitFor.IsNull() {
		var waitConfig waitForModel
		diags := rc.Data.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
		if !diags.HasError() && pipeline.hasActiveWaitConditions(waitConfig) {
			waited = true
		}
	}

	pipeline.UpdateStatus(rc, waited)

	if err := pipeline.UpdateProjection(rc); err != nil {
		tflog.Warn(ctx, "Failed to update projection", map[string]interface{}{"error": err.Error()})
	}

	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data manifestResourceModel

	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Use pipeline for setup
	pipeline := NewOperationPipeline(r)
	rc, err := pipeline.PrepareContext(ctx, &data, false)
	if err != nil {
		tflog.Info(ctx, "Skipping Read", map[string]interface{}{"reason": err.Error()})
		return
	}

	// Check if resource exists
	currentObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			tflog.Info(ctx, "Resource not found, removing from state")
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read Failed", err.Error())
		return
	}

	// Update YAML with current state
	yamlBytes, err := r.objectToYAML(currentObj)
	if err != nil {
		resp.Diagnostics.AddError("YAML Conversion Failed", err.Error())
		return
	}
	data.YAMLBody = types.StringValue(string(yamlBytes))

	// For Read, we should NOT update projection/ownership - just preserve what's in state
	// The projection and field ownership should only change during Create/Update operations

	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var state, plan manifestResourceModel

	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	diags = req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Validate connection change
	if r.connResolver != nil && !state.ClusterConnection.Equal(plan.ClusterConnection) {
		oldConn, err := r.convertObjectToConnectionModel(ctx, state.ClusterConnection)
		if err != nil {
			resp.Diagnostics.AddError("Failed to parse old connection", err.Error())
			return
		}

		newConn, err := r.convertObjectToConnectionModel(ctx, plan.ClusterConnection)
		if err != nil {
			resp.Diagnostics.AddError("Failed to parse new connection", err.Error())
			return
		}

		if err := r.connResolver.ValidateConnectionChange(oldConn, newConn); err != nil {
			resp.Diagnostics.AddError("Connection Change Blocked", err.Error())
			return
		}
	}

	// Use pipeline for setup
	pipeline := NewOperationPipeline(r)
	rc, err := pipeline.PrepareContext(ctx, &plan, true)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// Preserve ID and ownership
	plan.ID = state.ID
	r.setOwnershipAnnotation(rc.Object, plan.ID.ValueString())

	// Check force conflicts
	forceConflicts := false
	if !plan.ForceConflicts.IsNull() {
		forceConflicts = plan.ForceConflicts.ValueBool()
	}

	// Apply the update
	err = rc.Client.Apply(ctx, rc.Object, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        forceConflicts,
	})
	if err != nil {
		if isFieldConflictError(err) {
			resp.Diagnostics.AddError("Field Manager Conflict",
				"Another controller owns fields you're trying to modify. "+
					"Set force_conflicts = true to override.")
		} else {
			resp.Diagnostics.AddError("Update Failed", err.Error())
		}
		return
	}

	tflog.Info(ctx, "Resource updated", map[string]interface{}{
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})

	// Handle wait conditions
	waited := false
	if err := pipeline.ExecuteWait(rc); err != nil {
		resp.Diagnostics.AddWarning("Wait Failed",
			fmt.Sprintf("Resource updated but wait failed: %s", err))
		waited = true
	} else if !rc.Data.WaitFor.IsNull() {
		var waitConfig waitForModel
		diags := rc.Data.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
		if !diags.HasError() && pipeline.hasActiveWaitConditions(waitConfig) {
			waited = true
		}
	}

	pipeline.UpdateStatus(rc, waited)

	if err := pipeline.UpdateProjection(rc); err != nil {
		tflog.Warn(ctx, "Failed to update projection", map[string]interface{}{"error": err.Error()})
	}

	// Handle status transitions
	if !state.Status.IsNull() && plan.WaitFor.IsNull() {
		plan.Status = types.DynamicNull()
		tflog.Info(ctx, "Clearing status - wait_for was removed")
	}

	diags = resp.State.Set(ctx, &plan)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data manifestResourceModel

	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check delete protection
	if !data.DeleteProtection.IsNull() && data.DeleteProtection.ValueBool() {
		resp.Diagnostics.AddError(
			"Delete Protection Enabled",
			"This resource has delete protection enabled. Set delete_protection = false to allow deletion.",
		)
		return
	}

	// Use pipeline for setup
	pipeline := NewOperationPipeline(r)
	rc, err := pipeline.PrepareContext(ctx, &data, true)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// Get delete options
	timeout := r.getDeleteTimeout(data)
	forceDestroy := false
	if !data.ForceDestroy.IsNull() {
		forceDestroy = data.ForceDestroy.ValueBool()
	}

	// Check if resource exists
	_, err = rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			tflog.Info(ctx, "Resource already deleted")
			return
		}
		resp.Diagnostics.AddError("Failed to check resource", err.Error())
		return
	}

	// Delete the resource
	err = rc.Client.Delete(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName(), k8sclient.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		resp.Diagnostics.AddError("Deletion Failed", err.Error())
		return
	}

	// Wait for deletion if not force destroy
	if !forceDestroy {
		err = r.waitForDeletion(ctx, rc.Client, rc.GVR, rc.Object, timeout)
		if err != nil {
			r.handleDeletionTimeout(resp, rc.Client, rc.GVR, rc.Object, timeout, err)
			return
		}
	} else {
		// Force destroy - remove finalizers
		if err := r.forceDestroy(ctx, rc.Client, rc.GVR, rc.Object, resp); err != nil {
			resp.Diagnostics.AddError("Force Destroy Failed", err.Error())
			return
		}
	}
	if err != nil {
		if errors.IsNotFound(err) {
			tflog.Info(ctx, "Resource not found, treating as deleted")
			return
		}
		resp.Diagnostics.AddError("Deletion Failed", err.Error())
		return
	}

	tflog.Info(ctx, "Resource deleted", map[string]interface{}{
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})
}
