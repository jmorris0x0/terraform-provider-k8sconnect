package object

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

func (r *objectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// 1. Setup and extract plan data
	var data objectResourceModel
	diags := req.Plan.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 2. Generate resource ID
	data.ID = types.StringValue(common.GenerateID())

	// 3. Setup context
	rc, err := r.prepareContext(ctx, &data, false, false)
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

	// 6a. Surface any API warnings from apply operation
	surfaceK8sWarnings(ctx, rc.Client, rc.Object, &resp.Diagnostics)

	// 7. Phase 2 - Read back to get managedFields
	r.readResourceAfterCreate(ctx, rc)

	// 7a. Surface any API warnings from read operation
	surfaceK8sWarnings(ctx, rc.Client, rc.Object, &resp.Diagnostics)

	// 8. Update projection BEFORE state save
	if err := r.updateProjection(rc); err != nil {
		// Projection failed - save state with recovery flag (ADR-006)
		handleProjectionFailure(ctx, rc, resp.Private, &resp.State, &resp.Diagnostics, "created", err)
		return
	}

	// 8a. Populate object_ref output
	if err := r.populateObjectRef(ctx, rc); err != nil {
		resp.Diagnostics.AddError("Failed to populate object_ref",
			fmt.Sprintf("Failed to populate object_ref for %s: %s", formatResource(rc.Object), err.Error()))
		return
	}

	// 8b. Update field_ownership attribute in state
	updateFieldOwnershipData(ctx, rc.Data, rc.Object)

	// 8c. Save ownership baseline to private state for drift detection (ADR-021)
	ignoreFields := getIgnoreFields(ctx, rc.Data)
	saveOwnershipBaseline(ctx, resp.Private, rc.Object, ignoreFields)

	// 9. SAVE STATE after successful creation
	diags = resp.State.Set(ctx, rc.Data)
	resp.Diagnostics.Append(diags...)
}

