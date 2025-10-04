// internal/k8sconnect/resource/manifest/projection_test.go
package manifest

import (
	"reflect"
	"sort"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestExtractFieldPaths_StrategicMerge(t *testing.T) {
	tests := []struct {
		name          string
		obj           map[string]interface{}
		expectedPaths []string
	}{
		{
			name: "containers with strategic merge",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nginx",
							"image": "public.ecr.aws/nginx/nginx:1.21",
							"ports": []interface{}{
								map[string]interface{}{
									"containerPort": 80,
									"protocol":      "TCP",
								},
							},
						},
						map[string]interface{}{
							"name":  "sidecar",
							"image": "sidecar:v1",
						},
					},
					"replicas": 3,
				},
			},
			expectedPaths: []string{
				"spec.containers[name=nginx].name",
				"spec.containers[name=nginx].image",
				"spec.containers[name=nginx].ports",
				"spec.containers[name=sidecar].name",
				"spec.containers[name=sidecar].image",
				"spec.replicas",
			},
		},
		{
			name: "volumes and mounts",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"volumes": []interface{}{
						map[string]interface{}{
							"name": "config",
							"configMap": map[string]interface{}{
								"name": "app-config",
							},
						},
					},
					"containers": []interface{}{
						map[string]interface{}{
							"name": "app",
							"volumeMounts": []interface{}{
								map[string]interface{}{
									"name":      "config",
									"mountPath": "/etc/config",
								},
							},
						},
					},
				},
			},
			expectedPaths: []string{
				"spec.volumes[name=config].name",
				"spec.volumes[name=config].configMap.name",
				"spec.containers[name=app].name",
				"spec.containers[name=app].volumeMounts[name=config].name",
				"spec.containers[name=app].volumeMounts[name=config].mountPath",
			},
		},
		{
			name: "env vars",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name": "app",
							"env": []interface{}{
								map[string]interface{}{
									"name":  "LOG_LEVEL",
									"value": "info",
								},
								map[string]interface{}{
									"name":  "PORT",
									"value": "8080",
								},
							},
						},
					},
				},
			},
			expectedPaths: []string{
				"spec.containers[name=app].name",
				"spec.containers[name=app].env[name=LOG_LEVEL].name",
				"spec.containers[name=app].env[name=LOG_LEVEL].value",
				"spec.containers[name=app].env[name=PORT].name",
				"spec.containers[name=app].env[name=PORT].value",
			},
		},
		{
			name: "positional arrays (args, command)",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":    "nginx",
							"command": []interface{}{"/bin/sh"},
							"args":    []interface{}{"-c", "echo hello"},
						},
					},
				},
			},
			expectedPaths: []string{
				"spec.containers[name=nginx].name",
				"spec.containers[name=nginx].command[0]",
				"spec.containers[name=nginx].args[0]",
				"spec.containers[name=nginx].args[1]",
			},
		},
		{
			name: "CRD arrays (array-level tracking)",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"customField": []interface{}{
						map[string]interface{}{
							"name":  "item1",
							"value": "val1",
						},
						map[string]interface{}{
							"name":  "item2",
							"value": "val2",
						},
					},
					"ports": []interface{}{ // Not containers, so uses array-level
						map[string]interface{}{
							"port":     80,
							"protocol": "TCP",
						},
					},
				},
			},
			expectedPaths: []string{
				"spec.customField", // Array-level tracking for unknown fields
				"spec.ports",       // Array-level tracking (not container ports)
			},
		},
		{
			name: "mixed nested structures",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test",
					"namespace": "default",
					"labels": map[string]interface{}{
						"app":     "test",
						"version": "v1",
					},
				},
			},
			expectedPaths: []string{
				"metadata.name",
				"metadata.namespace",
				"metadata.labels.app",
				"metadata.labels.version",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := extractAllFieldsFromYAML(tt.obj, "")

			// Sort for consistent comparison
			sort.Strings(paths)
			sort.Strings(tt.expectedPaths)

			if !reflect.DeepEqual(paths, tt.expectedPaths) {
				t.Errorf("Path mismatch\nExpected:\n")
				for _, p := range tt.expectedPaths {
					t.Errorf("  %s", p)
				}
				t.Errorf("\nGot:\n")
				for _, p := range paths {
					t.Errorf("  %s", p)
				}
			}
		})
	}
}

