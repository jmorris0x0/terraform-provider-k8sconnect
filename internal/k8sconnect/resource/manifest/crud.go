// internal/k8sconnect/resource/manifest/crud.go
package manifest

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	fmt.Printf("=== ACTUAL CREATE FUNCTION CALLED ===\n")

	// 1. Setup and extract plan data
	var data manifestResourceModel
	diags := req.Plan.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 2. Generate resource ID
	data.ID = types.StringValue(r.generateID())

	// 3. Setup context (no more pipeline!)
	rc, err := r.prepareContext(ctx, &data, false)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// 4. Set ownership annotation
	r.setOwnershipAnnotation(rc.Object, data.ID.ValueString())

	// 5. Check if resource exists and verify ownership
	if err := r.checkResourceExistenceAndOwnership(ctx, rc, &data, resp); err != nil {
		return
	}

	// 6. Apply the resource
	if err := r.applyResourceWithConflictHandling(ctx, rc, rc.Data, resp, "Create"); err != nil {
		return
	}

	// 7. Phase 2 - Read back to get managedFields
	r.readResourceAfterCreate(ctx, rc)

	// 8. Execute wait conditions
	waited := r.handleWaitExecution(ctx, rc, resp, "created")

	// 9. Update status field
	fmt.Printf("Status BEFORE UpdateStatus - IsNull: %v, IsUnknown: %v\n",
		rc.Data.Status.IsNull(), rc.Data.Status.IsUnknown())

	if err := r.updateStatus(rc, waited); err != nil {
		tflog.Warn(ctx, "Failed to update status", map[string]interface{}{"error": err.Error()})
	}

	fmt.Printf("Status AFTER UpdateStatus - IsNull: %v, IsUnknown: %v\n",
		rc.Data.Status.IsNull(), rc.Data.Status.IsUnknown())

	// 10. Update projection
	if err := r.updateProjection(rc); err != nil {
		resp.Diagnostics.AddWarning("Projection Update Failed",
			fmt.Sprintf("Resource created but projection update failed: %s", err))
	}

	// 11. Save state
	fmt.Printf("FINAL Status before State.Set - IsNull: %v, IsUnknown: %v\n",
		rc.Data.Status.IsNull(), rc.Data.Status.IsUnknown())
	fmt.Printf("=== END Create ===\n\n")

	diags = resp.State.Set(ctx, rc.Data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// 1. Setup and extract state data
	var data manifestResourceModel
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 2. Setup context (no more pipeline!)
	rc, err := r.prepareContext(ctx, &data, false)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// 3. Read current state from Kubernetes
	currentObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			// Resource was deleted outside Terraform
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read Failed",
			fmt.Sprintf("Failed to read %s: %s", rc.Object.GetKind(), err))
		return
	}

	// 4. Check ownership
	if err := r.verifyOwnership(currentObj, data.ID.ValueString(), rc.Object, resp); err != nil {
		return
	}

	// 5. Update projection
	if err := r.updateProjectionFromCurrent(ctx, &data, currentObj, rc.Object); err != nil {
		resp.Diagnostics.AddError("Projection Failed",
			fmt.Sprintf("Failed to project managed fields: %s", err))
		return
	}

	// 6. Update field ownership
	r.updateFieldOwnershipData(ctx, &data, currentObj)

	// 7. Save refreshed state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	fmt.Printf("=== ACTUAL UPDATE FUNCTION CALLED ===\n")

	// 1. Setup and extract state/plan data
	var state, plan manifestResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	diags = req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 2. Setup context (no more pipeline!)
	rc, err := r.prepareContext(ctx, &plan, false)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// 3. Preserve ID and set ownership
	plan.ID = state.ID
	r.setOwnershipAnnotation(rc.Object, plan.ID.ValueString())

	// 4. Apply the updated resource
	if err := r.applyResourceWithConflictHandling(ctx, rc, rc.Data, resp, "Update"); err != nil {
		return
	}

	tflog.Info(ctx, "Resource updated", map[string]interface{}{
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})

	// 5. Execute wait conditions
	waited := r.handleWaitExecution(ctx, rc, resp, "updated")

	// 6. Update status
	r.updateStatus(rc, waited)

	// 7. Update projection
	if err := r.updateProjection(rc); err != nil {
		tflog.Warn(ctx, "Failed to update projection", map[string]interface{}{"error": err.Error()})
	}

	// 8. Handle status transitions
	if !state.Status.IsNull() && plan.WaitFor.IsNull() {
		plan.Status = types.DynamicNull()
		tflog.Info(ctx, "Clearing status - wait_for was removed")
	}

	// 9. Save updated state
	diags = resp.State.Set(ctx, &plan)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// 1. Setup and extract state data
	var data manifestResourceModel
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 2. Check delete protection
	if !data.DeleteProtection.IsNull() && data.DeleteProtection.ValueBool() {
		resp.Diagnostics.AddError(
			"Delete Protection Enabled",
			"This resource has delete protection enabled. Set delete_protection = false to allow deletion.",
		)
		return
	}

	// 3. Setup context (no more pipeline!)
	rc, err := r.prepareContext(ctx, &data, true)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// 4. Get delete options
	timeout := r.getDeleteTimeout(data)
	forceDestroy := false
	if !data.ForceDestroy.IsNull() {
		forceDestroy = data.ForceDestroy.ValueBool()
	}

	// 5. Check if resource exists
	_, err = rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			tflog.Info(ctx, "Resource already deleted")
			return
		}
		resp.Diagnostics.AddError("Failed to check resource", err.Error())
		return
	}

	// 6. Attempt normal deletion
	err = rc.Client.Delete(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName(), k8sclient.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		resp.Diagnostics.AddError("Deletion Failed", err.Error())
		return
	}

	// 7. Wait for deletion with timeout
	err = r.waitForDeletion(ctx, rc.Client, rc.GVR, rc.Object, timeout)
	if err != nil {
		if forceDestroy {
			// Normal deletion timed out, NOW try force
			tflog.Info(ctx, "Normal deletion timed out, attempting force destroy", map[string]interface{}{
				"timeout":  timeout,
				"resource": fmt.Sprintf("%s/%s", rc.Object.GetKind(), rc.Object.GetName()),
			})

			if err := r.forceDestroy(ctx, rc.Client, rc.GVR, rc.Object, resp); err != nil {
				tflog.Warn(ctx, "Force destroy encountered issues", map[string]interface{}{"error": err.Error()})
				// Don't fail - resource might already be gone
			}
		} else {
			// No force_destroy, show helpful error message
			r.handleDeletionTimeout(resp, rc.Client, rc.GVR, rc.Object, timeout, err)
			return
		}
	}

	// 8. Log successful deletion
	tflog.Info(ctx, "Resource deleted", map[string]interface{}{
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})
}
