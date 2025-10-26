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
// Note: After fixing shared ownership bug, we only track k8sconnect ownership, not other managers
func TestParseFieldsV1ToPathMap_RealWorldScenario(t *testing.T) {
	// Simulate managedFields with k8sconnect and other controllers
	k8sconnectFields := map[string]interface{}{
		"f:spec": map[string]interface{}{
			"f:selector": map[string]interface{}{},
			"f:template": map[string]interface{}{
				"f:metadata": map[string]interface{}{
					"f:labels": map[string]interface{}{
						"f:app": map[string]interface{}{},
					},
				},
				"f:spec": map[string]interface{}{
					"f:containers": map[string]interface{}{
						"k:{\"name\":\"nginx\"}": map[string]interface{}{
							"f:image": map[string]interface{}{},
							"f:name":  map[string]interface{}{},
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

	// HPA might own replicas, but we shouldn't track it
	hpaManagerFields := map[string]interface{}{
		"f:spec": map[string]interface{}{
			"f:replicas": map[string]interface{}{},
		},
	}

	k8sconnectFieldsRaw, _ := json.Marshal(k8sconnectFields)
	hpaFieldsRaw, _ := json.Marshal(hpaManagerFields)

	managedFields := []metav1.ManagedFieldsEntry{
		{
			Manager:    "hpa-controller",
			APIVersion: "apps/v1",
			FieldsV1: &metav1.FieldsV1{
				Raw: hpaFieldsRaw,
			},
		},
		{
			Manager:    "k8sconnect",
			APIVersion: "apps/v1",
			FieldsV1: &metav1.FieldsV1{
				Raw: k8sconnectFieldsRaw,
			},
		},
	}

	userJSON := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": 2,
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "nginx",
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app": "nginx",
					},
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nginx",
							"image": "nginx:1.21",
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

	// We should ONLY see k8sconnect ownership, even though hpa-controller also exists
	assert.Equal(t, "k8sconnect", ownership["spec.selector"].Manager,
		"spec.selector should be owned by k8sconnect")
	assert.Equal(t, "k8sconnect", ownership["spec.template.spec.containers[0].resources.limits.cpu"].Manager,
		"cpu limit should be owned by k8sconnect")
	assert.Equal(t, "k8sconnect", ownership["spec.template.spec.containers[0].image"].Manager,
		"container image should be owned by k8sconnect")

	// hpa-controller's fields should NOT be in our ownership map
	_, replicasOwned := ownership["spec.replicas"]
	assert.False(t, replicasOwned, "spec.replicas should NOT be tracked (owned by hpa-controller, not k8sconnect)")

	// Verify no other managers appear
	for _, owner := range ownership {
		assert.Equal(t, "k8sconnect", owner.Manager, "Only k8sconnect ownership should be tracked")
	}

	t.Logf("Field ownership map (k8sconnect only): %+v", ownership)
}

// TestParseFieldsV1ToPathMap_SharedOwnership tests that when multiple managers
// own the same field (shared ownership from identical values), we only report
// k8sconnect's ownership, not other managers.
func TestParseFieldsV1ToPathMap_SharedOwnership(t *testing.T) {
	// Simulate shared ownership: both k8sconnect and kubectl-patch own the same field
	k8sconnectFields := map[string]interface{}{
		"f:spec": map[string]interface{}{
			"f:replicas": map[string]interface{}{},
			"f:template": map[string]interface{}{
				"f:spec": map[string]interface{}{
					"f:containers": map[string]interface{}{
						"k:{\"name\":\"app\"}": map[string]interface{}{
							"f:env": map[string]interface{}{
								"k:{\"name\":\"EXTERNAL_VAR\"}": map[string]interface{}{
									".":       map[string]interface{}{},
									"f:name":  map[string]interface{}{}, // k8sconnect owns this
									"f:value": map[string]interface{}{}, // k8sconnect owns this too
								},
							},
							"f:name": map[string]interface{}{}, // Container name
						},
					},
				},
			},
		},
	}

	kubectlPatchFields := map[string]interface{}{
		"f:spec": map[string]interface{}{
			"f:template": map[string]interface{}{
				"f:spec": map[string]interface{}{
					"f:containers": map[string]interface{}{
						"k:{\"name\":\"app\"}": map[string]interface{}{
							"f:env": map[string]interface{}{
								"k:{\"name\":\"EXTERNAL_VAR\"}": map[string]interface{}{
									".":       map[string]interface{}{},
									"f:name":  map[string]interface{}{}, // kubectl-patch ALSO owns this (shared)
									"f:value": map[string]interface{}{}, // kubectl-patch owns this
								},
							},
							"f:name": map[string]interface{}{}, // kubectl-patch ALSO owns this (shared)
						},
					},
				},
			},
		},
	}

	k8sconnectFieldsRaw, _ := json.Marshal(k8sconnectFields)
	kubectlPatchFieldsRaw, _ := json.Marshal(kubectlPatchFields)

	userJSON := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": 2,
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name": "app",
							"env": []interface{}{
								map[string]interface{}{
									"name":  "EXTERNAL_VAR",
									"value": "external-value",
								},
							},
						},
					},
				},
			},
		},
	}

	t.Run("k8sconnect first in array", func(t *testing.T) {
		managedFields := []metav1.ManagedFieldsEntry{
			{
				Manager:    "k8sconnect",
				APIVersion: "apps/v1",
				FieldsV1: &metav1.FieldsV1{
					Raw: k8sconnectFieldsRaw,
				},
			},
			{
				Manager:    "kubectl-patch",
				APIVersion: "apps/v1",
				FieldsV1: &metav1.FieldsV1{
					Raw: kubectlPatchFieldsRaw,
				},
			},
		}

		ownership := ParseFieldsV1ToPathMap(managedFields, userJSON)

		// We should ONLY report k8sconnect ownership, even though kubectl-patch also owns these
		assert.Equal(t, "k8sconnect", ownership["spec.replicas"].Manager,
			"spec.replicas should be owned by k8sconnect only")
		assert.Equal(t, "k8sconnect", ownership["spec.template.spec.containers[0].env[0].name"].Manager,
			"env[0].name should be owned by k8sconnect only (not kubectl-patch)")
		assert.Equal(t, "k8sconnect", ownership["spec.template.spec.containers[0].env[0].value"].Manager,
			"env[0].value should be owned by k8sconnect only")
		assert.Equal(t, "k8sconnect", ownership["spec.template.spec.containers[0].name"].Manager,
			"container name should be owned by k8sconnect only (not kubectl-patch)")

		// kubectl-patch should NOT appear in ownership map at all
		for _, owner := range ownership {
			assert.NotEqual(t, "kubectl-patch", owner.Manager,
				"kubectl-patch should not appear in ownership map")
		}
	})

	t.Run("kubectl-patch first in array", func(t *testing.T) {
		// Test with reversed order - behavior should be IDENTICAL
		managedFields := []metav1.ManagedFieldsEntry{
			{
				Manager:    "kubectl-patch",
				APIVersion: "apps/v1",
				FieldsV1: &metav1.FieldsV1{
					Raw: kubectlPatchFieldsRaw,
				},
			},
			{
				Manager:    "k8sconnect",
				APIVersion: "apps/v1",
				FieldsV1: &metav1.FieldsV1{
					Raw: k8sconnectFieldsRaw,
				},
			},
		}

		ownership := ParseFieldsV1ToPathMap(managedFields, userJSON)

		// Result should be IDENTICAL to previous test - order shouldn't matter
		assert.Equal(t, "k8sconnect", ownership["spec.replicas"].Manager,
			"spec.replicas should be owned by k8sconnect only (order independent)")
		assert.Equal(t, "k8sconnect", ownership["spec.template.spec.containers[0].env[0].name"].Manager,
			"env[0].name should be owned by k8sconnect only (order independent)")
		assert.Equal(t, "k8sconnect", ownership["spec.template.spec.containers[0].env[0].value"].Manager,
			"env[0].value should be owned by k8sconnect only (order independent)")
		assert.Equal(t, "k8sconnect", ownership["spec.template.spec.containers[0].name"].Manager,
			"container name should be owned by k8sconnect only (order independent)")

		// kubectl-patch should NOT appear in ownership map at all
		for _, owner := range ownership {
			assert.NotEqual(t, "kubectl-patch", owner.Manager,
				"kubectl-patch should not appear in ownership map (order independent)")
		}
	})
}
