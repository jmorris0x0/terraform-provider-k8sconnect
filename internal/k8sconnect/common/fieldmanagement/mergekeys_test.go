// internal/k8sconnect/common/fieldmanagement/mergekeys_test.go
package fieldmanagement

import (
	"testing"
)

func TestMergeKeyMatcher_ParseMergeKey(t *testing.T) {
	matcher := NewMergeKeyMatcher()

	tests := []struct {
		name        string
		input       string
		expectError bool
		expectedKey map[string]interface{}
	}{
		{
			name:  "valid port and protocol",
			input: `k:{"port":80,"protocol":"TCP"}`,
			expectedKey: map[string]interface{}{
				"port":     float64(80), // JSON unmarshals numbers as float64
				"protocol": "TCP",
			},
		},
		{
			name:  "valid container name",
			input: `k:{"name":"nginx"}`,
			expectedKey: map[string]interface{}{
				"name": "nginx",
			},
		},
		{
			name:  "complex merge key",
			input: `k:{"containerPort":8080,"protocol":"TCP","name":"http"}`,
			expectedKey: map[string]interface{}{
				"containerPort": float64(8080),
				"protocol":      "TCP",
				"name":          "http",
			},
		},
		{
			name:        "not a merge key",
			input:       `f:someField`,
			expectError: true,
		},
		{
			name:        "invalid JSON",
			input:       `k:{"port":80`,
			expectError: true,
		},
		{
			name:        "empty string",
			input:       ``,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := matcher.ParseMergeKey(tt.input)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Check each expected field
			for key, expectedVal := range tt.expectedKey {
				actualVal, exists := result[key]
				if !exists {
					t.Errorf("expected key %q not found", key)
					continue
				}

				// Handle float64 comparison for numbers
				if expectedFloat, ok := expectedVal.(float64); ok {
					if actualFloat, ok := actualVal.(float64); ok {
						if expectedFloat != actualFloat {
							t.Errorf("key %q: expected %v, got %v", key, expectedFloat, actualFloat)
						}
					} else {
						t.Errorf("key %q: expected float64, got %T", key, actualVal)
					}
				} else if expectedVal != actualVal {
					t.Errorf("key %q: expected %v, got %v", key, expectedVal, actualVal)
				}
			}
		})
	}
}

func TestMergeKeyMatcher_FindArrayIndex(t *testing.T) {
	matcher := NewMergeKeyMatcher()

	tests := []struct {
		name          string
		array         []interface{}
		mergeKey      map[string]interface{}
		expectedIndex int
	}{
		{
			name: "find by port and protocol",
			array: []interface{}{
				map[string]interface{}{
					"port":       float64(80),
					"protocol":   "TCP",
					"targetPort": float64(8080),
				},
				map[string]interface{}{
					"port":       float64(443),
					"protocol":   "TCP",
					"targetPort": float64(8443),
				},
			},
			mergeKey: map[string]interface{}{
				"port":     float64(443),
				"protocol": "TCP",
			},
			expectedIndex: 1,
		},
		{
			name: "find by name",
			array: []interface{}{
				map[string]interface{}{
					"name":  "init",
					"image": "init:latest",
				},
				map[string]interface{}{
					"name":  "nginx",
					"image": "nginx:1.20",
				},
				map[string]interface{}{
					"name":  "sidecar",
					"image": "envoy:latest",
				},
			},
			mergeKey: map[string]interface{}{
				"name": "nginx",
			},
			expectedIndex: 1,
		},
		{
			name: "no match found",
			array: []interface{}{
				map[string]interface{}{
					"port":     float64(80),
					"protocol": "TCP",
				},
			},
			mergeKey: map[string]interface{}{
				"port":     float64(443),
				"protocol": "TCP",
			},
			expectedIndex: -1,
		},
		{
			name:  "empty array",
			array: []interface{}{},
			mergeKey: map[string]interface{}{
				"name": "anything",
			},
			expectedIndex: -1,
		},
		{
			name: "non-map items in array",
			array: []interface{}{
				"string-item",
				123,
				map[string]interface{}{
					"name": "target",
				},
			},
			mergeKey: map[string]interface{}{
				"name": "target",
			},
			expectedIndex: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := matcher.FindArrayIndex(tt.array, tt.mergeKey)
			if index != tt.expectedIndex {
				t.Errorf("expected index %d, got %d", tt.expectedIndex, index)
			}
		})
	}
}

func TestMergeKeyMatcher_ItemMatchesMergeKey(t *testing.T) {
	matcher := NewMergeKeyMatcher()

	tests := []struct {
		name     string
		item     map[string]interface{}
		mergeKey map[string]interface{}
		expected bool
	}{
		{
			name: "exact match",
			item: map[string]interface{}{
				"port":     float64(80),
				"protocol": "TCP",
			},
			mergeKey: map[string]interface{}{
				"port":     float64(80),
				"protocol": "TCP",
			},
			expected: true,
		},
		{
			name: "partial match - item has extra fields",
			item: map[string]interface{}{
				"port":       float64(80),
				"protocol":   "TCP",
				"targetPort": float64(8080),
				"name":       "http",
			},
			mergeKey: map[string]interface{}{
				"port":     float64(80),
				"protocol": "TCP",
			},
			expected: true,
		},
		{
			name: "user specified subset - k8s added defaults",
			item: map[string]interface{}{
				"port":       float64(80),
				"protocol":   "TCP", // Added by k8s
				"targetPort": float64(80),
			},
			mergeKey: map[string]interface{}{
				"port":     float64(80),
				"protocol": "TCP",
			},
			expected: true,
		},
		{
			name: "no match - different values",
			item: map[string]interface{}{
				"port":     float64(80),
				"protocol": "UDP",
			},
			mergeKey: map[string]interface{}{
				"port":     float64(80),
				"protocol": "TCP",
			},
			expected: false,
		},
		{
			name: "no match - missing required field",
			item: map[string]interface{}{
				"port": float64(80),
			},
			mergeKey: map[string]interface{}{
				"port":     float64(80),
				"protocol": "TCP",
			},
			expected: true,
		},
		{
			name: "type mismatch but string representation matches",
			item: map[string]interface{}{
				"port":     "80", // String instead of number
				"protocol": "TCP",
			},
			mergeKey: map[string]interface{}{
				"port":     float64(80),
				"protocol": "TCP",
			},
			expected: true, // Should match because fmt.Sprintf makes them equal
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matcher.ItemMatchesMergeKey(tt.item, tt.mergeKey)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestMergeKeyMatcher_Caching(t *testing.T) {
	matcher := NewMergeKeyMatcher()

	// Parse the same key multiple times
	key := `k:{"port":80,"protocol":"TCP"}`

	result1, err1 := matcher.ParseMergeKey(key)
	if err1 != nil {
		t.Fatalf("first parse failed: %v", err1)
	}

	result2, err2 := matcher.ParseMergeKey(key)
	if err2 != nil {
		t.Fatalf("second parse failed: %v", err2)
	}

	// Results should be equal
	if len(result1) != len(result2) {
		t.Errorf("cached result has different length")
	}

	for k, v1 := range result1 {
		v2, exists := result2[k]
		if !exists {
			t.Errorf("cached result missing key %q", k)
		}
		if v1 != v2 {
			t.Errorf("cached value differs for key %q", k)
		}
	}
}
