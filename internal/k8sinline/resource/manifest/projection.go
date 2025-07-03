// internal/k8sinline/resource/manifest/projection.go
package manifest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Strategic merge configurations for core Kubernetes resources
// Only includes the most common fields that everyone uses
var coreStrategicMergeKeys = map[string]string{
	"containers":       "name",
	"initContainers":   "name",
	"volumes":          "name",
	"volumeMounts":     "name",
	"env":              "name",
	"ports":            "containerPort", // For container ports
	"imagePullSecrets": "name",
}

// extractFieldPaths recursively extracts field paths from a Kubernetes object
// For strategic merge arrays, uses key-based paths (e.g., containers[name=nginx])
// For other arrays, uses positional paths (e.g., args[0])
// For CRDs and unknown arrays, falls back to array-level tracking
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
			// Recurse into nested objects
			nestedPaths := extractFieldPaths(v, currentPath)
			paths = append(paths, nestedPaths...)

		case []interface{}:
			// Handle arrays based on merge strategy
			if mergeKey, isStrategic := getStrategicMergeKey(key, prefix); isStrategic && mergeKey != "" {
				// Strategic merge array - use key-based paths
				for _, item := range v {
					if obj, ok := item.(map[string]interface{}); ok {
						if keyValue, exists := obj[mergeKey]; exists {
							// Create key-based path: containers[name=nginx]
							itemPath := fmt.Sprintf("%s[%s=%v]", currentPath, mergeKey, keyValue)
							paths = append(paths, extractFieldPaths(obj, itemPath)...)
						} else {
							// Item missing merge key - track whole array as fallback
							// Strategic merge array item missing key field
							// This is expected for some arrays, so we just track the whole array
							paths = append(paths, currentPath)
							break
						}
					}
				}
			} else if isLikelyCRDArray(key, v) {
				// For CRD arrays, use array-level tracking to avoid strategic merge issues
				paths = append(paths, currentPath)
				// For CRD arrays, use array-level tracking to avoid strategic merge issues
			} else {
				// Regular positional array (e.g., args, command)
				for i, item := range v {
					itemPath := fmt.Sprintf("%s[%d]", currentPath, i)
					if obj, ok := item.(map[string]interface{}); ok {
						paths = append(paths, extractFieldPaths(obj, itemPath)...)
					} else {
						// Scalar value in array
						paths = append(paths, itemPath)
					}
				}
			}

		default:
			// Leaf values (string, number, bool, nil)
			paths = append(paths, currentPath)
		}
	}

	return paths
}

// getStrategicMergeKey returns the merge key for known strategic merge arrays
func getStrategicMergeKey(fieldName, parentPath string) (string, bool) {
	// Special case for service ports (different from container ports)
	if fieldName == "ports" && strings.Contains(parentPath, "service.spec") {
		return "port", true
	}

	key, ok := coreStrategicMergeKeys[fieldName]
	return key, ok
}

// isLikelyCRDArray checks if an array might belong to a CRD
// Uses heuristics to detect CRD arrays that might use strategic merge
func isLikelyCRDArray(fieldName string, array []interface{}) bool {
	// Empty arrays aren't CRD-specific
	if len(array) == 0 {
		return false
	}

	// Check if this is a known core field
	if _, isCore := coreStrategicMergeKeys[fieldName]; isCore {
		return false
	}

	// If all items have a "name" field, it's likely a CRD with strategic merge
	hasNameField := true
	for _, item := range array {
		if obj, ok := item.(map[string]interface{}); ok {
			if _, hasName := obj["name"]; !hasName {
				hasNameField = false
				break
			}
		} else {
			// Not an object array
			return false
		}
	}

	return hasNameField
}

// projectFields extracts values from source object based on field paths
// Returns a new object containing only the specified paths
func projectFields(source map[string]interface{}, paths []string) (map[string]interface{}, error) {
	projection := make(map[string]interface{})

	for _, path := range paths {
		value, exists := getFieldByPath(source, path)
		if exists {
			if err := setFieldByPath(projection, path, value); err != nil {
				return nil, fmt.Errorf("failed to set path %s: %w", path, err)
			}
		}
	}

	return projection, nil
}

// getFieldByPath retrieves a value from an object using dot notation
// Handles both key-based paths (containers[name=nginx]) and positional paths (args[0])
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

// fromJSON converts a JSON string to map
func fromJSON(jsonStr string) (map[string]interface{}, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal projection: %w", err)
	}
	return obj, nil
}
