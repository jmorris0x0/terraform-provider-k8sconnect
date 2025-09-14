// internal/k8sconnect/resource/manifest/projection_v2.go
package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func extractOwnedPaths(ctx context.Context, managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) []string {
	fmt.Printf("\n=== extractOwnedPaths START ===\n")

	// Debug: show what's in userJSON
	userJSONBytes, _ := json.MarshalIndent(userJSON, "", "  ")
	fmt.Printf("UserJSON input:\n%s\n", userJSONBytes)

	// Find our field manager entry
	var ourFields *metav1.FieldsV1
	for _, mf := range managedFields {
		fmt.Printf("Found manager: %s, operation: %s\n", mf.Manager, mf.Operation)
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
	fmt.Printf("Field ownership from k8sconnect:\n%s\n", fieldsJSON)

	// Extract owned paths
	paths := []string{}
	parseOwnedFields(fields, "", userJSON, &paths)

	fmt.Printf("Extracted owned paths (%d total):\n", len(paths))
	for _, p := range paths {
		fmt.Printf("  - %s\n", p)
	}

	// Compare with what extractFieldPaths would give us
	standardPaths := extractFieldPaths(userJSON, "")
	fmt.Printf("Standard extractFieldPaths would give %d paths\n", len(standardPaths))

	// ALSO include fields from user's YAML that aren't covered by managedFields
	// These are fields like apiVersion, kind, metadata.name that are "owned" by creation
	fmt.Printf("Adding core fields...\n")

	for _, userPath := range standardPaths {
		// Add if not already in paths
		found := false
		for _, p := range paths {
			if p == userPath {
				found = true
				break
			}
		}
		if !found {
			// Check if this is a basic field we should always include
			if shouldIncludeUserField(userPath) {
				fmt.Printf("Adding core field: %s\n", userPath)
				paths = append(paths, userPath)
			}
		}
	}

	fmt.Printf("Final extracted paths (%d total):\n", len(paths))
	for _, p := range paths {
		fmt.Printf("  - %s\n", p)
	}

	fmt.Printf("=== extractOwnedPaths END ===\n\n")

	return paths
}

func parseOwnedFields(ownership map[string]interface{}, prefix string, userObj interface{}, paths *[]string) {
	// Handle nil userObj
	if userObj == nil {
		return
	}

	// Type switch on userObj to handle different types
	userMap, isMap := userObj.(map[string]interface{})
	userArray, isArray := userObj.([]interface{})

	for key, value := range ownership {
		if strings.HasPrefix(key, "f:") {
			// Regular field
			fieldName := strings.TrimPrefix(key, "f:")
			currentPath := fieldName
			if prefix != "" {
				currentPath = prefix + "." + fieldName
			}

			// Check if this field exists in user's object
			var userFieldValue interface{}
			if isMap {
				userFieldValue = userMap[fieldName]
			}

			if subFields, ok := value.(map[string]interface{}); ok && len(subFields) > 0 {
				// Has sub-fields - recurse
				// Skip the "." marker if present
				if _, hasDot := subFields["."]; hasDot && len(subFields) == 1 {
					// Just a marker, this is a leaf
					*paths = append(*paths, currentPath)
				} else {
					// Has actual sub-fields
					parseOwnedFields(subFields, currentPath, userFieldValue, paths)
				}
			} else {
				// Leaf field
				*paths = append(*paths, currentPath)
			}

		} else if strings.HasPrefix(key, "k:") {
			// Array item with merge key
			mergeKeyJSON := strings.TrimPrefix(key, "k:")
			fmt.Printf("Processing array key: %s at prefix: %s\n", mergeKeyJSON, prefix)

			// Parse the merge key to find which array item this refers to
			var mergeKey map[string]interface{}
			if err := json.Unmarshal([]byte(mergeKeyJSON), &mergeKey); err != nil {
				fmt.Printf("Failed to parse merge key: %v\n", err)
				continue
			}

			// Find the matching array index in userObj
			if !isArray {
				fmt.Printf("Expected array but got %T\n", userObj)
				continue
			}

			arrayIndex := -1
			for i, item := range userArray {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if matchesMergeKey(itemMap, mergeKey) {
						arrayIndex = i
						break
					}
				}
			}

			if arrayIndex == -1 {
				fmt.Printf("Could not find array item matching merge key: %v\n", mergeKey)
				continue
			}

			// Now process the fields within this array item
			if subFields, ok := value.(map[string]interface{}); ok {
				arrayItemPath := fmt.Sprintf("%s[%d]", prefix, arrayIndex)
				var arrayItemObj interface{}
				if arrayIndex < len(userArray) {
					arrayItemObj = userArray[arrayIndex]
				}
				parseOwnedFields(subFields, arrayItemPath, arrayItemObj, paths)
			}

		} else if key == "." {
			// Skip the "." marker - it just indicates the parent field itself is owned
			continue
		} else {
			fmt.Printf("Unknown key format: %s\n", key)
		}
	}
}

// matchesMergeKey checks if an item matches the merge key criteria
func matchesMergeKey(item map[string]interface{}, mergeKey map[string]interface{}) bool {
	for k, v := range mergeKey {
		itemVal, exists := item[k]
		if !exists {
			return false
		}
		// Convert both to strings for comparison (handles int vs float issues)
		if fmt.Sprintf("%v", itemVal) != fmt.Sprintf("%v", v) {
			return false
		}
	}
	return true
}

// shouldIncludeUserField checks if a field from user's YAML should always be included
func shouldIncludeUserField(path string) bool {
	// Always include these core fields that define the resource identity
	coreFields := []string{
		"apiVersion",
		"kind",
		"metadata.name",
		"metadata.namespace",
	}

	for _, core := range coreFields {
		if path == core {
			return true
		}
	}

	return false
}
