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

	// Collect ALL fields from ALL k8sconnect entries (both Apply and Update operations)
	allOwnedFields := make(map[string]interface{})

	for _, mf := range managedFields {
		fmt.Printf("Found manager: %s, operation: %s\n", mf.Manager, mf.Operation)
		if mf.Manager == "k8sconnect" && mf.FieldsV1 != nil {
			// Parse this entry's fields
			var fields map[string]interface{}
			if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
				fmt.Printf("Failed to parse FieldsV1 for %s/%s: %v\n", mf.Manager, mf.Operation, err)
				continue
			}

			fmt.Printf("Merging fields from k8sconnect/%s\n", mf.Operation)
			// Merge these fields into our accumulated ownership
			mergeFields(allOwnedFields, fields)
		}
	}

	if len(allOwnedFields) == 0 {
		fmt.Printf("No managedFields for k8sconnect, falling back\n")
		return extractFieldPaths(userJSON, "")
	}

	// Show what we own (combined from all k8sconnect entries)
	fieldsJSON, _ := json.MarshalIndent(allOwnedFields, "", "  ")
	fmt.Printf("Combined field ownership from k8sconnect:\n%s\n", fieldsJSON)

	// Extract owned paths
	paths := []string{}
	parseOwnedFields(allOwnedFields, "", userJSON, &paths)

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

	// FIX: Always add core fields even if not in user's YAML
	// This handles cases like namespace inference where Kubernetes adds the field
	coreFields := []string{
		"apiVersion",
		"kind",
		"metadata.name",
		"metadata.namespace",
	}

	for _, coreField := range coreFields {
		found := false
		for _, p := range paths {
			if p == coreField {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("Adding core field (always required): %s\n", coreField)
			paths = append(paths, coreField)
		}
	}

	fmt.Printf("Final extracted paths (%d total):\n", len(paths))
	for _, p := range paths {
		fmt.Printf("  - %s\n", p)
	}

	fmt.Printf("=== extractOwnedPaths END ===\n\n")

	return paths
}

// mergeFields recursively merges fields from source into dest
func mergeFields(dest, source map[string]interface{}) {
	for key, sourceVal := range source {
		if destVal, exists := dest[key]; exists {
			// Both have this key - need to merge if both are maps
			if destMap, ok := destVal.(map[string]interface{}); ok {
				if sourceMap, ok := sourceVal.(map[string]interface{}); ok {
					mergeFields(destMap, sourceMap)
					continue
				}
			}
		}
		// Otherwise just set/overwrite
		dest[key] = sourceVal
	}
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
	// Check if the user's specified fields match the corresponding merge key fields
	// This allows partial matching when user relies on K8s defaults

	// Count how many fields from merge key we can verify
	verifiableFields := 0
	matchedFields := 0

	for mergeField, mergeVal := range mergeKey {
		if itemVal, exists := item[mergeField]; exists {
			verifiableFields++
			if fmt.Sprintf("%v", itemVal) == fmt.Sprintf("%v", mergeVal) {
				matchedFields++
			}
		}
	}

	// If we could verify at least one field and all verifiable fields matched
	return verifiableFields > 0 && verifiableFields == matchedFields
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
