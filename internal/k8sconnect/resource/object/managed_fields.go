package object

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
)

// Type alias for compatibility
type ManagedFields = fieldmanagement.ManagedFields

// parseFieldsV1ToPathMap is a wrapper for the common implementation
func parseFieldsV1ToPathMap(managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) map[string]ManagedFields {
	return fieldmanagement.ParseFieldsV1ToPathMap(managedFields, userJSON)
}

// extractManagedFields returns ownership info for ALL fields
func extractManagedFields(obj *unstructured.Unstructured) map[string]ManagedFields {
	return fieldmanagement.ExtractManagedFields(obj)
}

// extractAllManagedFields extracts ownership for ALL managers (not just k8sconnect)
// This is used for ownership transition detection
func extractAllManagedFields(obj *unstructured.Unstructured) map[string][]string {
	return fieldmanagement.ExtractAllManagedFields(obj)
}

// updateManagedFieldsData updates the managed_fields attribute in the resource model
// It extracts, filters, flattens, and sets ownership data for clean UX
// NOTE: This function does NOT filter out ignore_fields from managed_fields.
// Users need visibility into who owns ignored fields. Filtering only status fields
// ensures consistency between plan and apply phases.
func updateManagedFieldsData(ctx context.Context, data *objectResourceModel, currentObj *unstructured.Unstructured) {
	// Extract ALL field ownership (map[string][]string)
	ownership := extractAllManagedFields(currentObj)

	// Filter out unwanted fields
	filteredOwnership := make(map[string][]string)
	for path, managers := range ownership {
		// Skip status fields - they're always owned by controllers and provide no actionable information
		if strings.HasPrefix(path, "status.") || path == "status" {
			continue
		}

		// Skip K8s system annotations that are added/updated unpredictably by controllers
		// These cause plan/apply inconsistencies because they appear after our dry-run prediction
		if fieldmanagement.IsKubernetesSystemAnnotation(path) {
			continue
		}

		filteredOwnership[path] = managers
	}

	// Flatten using the common logic
	ownershipMap := fieldmanagement.FlattenManagedFields(filteredOwnership)

	// Convert to types.Map
	mapValue, diags := types.MapValueFrom(ctx, types.StringType, ownershipMap)
	if diags.HasError() {
		tflog.Warn(ctx, "Failed to convert field ownership to map", map[string]interface{}{
			"diagnostics": diags,
		})
		// Set empty map on error
		emptyMap, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{})
		data.ManagedFields = emptyMap
	} else {
		data.ManagedFields = mapValue
	}
}

// saveOwnershipBaseline extracts ownership information from a K8s object
// and saves it to private state as a JSON-serialized baseline for drift detection (ADR-021).
// This baseline represents "what we owned at last Apply" and is NOT updated during Read operations.
func saveOwnershipBaseline(ctx context.Context, privateState interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}, obj *unstructured.Unstructured, ignoreFields []string) {
	// Extract ALL field ownership (map[string][]string)
	ownership := extractAllManagedFields(obj)

	// Flatten to map[string]string (first manager only, for simplicity)
	// This is sufficient for drift detection - we just need to know who owned what
	// IMPORTANT: Filter out ignored fields before saving to baseline (ADR-021 fix)
	baselineOwnership := make(map[string]string)
	for path, managers := range ownership {
		// Skip fields that are currently in ignore_fields
		if stringSliceContains(ignoreFields, path) {
			continue
		}
		if len(managers) > 0 {
			baselineOwnership[path] = managers[0] // Take first manager
		}
	}

	// Serialize to JSON
	baselineJSON, err := json.Marshal(baselineOwnership)
	if err != nil {
		tflog.Warn(ctx, "Failed to serialize ownership baseline", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	// Save to private state
	diags := privateState.SetKey(ctx, "ownership_baseline", baselineJSON)
	if diags.HasError() {
		tflog.Warn(ctx, "Failed to save ownership baseline to private state", map[string]interface{}{
			"diagnostics": diags,
		})
		return
	}

	tflog.Debug(ctx, "Saved ownership baseline to private state", map[string]interface{}{
		"field_count": len(baselineOwnership),
	})
}