func (r *objectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// 1. Setup and extract state data
	var data objectResourceModel
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 1a. Check for pending projection (opportunistic recovery - ADR-006)
	hasPendingProjection := checkPendingProjectionFlag(ctx, req.Private)
	if hasPendingProjection {
		tflog.Info(ctx, "Detected pending projection during refresh, will attempt recovery")
	}

	// 2. Setup context
	rc, err := r.prepareContext(ctx, &data, false, false)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// 2a. If GVR is empty, the resource type is not discoverable (e.g., CRD was deleted)
	// Treat this the same as resource not found - remove from state
	if rc.GVR.Empty() {
		tflog.Info(ctx, "Resource type not discoverable during read, treating as deleted", map[string]interface{}{
			"kind":      rc.Object.GetKind(),
			"name":      rc.Object.GetName(),
			"namespace": rc.Object.GetNamespace(),
		})
		resp.State.RemoveResource(ctx)
		return
	}

	// 3. Read current state from Kubernetes
	currentObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			// Resource was deleted outside Terraform
			resp.State.RemoveResource(ctx)
			return
		}
		resourceDesc := fmt.Sprintf("%s %s", rc.Object.GetKind(), rc.Object.GetName())
		severity, title, detail := r.classifyK8sError(err, "Read", resourceDesc)
		if severity == "warning" {
			resp.Diagnostics.AddWarning(title, detail)
		} else {
			resp.Diagnostics.AddError(title, detail)
		}
		return
	}

	// 3a. Surface any API warnings from read operation
	surfaceK8sWarnings(ctx, rc.Client, rc.Object, &resp.Diagnostics)

	// 4. Check ownership (skip if just imported without annotations)
	// When a resource is imported without k8sconnect annotations, we skip the ownership
	// check until Update adds the annotations. The flag is cleared by Update after applying.
	if !checkImportedWithoutAnnotationsFlag(ctx, req.Private) {
		if err := r.verifyOwnership(currentObj, data.ID.ValueString(), rc.Object, resp); err != nil {
			return
		}
	} else {
		// Don't clear the flag here - let Update clear it after adding annotations
		// This way, multiple Read calls during import verification all skip the ownership check
		tflog.Debug(ctx, "Skipped ownership verification for imported resource without annotations")
	}

	// 5. Update projection (with opportunistic recovery)
	if err := r.updateProjectionFromCurrent(ctx, &data, currentObj, rc.Object); err != nil {
		// If we had a pending projection, keep the flag and continue (don't fail refresh)
		if hasPendingProjection {
			tflog.Warn(ctx, "Projection still failing during refresh, keeping pending flag", map[string]interface{}{
				"error": err.Error(),
			})
			emptyMap, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{})
			data.ManagedStateProjection = emptyMap
			data.FieldOwnership = emptyMap
			setPendingProjectionFlag(ctx, resp.Private)
		} else {
			resp.Diagnostics.AddError("Projection Failed",
				fmt.Sprintf("Failed to project managed fields for %s: %s", formatResource(rc.Object), err))
			return
		}
	} else {
		// Projection succeeded - clear pending flag if it was set
		handleProjectionSuccess(ctx, hasPendingProjection, resp.Private, "during refresh")
	}

	// 6. Update field ownership
	updateFieldOwnershipData(ctx, &data, currentObj)

	// 7. Save refreshed state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *objectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// 1. Setup and extract state/plan data
	var state, plan objectResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	diags = req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 1a. Check for pending projection from previous failed apply (ADR-006)
	hasPendingProjection := checkPendingProjectionFlag(ctx, req.Private)
	if hasPendingProjection {
		tflog.Info(ctx, "Detected pending projection from previous apply, will retry")
	}

	// 2. Setup context
	rc, err := r.prepareContext(ctx, &plan, false, false)
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

	// 4a. Surface any API warnings from apply operation
	surfaceK8sWarnings(ctx, rc.Client, rc.Object, &resp.Diagnostics)

	tflog.Info(ctx, "Resource updated", map[string]interface{}{
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})

	// 4b. Fetch fresh object with updated managedFields after apply
	updatedObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read resource after update",
			fmt.Sprintf("Failed to read %s after update: %s", formatResource(rc.Object), err.Error()))
		return
	}
	rc.Object = updatedObj
	tflog.Debug(ctx, "Fetched fresh object after update for managedFields", map[string]interface{}{
		"has_managed_fields": len(rc.Object.GetManagedFields()) > 0,
	})

	// 5. Update projection (with recovery logic - ADR-006)
	if err := r.updateProjection(rc); err != nil {
		handleProjectionFailure(ctx, rc, resp.Private, &resp.State, &resp.Diagnostics, "updated", err)
		return
	}

	// Projection succeeded - clear pending flag if it was set
	handleProjectionSuccess(ctx, hasPendingProjection, resp.Private, "from previous apply")

	// 6. Populate object_ref output
	if err := r.populateObjectRef(ctx, rc); err != nil {
		resp.Diagnostics.AddError("Failed to populate object_ref",
			fmt.Sprintf("Failed to populate object_ref for %s: %s", formatResource(rc.Object), err.Error()))
		return
	}

	// 7. Clear ImportedWithoutAnnotations flag after first update
	if checkImportedWithoutAnnotationsFlag(ctx, req.Private) {
		clearImportedWithoutAnnotationsFlag(ctx, resp.Private)
	}

	// 7. Update field_ownership attribute in state
	updateFieldOwnershipData(ctx, &plan, rc.Object)

	// 7b. Save ownership baseline to private state for drift detection (ADR-021)
	ignoreFields := getIgnoreFields(ctx, &plan)
	saveOwnershipBaseline(ctx, resp.Private, rc.Object, ignoreFields)

	// 8. Save updated state
	diags = resp.State.Set(ctx, &plan)
	resp.Diagnostics.Append(diags...)
}

