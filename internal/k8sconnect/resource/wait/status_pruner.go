// internal/k8sconnect/resource/wait/status_pruner.go
package wait

import (
	"fmt"
	"strconv"
	"strings"
)

// pruneStatusToField extracts only the specified field path from the full status
// and returns it in the exact same structure
func pruneStatusToField(fullStatus map[string]interface{}, fieldPath string) map[string]interface{} {
	if fullStatus == nil || fieldPath == "" {
		return nil
	}

	path := strings.TrimPrefix(fieldPath, "status.")
	segments, err := parseFieldPath(path)
	if err != nil {
		return nil
	}

	// Navigate to get the value
	value, found, err := navigateToValue(fullStatus, segments)
	if !found || err != nil {
		return nil
	}

	// Rebuild the exact structure
	result, err := rebuildExactStructure(segments, value)
	if err != nil {
		return nil
	}

	return result
}

type pathSegment struct {
	name    string
	index   int
	isField bool
	isArray bool
}

func parseFieldPath(path string) ([]pathSegment, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}

	var result []pathSegment
	parts := strings.Split(path, ".")

	for _, part := range parts {
		if part == "" {
			continue
		}

		// Check for array notation like "ingress[0]"
		if idx := strings.Index(part, "["); idx > 0 {
			fieldName := part[:idx]
			remainder := part[idx:]

			// Add the field name
			result = append(result, pathSegment{name: fieldName, isField: true})

			// Parse array indices
			for len(remainder) > 0 {
				if !strings.HasPrefix(remainder, "[") {
					return nil, fmt.Errorf("invalid array notation in %s", part)
				}

				endIdx := strings.Index(remainder, "]")
				if endIdx == -1 {
					return nil, fmt.Errorf("unclosed bracket in %s", part)
				}

				indexStr := remainder[1:endIdx]
				index, err := strconv.Atoi(indexStr)
				if err != nil {
					return nil, fmt.Errorf("invalid array index %s", indexStr)
				}

				result = append(result, pathSegment{index: index, isArray: true})
				remainder = remainder[endIdx+1:]
			}
		} else {
			result = append(result, pathSegment{name: part, isField: true})
		}
	}

	return result, nil
}

func navigateToValue(current interface{}, segments []pathSegment) (interface{}, bool, error) {
	for i, seg := range segments {
		if seg.isArray {
			arr, ok := current.([]interface{})
			if !ok {
				return nil, false, fmt.Errorf("expected array at segment %d", i)
			}
			if seg.index < 0 || seg.index >= len(arr) {
				return nil, false, nil
			}
			current = arr[seg.index]
		} else {
			m, ok := current.(map[string]interface{})
			if !ok {
				return nil, false, fmt.Errorf("expected map at segment %d", i)
			}
			next, exists := m[seg.name]
			if !exists {
				return nil, false, nil
			}
			current = next
		}
	}

	return current, true, nil
}

// rebuildExactStructure creates the exact nested structure with arrays
func rebuildExactStructure(segments []pathSegment, value interface{}) (map[string]interface{}, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("empty path")
	}

	// Start from the end and work backwards
	var result interface{} = value

	// Process segments in reverse
	for i := len(segments) - 1; i >= 0; i-- {
		seg := segments[i]

		if seg.isArray {
			// Create array with element at the specific index
			arr := make([]interface{}, seg.index+1)
			arr[seg.index] = result
			result = arr
		} else if seg.isField {
			// Wrap in a map
			m := make(map[string]interface{})
			m[seg.name] = result
			result = m
		}
	}

	// Result should be a map at this point
	if m, ok := result.(map[string]interface{}); ok {
		return m, nil
	}

	return nil, fmt.Errorf("unexpected result type: %T", result)
}
