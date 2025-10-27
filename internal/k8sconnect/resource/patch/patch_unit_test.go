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

// Test helper functions (dead code tests removed)
