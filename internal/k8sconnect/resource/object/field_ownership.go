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
type FieldOwnership = fieldmanagement.FieldOwnership

// parseFieldsV1ToPathMap is a wrapper for the common implementation
func parseFieldsV1ToPathMap(managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) map[string]FieldOwnership {
	return fieldmanagement.ParseFieldsV1ToPathMap(managedFields, userJSON)
}

// extractFieldOwnership returns ownership info for ALL fields
func extractFieldOwnership(obj *unstructured.Unstructured) map[string]FieldOwnership {
	return fieldmanagement.ExtractFieldOwnership(obj)
}

// extractAllFieldOwnership extracts ownership for ALL managers (not just k8sconnect)
// This is used for ownership transition detection
func extractAllFieldOwnership(obj *unstructured.Unstructured) map[string][]string {
	return fieldmanagement.ExtractAllFieldOwnership(obj)
}

// updateFieldOwnershipData updates the field_ownership attribute in the resource model
// It extracts, filters, flattens, and sets ownership data for clean UX
// NOTE: This function does NOT filter out ignore_fields from field_ownership.
// Users need visibility into who owns ignored fields. Filtering only status fields
// ensures consistency between plan and apply phases.
func updateFieldOwnershipData(ctx context.Context, data *objectResourceModel, currentObj *unstructured.Unstructured) {
	// Extract ALL field ownership (map[string][]string)
	ownership := extractAllFieldOwnership(currentObj)

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
	ownershipMap := fieldmanagement.FlattenFieldOwnership(filteredOwnership)

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

// isInIgnoreFields checks if a field path matches any pattern in ignore_fields
func isInIgnoreFields(ctx context.Context, path string, ignoreFields types.List) bool {
	if ignoreFields.IsNull() || ignoreFields.IsUnknown() {
		return false
	}

	var patterns []string
	diags := ignoreFields.ElementsAs(ctx, &patterns, false)
	if diags.HasError() || len(patterns) == 0 {
		return false
	}

	for _, pattern := range patterns {
		// Exact match
		if path == pattern {
			return true
		}
		// TODO: Add JSONPath predicate matching if needed
		// For now, exact match is sufficient
	}
	return false
}

// saveOwnershipBaseline extracts ownership information from a K8s object
// and saves it to private state as a JSON-serialized baseline for drift detection (ADR-021).
// This baseline represents "what we owned at last Apply" and is NOT updated during Read operations.
func saveOwnershipBaseline(ctx context.Context, privateState interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}, obj *unstructured.Unstructured) {
	// Extract ALL field ownership (map[string][]string)
	ownership := extractAllFieldOwnership(obj)

	// Flatten to map[string]string (first manager only, for simplicity)
	// This is sufficient for drift detection - we just need to know who owned what
	baselineOwnership := make(map[string]string)
	for path, managers := range ownership {
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
