package object

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func extractOwnedPaths(ctx context.Context, managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) []string {
	// Collect ALL fields from ALL k8sconnect entries (both Apply and Update operations)
	allOwnedFields := make(map[string]interface{})

	for _, mf := range managedFields {
		if mf.Manager == "k8sconnect" && mf.FieldsV1 != nil {
			// Parse this entry's fields
			var fields map[string]interface{}
			if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
				continue
			}

			// Merge these fields into our accumulated ownership
			mergeFields(allOwnedFields, fields)
		}
	}

	if len(allOwnedFields) == 0 {
		// When no ownership info, extract ALL fields from user's YAML
		return extractAllFieldsFromYAML(userJSON, "")
	}

	// Extract owned paths
	paths := []string{}
	parseOwnedFields(allOwnedFields, "", userJSON, &paths)

	// Compare with what extractAllFieldsFromYAML would give us
	standardPaths := extractAllFieldsFromYAML(userJSON, "")

	// ALSO include fields from user's YAML that aren't covered by managedFields
	// These are fields like apiVersion, kind, metadata.name that are "owned" by creation
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
				paths = append(paths, userPath)
			}
		}
	}

	// Always add core fields even if not in user's YAML
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
			paths = append(paths, coreField)
		}
	}

	return paths
}

// extractAllFieldsFromYAML - used when no managedFields available
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

			// Parse the merge key using shared matcher
			mergeKey, err := matcher.ParseMergeKey(key)
			if err != nil {
				continue
			}

			// Find the matching array index in userObj
			if !isArray {
				continue
			}

			// Use shared matcher to find the array index
			arrayIndex := matcher.FindArrayIndex(userArray, mergeKey)
			if arrayIndex == -1 {
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
		}
	}
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

// flattenProjectionToMap converts nested projection to flat key-value map with dotted paths
// This enables clean, concise diffs in Terraform plan output
func flattenProjectionToMap(projection map[string]interface{}, paths []string) map[string]string {
	result := make(map[string]string, len(paths))

	for _, path := range paths {
		value, exists := getFieldByPath(projection, path)
		if exists {
			result[path] = formatValueForDisplay(value)
		}
	}

	return result
}

// formatValueForDisplay converts a value to string for display in flat map
func formatValueForDisplay(v interface{}) string {
	return common.FormatValueForDisplay(v)
}

