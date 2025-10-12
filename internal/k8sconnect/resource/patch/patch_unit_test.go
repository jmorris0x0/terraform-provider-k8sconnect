// internal/k8sconnect/resource/patch/patch_unit_test.go
package patch

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Test 1.1-1.5: Self-patching prevention
func TestIsManagedByThisState(t *testing.T) {
	r := &patchResource{}
	ctx := context.Background()

	tests := []struct {
		name        string
		obj         *unstructured.Unstructured
		wantManaged bool
		description string
	}{
		{
			name: "resource with k8sconnect terraform-id annotation",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test",
						"annotations": map[string]interface{}{
							"k8sconnect.terraform.io/terraform-id": "abc-123",
						},
					},
				},
			},
			wantManaged: true,
			description: "Should detect k8sconnect manifest ownership annotation",
		},
		{
			name: "resource with legacy k8sconnect annotation",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test",
						"annotations": map[string]interface{}{
							"k8sconnect.io/owned-by": "terraform-abc",
						},
					},
				},
			},
			wantManaged: true,
			description: "Should detect legacy k8sconnect ownership annotation",
		},
		{
			name: "resource with k8sconnect field manager",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test",
						"managedFields": []interface{}{
							map[string]interface{}{
								"manager": "k8sconnect",
							},
						},
					},
				},
			},
			wantManaged: true,
			description: "Should detect k8sconnect field manager",
		},
		{
			name: "resource with k8sconnect-something field manager (not patch)",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test",
						"managedFields": []interface{}{
							map[string]interface{}{
								"manager": "k8sconnect-manifest",
							},
						},
					},
				},
			},
			wantManaged: true,
			description: "Should detect k8sconnect-* field manager (except patch)",
		},
		{
			name: "resource with k8sconnect-patch field manager",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test",
						"managedFields": []interface{}{
							map[string]interface{}{
								"manager": "k8sconnect-patch-abc",
							},
						},
					},
				},
			},
			wantManaged: false,
			description: "Should NOT detect k8sconnect-patch (different patch instance)",
		},
		{
			name: "resource managed by external controller",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test",
						"managedFields": []interface{}{
							map[string]interface{}{
								"manager": "eks.amazonaws.com",
							},
						},
					},
				},
			},
			wantManaged: false,
			description: "Should allow patching external controller resources",
		},
		{
			name: "resource with no annotations or managedFields",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test",
					},
				},
			},
			wantManaged: false,
			description: "Should allow patching unmanaged resources",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup managedFields properly if present
			if mfRaw, ok := tt.obj.Object["metadata"].(map[string]interface{})["managedFields"]; ok {
				if mfSlice, ok := mfRaw.([]interface{}); ok {
					var managedFields []metav1.ManagedFieldsEntry
					for _, mf := range mfSlice {
						if mfMap, ok := mf.(map[string]interface{}); ok {
							if manager, ok := mfMap["manager"].(string); ok {
								managedFields = append(managedFields, metav1.ManagedFieldsEntry{
									Manager: manager,
								})
							}
						}
					}
					tt.obj.SetManagedFields(managedFields)
				}
			}

			got := r.isManagedByThisState(ctx, tt.obj)
			if got != tt.wantManaged {
				t.Errorf("isManagedByThisState() = %v, want %v\n%s", got, tt.wantManaged, tt.description)
			}
		})
	}
}

// Test helper functions

