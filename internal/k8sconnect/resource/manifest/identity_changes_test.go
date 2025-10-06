// internal/k8sconnect/resource/manifest/identity_changes_test.go
package manifest

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestDetectIdentityChanges(t *testing.T) {
	tests := []struct {
		name      string
		stateYAML string
		planYAML  string
		wantCount int
		wantFields []string
	}{
		{
			name: "kind changed",
			stateYAML: `apiVersion: v1
kind: Pod
metadata:
  name: test`,
			planYAML: `apiVersion: v1
kind: ConfigMap
metadata:
  name: test`,
			wantCount: 1,
			wantFields: []string{"kind"},
		},
		{
			name: "name changed",
			stateYAML: `apiVersion: v1
kind: Pod
metadata:
  name: old-name`,
			planYAML: `apiVersion: v1
kind: Pod
metadata:
  name: new-name`,
			wantCount: 1,
			wantFields: []string{"metadata.name"},
		},
		{
			name: "namespace changed",
			stateYAML: `apiVersion: v1
kind: Pod
metadata:
  name: test
  namespace: default`,
			planYAML: `apiVersion: v1
kind: Pod
metadata:
  name: test
  namespace: production`,
			wantCount: 1,
			wantFields: []string{"metadata.namespace"},
		},
		{
			name: "apiVersion changed",
			stateYAML: `apiVersion: apps/v1beta1
kind: Deployment
metadata:
  name: test`,
			planYAML: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test`,
			wantCount: 1,
			wantFields: []string{"apiVersion"},
		},
		{
			name: "multiple changes - kind and name",
			stateYAML: `apiVersion: v1
kind: Pod
metadata:
  name: old-name
  namespace: default`,
			planYAML: `apiVersion: v1
kind: ConfigMap
metadata:
  name: new-name
  namespace: default`,
			wantCount: 2,
			wantFields: []string{"kind", "metadata.name"},
		},
		{
			name: "all identity fields changed",
			stateYAML: `apiVersion: v1
kind: Pod
metadata:
  name: old-name
  namespace: default`,
			planYAML: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: new-name
  namespace: production`,
			wantCount: 4,
			wantFields: []string{"kind", "apiVersion", "metadata.name", "metadata.namespace"},
		},
		{
			name: "no changes - only labels changed",
			stateYAML: `apiVersion: v1
kind: Pod
metadata:
  name: test
  labels:
    app: old`,
			planYAML: `apiVersion: v1
kind: Pod
metadata:
  name: test
  labels:
    app: new`,
			wantCount: 0,
			wantFields: []string{},
		},
		{
			name: "no changes - spec changed",
			stateYAML: `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  key: old-value`,
			planYAML: `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  key: new-value`,
			wantCount: 0,
			wantFields: []string{},
		},
		{
			name: "cluster-scoped resource - no namespace in either",
			stateYAML: `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: test-role`,
			planYAML: `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: test-role`,
			wantCount: 0,
			wantFields: []string{},
		},
		{
			name: "cluster-scoped resource - namespace added (invalid but should detect)",
			stateYAML: `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: test-role`,
			planYAML: `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: test-role
  namespace: default`,
			wantCount: 1,
			wantFields: []string{"metadata.namespace"},
		},
		{
			name: "namespace removed (cluster-scoped migration)",
			stateYAML: `apiVersion: v1
kind: Pod
metadata:
  name: test
  namespace: default`,
			planYAML: `apiVersion: v1
kind: Pod
metadata:
  name: test`,
			wantCount: 1,
			wantFields: []string{"metadata.namespace"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse YAMLs
			r := &manifestResource{}
			stateObj, err := r.parseYAML(tt.stateYAML)
			if err != nil {
				t.Fatalf("Failed to parse state YAML: %v", err)
			}

			planObj, err := r.parseYAML(tt.planYAML)
			if err != nil {
				t.Fatalf("Failed to parse plan YAML: %v", err)
			}

			// Call detectIdentityChanges
			changes := r.detectIdentityChanges(stateObj, planObj)

			// Check count
			if len(changes) != tt.wantCount {
				t.Errorf("detectIdentityChanges() returned %d changes, want %d", len(changes), tt.wantCount)
				for _, c := range changes {
					t.Logf("  Change: %s (%s → %s)", c.Field, c.OldValue, c.NewValue)
				}
			}

			// Check fields
			gotFields := make(map[string]bool)
			for _, c := range changes {
				gotFields[c.Field] = true
			}

			for _, wantField := range tt.wantFields {
				if !gotFields[wantField] {
					t.Errorf("Expected change in field %q but not found", wantField)
				}
			}

			for gotField := range gotFields {
				found := false
				for _, wantField := range tt.wantFields {
					if gotField == wantField {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Unexpected change in field %q", gotField)
				}
			}
		})
	}
}

func TestFormatResourceIdentity(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected string
	}{
		{
			name: "namespaced resource",
			yaml: `apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: default`,
			expected: "v1/Pod default/my-pod",
		},
		{
			name: "cluster-scoped resource",
			yaml: `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: admin`,
			expected: "rbac.authorization.k8s.io/v1/ClusterRole admin",
		},
		{
			name: "resource with version in apiVersion",
			yaml: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: production`,
			expected: "apps/v1/Deployment production/app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &manifestResource{}
			obj, err := r.parseYAML(tt.yaml)
			if err != nil {
				t.Fatalf("Failed to parse YAML: %v", err)
			}

			result := formatResourceIdentity(obj)
			if result != tt.expected {
				t.Errorf("formatResourceIdentity() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestIdentityChange_Values(t *testing.T) {
	// Test that IdentityChange correctly captures old and new values
	r := &manifestResource{}

	stateYAML := `apiVersion: v1
kind: Pod
metadata:
  name: old-name
  namespace: default`

	planYAML := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: new-name
  namespace: production`

	stateObj, _ := r.parseYAML(stateYAML)
	planObj, _ := r.parseYAML(planYAML)

	changes := r.detectIdentityChanges(stateObj, planObj)

	expectedChanges := map[string]struct{ old, new string }{
		"kind":               {"Pod", "Deployment"},
		"apiVersion":         {"v1", "apps/v1"},
		"metadata.name":      {"old-name", "new-name"},
		"metadata.namespace": {"default", "production"},
	}

	for _, change := range changes {
		expected, ok := expectedChanges[change.Field]
		if !ok {
			t.Errorf("Unexpected change field: %s", change.Field)
			continue
		}

		if change.OldValue != expected.old {
			t.Errorf("Field %s: OldValue = %q, want %q", change.Field, change.OldValue, expected.old)
		}
		if change.NewValue != expected.new {
			t.Errorf("Field %s: NewValue = %q, want %q", change.Field, change.NewValue, expected.new)
		}
	}
}

func TestDetectIdentityChanges_EmptyValues(t *testing.T) {
	// Test edge cases with empty/missing values
	tests := []struct {
		name      string
		stateObj  *unstructured.Unstructured
		planObj   *unstructured.Unstructured
		wantCount int
	}{
		{
			name: "both have empty namespace",
			stateObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata": map[string]interface{}{
						"name": "test",
					},
				},
			},
			planObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata": map[string]interface{}{
						"name": "test",
					},
				},
			},
			wantCount: 0,
		},
		{
			name: "state has namespace, plan doesn't",
			stateObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata": map[string]interface{}{
						"name":      "test",
						"namespace": "default",
					},
				},
			},
			planObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata": map[string]interface{}{
						"name": "test",
					},
				},
			},
			wantCount: 1, // namespace changed from "default" to ""
		},
	}

	r := &manifestResource{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := r.detectIdentityChanges(tt.stateObj, tt.planObj)
			if len(changes) != tt.wantCount {
				t.Errorf("detectIdentityChanges() returned %d changes, want %d", len(changes), tt.wantCount)
			}
		})
	}
}
