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
		fmt.Printf("No managedFields for k8sconnect, using all fields from YAML\n")
		// When no ownership info, extract ALL fields from user's YAML
		return extractAllFieldsFromYAML(userJSON, "")
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

	// Compare with what extractAllFieldsFromYAML would give us
	standardPaths := extractAllFieldsFromYAML(userJSON, "")
	fmt.Printf("Standard extractAllFieldsFromYAML would give %d paths\n", len(standardPaths))

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

// extractAllFieldsFromYAML - used when no managedFields available
// This needs to match the behavior tests expect
func extractAllFieldsFromYAML(obj map[string]interface{}, prefix string) []string {
	// Just call the full extractFieldPaths since tests expect that behavior
	return extractFieldPaths(obj, prefix)
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

// projectFields extracts values from source object based on field paths
// This function must handle whatever paths extractOwnedPaths produces
func projectFields(source map[string]interface{}, paths []string) (map[string]interface{}, error) {
	projection := make(map[string]interface{})

	for _, path := range paths {
		value, exists := getFieldByPath(source, path)
		if exists {
			if err := setFieldByPath(projection, path, value); err != nil {
				// Fail safely - if we can't project a path, it's better to fail
				// than to produce incorrect results
				return nil, fmt.Errorf("failed to set path %s: %w", path, err)
			}
		}
		// If path doesn't exist, that's fine - the field was deleted
	}

	return projection, nil
}

// getFieldByPath retrieves a value from an object using dot notation
// Must handle all path formats that extractOwnedPaths produces
func getFieldByPath(obj map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	current := obj

	for i, part := range parts {
		// Check for array notation
		if idx := strings.Index(part, "["); idx >= 0 {
			fieldName := part[:idx]
			selector := part[idx+1 : len(part)-1] // Remove [ and ]

			// Get the array field
			field, ok := current[fieldName]
			if !ok {
				return nil, false
			}

			array, ok := field.([]interface{})
			if !ok {
				return nil, false
			}

			// Check if this is a full array path (no selector details)
			if selector == "" || i == len(parts)-1 {
				// Return the entire array
				return array, true
			}

			// Handle key-based selector (e.g., name=nginx)
			if strings.Contains(selector, "=") {
				keyParts := strings.SplitN(selector, "=", 2)
				if len(keyParts) != 2 {
					return nil, false
				}
				mergeKey := keyParts[0]
				keyValue := keyParts[1]

				// Find the item with matching key
				var found map[string]interface{}
				for _, item := range array {
					if obj, ok := item.(map[string]interface{}); ok {
						if fmt.Sprint(obj[mergeKey]) == keyValue {
							found = obj
							break
						}
					}
				}

				if found == nil {
					return nil, false
				}

				if i == len(parts)-1 {
					return found, true
				}

				current = found
			} else {
				// Positional selector (e.g., 0, 1, 2)
				var index int
				fmt.Sscanf(selector, "%d", &index)
				if index >= len(array) || index < 0 {
					return nil, false
				}

				if i == len(parts)-1 {
					return array[index], true
				}

				current, ok = array[index].(map[string]interface{})
				if !ok {
					return nil, false
				}
			}
		} else {
			// Regular field access
			field, ok := current[part]
			if !ok {
				return nil, false
			}

			if i == len(parts)-1 {
				return field, true
			}

			current, ok = field.(map[string]interface{})
			if !ok {
				return nil, false
			}
		}
	}

	return nil, false
}

// setFieldByPath sets a value in an object using dot notation
// Creates intermediate objects as needed
func setFieldByPath(obj map[string]interface{}, path string, value interface{}) error {
	parts := strings.Split(path, ".")
	current := obj

	for i, part := range parts {
		if i == len(parts)-1 {
			// Last part - set the value
			if idx := strings.Index(part, "["); idx >= 0 {
				fieldName := part[:idx]
				// For array paths, we need to set the entire array value
				// This happens when using array-level tracking
				current[fieldName] = value
			} else {
				current[part] = value
			}
			return nil
		}

		// Not the last part - ensure the path exists
		if idx := strings.Index(part, "["); idx >= 0 {
			fieldName := part[:idx]
			selector := part[idx+1 : len(part)-1]

			// Ensure array exists
			if current[fieldName] == nil {
				current[fieldName] = make([]interface{}, 0)
			}

			array, ok := current[fieldName].([]interface{})
			if !ok {
				return fmt.Errorf("expected array at %s", fieldName)
			}

			// Handle key-based selector
			if strings.Contains(selector, "=") {
				keyParts := strings.SplitN(selector, "=", 2)
				if len(keyParts) != 2 {
					return fmt.Errorf("invalid selector: %s", selector)
				}
				mergeKey := keyParts[0]
				keyValue := keyParts[1]

				// Find or create the item
				var found map[string]interface{}
				for _, item := range array {
					if obj, ok := item.(map[string]interface{}); ok {
						if fmt.Sprint(obj[mergeKey]) == keyValue {
							found = obj
							break
						}
					}
				}

				if found == nil {
					// Create new item
					found = make(map[string]interface{})
					found[mergeKey] = keyValue
					array = append(array, found)
					current[fieldName] = array
				}

				current = found
			} else {
				// Positional selector
				var index int
				fmt.Sscanf(selector, "%d", &index)

				// Ensure array is large enough
				for len(array) <= index {
					array = append(array, make(map[string]interface{}))
				}
				current[fieldName] = array

				if array[index] == nil {
					array[index] = make(map[string]interface{})
				}

				var ok bool
				current, ok = array[index].(map[string]interface{})
				if !ok {
					return fmt.Errorf("expected object at array index %d", index)
				}
			}
		} else {
			// Regular field
			if current[part] == nil {
				current[part] = make(map[string]interface{})
			}

			var ok bool
			current, ok = current[part].(map[string]interface{})
			if !ok {
				return fmt.Errorf("expected object at %s", part)
			}
		}
	}

	return nil
}

// toJSON converts a map to JSON string
func toJSON(obj map[string]interface{}) (string, error) {
	bytes, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("failed to marshal projection: %w", err)
	}
	return string(bytes), nil
}

