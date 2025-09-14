// internal/k8sconnect/resource/manifest/projection_v2.go
package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// extractOwnedPaths extracts field paths based on SSA field ownership
// In projection_v2.go, replace the stub:

func extractOwnedPaths(ctx context.Context, managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) []string {
	fmt.Printf("\n=== extractOwnedPaths START ===\n")

	// Find our field manager entry
	var ourFields *metav1.FieldsV1
	for _, mf := range managedFields {
		fmt.Printf("Found manager: %s\n", mf.Manager)
		if mf.Manager == "k8sconnect" {
			ourFields = mf.FieldsV1
			fmt.Printf("Using k8sconnect fields\n")
			break
		}
	}

	if ourFields == nil {
		fmt.Printf("No managedFields for k8sconnect, falling back\n")
		return extractFieldPaths(userJSON, "")
	}

	// Parse the FieldsV1 JSON
	var fields map[string]interface{}
	if err := json.Unmarshal(ourFields.Raw, &fields); err != nil {
		fmt.Printf("Failed to parse FieldsV1: %v\n", err)
		return extractFieldPaths(userJSON, "")
	}

	// Show what we own
	fieldsJSON, _ := json.MarshalIndent(fields, "", "  ")
	fmt.Printf("Field ownership:\n%s\n", fieldsJSON)

	// Extract owned paths
	paths := []string{}
	extractOwnedFieldPaths(fields, "", userJSON, &paths)

	fmt.Printf("Extracted paths (%d total):\n", len(paths))
	for _, p := range paths {
		fmt.Printf("  - %s\n", p)
	}
	fmt.Printf("=== extractOwnedPaths END ===\n\n")

	return paths
}

func extractOwnedFieldPaths(ownership map[string]interface{}, prefix string, userJSON map[string]interface{}, paths *[]string) {
	for key, value := range ownership {
		if strings.HasPrefix(key, "f:") {
			fieldName := strings.TrimPrefix(key, "f:")
			currentPath := fieldName
			if prefix != "" {
				currentPath = prefix + "." + fieldName
			}

			if subFields, ok := value.(map[string]interface{}); ok {
				// Recurse into nested fields
				extractOwnedFieldPaths(subFields, currentPath, getUserField(userJSON, fieldName), paths)
			} else {
				// Leaf field - add to paths
				*paths = append(*paths, currentPath)
			}
		} else if strings.HasPrefix(key, "k:") {
			// Array item - need to map to index
			fmt.Printf("Found array key: %s\n", key)
			// TODO: Parse the key and map to array index
		}
	}
}

func getUserField(obj map[string]interface{}, field string) map[string]interface{} {
	if obj == nil {
		return nil
	}
	if val, ok := obj[field]; ok {
		if m, ok := val.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}