func TestProjectFields_StrategicMerge(t *testing.T) {
	tests := []struct {
		name       string
		source     map[string]interface{}
		paths      []string
		expected   map[string]interface{}
		shouldFail bool
	}{
		{
			name: "project reordered containers",
			source: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "sidecar", // Reordered
							"image": "sidecar:v2",
						},
						map[string]interface{}{
							"name":  "nginx", // Reordered
							"image": "public.ecr.aws/nginx/nginx:1.21",
						},
					},
				},
			},
			paths: []string{
				"spec.containers[name=nginx].image",
				"spec.containers[name=sidecar].image",
			},
			expected: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nginx",
							"image": "public.ecr.aws/nginx/nginx:1.21",
						},
						map[string]interface{}{
							"name":  "sidecar",
							"image": "sidecar:v2",
						},
					},
				},
			},
		},
		{
			name: "missing items handled gracefully",
			source: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nginx",
							"image": "public.ecr.aws/nginx/nginx:1.21",
						},
					},
				},
			},
			paths: []string{
				"spec.containers[name=nginx].image",
				"spec.containers[name=missing].image", // Doesn't exist
			},
			expected: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nginx",
							"image": "public.ecr.aws/nginx/nginx:1.21",
						},
					},
				},
			},
		},
		{
			name: "array-level projection",
			source: map[string]interface{}{
				"spec": map[string]interface{}{
					"customArray": []interface{}{
						map[string]interface{}{"id": "1", "value": "a"},
						map[string]interface{}{"id": "2", "value": "b"},
					},
				},
			},
			paths: []string{
				"spec.customArray",
			},
			expected: map[string]interface{}{
				"spec": map[string]interface{}{
					"customArray": []interface{}{
						map[string]interface{}{"id": "1", "value": "a"},
						map[string]interface{}{"id": "2", "value": "b"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := projectFields(tt.source, tt.paths)

			if tt.shouldFail {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(result, tt.expected) {
				resultJSON, _ := toJSON(result)
				expectedJSON, _ := toJSON(tt.expected)
				t.Errorf("Projection mismatch\nExpected:\n%s\n\nGot:\n%s", expectedJSON, resultJSON)
			}
		})
	}
}

func TestGetFieldByPath_KeyBasedSelectors(t *testing.T) {
	obj := map[string]interface{}{
		"spec": map[string]interface{}{
			"containers": []interface{}{
				map[string]interface{}{
					"name":  "init",
					"image": "init:v1",
				},
				map[string]interface{}{
					"name":  "nginx",
					"image": "public.ecr.aws/nginx/nginx:1.21",
					"ports": []interface{}{
						map[string]interface{}{
							"containerPort": 80,
							"name":          "http",
						},
						map[string]interface{}{
							"containerPort": 443,
							"name":          "https",
						},
					},
				},
			},
		},
	}

	tests := []struct {
		path     string
		expected interface{}
		exists   bool
	}{
		{
			path:     "spec.containers[name=nginx].image",
			expected: "public.ecr.aws/nginx/nginx:1.21",
			exists:   true,
		},
		{
			path:     "spec.containers[name=nginx].ports[containerPort=443].name",
			expected: "https",
			exists:   true,
		},
		{
			path:     "spec.containers[name=missing].image",
			expected: nil,
			exists:   false,
		},
		{
			path: "spec.containers[name=nginx].ports",
			expected: []interface{}{
				map[string]interface{}{
					"containerPort": 80,
					"name":          "http",
				},
				map[string]interface{}{
					"containerPort": 443,
					"name":          "https",
				},
			},
			exists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result, exists := getFieldByPath(obj, tt.path)

			if exists != tt.exists {
				t.Errorf("existence mismatch: expected %v, got %v", tt.exists, exists)
			}

			if exists && !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("value mismatch: expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestProjection_QuantityNormalization(t *testing.T) {
	// What the user wrote
	userYAML := map[string]interface{}{
		"spec": map[string]interface{}{
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"memory": "1Gi",
					"cpu":    "100m",
				},
				"limits": map[string]interface{}{
					"memory": "2Gi",
					"cpu":    "1",
				},
			},
		},
	}

	// What Kubernetes returns (normalized)
	k8sNormalized := map[string]interface{}{
		"metadata": map[string]interface{}{
			"uid": "12345", // Extra field
		},
		"spec": map[string]interface{}{
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"memory": "1073741824", // 1Gi in bytes
					"cpu":    "0.1",        // 100m as decimal
				},
				"limits": map[string]interface{}{
					"memory": "2147483648", // 2Gi in bytes
					"cpu":    "1",          // Unchanged
				},
			},
		},
		"status": map[string]interface{}{ // Extra field
			"phase": "Running",
		},
	}

	// Extract paths from user's YAML
	paths := extractAllFieldsFromYAML(userYAML, "")

	// Project from normalized state
	projection, err := projectFields(k8sNormalized, paths)
	if err != nil {
		t.Fatalf("projection failed: %v", err)
	}

	// Verify we get normalized values
	resources := projection["spec"].(map[string]interface{})["resources"].(map[string]interface{})
	requests := resources["requests"].(map[string]interface{})

	// Check normalized values are preserved
	if requests["memory"] != "1073741824" {
		t.Errorf("expected normalized memory '1073741824', got %v", requests["memory"])
	}
	if requests["cpu"] != "0.1" {
		t.Errorf("expected normalized CPU '0.1', got %v", requests["cpu"])
	}

	// Also check limits are preserved (even though they weren't changed)
	limitsMap := resources["limits"].(map[string]interface{})
	if limitsMap["memory"] != "2147483648" {
		t.Errorf("expected normalized limit memory '2147483648', got %v", limitsMap["memory"])
	}
	if limitsMap["cpu"] != "1" {
		t.Errorf("expected limit cpu '1', got %v", limitsMap["cpu"])
	}

	// Verify we don't include unmanaged fields
	if _, hasUID := projection["metadata"]; hasUID {
		t.Error("projection should not include unmanaged metadata")
	}
	if _, hasStatus := projection["status"]; hasStatus {
		t.Error("projection should not include status")
	}
}

// TestFilterIgnoredPaths tests the core logic of filtering paths based on ignore patterns
func TestFilterIgnoredPaths(t *testing.T) {
	tests := []struct {
		name          string
		allPaths      []string
		ignoreFields  []string
		expectedPaths []string
	}{
		{
			name: "ignore simple field",
			allPaths: []string{
				"metadata.name",
				"metadata.namespace",
				"metadata.annotations",
				"spec.replicas",
			},
			ignoreFields: []string{
				"metadata.annotations",
			},
			expectedPaths: []string{
				"metadata.name",
				"metadata.namespace",
				"spec.replicas",
			},
		},
		{
			name: "ignore nested field",
			allPaths: []string{
				"spec.replicas",
				"spec.template.metadata.labels",
				"spec.template.spec.containers[name=nginx].image",
			},
			ignoreFields: []string{
				"spec.replicas",
			},
			expectedPaths: []string{
				"spec.template.metadata.labels",
				"spec.template.spec.containers[name=nginx].image",
			},
		},
		{
			name: "ignore array element by index",
			allPaths: []string{
				"webhooks[0].clientConfig.service.name",
				"webhooks[0].clientConfig.caBundle",
				"webhooks[0].name",
				"webhooks[1].name",
			},
			ignoreFields: []string{
				"webhooks[0].clientConfig.caBundle",
			},
			expectedPaths: []string{
				"webhooks[0].clientConfig.service.name",
				"webhooks[0].name",
				"webhooks[1].name",
			},
		},
		{
			name: "ignore multiple fields",
			allPaths: []string{
				"metadata.annotations",
				"metadata.labels",
				"spec.replicas",
				"spec.template.spec.containers[name=nginx].image",
			},
			ignoreFields: []string{
				"metadata.annotations",
				"spec.replicas",
			},
			expectedPaths: []string{
				"metadata.labels",
				"spec.template.spec.containers[name=nginx].image",
			},
		},
		{
			name: "ignore field with children removes all children",
			allPaths: []string{
				"metadata.name",
				"metadata.annotations.app",
				"metadata.annotations.version",
				"spec.replicas",
			},
			ignoreFields: []string{
				"metadata.annotations",
			},
			expectedPaths: []string{
				"metadata.name",
				"spec.replicas",
			},
		},
		{
			name: "no ignore fields returns all paths",
			allPaths: []string{
				"metadata.name",
				"spec.replicas",
			},
			ignoreFields: []string{},
			expectedPaths: []string{
				"metadata.name",
				"spec.replicas",
			},
		},
		{
			name: "ignore non-existent field doesn't break",
			allPaths: []string{
				"metadata.name",
				"spec.replicas",
			},
			ignoreFields: []string{
				"spec.nonexistent",
			},
			expectedPaths: []string{
				"metadata.name",
				"spec.replicas",
			},
		},
		{
			name: "ignore field used by HPA",
			allPaths: []string{
				"metadata.name",
				"spec.replicas",
				"spec.template.spec.containers[name=nginx].image",
			},
			ignoreFields: []string{
				"spec.replicas", // HPA modifies this
			},
			expectedPaths: []string{
				"metadata.name",
				"spec.template.spec.containers[name=nginx].image",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterIgnoredPaths(tt.allPaths, tt.ignoreFields)

			// Sort for comparison
			sort.Strings(result)
			sort.Strings(tt.expectedPaths)

			if !reflect.DeepEqual(result, tt.expectedPaths) {
				t.Errorf("filterIgnoredPaths() mismatch\nGot:      %v\nExpected: %v", result, tt.expectedPaths)
			}
		})
	}
}

// TestPathMatchesIgnorePattern tests the matching logic
func TestPathMatchesIgnorePattern(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		ignorePattern string
		shouldMatch   bool
	}{
		{
			name:          "exact match",
			path:          "metadata.annotations",
			ignorePattern: "metadata.annotations",
			shouldMatch:   true,
		},
		{
			name:          "parent should match child",
			path:          "metadata.annotations.app",
			ignorePattern: "metadata.annotations",
			shouldMatch:   true,
		},
		{
			name:          "deep child should match parent",
			path:          "metadata.annotations.app.kubernetes.io/name",
			ignorePattern: "metadata.annotations",
			shouldMatch:   true,
		},
		{
			name:          "different field no match",
			path:          "metadata.labels",
			ignorePattern: "metadata.annotations",
			shouldMatch:   false,
		},
		{
			name:          "array index exact match",
			path:          "webhooks[0].clientConfig.caBundle",
			ignorePattern: "webhooks[0].clientConfig.caBundle",
			shouldMatch:   true,
		},
		{
			name:          "array index different index",
			path:          "webhooks[1].clientConfig.caBundle",
			ignorePattern: "webhooks[0].clientConfig.caBundle",
			shouldMatch:   false,
		},
		{
			name:          "strategic merge key exact match",
			path:          "spec.containers[name=nginx].image",
			ignorePattern: "spec.containers[name=nginx].image",
			shouldMatch:   true,
		},
		{
			name:          "strategic merge key child match",
			path:          "spec.containers[name=nginx].env[name=LOG_LEVEL].value",
			ignorePattern: "spec.containers[name=nginx].env",
			shouldMatch:   true,
		},
		{
			name:          "strategic merge key different name",
			path:          "spec.containers[name=sidecar].image",
			ignorePattern: "spec.containers[name=nginx].image",
			shouldMatch:   false,
		},
		{
			name:          "prefix match without full segment",
			path:          "metadata.labels",
			ignorePattern: "metadata.label",
			shouldMatch:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pathMatchesIgnorePattern(tt.path, tt.ignorePattern)
			if result != tt.shouldMatch {
				t.Errorf("pathMatchesIgnorePattern(%q, %q) = %v, want %v",
					tt.path, tt.ignorePattern, result, tt.shouldMatch)
			}
		})
	}
}

