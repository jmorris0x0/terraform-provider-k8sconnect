// internal/k8sconnect/resource/manifest/projection_test.go
package manifest

import (
	"reflect"
	"sort"
	"testing"
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