// filterIgnoredPaths removes paths that match any ignore pattern
// Supports JSONPath predicates like: containers[?(@.name=='nginx')].image
func filterIgnoredPaths(allPaths []string, ignoreFields []string, obj map[string]interface{}) []string {
	if len(ignoreFields) == 0 {
		return allPaths
	}

	filtered := make([]string, 0, len(allPaths))
	for _, path := range allPaths {
		ignored := false
		for _, ignorePattern := range ignoreFields {
			if pathMatchesIgnorePattern(path, ignorePattern, obj) {
				ignored = true
				break
			}
		}
		if !ignored {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

// resolveJSONPathPredicates converts JSONPath predicates to positional selectors
// Example: containers[?(@.name=='nginx')].image -> containers[0].image
func resolveJSONPathPredicates(pattern string, obj map[string]interface{}) string {
	// Regex to match JSONPath predicates: [?(@.field=='value')] or [?(@.field=="value")]
	predicateRegex := regexp.MustCompile(`\[\?\(@\.([^=]+)==['"]([^'"]+)['"]\)\]`)

	result := pattern

	// Find all predicates and resolve them left-to-right
	for {
		match := predicateRegex.FindStringSubmatchIndex(result)
		if match == nil {
			break // No more predicates
		}

		// Extract the field name and value from the predicate
		field := result[match[2]:match[3]] // e.g., "name"
		value := result[match[4]:match[5]] // e.g., "nginx"
		predicateStart := match[0]
		predicateEnd := match[1]

		// Build the path to the array containing this predicate
		// Use the ALREADY RESOLVED portion of the path up to the predicate
		pathToArray := result[:predicateStart]

		// Navigate to the array in the object using the current (partially resolved) path
		array, ok := navigateToResolvedPath(obj, pathToArray)
		if !ok {
			// Can't resolve - leave predicate as-is
			break
		}

		// Find the index where field==value
		arraySlice, ok := array.([]interface{})
		if !ok {
			break
		}

		index := -1
		for i, item := range arraySlice {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if itemValue, exists := itemMap[field]; exists {
				if fmt.Sprintf("%v", itemValue) == value {
					index = i
					break
				}
			}
		}

		if index == -1 {
			// No match found - can't resolve
			break
		}

		// Replace the predicate with a positional selector
		result = result[:predicateStart] + fmt.Sprintf("[%d]", index) + result[predicateEnd:]
	}

	return result
}

// navigateToResolvedPath walks through the object following a path that may contain positional selectors
// This is used after some predicates have been resolved to [index] notation
func navigateToResolvedPath(obj map[string]interface{}, path string) (interface{}, bool) {
	if path == "" {
		return obj, true
	}

	// Parse the path segment by segment, handling both dots and array indices
	current := interface{}(obj)
	remaining := path

	for remaining != "" {
		// Find the next segment (either before a dot or before an array selector)
		dotIdx := strings.Index(remaining, ".")
		bracketIdx := strings.Index(remaining, "[")

		var segment string
		var nextIdx int

		if dotIdx == -1 && bracketIdx == -1 {
			// Last segment
			segment = remaining
			remaining = ""
		} else if dotIdx == -1 {
			// Only bracket found
			segment = remaining[:bracketIdx]
			nextIdx = bracketIdx
		} else if bracketIdx == -1 {
			// Only dot found
			segment = remaining[:dotIdx]
			nextIdx = dotIdx + 1
		} else if bracketIdx < dotIdx {
			// Bracket comes first
			segment = remaining[:bracketIdx]
			nextIdx = bracketIdx
		} else {
			// Dot comes first
			segment = remaining[:dotIdx]
			nextIdx = dotIdx + 1
		}

		// Navigate through the segment if it's not empty
		if segment != "" {
			currentMap, ok := current.(map[string]interface{})
			if !ok {
				return nil, false
			}
			next, exists := currentMap[segment]
			if !exists {
				return nil, false
			}
			current = next
		}

		// Handle array selector if present
		if bracketIdx != -1 && (dotIdx == -1 || bracketIdx < dotIdx) {
			// Extract the array index
			closeBracket := strings.Index(remaining[bracketIdx:], "]")
			if closeBracket == -1 {
				return nil, false
			}
			closeBracket += bracketIdx

			indexStr := remaining[bracketIdx+1 : closeBracket]
			var index int
			if _, err := fmt.Sscanf(indexStr, "%d", &index); err != nil {
				return nil, false
			}

			// Navigate into array
			arraySlice, ok := current.([]interface{})
			if !ok || index < 0 || index >= len(arraySlice) {
				return nil, false
			}
			current = arraySlice[index]

			nextIdx = closeBracket + 1
			if nextIdx < len(remaining) && remaining[nextIdx] == '.' {
				nextIdx++
			}
		}

		if nextIdx >= len(remaining) {
			remaining = ""
		} else {
			remaining = remaining[nextIdx:]
		}
	}

	return current, true
}

// navigateToPath walks through the object following a dotted path
// Returns the value at that path, or nil if not found
func navigateToPath(obj map[string]interface{}, path string) (interface{}, bool) {
	if path == "" {
		return obj, true
	}

	// Remove trailing array selector if present (we want the array itself)
	path = strings.TrimSuffix(path, "]")
	if idx := strings.LastIndex(path, "["); idx != -1 {
		path = path[:idx]
	}

	segments := strings.Split(path, ".")
	current := interface{}(obj)

	for _, seg := range segments {
		if seg == "" {
			continue
		}

		// Navigate into object
		currentMap, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}

		next, exists := currentMap[seg]
		if !exists {
			return nil, false
		}

		current = next
	}

	return current, true
}

// pathMatchesIgnorePattern checks if a path matches an ignore pattern
// Pattern matches if it's a prefix of the path (allowing parent fields to ignore children)
// Supports JSONPath predicates: containers[?(@.name=='nginx')].image
func pathMatchesIgnorePattern(path, pattern string, obj map[string]interface{}) bool {
	pathSegments := parsePath(path)

	// Resolve any JSONPath predicates in the pattern to positional selectors
	resolvedPattern := resolveJSONPathPredicates(pattern, obj)
	patternSegments := parsePath(resolvedPattern)

	// Pattern must be <= path length (prefix or exact match)
	if len(patternSegments) > len(pathSegments) {
		return false
	}

	// Compare each segment of the pattern
	for i, patternSeg := range patternSegments {
		if !segmentsMatch(pathSegments[i], patternSeg) {
			return false
		}
	}

	return true
}

// segmentsMatch checks if two path segments match
func segmentsMatch(pathSeg, patternSeg PathSegment) bool {
	// Field names must match
	if pathSeg.Field != patternSeg.Field {
		return false
	}

	// If pattern has selector, it must match exactly
	if patternSeg.Selector != nil {
		if pathSeg.Selector == nil {
			return false
		}
		return selectorsMatch(pathSeg.Selector, patternSeg.Selector)
	}

	// Pattern has no selector - matches regardless of path's selector
	return true
}

// selectorsMatch checks if two array selectors match
func selectorsMatch(pathSel, patternSel *ArraySelector) bool {
	if pathSel.Type != patternSel.Type {
		return false
	}

	switch patternSel.Type {
	case "positional":
		return pathSel.Index == patternSel.Index
	case "keyed":
		return pathSel.KeyField == patternSel.KeyField &&
			pathSel.KeyValue == patternSel.KeyValue
	case "empty":
		return true
	default:
		return false
	}
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

// removeFieldsFromObject creates a copy of the object with specified fields removed.
// This is used to omit ignored fields from SSA Apply patches, allowing other controllers
// to manage those fields without ownership conflicts.
func removeFieldsFromObject(obj *unstructured.Unstructured, ignorePatterns []string) *unstructured.Unstructured {
	result := obj.DeepCopy()

	for _, pattern := range ignorePatterns {
		segments := parsePath(pattern)
		removeFieldFromUnstructured(result.Object, segments, 0)
	}

	return result
}

// removeFieldFromUnstructured recursively removes a field from an unstructured map
func removeFieldFromUnstructured(obj map[string]interface{}, segments []PathSegment, depth int) {
	if depth >= len(segments) {
		return
	}

	seg := segments[depth]
	isLastSegment := depth == len(segments)-1

	// Handle array selector
	if seg.Selector != nil {
		arr, ok := obj[seg.Field].([]interface{})
		if !ok {
			return // Field doesn't exist or isn't an array
		}

		switch seg.Selector.Type {
		case "positional":
			// Remove specific array element
			if isLastSegment {
				// Remove the entire indexed element
				if seg.Selector.Index >= 0 && seg.Selector.Index < len(arr) {
					obj[seg.Field] = append(arr[:seg.Selector.Index], arr[seg.Selector.Index+1:]...)
				}
			} else {
				// Traverse into the array element
				if seg.Selector.Index >= 0 && seg.Selector.Index < len(arr) {
					if item, ok := arr[seg.Selector.Index].(map[string]interface{}); ok {
						removeFieldFromUnstructured(item, segments, depth+1)
					}
				}
			}

		case "keyed":
			// Find and remove matching array element
			for i, item := range arr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if keyVal, exists := itemMap[seg.Selector.KeyField]; exists {
						if keyVal == seg.Selector.KeyValue {
							if isLastSegment {
								// Remove this array element
								obj[seg.Field] = append(arr[:i], arr[i+1:]...)
							} else {
								// Traverse into this element
								removeFieldFromUnstructured(itemMap, segments, depth+1)
							}
							return
						}
					}
				}
			}

		case "empty":
			// Remove entire array
			if isLastSegment {
				delete(obj, seg.Field)
			}
		}
		return
	}

	// Handle regular field
	if isLastSegment {
		// Remove the field
		delete(obj, seg.Field)
	} else {
		// Traverse deeper
		if nested, ok := obj[seg.Field].(map[string]interface{}); ok {
			removeFieldFromUnstructured(nested, segments, depth+1)

			// CRITICAL: If removing the child field left the parent map empty,
			// remove the parent too. Otherwise SSA will interpret the empty map
			// as "claim ownership of the parent and set it to empty", causing
			// field ownership consolidation.
			if len(nested) == 0 {
				delete(obj, seg.Field)
			}
		}
	}
}
