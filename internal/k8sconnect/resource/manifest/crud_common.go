// internal/k8sconnect/resource/manifest/crud_common.go
package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

// checkResourceExistenceAndOwnership checks if resource exists and verifies ownership
func (r *manifestResource) checkResourceExistenceAndOwnership(ctx context.Context, rc *ResourceContext, data *manifestResourceModel, resp *resource.CreateResponse) error {
	existingObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err == nil {
		// Resource exists - check ownership
		existingID := r.getOwnershipID(existingObj)
		if existingID != "" && existingID != data.ID.ValueString() {
			// Different ID - owned by another state
			resp.Diagnostics.AddError(
				"Resource Already Managed",
				fmt.Sprintf("resource managed by different k8sconnect resource (Terraform ID: %s)", existingID),
			)
			return fmt.Errorf("resource already managed")
		}
		// If existingID is empty (unowned) or matches our ID, we can proceed
	} else if !errors.IsNotFound(err) {
		// Real error checking if resource exists
		resp.Diagnostics.AddError("Existence Check Failed",
			fmt.Sprintf("Failed to check if resource exists: %s", err))
		return err
	}
	return nil
}

// applyResourceWithConflictHandling applies resource and handles field conflicts.
// Omits ignore_fields from the Apply patch to avoid taking ownership of those fields.
func (r *manifestResource) applyResourceWithConflictHandling(ctx context.Context, rc *ResourceContext, data *manifestResourceModel, resp interface{}, operation string) error {
	// Check force conflicts from the data parameter
	forceConflicts := false
	if !data.ForceConflicts.IsNull() {
		forceConflicts = data.ForceConflicts.ValueBool()
	}

	// Prepare the object to apply
	objToApply := rc.Object.DeepCopy()

	// On Update, filter out ignored fields to release ownership to other controllers
	// On Create, send everything to establish initial state
	if operation == "Update" {
		if ignoreFields := getIgnoreFields(ctx, data); ignoreFields != nil {
			// Debug: show before filtering
			beforeJSON, _ := objToApply.MarshalJSON()
			fmt.Printf("DEBUG %s: Object BEFORE filtering ignore_fields:\n%s\n", operation, string(beforeJSON))

			objToApply = removeFieldsFromObject(objToApply, ignoreFields)
			tflog.Debug(ctx, "Filtered ignored fields from Apply patch", map[string]interface{}{
				"ignored_fields": ignoreFields,
			})

			// Debug: show after filtering
			afterJSON, _ := objToApply.MarshalJSON()
			fmt.Printf("DEBUG %s: Object AFTER filtering ignore_fields:\n%s\n", operation, string(afterJSON))
		}
	}

	// Apply the resource (with ignored fields filtered out on Update)
	err := rc.Client.Apply(ctx, objToApply, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        forceConflicts,
	})

	// THIS IS THE MISSING LOGIC FROM THE ORIGINAL:
	if err != nil && isFieldConflictError(err) && !forceConflicts {
		fmt.Printf("Checking if conflicts are only with self: %v\n", err.Error())
		if conflictsOnlyWithSelf(err) {
			fmt.Printf("Self-conflict detected, forcing update\n")
			tflog.Info(ctx, "Detected drift in fields we own, forcing update")
			err = rc.Client.Apply(ctx, rc.Object, k8sclient.ApplyOptions{
				FieldManager: "k8sconnect",
				Force:        true,
			})
		} else {
			fmt.Printf("Not a self-conflict, not forcing\n")
		}
	}

	if err != nil {
		if isFieldConflictError(err) {
			fmt.Printf("DEBUG %s: Field conflict error details: %v\n", operation, err)
			r.addFieldConflictError(resp, operation)
		} else {
			r.addOperationError(resp, operation, err)
		}
		return err
	}
	return nil
}