// TestProjectFieldsWithIgnore tests that projectFields respects ignore_fields
func TestProjectFieldsWithIgnore(t *testing.T) {
	tests := []struct {
		name         string
		source       map[string]interface{}
		paths        []string
		ignoreFields []string
		expected     map[string]interface{}
	}{
		{
			name: "ignore annotations in projection",
			source: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test",
					"namespace": "default",
					"annotations": map[string]interface{}{
						"controller.io/modified": "true",
					},
				},
				"spec": map[string]interface{}{
					"replicas": float64(3),
				},
			},
			paths: []string{
				"metadata.name",
				"metadata.namespace",
				"metadata.annotations.controller.io/modified",
				"spec.replicas",
			},
			ignoreFields: []string{
				"metadata.annotations",
			},
			expected: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"replicas": float64(3),
				},
			},
		},
		{
			name: "ignore replicas modified by HPA",
			source: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "deployment",
				},
				"spec": map[string]interface{}{
					"replicas": float64(5), // Modified by HPA
				},
			},
			paths: []string{
				"metadata.name",
				"spec.replicas",
			},
			ignoreFields: []string{
				"spec.replicas",
			},
			expected: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "deployment",
				},
				// spec is omitted when all its fields are ignored
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Filter paths first
			filteredPaths := filterIgnoredPaths(tt.paths, tt.ignoreFields)

			// Then project
			result, err := projectFields(tt.source, filteredPaths)
			if err != nil {
				t.Fatalf("projectFields() error = %v", err)
			}

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("projectFields() with ignore mismatch\nGot:      %+v\nExpected: %+v", result, tt.expected)
			}
		})
	}
}