func (r *objectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// 1. Setup and extract state data
	var data objectResourceModel
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

	// 3. Setup context
	rc, err := r.prepareContext(ctx, &data, true, true)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// 3a. If GVR discovery failed (e.g., CRD deleted before CR), assume already deleted
	if rc.GVR.Empty() {
		tflog.Info(ctx, "Resource type no longer discoverable, assuming already deleted", map[string]interface{}{
			"kind":      rc.Object.GetKind(),
			"name":      rc.Object.GetName(),
			"namespace": rc.Object.GetNamespace(),
		})
		return
	}

	// 4. Get delete options
	timeout := r.getDeleteTimeout(data)
	forceDestroy := false
	if !data.ForceDestroy.IsNull() {
		forceDestroy = data.ForceDestroy.ValueBool()
	}

	// 5. Check if resource exists and verify ownership
	liveObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			tflog.Info(ctx, "Resource already deleted")
			return
		}
		resourceDesc := fmt.Sprintf("%s %s", rc.Object.GetKind(), rc.Object.GetName())
		severity, title, detail := r.classifyK8sError(err, "Delete", resourceDesc)
		if severity == "warning" {
			resp.Diagnostics.AddWarning(title, detail)
		} else {
			resp.Diagnostics.AddError(title, detail)
		}
		return
	}

	// 5a. Verify ownership - if resource has different terraform-id, it's been replaced
	existingID := r.getOwnershipID(liveObj)
	expectedID := data.ID.ValueString()
	if existingID != "" && existingID != expectedID {
		// Resource exists but is owned by a different Terraform instance
		// This happens when for_each key changes: new instance overwrites old via SSA
		tflog.Info(ctx, "Resource has been replaced by different Terraform instance - skipping deletion", map[string]interface{}{
			"kind":        rc.Object.GetKind(),
			"name":        rc.Object.GetName(),
			"namespace":   rc.Object.GetNamespace(),
			"expected_id": expectedID,
			"existing_id": existingID,
		})
		return
	}

	// 6. Attempt normal deletion
	err = rc.Client.Delete(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName(), k8sclient.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		resourceDesc := fmt.Sprintf("%s %s", rc.Object.GetKind(), rc.Object.GetName())
		severity, title, detail := r.classifyK8sError(err, "Delete", resourceDesc)
		if severity == "warning" {
			resp.Diagnostics.AddWarning(title, detail)
		} else {
			resp.Diagnostics.AddError(title, detail)
		}
		return
	}

	// 6a. Re-check ownership after delete to handle race with parallel SSA Apply
	// When Terraform replaces a resource (e.g., for_each key change), Create() and Delete()
	// run in parallel. If SSA Apply from Create() completes between our ownership check (step 5a)
	// and the Delete() call above, the object will have a different terraform-id. In this case,
	// skip waiting for deletion since the object is now managed by a different Terraform resource.
	liveObj, err = rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err == nil {
		// Object still exists - check if it's been replaced
		currentID := r.getOwnershipID(liveObj)
		if currentID != "" && currentID != expectedID {
			// Resource has been replaced by a different Terraform instance during deletion
			tflog.Info(ctx, "Resource was replaced during deletion - skipping wait", map[string]interface{}{
				"kind":        rc.Object.GetKind(),
				"name":        rc.Object.GetName(),
				"namespace":   rc.Object.GetNamespace(),
				"expected_id": expectedID,
				"current_id":  currentID,
			})
			return
		}
	} else if !errors.IsNotFound(err) {
		// Unexpected error during post-delete check - log but continue
		tflog.Warn(ctx, "Failed to verify deletion status, continuing with wait", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// 7. Wait for deletion with timeout, continuing to check ownership on each iteration
	err = r.waitForDeletion(ctx, rc.Client, rc.GVR, rc.Object, timeout, expectedID)
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

// Private state flag helpers

// checkPendingProjectionFlag checks if there's a pending projection from a previous failed apply
func checkPendingProjectionFlag(ctx context.Context, getter interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}) bool {
	data, _ := getter.GetKey(ctx, "pending_projection")
	return data != nil && string(data) == "true"
}

// setPendingProjectionFlag sets the pending projection flag in private state
func setPendingProjectionFlag(ctx context.Context, setter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}) {
	setter.SetKey(ctx, "pending_projection", []byte("true"))
}

// clearPendingProjectionFlag clears the pending projection flag in private state
func clearPendingProjectionFlag(ctx context.Context, setter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}) {
	setter.SetKey(ctx, "pending_projection", nil)
}

