// internal/k8sconnect/resource/wait/status_pruner_test.go
package wait

import (
	"reflect"
	"testing"
)

func TestParseFieldPath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		expected    []pathSegment
		expectError bool
	}{
		{
			name: "simple field",
			path: "replicas",
			expected: []pathSegment{
				{name: "replicas", isField: true},
			},
		},
		{
			name: "nested fields",
			path: "loadBalancer.ingress",
			expected: []pathSegment{
				{name: "loadBalancer", isField: true},
				{name: "ingress", isField: true},
			},
		},
		{
			name: "field with array index",
			path: "ingress[0]",
			expected: []pathSegment{
				{name: "ingress", isField: true},
				{index: 0, isArray: true},
			},
		},
		{
			name: "nested with array",
			path: "loadBalancer.ingress[0].ip",
			expected: []pathSegment{
				{name: "loadBalancer", isField: true},
				{name: "ingress", isField: true},
				{index: 0, isArray: true},
				{name: "ip", isField: true},
			},
		},
		{
			name: "multiple array indices",
			path: "conditions[0].reasons[1]",
			expected: []pathSegment{
				{name: "conditions", isField: true},
				{index: 0, isArray: true},
				{name: "reasons", isField: true},
				{index: 1, isArray: true},
			},
		},
		{
			name: "nested array notation",
			path: "items[0][1]",
			expected: []pathSegment{
				{name: "items", isField: true},
				{index: 0, isArray: true},
				{index: 1, isArray: true},
			},
		},
		{
			name:        "empty path",
			path:        "",
			expectError: true,
		},
		{
			name:        "unclosed bracket",
			path:        "ingress[0",
			expectError: true,
		},
		{
			name:        "invalid array index",
			path:        "ingress[abc]",
			expectError: true,
		},
		{
			name: "bracket without field treated as field name",
			path: "[0]",
			expected: []pathSegment{
				{name: "[0]", isField: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseFieldPath(tt.path)

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

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseFieldPath() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestNavigateToValue(t *testing.T) {
	tests := []struct {
		name        string
		current     interface{}
		segments    []pathSegment
		expected    interface{}
		found       bool
		expectError bool
	}{
		{
			name: "simple field",
			current: map[string]interface{}{
				"replicas": 3,
			},
			segments: []pathSegment{
				{name: "replicas", isField: true},
			},
			expected: 3,
			found:    true,
		},
		{
			name: "nested fields",
			current: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{"ip": "1.2.3.4"},
					},
				},
			},
			segments: []pathSegment{
				{name: "loadBalancer", isField: true},
				{name: "ingress", isField: true},
			},
			expected: []interface{}{
				map[string]interface{}{"ip": "1.2.3.4"},
			},
			found: true,
		},
		{
			name: "array access",
			current: map[string]interface{}{
				"items": []interface{}{"first", "second", "third"},
			},
			segments: []pathSegment{
				{name: "items", isField: true},
				{index: 1, isArray: true},
			},
			expected: "second",
			found:    true,
		},
		{
			name: "nested array and field",
			current: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{"ip": "1.2.3.4"},
						map[string]interface{}{"ip": "5.6.7.8"},
					},
				},
			},
			segments: []pathSegment{
				{name: "loadBalancer", isField: true},
				{name: "ingress", isField: true},
				{index: 1, isArray: true},
				{name: "ip", isField: true},
			},
			expected: "5.6.7.8",
			found:    true,
		},
		{
			name: "field not found",
			current: map[string]interface{}{
				"replicas": 3,
			},
			segments: []pathSegment{
				{name: "nonexistent", isField: true},
			},
			found: false,
		},
		{
			name: "array index out of bounds",
			current: map[string]interface{}{
				"items": []interface{}{"first", "second"},
			},
			segments: []pathSegment{
				{name: "items", isField: true},
				{index: 5, isArray: true},
			},
			found: false,
		},
		{
			name: "negative array index",
			current: map[string]interface{}{
				"items": []interface{}{"first", "second"},
			},
			segments: []pathSegment{
				{name: "items", isField: true},
				{index: -1, isArray: true},
			},
			found: false,
		},
		{
			name: "expected map but got array",
			current: map[string]interface{}{
				"items": []interface{}{"first", "second"},
			},
			segments: []pathSegment{
				{name: "items", isField: true},
				{name: "field", isField: true},
			},
			expectError: true,
		},
		{
			name: "expected array but got map",
			current: map[string]interface{}{
				"data": map[string]interface{}{"key": "value"},
			},
			segments: []pathSegment{
				{name: "data", isField: true},
				{index: 0, isArray: true},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, found, err := navigateToValue(tt.current, tt.segments)

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

			if found != tt.found {
				t.Errorf("navigateToValue() found = %v, want %v", found, tt.found)
			}

			if tt.found && !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("navigateToValue() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestRebuildExactStructure(t *testing.T) {
	tests := []struct {
		name        string
		segments    []pathSegment
		value       interface{}
		expected    map[string]interface{}
		expectError bool
	}{
		{
			name: "simple field",
			segments: []pathSegment{
				{name: "replicas", isField: true},
			},
			value: 3,
			expected: map[string]interface{}{
				"replicas": 3,
			},
		},
		{
			name: "nested fields",
			segments: []pathSegment{
				{name: "loadBalancer", isField: true},
				{name: "ingress", isField: true},
			},
			value: []interface{}{
				map[string]interface{}{"ip": "1.2.3.4"},
			},
			expected: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{"ip": "1.2.3.4"},
					},
				},
			},
		},
		{
			name: "field with array",
			segments: []pathSegment{
				{name: "ingress", isField: true},
				{index: 0, isArray: true},
			},
			value: map[string]interface{}{"ip": "1.2.3.4"},
			expected: map[string]interface{}{
				"ingress": []interface{}{
					map[string]interface{}{"ip": "1.2.3.4"},
				},
			},
		},
		{
			name: "nested with array at specific index",
			segments: []pathSegment{
				{name: "loadBalancer", isField: true},
				{name: "ingress", isField: true},
				{index: 2, isArray: true},
				{name: "ip", isField: true},
			},
			value: "1.2.3.4",
			expected: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						nil,
						nil,
						map[string]interface{}{
							"ip": "1.2.3.4",
						},
					},
				},
			},
		},
		{
			name: "multiple levels of nesting",
			segments: []pathSegment{
				{name: "status", isField: true},
				{name: "conditions", isField: true},
				{index: 0, isArray: true},
				{name: "type", isField: true},
			},
			value: "Ready",
			expected: map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{
							"type": "Ready",
						},
					},
				},
			},
		},
		{
			name:        "empty segments",
			segments:    []pathSegment{},
			value:       "test",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := rebuildExactStructure(tt.segments, tt.value)

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

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("rebuildExactStructure() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestPruneStatusToField(t *testing.T) {
	tests := []struct {
		name       string
		fullStatus map[string]interface{}
		fieldPath  string
		expected   map[string]interface{}
	}{
		{
			name: "simple field extraction",
			fullStatus: map[string]interface{}{
				"replicas":      3,
				"readyReplicas": 3,
				"phase":         "Running",
			},
			fieldPath: "status.replicas",
			expected: map[string]interface{}{
				"replicas": 3,
			},
		},
		{
			name: "nested field extraction",
			fullStatus: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{"ip": "1.2.3.4"},
					},
				},
				"replicas": 3,
			},
			fieldPath: "status.loadBalancer.ingress",
			expected: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{"ip": "1.2.3.4"},
					},
				},
			},
		},
		{
			name: "array element extraction",
			fullStatus: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{"ip": "1.2.3.4"},
						map[string]interface{}{"ip": "5.6.7.8"},
					},
				},
			},
			fieldPath: "status.loadBalancer.ingress[0].ip",
			expected: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{
							"ip": "1.2.3.4",
						},
					},
				},
			},
		},
		{
			name: "field without status prefix",
			fullStatus: map[string]interface{}{
				"replicas": 3,
				"phase":    "Running",
			},
			fieldPath: "replicas",
			expected: map[string]interface{}{
				"replicas": 3,
			},
		},
		{
			name:       "nil status",
			fullStatus: nil,
			fieldPath:  "status.replicas",
			expected:   nil,
		},
		{
			name: "empty field path",
			fullStatus: map[string]interface{}{
				"replicas": 3,
			},
			fieldPath: "",
			expected:  nil,
		},
		{
			name: "field not found",
			fullStatus: map[string]interface{}{
				"replicas": 3,
			},
			fieldPath: "status.nonexistent",
			expected:  nil,
		},
		{
			name: "array index out of bounds",
			fullStatus: map[string]interface{}{
				"items": []interface{}{"first", "second"},
			},
			fieldPath: "status.items[5]",
			expected:  nil,
		},
		{
			name: "complex nested structure",
			fullStatus: map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Progressing",
						"status": "True",
					},
					map[string]interface{}{
						"type":   "Available",
						"status": "True",
					},
				},
				"replicas": 3,
			},
			fieldPath: "status.conditions[1].type",
			expected: map[string]interface{}{
				"conditions": []interface{}{
					nil,
					map[string]interface{}{
						"type": "Available",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pruneStatusToField(tt.fullStatus, tt.fieldPath)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("pruneStatusToField() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}