// TestIgnoreFieldsIntegration tests the full integration with extractOwnedPaths
func TestIgnoreFieldsIntegration(t *testing.T) {
	t.Run("HPA modifies replicas - should not show drift when ignored", func(t *testing.T) {
		// User's YAML
		userYAML := map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name": "nginx",
			},
			"spec": map[string]interface{}{
				"replicas": float64(3),
			},
		}

		// Current state in cluster (HPA changed replicas)
		clusterState := map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name": "nginx",
			},
			"spec": map[string]interface{}{
				"replicas": float64(5), // HPA changed this
			},
		}

		ignoreFields := []string{"spec.replicas"}

		// Extract paths from user YAML
		allPaths := extractFieldPaths(userYAML, "")

		// Filter ignored paths
		filteredPaths := filterIgnoredPaths(allPaths, ignoreFields)

		// Project both states
		userProjection, err := projectFields(userYAML, filteredPaths)
		if err != nil {
			t.Fatalf("Failed to project user state: %v", err)
		}

		clusterProjection, err := projectFields(clusterState, filteredPaths)
		if err != nil {
			t.Fatalf("Failed to project cluster state: %v", err)
		}

		// They should match because we ignored the field that changed
		if !reflect.DeepEqual(userProjection, clusterProjection) {
			t.Errorf("Projections should match when ignoring changed field\nUser:    %+v\nCluster: %+v",
				userProjection, clusterProjection)
		}

		// Verify spec.replicas is NOT in the projection
		if spec, ok := userProjection["spec"].(map[string]interface{}); ok {
			if _, hasReplicas := spec["replicas"]; hasReplicas {
				t.Error("spec.replicas should not be in projection when ignored")
			}
		}
	})
}

