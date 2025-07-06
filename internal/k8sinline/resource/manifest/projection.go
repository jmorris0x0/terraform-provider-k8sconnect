// internal/k8sinline/resource/manifest/projection.go
package manifest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CRITICAL: This map ONLY contains fields we are 100% certain about.
// These are from the core Kubernetes API and have been stable since v1.
// If you're not sure, DO NOT add it here. Losing diff granularity by
// falling back to Array-level tracking is safer than being wrong.
var absolutelyCertainStrategicMergeKeys = map[string]string{
	// Pod spec containers - guaranteed since Kubernetes v1.0
	"containers":     "name",
	"initContainers": "name",
	"volumes":        "name",
	"volumeMounts":   "name",
	"env":            "name",

	// Only add more if you can point to the exact line in Kubernetes source code
	// that proves it uses strategic merge with that specific key
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

// projectFields extracts values from source object based on field paths
// This function must handle whatever paths extractFieldPaths produces
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
// Must handle all path formats that extractFieldPaths produces
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
