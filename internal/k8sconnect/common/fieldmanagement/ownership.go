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
// This is the core parsing logic that both resources can use.
//
// IMPORTANT: We only track ownership for the "k8sconnect" manager, not other managers.
// This is intentional - we don't control whether other controllers become co-owners
// (when they apply identical values with SSA), so tracking co-ownership would cause
// plan churn from external changes we can't prevent. We only care: "Do WE own this field?"
//
// When multiple managers co-own a field (identical values with SSA), we report "k8sconnect"
// if we're one of the co-owners. External co-ownership is not tracked.
func ParseFieldsV1ToPathMap(managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) map[string]FieldOwnership {
	result := make(map[string]FieldOwnership)

	// Only process k8sconnect's ownership entry
	// Ignore other managers (kubectl-patch, hpa-controller, etc.) even if they co-own fields
	for _, mf := range managedFields {
		if mf.Manager != "k8sconnect" {
			continue // Skip non-k8sconnect managers
		}

		if mf.FieldsV1 == nil {
			continue
		}

		var fields map[string]interface{}
		if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
			continue
		}

		// Extract paths owned by k8sconnect
		paths := extractPathsFromFieldsV1(fields, "", userJSON)

		// Record k8sconnect ownership for each path
		for _, path := range paths {
			// Skip internal k8sconnect annotations - these are implementation details
			// and should not be tracked as user-managed fields
			if strings.HasPrefix(path, "metadata.annotations.k8sconnect.terraform.io/") {
				continue
			}
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
//
// Like ParseFieldsV1ToPathMap, this only tracks k8sconnect ownership, not other managers.
func ExtractFieldOwnershipMap(obj *unstructured.Unstructured) map[string]string {
	result := make(map[string]string)

	for _, mf := range obj.GetManagedFields() {
		if mf.Manager != "k8sconnect" {
			continue // Only track k8sconnect ownership
		}

		if mf.FieldsV1 == nil {
			continue
		}

		var fields map[string]interface{}
		if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
			continue
		}

		// Extract paths owned by k8sconnect
		paths := extractPathsFromFieldsV1Simple(fields, "")
		for _, path := range paths {
			// Skip internal k8sconnect annotations - these are implementation details
			// and should not be tracked as user-managed fields
			if strings.HasPrefix(path, "metadata.annotations.k8sconnect.terraform.io/") {
				continue
			}
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

// ExtractAllFieldOwnership extracts field ownership for ALL managers, not just k8sconnect
// This is used for ownership transition detection to identify when external controllers
// take ownership of fields from k8sconnect.
//
// Unlike ExtractFieldOwnershipMap which only tracks k8sconnect ownership,
// this function returns ALL managers for each field path to properly handle shared ownership.
// When multiple managers co-own a field (via SSA), all of them are included in the slice.
func ExtractAllFieldOwnership(obj *unstructured.Unstructured) map[string][]string {
	result := make(map[string][]string)

	// Process ALL managers, not just k8sconnect
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
			// Skip internal k8sconnect annotations
			if strings.HasPrefix(path, "metadata.annotations.k8sconnect.terraform.io/") {
				continue
			}

			// Append this manager to the ownership list for this path
			// If multiple managers own the same field (co-ownership with SSA),
			// all of them are included in the slice.
			result[path] = append(result[path], mf.Manager)
		}
	}

	return result
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