// checkImportedWithoutAnnotationsFlag checks if resource was imported without annotations
func checkImportedWithoutAnnotationsFlag(ctx context.Context, getter interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}) bool {
	data, _ := getter.GetKey(ctx, "imported_without_annotations")
	return data != nil && string(data) == "true"
}

// clearImportedWithoutAnnotationsFlag clears the imported_without_annotations flag
func clearImportedWithoutAnnotationsFlag(ctx context.Context, setter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}) {
	setter.SetKey(ctx, "imported_without_annotations", nil)
}

// handleProjectionSuccess handles successful projection recovery per ADR-006
func handleProjectionSuccess(ctx context.Context, hasPendingProjection bool, privateSetter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}, operation string) {
	if hasPendingProjection {
		tflog.Info(ctx, fmt.Sprintf("Successfully completed pending projection %s", operation))
		clearPendingProjectionFlag(ctx, privateSetter)
	}
}

// handleProjectionFailure handles projection calculation failures per ADR-006
// This is a helper function that encapsulates the ADR-006 recovery pattern
func handleProjectionFailure(
	ctx context.Context,
	rc *ResourceContext,
	privateSetter interface {
		SetKey(context.Context, string, []byte) diag.Diagnostics
	},
	stateSetter *tfsdk.State,
	diagnostics *diag.Diagnostics,
	operation string,
	err error,
) {
	tflog.Warn(ctx, "Projection calculation failed, will retry on next apply", map[string]interface{}{
		"error": err.Error(),
	})

	// Set empty projection and field ownership - both must be known for Terraform to save state
	emptyMap, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{})
	rc.Data.ManagedStateProjection = emptyMap
	rc.Data.FieldOwnership = emptyMap

	// Save state with pending projection flag in Private state
	setPendingProjectionFlag(ctx, privateSetter)
	diags := stateSetter.Set(ctx, rc.Data)
	diagnostics.Append(diags...)

	// Return error to stop CI/CD pipeline
	diagnostics.AddError(
		"Projection Calculation Failed",
		fmt.Sprintf("%s was %s successfully but projection calculation failed: %s\n\n"+
			"This is typically caused by network issues. Run 'terraform apply' again to complete the operation.",
			formatResource(rc.Object), operation, err),
	)
}

// surfaceK8sWarnings checks for Kubernetes API warnings and adds them as Terraform diagnostics
func surfaceK8sWarnings(ctx context.Context, client k8sclient.K8sClient, obj interface {
	GetKind() string
	GetName() string
}, diagnostics *diag.Diagnostics) {
	warnings := client.GetWarnings()
	for _, warning := range warnings {
		diagnostics.AddWarning(
			fmt.Sprintf("Kubernetes API Warning (%s/%s)", obj.GetKind(), obj.GetName()),
			fmt.Sprintf("The Kubernetes API server returned a warning:\n\n%s", warning),
		)
		tflog.Warn(ctx, "Kubernetes API warning", map[string]interface{}{
			"warning": warning,
			"kind":    obj.GetKind(),
			"name":    obj.GetName(),
		})
	}
}

// populateObjectRef extracts resource identity and populates object_ref output
func (r *objectResource) populateObjectRef(ctx context.Context, rc *ResourceContext) error {
	objRef := objectRefModel{
		APIVersion: types.StringValue(rc.Object.GetAPIVersion()),
		Kind:       types.StringValue(rc.Object.GetKind()),
		Name:       types.StringValue(rc.Object.GetName()),
	}

	// Namespace is optional (null for cluster-scoped resources)
	if ns := rc.Object.GetNamespace(); ns != "" {
		objRef.Namespace = types.StringValue(ns)
	} else {
		objRef.Namespace = types.StringNull()
	}

	// Convert to types.Object
	objRefValue, diags := types.ObjectValueFrom(ctx, map[string]attr.Type{
		"api_version": types.StringType,
		"kind":        types.StringType,
		"name":        types.StringType,
		"namespace":   types.StringType,
	}, objRef)

	if diags.HasError() {
		return fmt.Errorf("failed to convert object_ref to types.Object: %v", diags)
	}

	rc.Data.ObjectRef = objRefValue
	return nil
}
