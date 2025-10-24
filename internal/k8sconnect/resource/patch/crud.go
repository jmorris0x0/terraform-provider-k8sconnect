package patch

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

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

	// 7. Capture ownership BEFORE patching
	patchContent := r.getPatchContent(data)
	patchedFieldPaths, err := r.extractPatchFieldPaths(ctx, patchContent, r.determinePatchType(data))
	if err != nil {
		resp.Diagnostics.AddError("Failed to Parse Patch",
			fmt.Sprintf("Failed to parse patch for %s: %s", formatTarget(target), err.Error()))
		return
	}
	previousOwners := fieldmanagement.ExtractFieldOwnershipForPaths(targetObj, patchedFieldPaths)

	// 8. Apply patch using Server-Side Apply
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

	// 10. Extract field ownership (only for our manager)
	fieldOwnership := extractFieldOwnershipForManager(patchedObj, fieldManager)
	ownershipMap, diags := types.MapValueFrom(ctx, types.StringType, fieldOwnership)
	resp.Diagnostics.Append(diags...)
	if !resp.Diagnostics.HasError() {
		data.FieldOwnership = ownershipMap
	}

	// 11. Store previous owners
	previousOwnersMap, diags := types.MapValueFrom(ctx, types.StringType, previousOwners)
	resp.Diagnostics.Append(diags...)
	if !resp.Diagnostics.HasError() {
		data.PreviousOwners = previousOwnersMap
	}

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
		// Format drifted fields for display (limit to first 5 for readability)
		fieldsList := driftedFields
		if len(fieldsList) > 5 {
			fieldsList = append(driftedFields[:5], fmt.Sprintf("... and %d more", len(driftedFields)-5))
		}
		fieldsStr := strings.Join(fieldsList, ", ")

		// Build kubectl command (with or without namespace)
		kubectlCmd := fmt.Sprintf("kubectl get %s %s",
			strings.ToLower(target.Kind.ValueString()),
			target.Name.ValueString())
		if !target.Namespace.IsNull() && target.Namespace.ValueString() != "" {
			kubectlCmd += fmt.Sprintf(" -n %s", target.Namespace.ValueString())
		}
		kubectlCmd += " -o yaml"

		resp.Diagnostics.AddWarning(
			"Patched Field Values Changed Externally",
			fmt.Sprintf("Fields modified externally and automatically corrected: %s\n\n"+
				"The patch has been re-applied to restore your desired values. If another controller keeps modifying these fields, consider:\n"+
				"• Removing this patch to allow the other controller to manage these fields\n"+
				"• Reconfiguring the other controller to avoid conflicts\n\n"+
				"To investigate: %s",
				fieldsStr,
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

	// 9. Update field ownership tracking (only for our manager)
	fieldOwnership := extractFieldOwnershipForManager(currentObj, fieldManager)
	ownershipMap, diags := types.MapValueFrom(ctx, types.StringType, fieldOwnership)
	resp.Diagnostics.Append(diags...)
	if !resp.Diagnostics.HasError() {
		data.FieldOwnership = ownershipMap
	}

	// 10. Save refreshed state
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

	// If fields were removed, transfer ownership back to previous owners
	if len(removedFields) > 0 {
		tflog.Info(ctx, "Detected removed fields, transferring ownership back", map[string]interface{}{
			"target":        formatTarget(target),
			"removed_count": len(removedFields),
		})

		// Get previous owners map
		var previousOwnersMap map[string]string
		if !state.PreviousOwners.IsNull() {
			diags = state.PreviousOwners.ElementsAs(ctx, &previousOwnersMap, false)
			if diags.HasError() {
				tflog.Warn(ctx, "Failed to parse previous owners for removed fields", map[string]interface{}{
					"error": diags.Errors(),
				})
			}
		}

		// Transfer removed fields back to their previous owners
		if len(previousOwnersMap) > 0 {
			err = r.transferRemovedFieldsBack(ctx, client, currentObj, gvr, removedFields, previousOwnersMap, fieldManager)
			if err != nil {
				resp.Diagnostics.AddWarning(
					"Failed to Transfer Removed Fields",
					fmt.Sprintf("Some fields were removed from the patch but could not be transferred back to their previous owners: %v\n\n"+
						"These fields may become unmanaged.", err),
				)
			} else {
				tflog.Info(ctx, "Successfully transferred removed fields back", map[string]interface{}{
					"target": formatTarget(target),
				})
			}

			// Surface any API warnings from transfer operation
			surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

			// CRITICAL: Refetch the resource to get updated managedFields after ownership transfer
			// Without this, SSA will use stale ownership info and may delete transferred fields
			_, currentObj, err = r.getTargetResource(ctx, client, target)
			if err != nil {
				resp.Diagnostics.AddError(
					"Failed to Refetch Resource After Ownership Transfer",
					fmt.Sprintf("Ownership was transferred but failed to refetch resource state: %v", err),
				)
				return
			}

			surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

			tflog.Debug(ctx, "Refetched resource after ownership transfer", map[string]interface{}{
				"target": formatTarget(target),
			})
		} else {
			tflog.Warn(ctx, "No previous owners found for removed fields - they will become unmanaged", map[string]interface{}{
				"removed_count": len(removedFields),
			})
		}
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

	// 9. Update field ownership (only for our manager)
	fieldOwnership := extractFieldOwnershipForManager(patchedObj, fieldManager)
	ownershipMap, diags := types.MapValueFrom(ctx, types.StringType, fieldOwnership)
	resp.Diagnostics.Append(diags...)
	if !resp.Diagnostics.HasError() {
		plan.FieldOwnership = ownershipMap
	}

	// 10. Preserve previous owners (only set during Create)
	plan.PreviousOwners = state.PreviousOwners

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

	// 5. Transfer ownership back to original owners (per-field clean handoff)
	fieldManager := fmt.Sprintf("k8sconnect-patch-%s", data.ID.ValueString())

	// Get previous owners to determine who to transfer back to
	var previousOwnersMap map[string]string
	if !data.PreviousOwners.IsNull() {
		diags = data.PreviousOwners.ElementsAs(ctx, &previousOwnersMap, false)
		if diags.HasError() {
			tflog.Warn(ctx, "Failed to parse previous owners", map[string]interface{}{
				"error": diags.Errors(),
			})
			previousOwnersMap = nil
		}
	}

	// If we have previous owners, transfer ownership back per-field
	if len(previousOwnersMap) > 0 {
		// Get the GVR
		gvr, _, err := r.getTargetResource(ctx, client, target)
		if err != nil {
			tflog.Warn(ctx, "Failed to get GVR for ownership transfer", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			// Surface any API warnings from get operation
			surfaceK8sWarnings(ctx, client, &resp.Diagnostics)
			// Group fields by their previous owner
			fieldsByOwner := groupFieldsByPreviousOwner(previousOwnersMap)

			tflog.Info(ctx, "Transferring ownership back to original owners",
				map[string]interface{}{
					"target":      formatTarget(target),
					"from":        fieldManager,
					"owner_count": len(fieldsByOwner),
					"note":        "Values remain unchanged, only ownership transfers",
				})

			// Transfer each group back to its original owner
			// IMPORTANT: Refetch resource before each transfer to ensure idempotency
			for owner, fields := range fieldsByOwner {
				// Refetch current state to check ownership (idempotent retry)
				_, currentObj, err := r.getTargetResource(ctx, client, target)
				if err != nil {
					tflog.Warn(ctx, "Failed to fetch resource for ownership transfer", map[string]interface{}{
						"owner": owner,
						"error": err.Error(),
					})
					continue
				}

				// Surface any API warnings from get operation
				surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

				// Filter fields to only transfer those currently owned by our patch manager
				fieldsToTransfer := r.filterFieldsOwnedByManager(currentObj, fields, fieldManager)
				if len(fieldsToTransfer) == 0 {
					tflog.Debug(ctx, "No fields currently owned by this patch, skipping", map[string]interface{}{
						"owner":        owner,
						"total_fields": len(fields),
					})
					continue
				}

				err = r.transferOwnershipForFields(ctx, client, currentObj, gvr, fieldsToTransfer, owner)

				// Surface any API warnings from transfer operation
				surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

				if err != nil {
					tflog.Warn(ctx, "Failed to transfer ownership for some fields", map[string]interface{}{
						"owner": owner,
						"error": err.Error(),
					})
					// Continue with other owners
				} else {
					tflog.Debug(ctx, "Successfully transferred ownership", map[string]interface{}{
						"owner":       owner,
						"field_count": len(fieldsToTransfer),
						"skipped":     len(fields) - len(fieldsToTransfer),
					})
				}
			}
		}
	} else {
		tflog.Warn(ctx, "No previous owners found - ownership will remain orphaned",
			map[string]interface{}{
				"target":        formatTarget(target),
				"field_manager": fieldManager,
				"note":          "Future patches may need force=true to reclaim these fields",
			})
	}

	// That's it - state removed automatically by framework
	// The patched values stay on the resource
	// Ownership has been transferred back to the original owners (if found)
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

// groupFieldsByPreviousOwner groups field paths by their previous owner
// Input: {"spec.replicas": "hpa", "metadata.labels.foo": "kubectl", "metadata.labels.bar": "kubectl"}
// Output: {"hpa": ["spec.replicas"], "kubectl": ["metadata.labels.foo", "metadata.labels.bar"]}
func groupFieldsByPreviousOwner(previousOwners map[string]string) map[string][]string {
	result := make(map[string][]string)

	for field, owner := range previousOwners {
		result[owner] = append(result[owner], field)
	}

	return result
}

// filterFieldsOwnedByManager filters a list of fields to only those currently owned by the specified field manager
// This ensures idempotent destroy - if a field was already transferred, we skip it
func (r *patchResource) filterFieldsOwnedByManager(obj *unstructured.Unstructured, fields []string, fieldManager string) []string {
	currentOwnership := fieldmanagement.ExtractFieldOwnershipMap(obj)

	var ownedFields []string
	for _, field := range fields {
		if owner, exists := currentOwnership[field]; exists && owner == fieldManager {
			ownedFields = append(ownedFields, field)
		}
	}

	return ownedFields
}

// transferOwnershipForFields transfers ownership of specific fields to a new owner
// It builds a partial patch containing only those fields and applies it with the target field manager
func (r *patchResource) transferOwnershipForFields(ctx context.Context, client k8sclient.K8sClient, targetObj *unstructured.Unstructured, gvr schema.GroupVersionResource, fields []string, targetOwner string) error {
	// Build a patch containing only the specified fields with their current values
	partialPatch := buildPartialPatch(targetObj, fields)

	if len(partialPatch) == 0 {
		return fmt.Errorf("no fields to transfer")
	}

	// Create an unstructured object for the partial patch
	patchObj := &unstructured.Unstructured{Object: partialPatch}
	patchObj.SetAPIVersion(targetObj.GetAPIVersion())
	patchObj.SetKind(targetObj.GetKind())
	patchObj.SetName(targetObj.GetName())
	patchObj.SetNamespace(targetObj.GetNamespace())

	// Apply with the target owner's field manager
	err := client.Apply(ctx, patchObj, k8sclient.ApplyOptions{
		FieldManager:    targetOwner,
		Force:           true,     // Required to take ownership
		FieldValidation: "Strict", // ADR-017: Validate fields against OpenAPI schema during apply
	})

	return err
}

// buildPartialPatch extracts specific field paths from an object to build a partial patch
// This is used to transfer ownership of only certain fields
func buildPartialPatch(obj *unstructured.Unstructured, fieldPaths []string) map[string]interface{} {
	result := make(map[string]interface{})

	for _, fieldPath := range fieldPaths {
		// Split path like "spec.template.spec.containers[0].env"
		parts := parseFieldPath(fieldPath)

		// Extract the value at this path
		value := getNestedValue(obj.Object, parts)
		if value != nil {
			// Set it in the result
			setNestedValue(result, parts, value)
		}
	}

	return result
}

// parseFieldPath splits a field path into parts, handling array indices
// "spec.containers[0].name" -> ["spec", "containers", "0", "name"]
func parseFieldPath(path string) []string {
	// Replace array index notation with dot notation
	// "containers[0]" -> "containers.0"
	normalized := strings.ReplaceAll(path, "[", ".")
	normalized = strings.ReplaceAll(normalized, "]", "")

	return strings.Split(normalized, ".")
}

// getNestedValue retrieves a value at a nested path in a map
func getNestedValue(obj map[string]interface{}, parts []string) interface{} {
	if len(parts) == 0 {
		return nil
	}

	current := interface{}(obj)

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			current = v[part]
		case []interface{}:
			// Handle array index
			idx := 0
			n, err := fmt.Sscanf(part, "%d", &idx)
			if err != nil || n != 1 {
				// Not a valid integer index
				return nil
			}
			if idx >= 0 && idx < len(v) {
				current = v[idx]
			} else {
				return nil
			}
		default:
			return nil
		}

		if current == nil {
			return nil
		}
	}

	return current
}

// setNestedValue sets a value at a nested path in a map, creating intermediate maps as needed
func setNestedValue(obj map[string]interface{}, parts []string, value interface{}) {
	if len(parts) == 0 {
		return
	}

	current := obj

	// Navigate to the parent of the target field
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]

		if _, exists := current[part]; !exists {
			current[part] = make(map[string]interface{})
		}

		if nextMap, ok := current[part].(map[string]interface{}); ok {
			current = nextMap
		} else {
			// Can't navigate further
			return
		}
	}

	// Set the final value
	current[parts[len(parts)-1]] = value
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

// transferRemovedFieldsBack transfers ownership of removed fields back to their previous owners
// This ensures that when fields are removed from a patch, they don't become unmanaged
func (r *patchResource) transferRemovedFieldsBack(ctx context.Context, client k8sclient.K8sClient, currentObj *unstructured.Unstructured, gvr schema.GroupVersionResource, removedFields []string, previousOwnersMap map[string]string, currentFieldManager string) error {
	// Filter to only fields we actually own
	fieldsWeOwn := r.filterFieldsOwnedByManager(currentObj, removedFields, currentFieldManager)

	if len(fieldsWeOwn) == 0 {
		tflog.Debug(ctx, "No removed fields are currently owned by this patch", map[string]interface{}{
			"removed_count": len(removedFields),
		})
		return nil
	}

	// Group removed fields by their previous owner
	fieldsByOwner := make(map[string][]string)
	for _, field := range fieldsWeOwn {
		if previousOwner, exists := previousOwnersMap[field]; exists {
			fieldsByOwner[previousOwner] = append(fieldsByOwner[previousOwner], field)
		} else {
			// No previous owner found - this field will become unmanaged
			tflog.Warn(ctx, "No previous owner found for removed field", map[string]interface{}{
				"field": field,
			})
		}
	}

	// Transfer each group back to its previous owner
	for owner, fields := range fieldsByOwner {
		err := r.transferOwnershipForFields(ctx, client, currentObj, gvr, fields, owner)
		if err != nil {
			tflog.Warn(ctx, "Failed to transfer ownership for removed fields", map[string]interface{}{
				"owner": owner,
				"error": err.Error(),
			})
			// Continue with other owners
		} else {
			tflog.Info(ctx, "Transferred ownership for removed fields", map[string]interface{}{
				"owner":       owner,
				"field_count": len(fields),
			})
		}
	}

	return nil
}