func TestGroupFieldsByPreviousOwner(t *testing.T) {
	tests := []struct {
		name           string
		previousOwners map[string]string
		want           map[string][]string
	}{
		{
			name: "single owner, multiple fields",
			previousOwners: map[string]string{
				"spec.replicas":            "hpa",
				"spec.template.spec.env":   "hpa",
				"spec.template.spec.image": "hpa",
			},
			want: map[string][]string{
				"hpa": {"spec.replicas", "spec.template.spec.env", "spec.template.spec.image"},
			},
		},
		{
			name: "multiple owners",
			previousOwners: map[string]string{
				"spec.replicas":          "hpa",
				"metadata.labels.foo":    "kubectl",
				"metadata.labels.bar":    "kubectl",
				"spec.template.spec.env": "eks.amazonaws.com",
			},
			want: map[string][]string{
				"hpa":               {"spec.replicas"},
				"kubectl":           {"metadata.labels.foo", "metadata.labels.bar"},
				"eks.amazonaws.com": {"spec.template.spec.env"},
			},
		},
		{
			name:           "empty map",
			previousOwners: map[string]string{},
			want:           map[string][]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := groupFieldsByPreviousOwner(tt.previousOwners)

			if len(got) != len(tt.want) {
				t.Errorf("groupFieldsByPreviousOwner() returned %d groups, want %d", len(got), len(tt.want))
			}

			for owner, wantFields := range tt.want {
				gotFields, exists := got[owner]
				if !exists {
					t.Errorf("groupFieldsByPreviousOwner() missing owner %s", owner)
					continue
				}

				if len(gotFields) != len(wantFields) {
					t.Errorf("groupFieldsByPreviousOwner() owner %s has %d fields, want %d", owner, len(gotFields), len(wantFields))
				}

				// Check all fields are present (order doesn't matter for maps)
				fieldMap := make(map[string]bool)
				for _, f := range gotFields {
					fieldMap[f] = true
				}
				for _, wantField := range wantFields {
					if !fieldMap[wantField] {
						t.Errorf("groupFieldsByPreviousOwner() owner %s missing field %s", owner, wantField)
					}
				}
			}
		})
	}
}

func TestParseFieldPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []string
	}{
		{
			name: "simple field",
			path: "spec.replicas",
			want: []string{"spec", "replicas"},
		},
		{
			name: "nested field",
			path: "spec.template.spec.containers",
			want: []string{"spec", "template", "spec", "containers"},
		},
		{
			name: "array index",
			path: "spec.containers[0].name",
			want: []string{"spec", "containers", "0", "name"},
		},
		{
			name: "multiple array indices",
			path: "spec.containers[0].env[1].value",
			want: []string{"spec", "containers", "0", "env", "1", "value"},
		},
		{
			name: "metadata field",
			path: "metadata.labels.foo",
			want: []string{"metadata", "labels", "foo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFieldPath(tt.path)

			if len(got) != len(tt.want) {
				t.Errorf("parseFieldPath() = %v, want %v", got, tt.want)
				return
			}

			for i, part := range got {
				if part != tt.want[i] {
					t.Errorf("parseFieldPath() part %d = %s, want %s", i, part, tt.want[i])
				}
			}
		})
	}
}

func TestGetNestedValue(t *testing.T) {
	obj := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": int64(3),
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nginx",
							"image": "nginx:1.19",
						},
					},
				},
			},
		},
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"app": "test",
			},
		},
	}

	tests := []struct {
		name  string
		parts []string
		want  interface{}
	}{
		{
			name:  "simple field",
			parts: []string{"metadata", "labels", "app"},
			want:  "test",
		},
		{
			name:  "numeric field",
			parts: []string{"spec", "replicas"},
			want:  int64(3),
		},
		{
			name:  "array element",
			parts: []string{"spec", "template", "spec", "containers", "0", "name"},
			want:  "nginx",
		},
		{
			name:  "non-existent field",
			parts: []string{"spec", "nonexistent"},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getNestedValue(obj, tt.parts)

			if got != tt.want {
				t.Errorf("getNestedValue() = %v (type %T), want %v (type %T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func TestSetNestedValue(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		value interface{}
		check func(map[string]interface{}) bool
	}{
		{
			name:  "simple field",
			parts: []string{"spec", "replicas"},
			value: int64(5),
			check: func(obj map[string]interface{}) bool {
				spec, ok := obj["spec"].(map[string]interface{})
				if !ok {
					return false
				}
				replicas, ok := spec["replicas"].(int64)
				return ok && replicas == int64(5)
			},
		},
		{
			name:  "nested field",
			parts: []string{"metadata", "labels", "app"},
			value: "myapp",
			check: func(obj map[string]interface{}) bool {
				metadata, ok := obj["metadata"].(map[string]interface{})
				if !ok {
					return false
				}
				labels, ok := metadata["labels"].(map[string]interface{})
				if !ok {
					return false
				}
				app, ok := labels["app"].(string)
				return ok && app == "myapp"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := make(map[string]interface{})
			setNestedValue(obj, tt.parts, tt.value)

			if !tt.check(obj) {
				t.Errorf("setNestedValue() did not set value correctly. Result: %+v", obj)
			}
		})
	}
}
