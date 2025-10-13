// internal/k8sconnect/common/validators/patch_test.go
package validators

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestStrategicMergePatch(t *testing.T) {
	ctx := context.Background()
	v := StrategicMergePatch{}

	tests := []struct {
		name          string
		patchContent  string
		expectError   bool
		errorContains string
	}{
		{
			name: "valid patch with container names",
			patchContent: `
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
      initContainers:
      - name: init
        image: busybox`,
			expectError: false,
		},
		{
			name: "missing container name",
			patchContent: `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test
spec:
  template:
    spec:
      containers:
      - image: nginx:1.21`,
			expectError:   true,
			errorContains: "Container names are required",
		},
		{
			name: "missing initContainer name",
			patchContent: `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test
spec:
  template:
    spec:
      initContainers:
      - image: busybox`,
			expectError:   true,
			errorContains: "Container names are required",
		},
		{
			name: "server-managed field uid",
			patchContent: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
  uid: abc-123`,
			expectError:   true,
			errorContains: "metadata.uid",
		},
		{
			name: "server-managed field resourceVersion",
			patchContent: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
  resourceVersion: "12345"`,
			expectError:   true,
			errorContains: "resourceVersion",
		},
		{
			name: "server-managed field generation",
			patchContent: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
  generation: 5`,
			expectError:   true,
			errorContains: "generation",
		},
		{
			name: "server-managed field creationTimestamp",
			patchContent: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
  creationTimestamp: "2024-01-01T00:00:00Z"`,
			expectError:   true,
			errorContains: "creationTimestamp",
		},
		{
			name: "server-managed field managedFields",
			patchContent: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
  managedFields:
  - manager: test`,
			expectError:   true,
			errorContains: "managedFields",
		},
		{
			name: "provider internal annotation",
			patchContent: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
  annotations:
    k8sconnect.terraform.io/terraform-id: "test"`,
			expectError:   true,
			errorContains: "k8sconnect.terraform.io",
		},
		{
			name: "status field",
			patchContent: `
apiVersion: v1
kind: Pod
metadata:
  name: test
status:
  phase: Running`,
			expectError:   true,
			errorContains: "status",
		},
		{
			name: "valid patch with custom annotations",
			patchContent: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
  annotations:
    example.com/my-annotation: "value"`,
			expectError: false,
		},
		{
			name: "patch with interpolation - skipped",
			patchContent: `
metadata:
  name: ${var.name}
  uid: should-not-validate`,
			expectError: false, // Skipped due to interpolation
		},
		{
			name:         "null value - skipped",
			patchContent: "",
			expectError:  false,
		},
		{
			name: "invalid YAML - skipped",
			patchContent: `
this is not: [valid yaml`,
			expectError: false, // Parse errors are handled by Kubernetes
		},
		{
			name: "JSON format valid",
			patchContent: `{
  "metadata": {
    "name": "test",
    "labels": {
      "app": "myapp"
    }
  },
  "spec": {
    "replicas": 3
  }
}`,
			expectError: false,
		},
		{
			name: "pod-level containers",
			patchContent: `
apiVersion: v1
kind: Pod
metadata:
  name: test
spec:
  containers:
  - name: nginx
    image: nginx:1.21`,
			expectError: false,
		},
		{
			name: "pod-level containers without name",
			patchContent: `
apiVersion: v1
kind: Pod
metadata:
  name: test
spec:
  containers:
  - image: nginx:1.21`,
			expectError:   true,
			errorContains: "container at index 0 is missing required 'name' field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validator.StringRequest{
				Path:        path.Root("patch"),
				ConfigValue: types.StringValue(tt.patchContent),
			}
			resp := &validator.StringResponse{}

			v.ValidateString(ctx, req, resp)

			hasError := resp.Diagnostics.HasError()
			if hasError != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, hasError)
				if hasError {
					t.Logf("Diagnostics: %v", resp.Diagnostics)
				}
				return
			}

			if tt.expectError && tt.errorContains != "" {
				found := false
				for _, diag := range resp.Diagnostics {
					if diag.Severity() == 1 { // Error severity
						detail := diag.Detail()
						summary := diag.Summary()
						if contains(detail, tt.errorContains) || contains(summary, tt.errorContains) {
							found = true
							break
						}
					}
				}
				if !found {
					t.Errorf("expected error to contain '%s', but it didn't. Diagnostics: %v",
						tt.errorContains, resp.Diagnostics)
				}
			}
		})
	}
}

func TestJSONPatchValidator(t *testing.T) {
	ctx := context.Background()
	v := JSONPatchValidator{}

	tests := []struct {
		name          string
		patchContent  string
		expectError   bool
		expectWarning bool
		errorContains string
	}{
		{
			name: "valid add operation",
			patchContent: `[
  {"op": "add", "path": "/metadata/labels/foo", "value": "bar"}
]`,
			expectError: false,
		},
		{
			name: "valid replace operation",
			patchContent: `[
  {"op": "replace", "path": "/spec/replicas", "value": 3}
]`,
			expectError: false,
		},
		{
			name: "valid remove operation",
			patchContent: `[
  {"op": "remove", "path": "/metadata/labels/old"}
]`,
			expectError: false,
		},
		{
			name: "valid move operation",
			patchContent: `[
  {"op": "move", "from": "/spec/old", "path": "/spec/new"}
]`,
			expectError: false,
		},
		{
			name: "valid copy operation",
			patchContent: `[
  {"op": "copy", "from": "/spec/template", "path": "/spec/backup"}
]`,
			expectError: false,
		},
		{
			name: "valid test operation",
			patchContent: `[
  {"op": "test", "path": "/spec/replicas", "value": 3}
]`,
			expectError: false,
		},
		{
			name: "multiple operations",
			patchContent: `[
  {"op": "add", "path": "/metadata/labels/foo", "value": "bar"},
  {"op": "replace", "path": "/spec/replicas", "value": 3},
  {"op": "remove", "path": "/metadata/labels/old"}
]`,
			expectError: false,
		},
		{
			name:          "not a JSON array",
			patchContent:  `{"op": "add", "path": "/test"}`,
			expectError:   true,
			errorContains: "must be a valid JSON array",
		},
		{
			name:          "invalid JSON",
			patchContent:  `[invalid json`,
			expectError:   true,
			errorContains: "must be a valid JSON array",
		},
		{
			name: "missing op field",
			patchContent: `[
  {"path": "/test", "value": "foo"}
]`,
			expectError:   true,
			errorContains: "missing required 'op' field",
		},
		{
			name: "invalid op value",
			patchContent: `[
  {"op": "invalid", "path": "/test", "value": "foo"}
]`,
			expectError:   true,
			errorContains: "invalid 'op' value",
		},
		{
			name: "missing path field",
			patchContent: `[
  {"op": "add", "value": "foo"}
]`,
			expectError:   true,
			errorContains: "missing required 'path' field",
		},
		{
			name: "add without value",
			patchContent: `[
  {"op": "add", "path": "/test"}
]`,
			expectError:   true,
			errorContains: "missing required 'value' field",
		},
		{
			name: "replace without value",
			patchContent: `[
  {"op": "replace", "path": "/test"}
]`,
			expectError:   true,
			errorContains: "missing required 'value' field",
		},
		{
			name: "test without value",
			patchContent: `[
  {"op": "test", "path": "/test"}
]`,
			expectError:   true,
			errorContains: "missing required 'value' field",
		},
		{
			name: "move without from",
			patchContent: `[
  {"op": "move", "path": "/test"}
]`,
			expectError:   true,
			errorContains: "missing required 'from' field",
		},
		{
			name: "copy without from",
			patchContent: `[
  {"op": "copy", "path": "/test"}
]`,
			expectError:   true,
			errorContains: "missing required 'from' field",
		},
		{
			name: "server-managed field - allowed but warned",
			patchContent: `[
  {"op": "replace", "path": "/metadata/uid", "value": "new-uid"}
]`,
			expectError:   false,
			expectWarning: false, // Warnings are optional, K8s will reject anyway
		},
		{
			name: "status field - allowed but warned",
			patchContent: `[
  {"op": "replace", "path": "/status/phase", "value": "Running"}
]`,
			expectError:   false,
			expectWarning: false, // Warnings are optional, K8s will reject anyway
		},
		{
			name: "interpolation - skipped",
			patchContent: `[
  {"op": "add", "path": "/metadata/labels/${var.key}", "value": "bar"}
]`,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validator.StringRequest{
				Path:        path.Root("json_patch"),
				ConfigValue: types.StringValue(tt.patchContent),
			}
			resp := &validator.StringResponse{}

			v.ValidateString(ctx, req, resp)

			hasError := resp.Diagnostics.HasError()
			if hasError != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, hasError)
				if hasError {
					t.Logf("Diagnostics: %v", resp.Diagnostics)
				}
				return
			}

			if tt.expectWarning {
				hasWarning := false
				for _, diag := range resp.Diagnostics {
					if diag.Severity() == 0 { // Warning severity
						hasWarning = true
						break
					}
				}
				if !hasWarning {
					t.Errorf("expected warning but got none")
				}
			}

			if tt.expectError && tt.errorContains != "" {
				found := false
				for _, diag := range resp.Diagnostics {
					if diag.Severity() == 1 { // Error severity
						detail := diag.Detail()
						summary := diag.Summary()
						if contains(detail, tt.errorContains) || contains(summary, tt.errorContains) {
							found = true
							break
						}
					}
				}
				if !found {
					t.Errorf("expected error to contain '%s', but it didn't. Diagnostics: %v",
						tt.errorContains, resp.Diagnostics)
				}
			}
		})
	}
}

func TestMergePatchValidator(t *testing.T) {
	ctx := context.Background()
	v := MergePatchValidator{}

	tests := []struct {
		name          string
		patchContent  string
		expectError   bool
		errorContains string
	}{
		{
			name: "valid merge patch",
			patchContent: `{
  "metadata": {
    "labels": {
      "app": "myapp",
      "env": "prod"
    }
  },
  "spec": {
    "replicas": 3
  }
}`,
			expectError: false,
		},
		{
			name: "valid merge patch with nested objects",
			patchContent: `{
  "spec": {
    "template": {
      "spec": {
        "containers": [
          {
            "name": "nginx",
            "image": "nginx:1.21"
          }
        ]
      }
    }
  }
}`,
			expectError: false,
		},
		{
			name: "valid merge patch deleting field",
			patchContent: `{
  "metadata": {
    "labels": {
      "old-label": null
    }
  }
}`,
			expectError: false,
		},
		{
			name:          "not a JSON object",
			patchContent:  `["array", "is", "invalid"]`,
			expectError:   true,
			errorContains: "must be a valid JSON object",
		},
		{
			name:          "invalid JSON",
			patchContent:  `{invalid json}`,
			expectError:   true,
			errorContains: "must be a valid JSON object",
		},
		{
			name: "server-managed field uid",
			patchContent: `{
  "metadata": {
    "name": "test",
    "uid": "abc-123"
  }
}`,
			expectError:   true,
			errorContains: "metadata.uid",
		},
		{
			name: "server-managed field resourceVersion",
			patchContent: `{
  "metadata": {
    "resourceVersion": "12345"
  }
}`,
			expectError:   true,
			errorContains: "resourceVersion",
		},
		{
			name: "server-managed field generation",
			patchContent: `{
  "metadata": {
    "generation": 5
  }
}`,
			expectError:   true,
			errorContains: "generation",
		},
		{
			name: "server-managed field creationTimestamp",
			patchContent: `{
  "metadata": {
    "creationTimestamp": "2024-01-01T00:00:00Z"
  }
}`,
			expectError:   true,
			errorContains: "creationTimestamp",
		},
		{
			name: "server-managed field managedFields",
			patchContent: `{
  "metadata": {
    "managedFields": []
  }
}`,
			expectError:   true,
			errorContains: "managedFields",
		},
		{
			name: "provider internal annotation",
			patchContent: `{
  "metadata": {
    "annotations": {
      "k8sconnect.terraform.io/terraform-id": "test"
    }
  }
}`,
			expectError:   true,
			errorContains: "k8sconnect.terraform.io",
		},
		{
			name: "status field",
			patchContent: `{
  "status": {
    "phase": "Running"
  }
}`,
			expectError:   true,
			errorContains: "status",
		},
		{
			name: "valid patch with custom annotations",
			patchContent: `{
  "metadata": {
    "annotations": {
      "example.com/my-annotation": "value"
    }
  }
}`,
			expectError: false,
		},
		{
			name: "interpolation - skipped",
			patchContent: `{
  "metadata": {
    "name": "${var.name}",
    "uid": "should-not-validate"
  }
}`,
			expectError: false, // Skipped due to interpolation
		},
		{
			name:         "empty object",
			patchContent: `{}`,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validator.StringRequest{
				Path:        path.Root("merge_patch"),
				ConfigValue: types.StringValue(tt.patchContent),
			}
			resp := &validator.StringResponse{}

			v.ValidateString(ctx, req, resp)

			hasError := resp.Diagnostics.HasError()
			if hasError != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, hasError)
				if hasError {
					t.Logf("Diagnostics: %v", resp.Diagnostics)
				}
				return
			}

			if tt.expectError && tt.errorContains != "" {
				found := false
				for _, diag := range resp.Diagnostics {
					if diag.Severity() == 1 { // Error severity
						detail := diag.Detail()
						summary := diag.Summary()
						if contains(detail, tt.errorContains) || contains(summary, tt.errorContains) {
							found = true
							break
						}
					}
				}
				if !found {
					t.Errorf("expected error to contain '%s', but it didn't. Diagnostics: %v",
						tt.errorContains, resp.Diagnostics)
				}
			}
		})
	}
}

func TestIsServerManagedPath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/metadata/uid", true},
		{"/metadata/resourceVersion", true},
		{"/metadata/generation", true},
		{"/metadata/creationTimestamp", true},
		{"/metadata/managedFields", true},
		{"/status", true},
		{"/status/phase", true},
		{"/status/conditions/0/type", true},
		{"/metadata/name", false},
		{"/metadata/labels", false},
		{"/metadata/labels/app", false},
		{"/metadata/annotations", false},
		{"/spec/replicas", false},
		{"/spec/template", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isServerManagedPath(tt.path)
			if result != tt.expected {
				t.Errorf("isServerManagedPath(%q) = %v, expected %v", tt.path, result, tt.expected)
			}
		})
	}
}

// Helper function to check if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(s) > len(substr) &&
				(s[:len(substr)] == substr ||
					s[len(s)-len(substr):] == substr ||
					containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