// TestRemoveFieldsFromObject tests removing fields from unstructured objects
func TestRemoveFieldsFromObject(t *testing.T) {
	tests := []struct {
		name           string
		input          map[string]interface{}
		ignorePatterns []string
		expected       map[string]interface{}
	}{
		{
			name: "remove simple field",
			input: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"replicas": float64(3),
					"selector": map[string]interface{}{
						"app": "nginx",
					},
				},
			},
			ignorePatterns: []string{"spec.replicas"},
			expected: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{
						"app": "nginx",
					},
				},
			},
		},
		{
			name: "remove deeply nested field",
			input: map[string]interface{}{
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{
									"name":  "nginx",
									"image": "nginx:1.21",
								},
							},
						},
					},
				},
			},
			ignorePatterns: []string{"spec.template.spec.containers"},
			// Expected: empty map because removing the only field leaves empty parents,
			// which are cleaned up to prevent SSA field ownership consolidation
			expected: map[string]interface{}{},
		},
		{
			name: "remove multiple fields",
			input: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":        "test",
					"annotations": map[string]interface{}{"foo": "bar"},
				},
				"spec": map[string]interface{}{
					"replicas": float64(3),
					"selector": map[string]interface{}{"app": "nginx"},
				},
			},
			ignorePatterns: []string{"metadata.annotations", "spec.replicas"},
			expected: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "test",
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": "nginx"},
				},
			},
		},
		{
			name: "remove array element by index",
			input: map[string]interface{}{
				"webhooks": []interface{}{
					map[string]interface{}{
						"name": "webhook1",
						"clientConfig": map[string]interface{}{
							"caBundle": "base64data",
							"url":      "https://example.com",
						},
					},
				},
			},
			ignorePatterns: []string{"webhooks[0].clientConfig.caBundle"},
			expected: map[string]interface{}{
				"webhooks": []interface{}{
					map[string]interface{}{
						"name": "webhook1",
						"clientConfig": map[string]interface{}{
							"url": "https://example.com",
						},
					},
				},
			},
		},
		{
			name: "remove array element by key",
			input: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nginx",
							"image": "nginx:1.21",
							"resources": map[string]interface{}{
								"requests": map[string]interface{}{
									"cpu": "100m",
								},
							},
						},
					},
				},
			},
			ignorePatterns: []string{"spec.containers[name=nginx].resources"},
			expected: map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nginx",
							"image": "nginx:1.21",
						},
					},
				},
			},
		},
		{
			name: "nonexistent field - no error",
			input: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "test",
				},
			},
			ignorePatterns: []string{"spec.replicas"},
			expected: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "test",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: tt.input}
			result := removeFieldsFromObject(obj, tt.ignorePatterns)

			if !reflect.DeepEqual(result.Object, tt.expected) {
				t.Errorf("removeFieldsFromObject() mismatch\nGot:      %+v\nExpected: %+v",
					result.Object, tt.expected)
			}

			// Verify original object wasn't modified (DeepCopy test)
			if reflect.DeepEqual(obj.Object, result.Object) && len(tt.ignorePatterns) > 0 {
				// Only fail if we actually removed something
				if !reflect.DeepEqual(tt.input, tt.expected) {
					t.Error("removeFieldsFromObject() modified the original object")
				}
			}
		})
	}
}

