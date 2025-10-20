package common

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestConvertToAttrValue(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		input       interface{}
		expectType  string // Expected type name
		expectError bool
		validate    func(t *testing.T, val attr.Value) // Optional custom validation
	}{
		// Primitive types
		{
			name:       "string value",
			input:      "hello world",
			expectType: "types.String",
			validate: func(t *testing.T, val attr.Value) {
				strVal, ok := val.(types.String)
				if !ok {
					t.Fatalf("expected types.String, got %T", val)
				}
				if strVal.ValueString() != "hello world" {
					t.Errorf("expected 'hello world', got %q", strVal.ValueString())
				}
			},
		},
		{
			name:       "empty string",
			input:      "",
			expectType: "types.String",
			validate: func(t *testing.T, val attr.Value) {
				strVal := val.(types.String)
				if strVal.ValueString() != "" {
					t.Errorf("expected empty string, got %q", strVal.ValueString())
				}
			},
		},
		{
			name:       "bool true",
			input:      true,
			expectType: "types.Bool",
			validate: func(t *testing.T, val attr.Value) {
				boolVal := val.(types.Bool)
				if !boolVal.ValueBool() {
					t.Error("expected true, got false")
				}
			},
		},
		{
			name:       "bool false",
			input:      false,
			expectType: "types.Bool",
			validate: func(t *testing.T, val attr.Value) {
				boolVal := val.(types.Bool)
				if boolVal.ValueBool() {
					t.Error("expected false, got true")
				}
			},
		},
		{
			name:       "float64",
			input:      3.14,
			expectType: "types.Float64",
			validate: func(t *testing.T, val attr.Value) {
				floatVal := val.(types.Float64)
				if floatVal.ValueFloat64() != 3.14 {
					t.Errorf("expected 3.14, got %v", floatVal.ValueFloat64())
				}
			},
		},
		{
			name:       "int64",
			input:      int64(42),
			expectType: "types.Int64",
			validate: func(t *testing.T, val attr.Value) {
				intVal := val.(types.Int64)
				if intVal.ValueInt64() != 42 {
					t.Errorf("expected 42, got %v", intVal.ValueInt64())
				}
			},
		},
		{
			name:       "int (converted to int64)",
			input:      100,
			expectType: "types.Int64",
			validate: func(t *testing.T, val attr.Value) {
				intVal := val.(types.Int64)
				if intVal.ValueInt64() != 100 {
					t.Errorf("expected 100, got %v", intVal.ValueInt64())
				}
			},
		},
		{
			name:       "nil value",
			input:      nil,
			expectType: "types.String",
			validate: func(t *testing.T, val attr.Value) {
				strVal := val.(types.String)
				if !strVal.IsNull() {
					t.Error("expected null string")
				}
			},
		},

		// Lists
		{
			name:       "empty list",
			input:      []interface{}{},
			expectType: "types.List",
			validate: func(t *testing.T, val attr.Value) {
				listVal := val.(types.List)
				if !listVal.IsNull() {
					t.Error("expected null list for empty slice")
				}
			},
		},
		{
			name:       "string list",
			input:      []interface{}{"foo", "bar", "baz"},
			expectType: "types.List",
			validate: func(t *testing.T, val attr.Value) {
				listVal := val.(types.List)
				if len(listVal.Elements()) != 3 {
					t.Errorf("expected 3 elements, got %d", len(listVal.Elements()))
				}
				// Check first element
				firstElem := listVal.Elements()[0].(types.String)
				if firstElem.ValueString() != "foo" {
					t.Errorf("expected 'foo', got %q", firstElem.ValueString())
				}
			},
		},
		{
			name:       "int list",
			input:      []interface{}{int64(1), int64(2), int64(3)},
			expectType: "types.List",
			validate: func(t *testing.T, val attr.Value) {
				listVal := val.(types.List)
				if len(listVal.Elements()) != 3 {
					t.Errorf("expected 3 elements, got %d", len(listVal.Elements()))
				}
				firstElem := listVal.Elements()[0].(types.Int64)
				if firstElem.ValueInt64() != 1 {
					t.Errorf("expected 1, got %v", firstElem.ValueInt64())
				}
			},
		},
		{
			name:       "bool list",
			input:      []interface{}{true, false, true},
			expectType: "types.List",
			validate: func(t *testing.T, val attr.Value) {
				listVal := val.(types.List)
				if len(listVal.Elements()) != 3 {
					t.Errorf("expected 3 elements, got %d", len(listVal.Elements()))
				}
			},
		},

		// Maps (objects)
		{
			name: "simple map",
			input: map[string]interface{}{
				"name": "test",
				"port": int64(8080),
			},
			expectType: "types.Object",
			validate: func(t *testing.T, val attr.Value) {
				objVal := val.(types.Object)
				attrs := objVal.Attributes()
				if len(attrs) != 2 {
					t.Errorf("expected 2 attributes, got %d", len(attrs))
				}
				nameVal := attrs["name"].(types.String)
				if nameVal.ValueString() != "test" {
					t.Errorf("expected 'test', got %q", nameVal.ValueString())
				}
				portVal := attrs["port"].(types.Int64)
				if portVal.ValueInt64() != 8080 {
					t.Errorf("expected 8080, got %v", portVal.ValueInt64())
				}
			},
		},
		{
			name:       "empty map",
			input:      map[string]interface{}{},
			expectType: "types.Object",
			validate: func(t *testing.T, val attr.Value) {
				objVal := val.(types.Object)
				if len(objVal.Attributes()) != 0 {
					t.Errorf("expected 0 attributes, got %d", len(objVal.Attributes()))
				}
			},
		},
		{
			name: "map with managedFields (should be skipped)",
			input: map[string]interface{}{
				"name":          "test",
				"managedFields": []interface{}{map[string]interface{}{"field": "value"}},
			},
			expectType: "types.Object",
			validate: func(t *testing.T, val attr.Value) {
				objVal := val.(types.Object)
				attrs := objVal.Attributes()
				if len(attrs) != 1 {
					t.Errorf("expected 1 attribute (managedFields skipped), got %d", len(attrs))
				}
				if _, exists := attrs["managedFields"]; exists {
					t.Error("managedFields should be skipped")
				}
				if _, exists := attrs["name"]; !exists {
					t.Error("name should exist")
				}
			},
		},

		// Nested structures
		{
			name: "nested map",
			input: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "my-pod",
					"namespace": "default",
				},
			},
			expectType: "types.Object",
			validate: func(t *testing.T, val attr.Value) {
				objVal := val.(types.Object)
				metadataVal := objVal.Attributes()["metadata"].(types.Object)
				nameVal := metadataVal.Attributes()["name"].(types.String)
				if nameVal.ValueString() != "my-pod" {
					t.Errorf("expected 'my-pod', got %q", nameVal.ValueString())
				}
			},
		},
		{
			name: "list of maps",
			input: []interface{}{
				map[string]interface{}{"name": "item1"},
				map[string]interface{}{"name": "item2"},
			},
			expectType: "types.List",
			validate: func(t *testing.T, val attr.Value) {
				listVal := val.(types.List)
				if len(listVal.Elements()) != 2 {
					t.Errorf("expected 2 elements, got %d", len(listVal.Elements()))
				}
				firstItem := listVal.Elements()[0].(types.Object)
				nameVal := firstItem.Attributes()["name"].(types.String)
				if nameVal.ValueString() != "item1" {
					t.Errorf("expected 'item1', got %q", nameVal.ValueString())
				}
			},
		},
		{
			name: "map with nested lists",
			input: map[string]interface{}{
				"ports": []interface{}{int64(80), int64(443)},
			},
			expectType: "types.Object",
			validate: func(t *testing.T, val attr.Value) {
				objVal := val.(types.Object)
				portsVal := objVal.Attributes()["ports"].(types.List)
				if len(portsVal.Elements()) != 2 {
					t.Errorf("expected 2 ports, got %d", len(portsVal.Elements()))
				}
			},
		},
		{
			name: "deeply nested structure",
			input: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nginx",
							"image": "nginx:latest",
							"ports": []interface{}{
								map[string]interface{}{
									"containerPort": int64(80),
									"protocol":      "TCP",
								},
							},
						},
					},
				},
			},
			expectType: "types.Object",
			validate: func(t *testing.T, val attr.Value) {
				objVal := val.(types.Object)
				specVal := objVal.Attributes()["spec"].(types.Object)
				containersVal := specVal.Attributes()["containers"].(types.List)
				if len(containersVal.Elements()) != 1 {
					t.Fatalf("expected 1 container, got %d", len(containersVal.Elements()))
				}
				containerVal := containersVal.Elements()[0].(types.Object)
				nameVal := containerVal.Attributes()["name"].(types.String)
				if nameVal.ValueString() != "nginx" {
					t.Errorf("expected 'nginx', got %q", nameVal.ValueString())
				}
			},
		},

		// Edge cases and fallback behavior
		{
			name:       "unsupported type (uint32) - fallback to string",
			input:      uint32(42),
			expectType: "types.String",
			validate: func(t *testing.T, val attr.Value) {
				strVal := val.(types.String)
				if strVal.ValueString() != "42" {
					t.Errorf("expected '42', got %q", strVal.ValueString())
				}
			},
		},
		{
			name:       "custom struct - fallback to string",
			input:      struct{ Name string }{Name: "test"},
			expectType: "types.String",
			validate: func(t *testing.T, val attr.Value) {
				strVal := val.(types.String)
				if !strings.Contains(strVal.ValueString(), "test") {
					t.Errorf("expected string containing 'test', got %q", strVal.ValueString())
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertToAttrValue(ctx, tc.input)

			if tc.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result == nil {
				t.Fatal("expected non-nil result")
			}

			// Run custom validation if provided
			if tc.validate != nil {
				tc.validate(t, result)
			}
		})
	}
}

