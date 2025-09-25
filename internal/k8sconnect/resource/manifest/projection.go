// internal/k8sconnect/resource/manifest/projection.go
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

// internal/k8sconnect/resource/manifest/projection_v2.go

var matcher = NewMergeKeyMatcher()

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
			fmt.Printf("Processing array key: %s at prefix: %s\n", key, prefix)

			// Parse the merge key using shared matcher
			mergeKey, err := matcher.ParseMergeKey(key)
			if err != nil {
				fmt.Printf("Failed to parse merge key: %v\n", err)
				continue
			}

			// Find the matching array index in userObj
			if !isArray {
				fmt.Printf("Expected array but got %T\n", userObj)
				continue
			}

			// Use shared matcher to find the array index
			arrayIndex := matcher.FindArrayIndex(userArray, mergeKey)
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
	return matcher.ItemMatchesMergeKey(item, mergeKey)
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

// ArraySelector handles all array access patterns
type ArraySelector struct {
	Type     string // "empty", "positional", "keyed"
	Index    int    // For positional
	KeyField string // For keyed (e.g., "name")
	KeyValue string // For keyed (e.g., "nginx")
}

// parseArraySelector parses selectors like "0", "name=nginx", or ""
func parseArraySelector(selector string) ArraySelector {
	if selector == "" {
		return ArraySelector{Type: "empty"}
	}

	if strings.Contains(selector, "=") {
		parts := strings.SplitN(selector, "=", 2)
		if len(parts) == 2 {
			return ArraySelector{
				Type:     "keyed",
				KeyField: parts[0],
				KeyValue: parts[1],
			}
		}
	}

	// Try to parse as number
	var index int
	if n, _ := fmt.Sscanf(selector, "%d", &index); n == 1 {
		return ArraySelector{
			Type:  "positional",
			Index: index,
		}
	}

	// Fallback - treat as empty
	return ArraySelector{Type: "empty"}
}

// findInArray locates an element in an array using the selector
func findInArray(array []interface{}, selector ArraySelector) (element interface{}, index int, found bool) {
	switch selector.Type {
	case "empty":
		// Return the whole array
		return array, -1, true

	case "positional":
		if selector.Index >= 0 && selector.Index < len(array) {
			return array[selector.Index], selector.Index, true
		}
		return nil, -1, false

	case "keyed":
		for i, item := range array {
			if obj, ok := item.(map[string]interface{}); ok {
				if fmt.Sprint(obj[selector.KeyField]) == selector.KeyValue {
					return obj, i, true
				}
			}
		}
		return nil, -1, false

	default:
		return nil, -1, false
	}
}

// ensureArrayElement ensures an array element exists, creating if necessary
func ensureArrayElement(array []interface{}, selector ArraySelector) ([]interface{}, map[string]interface{}, error) {
	switch selector.Type {
	case "positional":
		// Extend array if needed
		for len(array) <= selector.Index {
			array = append(array, make(map[string]interface{}))
		}
		element, ok := array[selector.Index].(map[string]interface{})
		if !ok {
			element = make(map[string]interface{})
			array[selector.Index] = element
		}
		return array, element, nil

	case "keyed":
		// Look for existing element
		for _, item := range array {
			if obj, ok := item.(map[string]interface{}); ok {
				if fmt.Sprint(obj[selector.KeyField]) == selector.KeyValue {
					return array, obj, nil
				}
			}
		}
		// Create new element
		element := make(map[string]interface{})
		element[selector.KeyField] = selector.KeyValue
		array = append(array, element)
		return array, element, nil

	default:
		return nil, nil, fmt.Errorf("cannot ensure element for selector type: %s", selector.Type)
	}
}

// PathSegment represents one part of a dot-notation path
type PathSegment struct {
	Field    string
	Selector *ArraySelector // nil for non-array fields
}

// parsePath converts "spec.containers[name=nginx].image" into segments
func parsePath(path string) []PathSegment {
	parts := strings.Split(path, ".")
	segments := make([]PathSegment, 0, len(parts))

	for _, part := range parts {
		segment := PathSegment{Field: part}

		if idx := strings.Index(part, "["); idx >= 0 {
			segment.Field = part[:idx]
			selectorStr := part[idx+1 : len(part)-1]
			selector := parseArraySelector(selectorStr)
			segment.Selector = &selector
		}

		segments = append(segments, segment)
	}

	return segments
}

// getFieldByPath retrieves a value from an object using dot notation
func getFieldByPath(obj map[string]interface{}, path string) (interface{}, bool) {
	segments := parsePath(path)
	var current interface{} = obj

	for i, segment := range segments {
		// Navigate to the field
		currentMap, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}

		fieldValue, exists := currentMap[segment.Field]
		if !exists {
			return nil, false
		}

		// Handle array selector if present
		if segment.Selector != nil {
			array, ok := fieldValue.([]interface{})
			if !ok {
				return nil, false
			}

			// Check if this is the last segment or we want the full array
			if i == len(segments)-1 || segment.Selector.Type == "empty" {
				element, _, found := findInArray(array, *segment.Selector)
				return element, found
			}

			// Navigate into array element
			element, _, found := findInArray(array, *segment.Selector)
			if !found {
				return nil, false
			}
			current = element
		} else {
			// Regular field - check if we're done
			if i == len(segments)-1 {
				return fieldValue, true
			}
			current = fieldValue
		}
	}

	return nil, false
}

// setFieldByPath sets a value in an object using dot notation
func setFieldByPath(obj map[string]interface{}, path string, value interface{}) error {
	segments := parsePath(path)
	current := obj

	for i, segment := range segments {
		isLast := (i == len(segments)-1)

		if isLast {
			// Setting the final value
			if segment.Selector != nil {
				// Array field - set the whole array
				current[segment.Field] = value
			} else {
				// Regular field
				current[segment.Field] = value
			}
			return nil
		}

		// Not the last segment - ensure path exists
		if segment.Selector != nil {
			// Array field - ensure it exists
			if current[segment.Field] == nil {
				current[segment.Field] = make([]interface{}, 0)
			}

			array, ok := current[segment.Field].([]interface{})
			if !ok {
				return fmt.Errorf("field %s is not an array", segment.Field)
			}

			// Ensure the array element exists
			updatedArray, element, err := ensureArrayElement(array, *segment.Selector)
			if err != nil {
				return fmt.Errorf("cannot navigate through array at %s: %w", segment.Field, err)
			}

			current[segment.Field] = updatedArray
			current = element
		} else {
			// Regular field - ensure it exists as a map
			if current[segment.Field] == nil {
				current[segment.Field] = make(map[string]interface{})
			}

			next, ok := current[segment.Field].(map[string]interface{})
			if !ok {
				return fmt.Errorf("field %s is not an object", segment.Field)
			}
			current = next
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
