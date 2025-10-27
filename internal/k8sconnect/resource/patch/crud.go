package patch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
)

func (r *patchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// 1. Extract plan data
	var data patchResourceModel
	diags := req.Plan.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 2. Generate unique ID
	data.ID = types.StringValue(common.GenerateID())

	// 3. Setup client
	client, err := r.setupClient(ctx, &data, &resp.Diagnostics)
	if err != nil {
		return
	}

	// 4. Get target information
	var target patchTargetModel
	diags = data.Target.As(ctx, &target, basetypes.ObjectAsOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 5. Get the target resource (must exist)
	gvr, targetObj, err := r.getTargetResource(ctx, client, target)
	if err != nil {
		if errors.IsNotFound(err) {
			resp.Diagnostics.AddError(
				"Target Resource Not Found",
				fmt.Sprintf("k8sconnect_patch can only modify existing resources. "+
					"The target resource does not exist.\n\n"+
					"Target: %s\n\n"+
					"To create new resources, use k8sconnect_object instead.",
					formatTarget(target)),
			)
			return
		}
		// Use error classification for other K8s API errors
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Get Target Resource", formatTarget(target))
		return
	}

	// Surface any API warnings from get operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

	// 6. CRITICAL VALIDATION: Prevent self-patching
	if r.isManagedByThisState(ctx, targetObj) {
		resp.Diagnostics.AddError(
			"Cannot Patch Own Resource",
			fmt.Sprintf("This resource is already managed by k8sconnect_object "+
				"in this Terraform state.\n\n"+
				"You cannot patch resources you already own. Instead:\n"+
				"1. Modify the k8sconnect_object directly, or\n"+
				"2. Use ignore_fields to allow external controllers to manage specific fields\n\n"+
				"Target: %s",
				formatTarget(target)),
		)
		return
	}

	// 7. Apply patch using Server-Side Apply
	fieldManager := fmt.Sprintf("k8sconnect-patch-%s", data.ID.ValueString())
	patchedObj, err := r.applyPatch(ctx, client, targetObj, data, fieldManager, gvr)
	if err != nil {
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Apply Patch", formatTarget(target))
		return
	}

	// Surface any API warnings from patch operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

	tflog.Info(ctx, "Patch applied successfully", map[string]interface{}{
		"target":        formatTarget(target),
		"field_manager": fieldManager,
	})

	// 9. Store ONLY patched fields
	managedFields, err := fieldmanagement.ExtractManagedFieldsForManager(patchedObj, fieldManager)
	if err != nil {
		resp.Diagnostics.AddWarning("Failed to Extract Managed Fields",
			fmt.Sprintf("Failed to extract managed fields for %s (field manager: %s): %s",
				formatTarget(target), fieldManager, err.Error()))
		managedFields = "{}"
	}
	data.ManagedFields = types.StringValue(managedFields)

	// 10. Track field ownership in private state
	fieldOwnership := extractFieldOwnershipForManager(patchedObj, fieldManager)
	setFieldOwnershipInPrivateState(ctx, resp.Private, fieldOwnership)

	// 12. Save state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *patchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// 1. Extract state data
	var data patchResourceModel
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 2. Setup client
	client, err := r.setupClient(ctx, &data, &resp.Diagnostics)
	if err != nil {
		return
	}

	// 3. Get target information
	var target patchTargetModel
	diags = data.Target.As(ctx, &target, basetypes.ObjectAsOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 4. Get current resource state
	gvr, currentObj, err := r.getTargetResource(ctx, client, target)
	if err != nil {
		if errors.IsNotFound(err) {
			// Target deleted externally - remove patch from state
			tflog.Info(ctx, "Target resource deleted, removing patch from state")
			resp.State.RemoveResource(ctx)
			return
		}
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Read Target Resource", formatTarget(target))
		return
	}

	// Surface any API warnings from get operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

	// 5. Detect value drift (compare desired patch values with actual current values)
	fieldManager := fmt.Sprintf("k8sconnect-patch-%s", data.ID.ValueString())
	valueDriftDetected, driftedFields, err := r.detectValueDrift(ctx, currentObj, data)
	if err != nil {
		tflog.Warn(ctx, "Failed to check for value drift", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// 6. If value drift detected, warn and re-apply patch to correct it
	if valueDriftDetected {
		// Extract current field ownership to show which controllers are fighting
		patchContent := r.getPatchContent(data)
		patchType := r.determinePatchType(data)
		patchedFieldPaths, _ := r.extractPatchFieldPaths(ctx, patchContent, patchType)
		currentOwnership := fieldmanagement.ExtractFieldOwnershipForPaths(currentObj, patchedFieldPaths)

		// Format drifted fields with ownership info (limit to first 5 for readability)
		var fieldDetails []string
		displayLimit := 5
		for i, field := range driftedFields {
			if i >= displayLimit {
				fieldDetails = append(fieldDetails, fmt.Sprintf("... and %d more", len(driftedFields)-displayLimit))
				break
			}
			if owner, exists := currentOwnership[field]; exists && owner != fieldManager {
				fieldDetails = append(fieldDetails, fmt.Sprintf("  - %s (owned by \"%s\")", field, owner))
			} else {
				fieldDetails = append(fieldDetails, fmt.Sprintf("  - %s", field))
			}
		}

		// Build kubectl command (with or without namespace)
		kubectlCmd := fmt.Sprintf("kubectl get %s %s",
			strings.ToLower(target.Kind.ValueString()),
			target.Name.ValueString())
		if !target.Namespace.IsNull() && target.Namespace.ValueString() != "" {
			kubectlCmd += fmt.Sprintf(" -n %s", target.Namespace.ValueString())
		}
		kubectlCmd += " -o yaml"

		resp.Diagnostics.AddWarning(
			"Field Ownership Conflict - Controllers Fighting",
			fmt.Sprintf("Other controllers modified fields we manage and will be forcefully corrected:\n%s\n\n"+
				"The patch has been re-applied with force=true to restore your values. This indicates controllers are fighting over these fields.\n\n"+
				"If another controller keeps modifying these fields, consider:\n"+
				"• Removing this patch to allow the other controller to manage these fields\n"+
				"• Reconfiguring or disabling the other controller to avoid conflicts\n\n"+
				"To investigate: %s",
				strings.Join(fieldDetails, "\n"),
				kubectlCmd),
		)

		tflog.Info(ctx, "Value drift detected, re-applying patch to correct drift", map[string]interface{}{
			"target":        formatTarget(target),
			"field_manager": fieldManager,
		})

		// Re-apply the patch to correct drift
		patchedObj, err := r.applyPatch(ctx, client, currentObj, data, fieldManager, gvr)
		if err != nil {
			resp.Diagnostics.AddWarning(
				"Failed to Correct Drift",
				fmt.Sprintf("Detected drift in patched values but failed to re-apply patch: %v\n\n"+
					"Manual intervention may be required to restore desired state.", err),
			)
			// Continue with read even if re-apply failed
		} else {
			// Update currentObj to the corrected state
			currentObj = patchedObj
			tflog.Info(ctx, "Drift corrected successfully", map[string]interface{}{
				"target": formatTarget(target),
			})
		}

		// Surface any API warnings from patch operation
		surfaceK8sWarnings(ctx, client, &resp.Diagnostics)
	}

	// 7. Extract ONLY fields we patched (using our field manager)
	currentManagedFields, err := fieldmanagement.ExtractManagedFieldsForManager(currentObj, fieldManager)
	if err != nil {
		tflog.Warn(ctx, "Failed to extract managed fields during read", map[string]interface{}{
			"error": err.Error(),
		})
		currentManagedFields = "{}"
	}

	// 8. Update managed fields in state
	if currentManagedFields != data.ManagedFields.ValueString() {
		tflog.Debug(ctx, "Managed fields changed (ownership or structure drift)")
		data.ManagedFields = types.StringValue(currentManagedFields)
	}

	// 8a. Track field ownership in private state
	fieldOwnership := extractFieldOwnershipForManager(currentObj, fieldManager)
	setFieldOwnershipInPrivateState(ctx, resp.Private, fieldOwnership)

	// 9. Save refreshed state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)

	_ = gvr // unused but needed for consistency
}

func (r *patchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// 1. Extract state and plan data
	var state, plan patchResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	diags = req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 2. Check if target changed (requires replacement)
	if !targetsEqual(state.Target, plan.Target) {
		resp.Diagnostics.AddError(
			"Target Changed",
			"Changing the patch target requires resource replacement. "+
				"Destroy the old patch and create a new one.")
		return
	}

	// 3. Preserve ID
	plan.ID = state.ID

	// 4. Setup client
	client, err := r.setupClient(ctx, &plan, &resp.Diagnostics)
	if err != nil {
		return
	}

	// 5. Get target information
	var target patchTargetModel
	diags = plan.Target.As(ctx, &target, basetypes.ObjectAsOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 6. Get current resource
	gvr, currentObj, err := r.getTargetResource(ctx, client, target)
	if err != nil {
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Get Target Resource", formatTarget(target))
		return
	}

	// Surface any API warnings from get operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

	// 7. CRITICAL: Detect removed fields and transfer ownership back
	fieldManager := fmt.Sprintf("k8sconnect-patch-%s", plan.ID.ValueString())

	// Extract current fields from state's managed_fields
	var currentFieldPaths []string
	if !state.ManagedFields.IsNull() && state.ManagedFields.ValueString() != "" && state.ManagedFields.ValueString() != "{}" {
		var err error
		currentFieldPaths, err = fieldmanagement.ExtractFieldPathsFromManagedFieldsJSON(state.ManagedFields.ValueString())
		if err != nil {
			tflog.Warn(ctx, "Failed to extract current field paths from managed_fields", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	// Extract new fields from plan's patch content
	newPatchContent := r.getPatchContent(plan)
	newPatchType := r.determinePatchType(plan)
	newFieldPaths, err := r.extractPatchFieldPaths(ctx, newPatchContent, newPatchType)
	if err != nil {
		resp.Diagnostics.AddError("Failed to Parse New Patch Content",
			fmt.Sprintf("Failed to parse new patch content for %s: %s", formatTarget(target), err.Error()))
		return
	}

	// Calculate removed fields (fields in current but not in new)
	removedFields := findRemovedFields(currentFieldPaths, newFieldPaths)

	// If fields were removed, they will become unmanaged (no longer patched by this resource)
	// Note: Previous owner tracking was removed per ADR-020
	if len(removedFields) > 0 {
		tflog.Info(ctx, "Detected removed fields - they will become unmanaged", map[string]interface{}{
			"target":        formatTarget(target),
			"removed_count": len(removedFields),
		})
	}

	// 8. Re-apply updated patch
	patchedObj, err := r.applyPatch(ctx, client, currentObj, plan, fieldManager, gvr)
	if err != nil {
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Update Patch", formatTarget(target))
		return
	}

	// Surface any API warnings from patch operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

	tflog.Info(ctx, "Patch updated successfully", map[string]interface{}{
		"target":        formatTarget(target),
		"field_manager": fieldManager,
	})

	// 8. Update managed fields
	managedFields, err := fieldmanagement.ExtractManagedFieldsForManager(patchedObj, fieldManager)
	if err != nil {
		resp.Diagnostics.AddWarning("Failed to Extract Managed Fields",
			fmt.Sprintf("Failed to extract managed fields for %s (field manager: %s): %s",
				formatTarget(target), fieldManager, err.Error()))
		managedFields = "{}"
	}
	plan.ManagedFields = types.StringValue(managedFields)

	// 9. Preserve previous owners (only set during Create)

	// 10. Track field ownership in private state
	fieldOwnership := extractFieldOwnershipForManager(patchedObj, fieldManager)
	setFieldOwnershipInPrivateState(ctx, resp.Private, fieldOwnership)

	// 11. Save updated state
	diags = resp.State.Set(ctx, &plan)
	resp.Diagnostics.Append(diags...)
}

func (r *patchResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// 1. Extract state data
	var data patchResourceModel
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 2. Setup client
	client, err := r.setupClient(ctx, &data, &resp.Diagnostics)
	if err != nil {
		// Can't connect to release ownership - log and continue
		tflog.Warn(ctx, "Failed to connect for cleanup", map[string]interface{}{"error": err.Error()})
		return
	}

	// 3. Get target information
	var target patchTargetModel
	diags = data.Target.As(ctx, &target, basetypes.ObjectAsOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 4. Check if target still exists
	_, _, err = r.getTargetResource(ctx, client, target)
	if err != nil {
		if errors.IsNotFound(err) {
			// Target already deleted - nothing to do
			tflog.Info(ctx, "Target resource already deleted")
			return
		}
		// Connection error or other issue - log and continue
		tflog.Warn(ctx, "Failed to verify target resource exists", map[string]interface{}{"error": err.Error()})
		return
	}

	// Surface any API warnings from get operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

	// That's it - state removed automatically by framework
	// The patched values stay on the resource
	// Note: Previous owner tracking and transfer-back logic was removed per ADR-020
}

func (r *patchResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddError(
		"Import Not Supported",
		"k8sconnect_patch resources cannot be imported. "+
			"Import is not supported because:\n"+
			"1. Patches represent partial ownership, not full resource state\n"+
			"2. There's no way to determine the original patch content from the current state\n"+
			"Instead, define the patch in your Terraform configuration.")
}

// surfaceK8sWarnings checks for Kubernetes API warnings and adds them as Terraform diagnostics
func surfaceK8sWarnings(ctx context.Context, client k8sclient.K8sClient, diagnostics *diag.Diagnostics) {
	warnings := client.GetWarnings()
	for _, warning := range warnings {
		diagnostics.AddWarning(
			"Kubernetes API Warning",
			fmt.Sprintf("The Kubernetes API server returned a warning:\n\n%s", warning),
		)
		tflog.Warn(ctx, "Kubernetes API warning", map[string]interface{}{
			"warning": warning,
		})
	}
}

// findRemovedFields finds fields that are in currentFields but not in newFields
func findRemovedFields(currentFields, newFields []string) []string {
	newFieldsSet := make(map[string]bool)
	for _, field := range newFields {
		newFieldsSet[field] = true
	}

	var removed []string
	for _, field := range currentFields {
		if !newFieldsSet[field] {
			removed = append(removed, field)
		}
	}

	return removed
}

// getFieldOwnershipFromPrivateState retrieves field ownership map from private state
func getFieldOwnershipFromPrivateState(ctx context.Context, getter interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}) map[string]string {
	data, _ := getter.GetKey(ctx, privateStateKeyOwnership)
	if data == nil {
		return nil
	}

	var ownership map[string]string
	if err := json.Unmarshal(data, &ownership); err != nil {
		tflog.Warn(ctx, "Failed to unmarshal field ownership from private state", map[string]interface{}{
			"error": err.Error(),
		})
		return nil
	}
	return ownership
}

// setFieldOwnershipInPrivateState stores field ownership map in private state
func setFieldOwnershipInPrivateState(ctx context.Context, setter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}, ownership map[string]string) {
	if ownership == nil || len(ownership) == 0 {
		setter.SetKey(ctx, privateStateKeyOwnership, nil)
		return
	}

	data, err := json.Marshal(ownership)
	if err != nil {
		tflog.Warn(ctx, "Failed to marshal field ownership for private state", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}
	setter.SetKey(ctx, privateStateKeyOwnership, data)
}
