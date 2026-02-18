package object

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestGetFieldValue_DottedKeys verifies that getFieldValue correctly resolves
// field paths where map keys themselves contain dots.
//
// Bug: getFieldValue splits on "." which breaks for keys containing dots
// (annotation keys like "kubectl.kubernetes.io/last-applied", ConfigMap data
// keys like "config.yaml"). This causes drift warnings to show "(none) → (none)"
// instead of actual values. Soaktest Phase 7 UX issue.
func TestGetFieldValue_DottedKeys(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-cm",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"kubectl.kubernetes.io/last-applied-configuration": `{"some":"json"}`,
					"app.kubernetes.io/version":                        "1.0",
				},
				"labels": map[string]interface{}{
					"app.kubernetes.io/name":    "myapp",
					"app.kubernetes.io/part-of": "suite",
				},
			},
			"data": map[string]interface{}{
				"simple-key":     "value1",
				"config.yaml":    "key: value",
				"app.conf":       "setting=true",
				"nested.dot.key": "deeply-dotted",
			},
		},
	}

	tests := []struct {
		name     string
		path     string
		wantVal  interface{}
		wantNone bool // expect nil
	}{
		// Simple paths (no dots in keys) - should work as before
		{
			name:    "simple nested path",
			path:    "metadata.name",
			wantVal: "test-cm",
		},
		{
			name:    "simple data key",
			path:    "data.simple-key",
			wantVal: "value1",
		},
		{
			name:    "top-level field",
			path:    "kind",
			wantVal: "ConfigMap",
		},

		// Keys with dots - the bug scenario
		{
			name:    "ConfigMap data key with dot",
			path:    "data.config.yaml",
			wantVal: "key: value",
		},
		{
			name:    "ConfigMap data key with dot - app.conf",
			path:    "data.app.conf",
			wantVal: "setting=true",
		},
		{
			name:    "ConfigMap data key with multiple dots",
			path:    "data.nested.dot.key",
			wantVal: "deeply-dotted",
		},
		{
			name:    "annotation with dots",
			path:    "metadata.annotations.kubectl.kubernetes.io/last-applied-configuration",
			wantVal: `{"some":"json"}`,
		},
		{
			name:    "annotation with dots - app version",
			path:    "metadata.annotations.app.kubernetes.io/version",
			wantVal: "1.0",
		},
		{
			name:    "label with dots",
			path:    "metadata.labels.app.kubernetes.io/name",
			wantVal: "myapp",
		},

		// Non-existent paths should still return nil
		{
			name:     "non-existent path",
			path:     "data.nonexistent",
			wantNone: true,
		},
		{
			name:     "nil object",
			path:     "metadata.name",
			wantNone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var testObj *unstructured.Unstructured
			if tt.name != "nil object" {
				testObj = obj
			}

			got := getFieldValue(testObj, tt.path)

			if tt.wantNone {
				if got != nil {
					t.Errorf("getFieldValue(%q) = %v, want nil", tt.path, got)
				}
				return
			}

			if got == nil {
				t.Fatalf("getFieldValue(%q) = nil, want %v", tt.path, tt.wantVal)
			}

			// Compare as strings for simplicity
			gotStr, ok := got.(string)
			if !ok {
				t.Fatalf("getFieldValue(%q) returned non-string %T: %v", tt.path, got, got)
			}
			wantStr, ok := tt.wantVal.(string)
			if !ok {
				t.Fatalf("wantVal is non-string %T: %v", tt.wantVal, tt.wantVal)
			}
			if gotStr != wantStr {
				t.Errorf("getFieldValue(%q) = %q, want %q", tt.path, gotStr, wantStr)
			}
		})
	}
}
