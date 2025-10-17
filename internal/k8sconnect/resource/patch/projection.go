// internal/k8sconnect/resource/patch/projection.go
package patch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// extractPatchedPaths gets field paths that this patch owns from managedFields
// For patches, we only look at fields owned by our specific field manager
func extractPatchedPaths(ctx context.Context, managedFields []metav1.ManagedFieldsEntry, fieldManager string) []string {
	var paths []string

	for _, mf := range managedFields {
		if mf.Manager == fieldManager && mf.FieldsV1 != nil {
			var fields map[string]interface{}
			if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
				tflog.Warn(ctx, "Failed to parse managedFields for patch projection", map[string]interface{}{
					"manager": fieldManager,
					"error":   err.Error(),
				})
				continue
			}

			// Extract paths from FieldsV1 structure
			parsePatchFieldsV1(fields, "", &paths)
		}
	}

	return paths
}

// parsePatchFieldsV1 extracts field paths from FieldsV1 structure
// Simplified version for patches - just extracts the owned paths
func parsePatchFieldsV1(fields map[string]interface{}, prefix string, paths *[]string) {
	for key, value := range fields {
		if strings.HasPrefix(key, "f:") {
			// Regular field
			fieldName := strings.TrimPrefix(key, "f:")
			currentPath := fieldName
			if prefix != "" {
				currentPath = prefix + "." + fieldName
			}

			if subFields, ok := value.(map[string]interface{}); ok && len(subFields) > 0 {
				// Check if it's just a marker or has actual sub-fields
				if _, hasDot := subFields["."]; hasDot && len(subFields) == 1 {
					// Just a marker, this is a leaf
					*paths = append(*paths, currentPath)
				} else {
					// Has actual sub-fields, recurse
					parsePatchFieldsV1(subFields, currentPath, paths)
				}
			} else {
				// Leaf field
				*paths = append(*paths, currentPath)
			}
		} else if key == "." {
			// Skip the "." marker
			continue
		}
		// For patches, we skip array handling (k:) since we're just showing what was patched
		// The patched state from dry-run already has the correct structure
	}
}

// projectPatchedFields extracts values from the patched object based on paths
// Simplified version for patches
func projectPatchedFields(source map[string]interface{}, paths []string) (map[string]interface{}, error) {
	projection := make(map[string]interface{})

	for _, path := range paths {
		value, exists := getFieldByPathSimple(source, path)
		if exists {
			if err := setFieldByPathSimple(projection, path, value); err != nil {
				return nil, fmt.Errorf("failed to set path %s: %w", path, err)
			}
		}
	}

	return projection, nil
}

// getFieldByPathSimple retrieves a value using simple dot notation (no array selectors)
func getFieldByPathSimple(obj map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	var current interface{} = obj

	for i, part := range parts {
		currentMap, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}

		value, exists := currentMap[part]
		if !exists {
			return nil, false
		}

		if i == len(parts)-1 {
			return value, true
		}
		current = value
	}

	return nil, false
}

// setFieldByPathSimple sets a value using simple dot notation (no array selectors)
func setFieldByPathSimple(obj map[string]interface{}, path string, value interface{}) error {
	parts := strings.Split(path, ".")
	current := obj

	for i, part := range parts {
		if i == len(parts)-1 {
			// Set the final value
			current[part] = value
			return nil
		}

		// Ensure the intermediate path exists
		if current[part] == nil {
			current[part] = make(map[string]interface{})
		}

		next, ok := current[part].(map[string]interface{})
		if !ok {
			return fmt.Errorf("field %s is not an object", part)
		}
		current = next
	}

	return nil
}

// flattenPatchProjectionToMap converts nested projection to flat key-value map
// Simplified version for patches
func flattenPatchProjectionToMap(projection map[string]interface{}, paths []string) map[string]string {
	result := make(map[string]string, len(paths))

	for _, path := range paths {
		value, exists := getFieldByPathSimple(projection, path)
		if exists {
			result[path] = formatPatchValue(value)
		}
	}

	return result
}

// formatPatchValue converts a value to string for display
func formatPatchValue(v interface{}) string {
	if v == nil {
		return "<nil>"
	}

	switch val := v.(type) {
	case string:
		return val
	case int, int32, int64, float32, float64, bool:
		return fmt.Sprintf("%v", val)
	case map[string]interface{}, []interface{}:
		// Complex types - use compact JSON
		bytes, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("<error: %v>", err)
		}
		return string(bytes)
	default:
		return fmt.Sprintf("%v", val)
	}
}
