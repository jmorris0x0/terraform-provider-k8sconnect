// internal/k8sconnect/common/fieldmanagement/ownership_test.go
package fieldmanagement

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestExtractPathsFromFieldsV1_SimpleFields tests extraction of simple top-level fields
func TestExtractPathsFromFieldsV1_SimpleFields(t *testing.T) {
	fieldsV1 := map[string]interface{}{
		"f:spec": map[string]interface{}{
			"f:replicas": map[string]interface{}{},
		},
	}

	userJSON := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": 2,
		},
	}

	paths := extractPathsFromFieldsV1(fieldsV1, "", userJSON)

	assert.Contains(t, paths, "spec.replicas", "Should extract spec.replicas path")
}

// TestExtractPathsFromFieldsV1_NestedContainerResources tests extraction of deeply nested container resource fields
// This reproduces the bug where spec.template.spec.containers[0].resources.limits.cpu is not extracted
func TestExtractPathsFromFieldsV1_NestedContainerResources(t *testing.T) {
	// This is what Kubernetes managedFields looks like for container resources
	fieldsV1 := map[string]interface{}{
		"f:spec": map[string]interface{}{
			"f:template": map[string]interface{}{
				"f:spec": map[string]interface{}{
					"f:containers": map[string]interface{}{
						"k:{\"name\":\"nginx\"}": map[string]interface{}{
							"f:resources": map[string]interface{}{
								"f:limits": map[string]interface{}{
									"f:cpu": map[string]interface{}{},
								},
							},
						},
					},
				},
			},
		},
	}

	// User's YAML representation
	userJSON := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name": "nginx",
							"resources": map[string]interface{}{
								"limits": map[string]interface{}{
									"cpu": "100m",
								},
							},
						},
					},
				},
			},
		},
	}

	paths := extractPathsFromFieldsV1(fieldsV1, "", userJSON)

	// These are the paths we expect to extract
	expectedPaths := []string{
		"spec.template.spec.containers[0].resources.limits.cpu",
	}

	for _, expectedPath := range expectedPaths {
		assert.Contains(t, paths, expectedPath, "Should extract path: %s", expectedPath)
	}

	t.Logf("Extracted paths: %v", paths)
}

// TestExtractPathsFromFieldsV1_MultipleContainerResources tests CPU and Memory together
func TestExtractPathsFromFieldsV1_MultipleContainerResources(t *testing.T) {
	fieldsV1 := map[string]interface{}{
		"f:spec": map[string]interface{}{
			"f:template": map[string]interface{}{
				"f:spec": map[string]interface{}{
					"f:containers": map[string]interface{}{
						"k:{\"name\":\"nginx\"}": map[string]interface{}{
							"f:resources": map[string]interface{}{
								"f:limits": map[string]interface{}{
									"f:cpu":    map[string]interface{}{},
									"f:memory": map[string]interface{}{},
								},
							},
						},
					},
				},
			},
		},
	}

	userJSON := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name": "nginx",
							"resources": map[string]interface{}{
								"limits": map[string]interface{}{
									"cpu":    "100m",
									"memory": "128Mi",
								},
							},
						},
					},
				},
			},
		},
	}

	paths := extractPathsFromFieldsV1(fieldsV1, "", userJSON)

	expectedPaths := []string{
		"spec.template.spec.containers[0].resources.limits.cpu",
		"spec.template.spec.containers[0].resources.limits.memory",
	}

	for _, expectedPath := range expectedPaths {
		assert.Contains(t, paths, expectedPath, "Should extract path: %s", expectedPath)
	}

	t.Logf("Extracted paths: %v", paths)
}

// TestParseFieldsV1ToPathMap_RealWorldScenario tests with actual managedFields from Kubernetes
func TestParseFieldsV1ToPathMap_RealWorldScenario(t *testing.T) {
	// Simulate managedFields from two different controllers
	hpaManagerFields := map[string]interface{}{
		"f:spec": map[string]interface{}{
			"f:replicas": map[string]interface{}{},
		},
	}

	resourceControllerFields := map[string]interface{}{
		"f:spec": map[string]interface{}{
			"f:template": map[string]interface{}{
				"f:spec": map[string]interface{}{
					"f:containers": map[string]interface{}{
						"k:{\"name\":\"nginx\"}": map[string]interface{}{
							"f:resources": map[string]interface{}{
								"f:limits": map[string]interface{}{
									"f:cpu": map[string]interface{}{},
								},
							},
						},
					},
				},
			},
		},
	}

	hpaFieldsRaw, _ := json.Marshal(hpaManagerFields)
	resourceFieldsRaw, _ := json.Marshal(resourceControllerFields)

	managedFields := []metav1.ManagedFieldsEntry{
		{
			Manager:    "hpa-controller",
			APIVersion: "apps/v1",
			FieldsV1: &metav1.FieldsV1{
				Raw: hpaFieldsRaw,
			},
		},
		{
			Manager:    "resource-controller",
			APIVersion: "apps/v1",
			FieldsV1: &metav1.FieldsV1{
				Raw: resourceFieldsRaw,
			},
		},
	}

	userJSON := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": 2,
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name": "nginx",
							"resources": map[string]interface{}{
								"limits": map[string]interface{}{
									"cpu": "100m",
								},
							},
						},
					},
				},
			},
		},
	}

	ownership := ParseFieldsV1ToPathMap(managedFields, userJSON)

	// Verify ownership
	assert.Equal(t, "hpa-controller", ownership["spec.replicas"].Manager, "spec.replicas should be owned by hpa-controller")
	assert.Equal(t, "resource-controller", ownership["spec.template.spec.containers[0].resources.limits.cpu"].Manager,
		"spec.template.spec.containers[0].resources.limits.cpu should be owned by resource-controller")

	t.Logf("Field ownership map: %+v", ownership)
}
