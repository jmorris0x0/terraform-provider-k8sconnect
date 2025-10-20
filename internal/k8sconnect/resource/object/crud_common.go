// internal/k8sconnect/resource/object/crud_common.go
package object

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

// checkResourceExistenceAndOwnership checks if resource exists and verifies ownership
func (r *objectResource) checkResourceExistenceAndOwnership(ctx context.Context, rc *ResourceContext, data *objectResourceModel, resp *resource.CreateResponse) error {
	// Skip check if GVR is empty (CRD not found during prepareContext)
	// The apply retry logic will handle this case
	if rc.GVR.Empty() {
		tflog.Debug(ctx, "Skipping existence check - GVR not available (likely CRD not found yet)")
		return nil
	}

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
	} else if !errors.IsNotFound(err) && !r.isNamespaceNotFoundError(err) {
		// If namespace doesn't exist, resource can't exist either - skip check
		// The apply retry logic will handle this case
		if r.isNamespaceNotFoundError(err) {
			tflog.Debug(ctx, "Namespace doesn't exist yet - skipping existence check", map[string]interface{}{
				"namespace": rc.Object.GetNamespace(),
				"resource":  rc.Object.GetName(),
			})
			return nil
		}

		// Real error checking if resource exists
		resp.Diagnostics.AddError("Existence Check Failed",
			fmt.Sprintf("Failed to check if resource exists: %s", err))
		return err
	}
	return nil
}

