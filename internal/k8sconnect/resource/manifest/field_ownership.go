// internal/k8sconnect/resource/manifest/field_ownership.go
package manifest

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type FieldOwnership struct {
	Manager string `json:"manager"`
	Version string `json:"version,omitempty"`
}

func extractFieldOwnership(obj *unstructured.Unstructured) map[string]FieldOwnership {
	ownership := make(map[string]FieldOwnership)

	managedFields := obj.GetManagedFields()
	for _, mf := range managedFields {
		if mf.FieldsV1 == nil || mf.FieldsV1.Raw == nil {
			continue
		}

		// Parse the FieldsV1 JSON
		var fields map[string]interface{}
		if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
			continue
		}

		// Extract paths with dot notation
		paths := parseFieldsV1(fields, "")
		for _, path := range paths {
			ownership[path] = FieldOwnership{
				Manager: mf.Manager,
				Version: mf.APIVersion,
			}
		}
	}

	return ownership
}

func parseFieldsV1(fields map[string]interface{}, prefix string) []string {
	var paths []string

	for key, value := range fields {
		// Skip the field marker
		if key == "." {
			continue
		}

		// Build the path
		currentPath := key
		if prefix != "" {
			currentPath = prefix + "." + key
		}

		// Check if it's a nested field
		if nestedFields, ok := value.(map[string]interface{}); ok {
			paths = append(paths, parseFieldsV1(nestedFields, currentPath)...)
		} else {
			paths = append(paths, currentPath)
		}
	}

	return paths
}