func TestFormatValueForDisplay(t *testing.T) {
	tests := []struct {
		name           string
		input          interface{}
		expectContains string // Expected substring in output
	}{
		{
			name:           "nil value",
			input:          nil,
			expectContains: "<nil>",
		},
		{
			name:           "string value",
			input:          "hello",
			expectContains: "hello",
		},
		{
			name:           "int value",
			input:          42,
			expectContains: "42",
		},
		{
			name:           "int64 value",
			input:          int64(100),
			expectContains: "100",
		},
		{
			name:           "float64 value",
			input:          3.14,
			expectContains: "3.14",
		},
		{
			name:           "bool true",
			input:          true,
			expectContains: "true",
		},
		{
			name:           "bool false",
			input:          false,
			expectContains: "false",
		},
		{
			name: "map to JSON",
			input: map[string]interface{}{
				"name": "test",
				"port": 8080,
			},
			expectContains: `"name":"test"`,
		},
		{
			name:           "list to JSON",
			input:          []interface{}{"a", "b", "c"},
			expectContains: `["a","b","c"]`,
		},
		{
			name: "nested structure to JSON",
			input: map[string]interface{}{
				"spec": map[string]interface{}{
					"replicas": 3,
				},
			},
			expectContains: `"replicas":3`,
		},
		{
			name:           "empty map",
			input:          map[string]interface{}{},
			expectContains: "{}",
		},
		{
			name:           "empty list",
			input:          []interface{}{},
			expectContains: "[]",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result := FormatValueForDisplay(tc.input)

			if !strings.Contains(result, tc.expectContains) {
				t.Errorf("expected result to contain %q, got %q", tc.expectContains, result)
			}
		})
	}
}
