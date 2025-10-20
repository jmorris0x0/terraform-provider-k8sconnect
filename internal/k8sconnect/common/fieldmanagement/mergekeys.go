package fieldmanagement

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MergeKeyMatcher finds array indices for k: style merge keys
type MergeKeyMatcher struct {
	// Cache to avoid re-parsing the same merge keys
	cache map[string]map[string]interface{}
}

func NewMergeKeyMatcher() *MergeKeyMatcher {
	return &MergeKeyMatcher{
		cache: make(map[string]map[string]interface{}),
	}
}

// ParseMergeKey extracts the merge key from a k: prefixed string
func (m *MergeKeyMatcher) ParseMergeKey(key string) (map[string]interface{}, error) {
	if !strings.HasPrefix(key, "k:") {
		return nil, fmt.Errorf("not a merge key")
	}

	// Check cache
	if cached, ok := m.cache[key]; ok {
		return cached, nil
	}

	mergeKeyJSON := strings.TrimPrefix(key, "k:")
	var mergeKey map[string]interface{}
	if err := json.Unmarshal([]byte(mergeKeyJSON), &mergeKey); err != nil {
		return nil, err
	}

	m.cache[key] = mergeKey
	return mergeKey, nil
}

// FindArrayIndex finds the index of an array item matching the merge key
func (m *MergeKeyMatcher) FindArrayIndex(array []interface{}, mergeKey map[string]interface{}) int {
	for i, item := range array {
		if itemMap, ok := item.(map[string]interface{}); ok {
			if m.ItemMatchesMergeKey(itemMap, mergeKey) {
				return i
			}
		}
	}
	return -1
}

// ItemMatchesMergeKey checks if an item matches the merge key
// Allows partial matches when user's fields are a subset of merge key
func (m *MergeKeyMatcher) ItemMatchesMergeKey(item map[string]interface{}, mergeKey map[string]interface{}) bool {
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