// TestRemoveFieldsTransition tests that removing ignore_fields allows reclaiming ownership
func TestRemoveFieldsTransition(t *testing.T) {
	t.Run("removing ignore_fields reclaims field in next update", func(t *testing.T) {
		input := map[string]interface{}{
			"spec": map[string]interface{}{
				"replicas": float64(3),
				"selector": map[string]interface{}{
					"app": "nginx",
				},
			},
		}

		// Step 1: With ignore_fields set, spec.replicas is omitted
		obj1 := &unstructured.Unstructured{Object: input}
		result1 := removeFieldsFromObject(obj1, []string{"spec.replicas"})

		if _, hasReplicas := result1.Object["spec"].(map[string]interface{})["replicas"]; hasReplicas {
			t.Error("spec.replicas should be removed when in ignore_fields")
		}

		// Step 2: With ignore_fields removed/empty, spec.replicas is included
		obj2 := &unstructured.Unstructured{Object: input}
		result2 := removeFieldsFromObject(obj2, []string{}) // Empty ignore list

		if _, hasReplicas := result2.Object["spec"].(map[string]interface{})["replicas"]; !hasReplicas {
			t.Error("spec.replicas should be present when ignore_fields is empty")
		}

		// Verify the value is correct
		if replicas := result2.Object["spec"].(map[string]interface{})["replicas"]; replicas != float64(3) {
			t.Errorf("Expected replicas=3, got %v", replicas)
		}
	})
}