// readResourceAfterCreate reads resource back to get managedFields (Phase 2)
func (r *manifestResource) readResourceAfterCreate(ctx context.Context, rc *ResourceContext) {
	createdObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		tflog.Warn(ctx, "Failed to read resource after create", map[string]interface{}{
			"error": err.Error(),
			"kind":  rc.Object.GetKind(),
			"name":  rc.Object.GetName(),
		})
	} else {
		rc.Object = createdObj
		tflog.Debug(ctx, "Read resource after create for managedFields", map[string]interface{}{
			"kind":          rc.Object.GetKind(),
			"name":          rc.Object.GetName(),
			"has_managed":   len(rc.Object.GetManagedFields()) > 0,
			"field_manager": "k8sconnect",
		})
	}

	tflog.Info(ctx, "Resource created", map[string]interface{}{
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})
}

// handleWaitExecution handles wait conditions and returns whether waiting occurred
func (r *manifestResource) handleWaitExecution(ctx context.Context, rc *ResourceContext, resp interface{}, action string) bool {
	waited := false
	if err := r.executeWait(rc); err != nil {
		fmt.Printf("Wait error: %v\n", err)
		r.addWaitError(resp, action, err)
		waited = true
	} else if !rc.Data.WaitFor.IsNull() {
		var waitConfig waitForModel
		diags := rc.Data.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
		if respWithDiags, ok := resp.(interface{ Append(...interface{}) }); ok {
			respWithDiags.Append(diags)
		}
		if !diags.HasError() && r.hasActiveWaitConditions(waitConfig) {
			waited = true
			fmt.Printf("Parsed wait_for - Field: %v\n", waitConfig.Field)
		}
	}
	return waited
}

// verifyOwnership checks if resource is owned by this Terraform resource
func (r *manifestResource) verifyOwnership(currentObj *unstructured.Unstructured, expectedID string, obj *unstructured.Unstructured, resp *resource.ReadResponse) error {
	annotations := currentObj.GetAnnotations()
	currentID := ""
	if annotations != nil {
		currentID = annotations["k8sconnect.terraform.io/terraform-id"]
	}

	if currentID == "" {
		resp.Diagnostics.AddError(
			"Resource Not Managed",
			fmt.Sprintf("The %s '%s' exists but is not managed by Terraform. Use 'terraform import' to manage it.",
				obj.GetKind(), obj.GetName()),
		)
		return fmt.Errorf("resource not managed")
	}

	if currentID != expectedID {
		resp.Diagnostics.AddError(
			"Resource Ownership Conflict",
			fmt.Sprintf("The %s '%s' is now managed by a different Terraform resource (ID: %s).",
				obj.GetKind(), obj.GetName(), currentID),
		)
		return fmt.Errorf("ownership conflict")
	}
	return nil
}

// updateProjectionFromCurrent updates projection from current Kubernetes state
func (r *manifestResource) updateProjectionFromCurrent(ctx context.Context, data *manifestResourceModel, currentObj, obj *unstructured.Unstructured) error {
	// Extract paths - use field ownership if flag is enabled
	var paths []string

	if len(currentObj.GetManagedFields()) > 0 {
		tflog.Debug(ctx, "Using field ownership for projection during Read", map[string]interface{}{
			"managers": len(currentObj.GetManagedFields()),
		})
		paths = extractOwnedPaths(ctx, currentObj.GetManagedFields(), obj.Object)
	} else {
		tflog.Warn(ctx, "No managedFields available during Read, using all fields from YAML")
		// When no ownership info, extract all fields from YAML
		paths = extractOwnedPaths(ctx, []metav1.ManagedFieldsEntry{}, obj.Object)
	}

	// Apply ignore_fields filtering if specified
	if ignoreFields := getIgnoreFields(ctx, data); ignoreFields != nil {
		paths = filterIgnoredPaths(paths, ignoreFields)
		tflog.Debug(ctx, "Applied ignore_fields filtering", map[string]interface{}{
			"ignored_count":  len(ignoreFields),
			"filtered_paths": len(paths),
		})
	}

	// Project the current state to only include fields we manage
	projection, err := projectFields(currentObj.Object, paths)
	if err != nil {
		return err
	}

	// Convert projection to JSON for storage
	projectionJSON, err := toJSON(projection)
	if err != nil {
		return err
	}

	fmt.Printf("=== READ: Projection for %s/%s ===\n", currentObj.GetKind(), currentObj.GetName())
	fmt.Printf("Projection: %s\n", projectionJSON[:min(500, len(projectionJSON))])
	fmt.Printf("=== END READ ===\n")

	// Update the projection in state - this is what enables drift detection
	data.ManagedStateProjection = types.StringValue(projectionJSON)

	tflog.Debug(ctx, "Updated managed state projection", map[string]interface{}{
		"id":              data.ID.ValueString(),
		"path_count":      len(paths),
		"projection_size": len(projectionJSON),
	})

	return nil
}