// applyResourceWithConflictHandling applies resource and handles field conflicts.
// Omits ignore_fields from the Apply patch to avoid taking ownership of those fields.
// applyWithCRDRetry applies a resource with automatic retry for missing dependencies
// This enables CRD/CR and namespace/resource to be applied together in a single terraform apply
func (r *objectResource) applyWithCRDRetry(ctx context.Context, client k8sclient.K8sClient, obj *unstructured.Unstructured, opts k8sclient.ApplyOptions) error {
	// Dependency retry backoff schedule: fast initial retries, ~30s total
	backoff := []time.Duration{
		100 * time.Millisecond,
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		5 * time.Second,
		10 * time.Second,
		10 * time.Second,
	}

	var lastErr error
	for attempt, delay := range backoff {
		// Try the apply operation
		err := client.Apply(ctx, obj, opts)
		if err == nil {
			// Success!
			if attempt > 0 {
				tflog.Info(ctx, "Resource applied successfully after dependency retry", map[string]interface{}{
					"attempts": attempt + 1,
					"kind":     obj.GetKind(),
					"name":     obj.GetName(),
				})
			}
			return nil
		}

		// Check if this is a dependency not ready error (CRD or namespace)
		if !r.isDependencyNotReadyError(err) {
			// Different error type - return immediately
			return err
		}

		lastErr = err

		// Log retry attempt with appropriate message
		reason := "CRD"
		if r.isNamespaceNotFoundError(err) {
			reason = "Namespace"
		}
		tflog.Debug(ctx, fmt.Sprintf("%s not ready, retrying", reason), map[string]interface{}{
			"attempt": attempt + 1,
			"delay":   delay,
			"kind":    obj.GetKind(),
			"name":    obj.GetName(),
		})

		// Wait before retry, respecting context cancellation
		select {
		case <-time.After(delay):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// All retries exhausted - return enhanced error
	return fmt.Errorf(
		"CRD for %s/%s not found after 30s.\n\n"+
			"This usually means:\n"+
			"1. The CRD doesn't exist and won't be created\n"+
			"2. The CRD is being created but needs more time to establish\n"+
			"3. There's a typo in the apiVersion or kind\n\n"+
			"Solutions:\n"+
			"- Ensure CRD resource has depends_on relationship\n"+
			"- Verify the CRD name matches the CR's apiVersion\n"+
			"- Apply CRDs first: terraform apply -target=<crd_resource>\n\n"+
			"Original error: %v",
		obj.GetKind(), obj.GetName(), lastErr,
	)
}

func (r *objectResource) applyResourceWithConflictHandling(ctx context.Context, rc *ResourceContext, data *objectResourceModel, resp interface{}, operation string) error {
	// Prepare the object to apply
	objToApply := rc.Object.DeepCopy()

	// On Update, filter out ignored fields to release ownership to other controllers
	// On Create, send everything to establish initial state
	if operation == "Update" {
		if ignoreFields := getIgnoreFields(ctx, data); ignoreFields != nil {
			objToApply = removeFieldsFromObject(objToApply, ignoreFields)
			tflog.Debug(ctx, "Filtered ignored fields from Apply patch", map[string]interface{}{
				"ignored_fields": ignoreFields,
			})
		}
	}

	// Apply the resource with CRD retry (always force conflicts)
	err := r.applyWithCRDRetry(ctx, rc.Client, objToApply, k8sclient.ApplyOptions{
		FieldManager:    "k8sconnect",
		Force:           true,     // Always force ownership of conflicted fields
		FieldValidation: "Strict", // ADR-017: Validate fields against OpenAPI schema during apply
	})

	if err != nil {
		if isFieldConflictError(err) {
			r.addFieldConflictError(resp, operation)
		} else {
			resourceDesc := fmt.Sprintf("%s %s", rc.Object.GetKind(), rc.Object.GetName())
			r.addOperationError(resp, operation, resourceDesc, err)
		}
		return err
	}
	return nil
}

// readResourceAfterCreate reads resource back to get managedFields (Phase 2)
func (r *objectResource) readResourceAfterCreate(ctx context.Context, rc *ResourceContext) {
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

// verifyOwnership checks if resource is owned by this Terraform resource
func (r *objectResource) verifyOwnership(currentObj *unstructured.Unstructured, expectedID string, obj *unstructured.Unstructured, resp *resource.ReadResponse) error {
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
func (r *objectResource) updateProjectionFromCurrent(ctx context.Context, data *objectResourceModel, currentObj, obj *unstructured.Unstructured) error {
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

	// Convert projection to flat map for clean diff display
	projectionMap := flattenProjectionToMap(projection, paths)

	// Convert to types.Map
	mapValue, diags := types.MapValueFrom(ctx, types.StringType, projectionMap)
	if diags.HasError() {
		tflog.Warn(ctx, "Failed to convert projection to map", map[string]interface{}{
			"diagnostics": diags,
		})
		// Set empty map on error
		emptyMap, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{})
		data.ManagedStateProjection = emptyMap
	} else {
		data.ManagedStateProjection = mapValue
	}

	tflog.Debug(ctx, "Updated managed state projection", map[string]interface{}{
		"id":         data.ID.ValueString(),
		"path_count": len(paths),
		"map_size":   len(projectionMap),
	})

	return nil
}

// updateFieldOwnershipData updates field ownership tracking data
func (r *objectResource) updateFieldOwnershipData(ctx context.Context, data *objectResourceModel, currentObj *unstructured.Unstructured) {
	ownership := extractFieldOwnership(currentObj)

	// Convert map[string]FieldOwnership to map[string]string (just manager names)
	// Filter out status fields - they're always owned by controllers and provide no actionable information
	ownershipMap := make(map[string]string, len(ownership))
	for path, owner := range ownership {
		// Skip status fields - they're read-only subresources managed by controllers
		// (similar to how status is filtered in yaml.go during object cleanup)
		if strings.HasPrefix(path, "status.") || path == "status" {
			continue
		}
		ownershipMap[path] = owner.Manager
	}

	// Convert to types.Map
	mapValue, diags := types.MapValueFrom(ctx, types.StringType, ownershipMap)
	if diags.HasError() {
		tflog.Warn(ctx, "Failed to convert field ownership to map", map[string]interface{}{
			"diagnostics": diags,
		})
		// Set empty map on error
		emptyMap, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{})
		data.FieldOwnership = emptyMap
	} else {
		data.FieldOwnership = mapValue
	}
}

// Error handling helpers
func (r *objectResource) addFieldConflictError(resp interface{}, operation string) {
	if createResp, ok := resp.(*resource.CreateResponse); ok {
		createResp.Diagnostics.AddError("Field Manager Conflict",
			"Another controller owns fields you're trying to set. "+
				"Add conflicting paths to ignore_fields to release ownership.")
	} else if updateResp, ok := resp.(*resource.UpdateResponse); ok {
		updateResp.Diagnostics.AddError("Field Manager Conflict",
			"Another controller owns fields you're trying to set. "+
				"Add conflicting paths to ignore_fields to release ownership.")
	}
}

func (r *objectResource) addOperationError(resp interface{}, operation string, resourceDesc string, err error) {
	// Classify the error for user-friendly messages
	severity, title, detail := r.classifyK8sError(err, operation, resourceDesc)

	if createResp, ok := resp.(*resource.CreateResponse); ok {
		if severity == "warning" {
			createResp.Diagnostics.AddWarning(title, detail)
		} else {
			createResp.Diagnostics.AddError(title, detail)
		}
	} else if updateResp, ok := resp.(*resource.UpdateResponse); ok {
		if severity == "warning" {
			updateResp.Diagnostics.AddWarning(title, detail)
		} else {
			updateResp.Diagnostics.AddError(title, detail)
		}
	}
}

// Utility functions
func isFieldConflictError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "conflict")
}

// getIgnoreFields extracts the ignore_fields list from the model.
// Returns nil if ignore_fields is not set or empty.
func getIgnoreFields(ctx context.Context, data *objectResourceModel) []string {
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
