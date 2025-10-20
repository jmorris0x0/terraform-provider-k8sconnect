package fieldmanagement

import (
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FieldOwnership represents ownership information for a field
type FieldOwnership struct {
	Manager string `json:"manager"`
	Version string `json:"version,omitempty"`
}

// ParseFieldsV1ToPathMap parses managedFields and returns a map of path -> FieldOwnership
// This is the core parsing logic that both resources can use
func ParseFieldsV1ToPathMap(managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) map[string]FieldOwnership {
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
	mergeKeyMatcher := NewMergeKeyMatcher()

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

// ExtractFieldOwnership returns ownership info for ALL fields
func ExtractFieldOwnership(obj *unstructured.Unstructured) map[string]FieldOwnership {
	// Use the same parsing logic but return the full ownership map
	return ParseFieldsV1ToPathMap(obj.GetManagedFields(), obj.Object)
}

// ExtractFieldOwnershipMap extracts field ownership as a simple map[path]manager
// This is a simplified version that returns just the manager name (not full FieldOwnership)
func ExtractFieldOwnershipMap(obj *unstructured.Unstructured) map[string]string {
	result := make(map[string]string)

	for _, mf := range obj.GetManagedFields() {
		if mf.FieldsV1 == nil {
			continue
		}

		var fields map[string]interface{}
		if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
			continue
		}

		// Extract paths owned by this manager
		paths := extractPathsFromFieldsV1Simple(fields, "")
		for _, path := range paths {
			result[path] = mf.Manager
		}
	}

	return result
}

// extractPathsFromFieldsV1Simple is a simplified version that doesn't need user JSON
// Used when we don't need array element resolution
func extractPathsFromFieldsV1Simple(fields map[string]interface{}, prefix string) []string {
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

				// Recurse for nested fields
				nestedPaths := extractPathsFromFieldsV1Simple(subFields, currentPath)
				paths = append(paths, nestedPaths...)
			} else {
				// Leaf field
				paths = append(paths, currentPath)
			}
		} else if strings.HasPrefix(key, "k:") {
			// Array element - we'll use simplified handling
			if subFields, ok := value.(map[string]interface{}); ok {
				// Extract array key info for path
				arrayKey := strings.TrimPrefix(key, "k:")
				arrayPath := fmt.Sprintf("%s%s", prefix, arrayKey)
				nestedPaths := extractPathsFromFieldsV1Simple(subFields, arrayPath)
				paths = append(paths, nestedPaths...)
			}
		}
	}

	return paths
}

// ExtractFieldOwnershipForPaths extracts ownership info for specific field paths
func ExtractFieldOwnershipForPaths(obj *unstructured.Unstructured, paths []string) map[string]string {
	result := make(map[string]string)

	// Get all ownership info
	allOwnership := ExtractFieldOwnershipMap(obj)

	// Filter to only the paths we care about
	for _, path := range paths {
		if owner, exists := allOwnership[path]; exists {
			result[path] = owner
		}
	}

	return result
}

// ExtractManagedFieldsForManager extracts the managed fields JSON for a specific field manager
func ExtractManagedFieldsForManager(obj *unstructured.Unstructured, fieldManager string) (string, error) {
	for _, mf := range obj.GetManagedFields() {
		if mf.Manager == fieldManager {
			if mf.FieldsV1 != nil {
				// Convert to a more readable JSON format
				var fields map[string]interface{}
				if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
					return "", fmt.Errorf("failed to parse managed fields: %w", err)
				}

				// Convert back to JSON string
				jsonBytes, err := json.Marshal(fields)
				if err != nil {
					return "", fmt.Errorf("failed to marshal managed fields: %w", err)
				}

				return string(jsonBytes), nil
			}
		}
	}

	// No fields managed by this manager
	return "{}", nil
}

// ExtractFieldPathsFromManagedFieldsJSON extracts field paths from a managed fields JSON string
// This is used when managed fields have been stored as a JSON string in Terraform state
// The JSON format is FieldsV1 format: {"f:data":{"f:field1":{},"f:field2":{}}}
func ExtractFieldPathsFromManagedFieldsJSON(managedFieldsJSON string) ([]string, error) {
	// Parse the JSON string
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(managedFieldsJSON), &fields); err != nil {
		return nil, fmt.Errorf("failed to parse managed fields JSON: %w", err)
	}

	// Extract field paths using the common logic
	paths := extractPathsFromFieldsV1Simple(fields, "")
	return paths, nil
}
