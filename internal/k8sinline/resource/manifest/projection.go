// internal/k8sinline/resource/manifest/projection.go
package manifest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// extractFieldPaths recursively extracts ONLY LEAF field paths from a Kubernetes object
// For example, given {"metadata": {"name": "foo"}}, returns ["metadata.name"] (NOT "metadata")
func extractFieldPaths(obj map[string]interface{}, prefix string) []string {
	var paths []string

	for key, value := range obj {
		var currentPath string
		if prefix == "" {
			currentPath = key
		} else {
			currentPath = prefix + "." + key
		}

		// DON'T add currentPath here - only add leaves!
		// paths = append(paths, currentPath)  // REMOVE THIS LINE

		// Recursively process nested objects
		if nested, ok := value.(map[string]interface{}); ok {
			nestedPaths := extractFieldPaths(nested, currentPath)
			paths = append(paths, nestedPaths...)
		} else if array, ok := value.([]interface{}); ok {
			// Handle arrays of objects (like containers)
			for i, item := range array {
				arrayPath := fmt.Sprintf("%s[%d]", currentPath, i)
				// DON'T add arrayPath here either!
				// paths = append(paths, arrayPath)  // REMOVE THIS LINE

				if nestedObj, ok := item.(map[string]interface{}); ok {
					nestedPaths := extractFieldPaths(nestedObj, arrayPath)
					paths = append(paths, nestedPaths...)
				} else {
					// This is a leaf value in an array
					paths = append(paths, arrayPath)
				}
			}
		} else {
			// This is a leaf value - NOW we add it
			paths = append(paths, currentPath)
		}
	}

	return paths
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

	// Remove this line - no filtering needed!
	// return filterServerGeneratedFields(projection), nil
	return projection, nil
}

// getFieldByPath retrieves a value from an object using dot notation
// Returns the value and whether it exists
func getFieldByPath(obj map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	current := obj

	for i, part := range parts {
		// Handle array notation like "containers[0]"
		if idx := strings.Index(part, "["); idx >= 0 {
			fieldName := part[:idx]
			arrayIdx := part[idx+1 : len(part)-1]

			field, ok := current[fieldName]
			if !ok {
				return nil, false
			}

			array, ok := field.([]interface{})
			if !ok {
				return nil, false
			}

			var index int
			fmt.Sscanf(arrayIdx, "%d", &index)
			if index >= len(array) {
				return nil, false
			}

			if i == len(parts)-1 {
				return array[index], true
			}

			current, ok = array[index].(map[string]interface{})
			if !ok {
				return nil, false
			}
		} else {
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
func setFieldByPath(obj map[string]interface{}, path string, value interface{}) error {
	parts := strings.Split(path, ".")
	current := obj

	for i, part := range parts {
		if i == len(parts)-1 {
			// Last part - set the value
			if idx := strings.Index(part, "["); idx >= 0 {
				fieldName := part[:idx]
				arrayIdx := part[idx+1 : len(part)-1]

				var index int
				fmt.Sscanf(arrayIdx, "%d", &index)

				// Ensure array exists and is large enough
				if current[fieldName] == nil {
					current[fieldName] = make([]interface{}, index+1)
				}

				array := current[fieldName].([]interface{})
				if len(array) <= index {
					newArray := make([]interface{}, index+1)
					copy(newArray, array)
					current[fieldName] = newArray
					array = newArray
				}

				array[index] = value
			} else {
				current[part] = value
			}
			return nil
		}

		// Not the last part - ensure the path exists
		if idx := strings.Index(part, "["); idx >= 0 {
			fieldName := part[:idx]
			arrayIdx := part[idx+1 : len(part)-1]

			var index int
			fmt.Sscanf(arrayIdx, "%d", &index)

			// Ensure array exists
			if current[fieldName] == nil {
				current[fieldName] = make([]interface{}, index+1)
			}

			array := current[fieldName].([]interface{})
			if len(array) <= index {
				newArray := make([]interface{}, index+1)
				copy(newArray, array)
				current[fieldName] = newArray
				array = newArray
			}

			// Ensure the array element is a map
			if array[index] == nil {
				array[index] = make(map[string]interface{})
			}

			var ok bool
			current, ok = array[index].(map[string]interface{})
			if !ok {
				return fmt.Errorf("path %s: expected object at array index %d", path, index)
			}
		} else {
			if current[part] == nil {
				current[part] = make(map[string]interface{})
			}

			var ok bool
			current, ok = current[part].(map[string]interface{})
			if !ok {
				return fmt.Errorf("path %s: expected object at %s", path, part)
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
