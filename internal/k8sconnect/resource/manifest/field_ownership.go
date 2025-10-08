// internal/k8sconnect/resource/manifest/field_ownership.go
package manifest

import (
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type FieldOwnership struct {
	Manager string `json:"manager"`
	Version string `json:"version,omitempty"`
}

// parseFieldsV1ToPathMap parses managedFields and returns a map of path -> FieldOwnership
// This is the core parsing logic that both functions can use
func parseFieldsV1ToPathMap(managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) map[string]FieldOwnership {
	result := make(map[string]FieldOwnership)

	// Process each field manager's fields
	for _, mf := range managedFields {
		if mf.FieldsV1 == nil {
			continue
		}

		var fields map[string]interface{}
		if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
			continue
		}

		// Extract paths owned by this manager
		paths := extractPathsFromFieldsV1(fields, "", userJSON)

		// Record ownership for each path
		for _, path := range paths {
			result[path] = FieldOwnership{
				Manager: mf.Manager,
				Version: mf.APIVersion,
			}
		}
	}

	return result
}

// extractPathsFromFieldsV1 recursively extracts field paths from FieldsV1 format
// Handles array keys like k:{"name":"nginx"}
func extractPathsFromFieldsV1(fields map[string]interface{}, prefix string, userJSON interface{}) []string {
	var paths []string

	for key, value := range fields {
		if strings.HasPrefix(key, "f:") {
			// Regular field
			fieldName := strings.TrimPrefix(key, "f:")
			currentPath := fieldName
			if prefix != "" {
				currentPath = prefix + "." + fieldName
			}

			if subFields, ok := value.(map[string]interface{}); ok && len(subFields) > 0 {
				// Has sub-fields - recurse
				if _, hasDot := subFields["."]; hasDot {
					// This field itself is owned
					paths = append(paths, currentPath)
				}

				// Get user's value for recursion
				var userValue interface{}
				if userMap, ok := userJSON.(map[string]interface{}); ok {
					userValue = userMap[fieldName]
				}

				// Recurse for nested fields
				nestedPaths := extractPathsFromFieldsV1(subFields, currentPath, userValue)
				paths = append(paths, nestedPaths...)
			} else {
				// Leaf field
				paths = append(paths, currentPath)
			}
		} else if strings.HasPrefix(key, "k:") {
			// Array key like k:{"name":"nginx"}
			// When we encounter a k: key, userJSON should be the array itself (from parent recursion)
			if userArray, ok := userJSON.([]interface{}); ok {
				// Parse the merge key to find which array element it refers to
				mergeKey, err := mergeKeyMatcher.ParseMergeKey(key)
				if err == nil {
					arrayIndex := mergeKeyMatcher.FindArrayIndex(userArray, mergeKey)
					if arrayIndex >= 0 {
						// Process array element fields
						if subFields, ok := value.(map[string]interface{}); ok {
							userElement := userArray[arrayIndex]
							elementPath := fmt.Sprintf("%s[%d]", prefix, arrayIndex)
							nestedPaths := extractPathsFromFieldsV1(subFields, elementPath, userElement)
							paths = append(paths, nestedPaths...)
						}
					}
				}
			}
		}
	}

	return paths
}

// extractFieldOwnership returns ownership info for ALL fields
func extractFieldOwnership(obj *unstructured.Unstructured) map[string]FieldOwnership {
	// Use the same parsing logic but return the full ownership map
	return parseFieldsV1ToPathMap(obj.GetManagedFields(), obj.Object)
}

var mergeKeyMatcher = NewMergeKeyMatcher()

// resolveArrayKey handles array keys like k:{"name":"nginx"}
// Returns empty string and -1 if not an array key or can't resolve
func resolveArrayKey(key string, prefix string, userJSON interface{}) (string, int) {
	fieldName, index := mergeKeyMatcher.ResolveArrayKey(key, userJSON)
	if fieldName == "" {
		return "", -1
	}

	arrayPath := fieldName
	if prefix != "" {
		arrayPath = prefix + "." + fieldName
	}
	return arrayPath, index
}

// addCoreFields adds fields that are always owned by the creator
func addCoreFields(paths []string, userJSON map[string]interface{}) []string {
	// Add core fields like apiVersion, kind, metadata.name
	coreFields := []string{"apiVersion", "kind", "metadata.name", "metadata.namespace"}
	for _, field := range coreFields {
		found := false
		for _, p := range paths {
			if p == field {
				found = true
				break
			}
		}
		if !found {
			paths = append(paths, field)
		}
	}
	return paths
}