// updateFieldOwnershipData updates field ownership tracking data
func (r *manifestResource) updateFieldOwnershipData(ctx context.Context, data *manifestResourceModel, currentObj *unstructured.Unstructured) {
	ownership := extractFieldOwnership(currentObj)

	ownershipJSON, err := json.Marshal(ownership)
	if err != nil {
		tflog.Warn(ctx, "Failed to marshal field ownership", map[string]interface{}{
			"error": err.Error(),
		})
		data.FieldOwnership = types.StringValue("{}")
	} else {
		data.FieldOwnership = types.StringValue(string(ownershipJSON))
	}
}

// Error handling helpers
func (r *manifestResource) addFieldConflictError(resp interface{}, operation string) {
	if createResp, ok := resp.(*resource.CreateResponse); ok {
		createResp.Diagnostics.AddError("Field Manager Conflict",
			"Another controller owns fields you're trying to set. "+
				"Add conflicting paths to ignore_fields to release ownership, or set force_conflicts = true to override.")
	} else if updateResp, ok := resp.(*resource.UpdateResponse); ok {
		updateResp.Diagnostics.AddError("Field Manager Conflict",
			"Another controller owns fields you're trying to set. "+
				"Add conflicting paths to ignore_fields to release ownership, or set force_conflicts = true to override.")
	}
}

func (r *manifestResource) addOperationError(resp interface{}, operation string, err error) {
	errorMsg := fmt.Sprintf("%s Failed", operation)
	if createResp, ok := resp.(*resource.CreateResponse); ok {
		createResp.Diagnostics.AddError(errorMsg, err.Error())
	} else if updateResp, ok := resp.(*resource.UpdateResponse); ok {
		updateResp.Diagnostics.AddError(errorMsg, err.Error())
	}
}

func (r *manifestResource) addWaitError(resp interface{}, action string, err error) {
	msg := fmt.Sprintf("Wait condition failed after resource was %s", action)
	detailMsg := fmt.Sprintf("The resource was successfully %s, but the wait condition failed: %s\n\n"+
		"You need to either:\n"+
		"1. Increase the timeout if more time is needed\n"+
		"2. Fix the underlying issue preventing the condition from being met\n"+
		"3. Review your wait_for configuration", action, err)

	if createResp, ok := resp.(*resource.CreateResponse); ok {
		createResp.Diagnostics.AddError(msg, detailMsg)
	} else if updateResp, ok := resp.(*resource.UpdateResponse); ok {
		updateResp.Diagnostics.AddError(msg, detailMsg)
	}
}

// Utility functions
func isFieldConflictError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "conflict")
}

// Helper function to check if all conflicts are with our own field manager
func conflictsOnlyWithSelf(err error) bool {
	errMsg := err.Error()
	// Check if the error mentions our field manager
	if !strings.Contains(errMsg, `conflict with "k8sconnect"`) {
		return false
	}

	// Count conflicts with our manager vs total conflicts
	totalConflicts := strings.Count(errMsg, `conflict with "`)
	ourConflicts := strings.Count(errMsg, `conflict with "k8sconnect"`)

	// If all conflicts are with our manager, the counts should match
	return totalConflicts == ourConflicts
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// getIgnoreFields extracts the ignore_fields list from the model.
// Returns nil if ignore_fields is not set or empty.
func getIgnoreFields(ctx context.Context, data *manifestResourceModel) []string {
	if data.IgnoreFields.IsNull() || data.IgnoreFields.IsUnknown() {
		return nil
	}

	var ignoreFields []string
	diags := data.IgnoreFields.ElementsAs(ctx, &ignoreFields, false)
	if diags.HasError() || len(ignoreFields) == 0 {
		return nil
	}

	return ignoreFields
}
