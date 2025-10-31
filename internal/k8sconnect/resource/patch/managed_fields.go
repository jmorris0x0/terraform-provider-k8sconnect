package patch

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
)

// Type alias for compatibility
type ManagedFields = fieldmanagement.ManagedFields

// updateManagedFieldsData updates the managed_fields attribute in the resource model
// It extracts, filters, flattens, and sets ownership data for clean UX
// For patch resources, we need to normalize the field manager name since it changes
// between plan (k8sconnect-patch-temp) and apply (k8sconnect-patch-{id})
func updateManagedFieldsData(ctx context.Context, data *patchResourceModel, currentObj *unstructured.Unstructured, fieldManager string) {
	// Extract ALL field ownership (map[string][]string)
	ownership := fieldmanagement.ExtractAllManagedFields(currentObj)

	// Apply filtering first, then flatten
	filteredOwnership := make(map[string][]string)
	for path, managers := range ownership {
		// Filter: Skip status fields
		if strings.HasPrefix(path, "status.") || path == "status" {
			continue
		}

		// Filter: Skip K8s system annotations that are added/updated unpredictably by controllers
		// These cause plan/apply inconsistencies because they appear after our dry-run prediction
		if fieldmanagement.IsKubernetesSystemAnnotation(path) {
			continue
		}

		// Filter: Only include fields owned by THIS patch's field manager
		// During CREATE plan, fieldManager is "k8sconnect-patch-temp"
		// During CREATE apply, fieldManager is "k8sconnect-patch-{id}"
		// We need to accept both to prevent inconsistencies
		hasOurManager := false
		ourManagers := make([]string, 0)
		for _, m := range managers {
			// Match exact field manager (works for UPDATE and CREATE apply)
			// OR match temp placeholder (works for CREATE plan)
			if m == fieldManager || m == "k8sconnect-patch-temp" {
				hasOurManager = true
				ourManagers = append(ourManagers, m)
			}
		}

		if !hasOurManager {
			continue
		}

		// Normalize field manager names to prevent plan/apply inconsistencies
		// Replace k8sconnect-patch-temp or k8sconnect-patch-{id} with generic "k8sconnect-patch"
		normalizedManagers := make([]string, 0, len(ourManagers))
		for _, m := range ourManagers {
			if strings.HasPrefix(m, "k8sconnect-patch-") {
				normalizedManagers = append(normalizedManagers, "k8sconnect-patch")
			} else {
				normalizedManagers = append(normalizedManagers, m)
			}
		}
		filteredOwnership[path] = normalizedManagers
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