// absolutelyCertainStrategicMergeKeys maps field names to their merge keys
// Only include fields we're 100% certain about
var absolutelyCertainStrategicMergeKeys = map[string]string{
	"containers":     "name",
	"volumes":        "name",
	"env":            "name",
	"volumeMounts":   "name",
	"initContainers": "name",
}

// isPositionalArray returns true ONLY for arrays we're certain are positional
func isPositionalArray(fieldName string) bool {
	switch fieldName {
	case "args", "command":
		// These are definitely positional
		return true
	default:
		// When in doubt, return false (will use array-level tracking)
		return false
	}
}

// extractFieldPaths recursively extracts field paths from a Kubernetes object
// FAIL-SAFE DESIGN: When in doubt, track at array level rather than make assumptions
func extractFieldPaths(obj map[string]interface{}, prefix string) []string {
	var paths []string

	for key, value := range obj {
		var currentPath string
		if prefix == "" {
			currentPath = key
		} else {
			currentPath = prefix + "." + key
		}

		switch v := value.(type) {
		case map[string]interface{}:
			// Recurse into nested objects - this is always safe
			nestedPaths := extractFieldPaths(v, currentPath)
			paths = append(paths, nestedPaths...)

		case []interface{}:
			// CRITICAL DECISION POINT: How to handle arrays

			// Only use strategic merge for fields we're ABSOLUTELY certain about
			mergeKey, isKnownStrategic := absolutelyCertainStrategicMergeKeys[key]

			if isKnownStrategic && mergeKey != "" {
				// We're 100% certain this uses strategic merge
				// But we still need to validate ALL items have the merge key
				tempPaths := []string{}
				allItemsValid := true

				for _, item := range v {
					if obj, ok := item.(map[string]interface{}); ok {
						if keyValue, exists := obj[mergeKey]; exists && keyValue != nil && keyValue != "" {
							itemPath := fmt.Sprintf("%s[%s=%v]", currentPath, mergeKey, keyValue)
							tempPaths = append(tempPaths, extractFieldPaths(obj, itemPath)...)
						} else {
							// Missing or empty merge key - CRITICAL: fall back to array-level tracking
							allItemsValid = false
							break
						}
					} else {
						// Not an object - fall back to array-level tracking
						allItemsValid = false
						break
					}
				}

				if allItemsValid {
					// All items have valid merge keys - safe to use strategic merge
					paths = append(paths, tempPaths...)
				} else {
					// Something unexpected - track entire array to be safe
					// This prevents disasters from malformed resources
					paths = append(paths, currentPath)
				}
			} else if isPositionalArray(key) {
				// Arrays we know are positional (args, command)
				for i, item := range v {
					itemPath := fmt.Sprintf("%s[%d]", currentPath, i)
					if obj, ok := item.(map[string]interface{}); ok {
						paths = append(paths, extractFieldPaths(obj, itemPath)...)
					} else {
						// Scalar value in array
						paths = append(paths, itemPath)
					}
				}
			} else {
				// UNKNOWN ARRAY TYPE - USE ARRAY-LEVEL TRACKING
				// This is the safe default for anything we're not certain about
				paths = append(paths, currentPath)
			}

		default:
			// Leaf values (string, number, bool, nil)
			paths = append(paths, currentPath)
		}
	}

	return paths
}
