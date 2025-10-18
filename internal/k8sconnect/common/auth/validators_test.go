// internal/k8sconnect/common/auth/validators_test.go
package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestURLValidator(t *testing.T) {
	ctx := context.Background()
	v := urlValidator{}

	tests := []struct {
		name          string
		value         types.String
		expectError   bool
		errorContains string
	}{
		{
			name:        "valid http URL",
			value:       types.StringValue("http://example.com"),
			expectError: false,
		},
		{
			name:        "valid https URL",
			value:       types.StringValue("https://kubernetes.default.svc"),
			expectError: false,
		},
		{
			name:        "valid URL with port",
			value:       types.StringValue("https://example.com:6443"),
			expectError: false,
		},
		{
			name:        "valid URL with path",
			value:       types.StringValue("https://example.com/api/v1"),
			expectError: false,
		},
		{
			name:        "valid URL with query params",
			value:       types.StringValue("https://example.com:6443/api?timeout=30"),
			expectError: false,
		},
		{
			name:        "valid URL with authentication",
			value:       types.StringValue("https://user:pass@example.com:6443"),
			expectError: false,
		},
		{
			name:          "missing scheme",
			value:         types.StringValue("example.com"),
			expectError:   true,
			errorContains: "must include a scheme",
		},
		{
			name:        "missing scheme with port - parsed as scheme:opaque",
			value:       types.StringValue("example.com:6443"),
			expectError: false, // url.Parse treats "example.com" as the scheme
		},
		{
			name:          "invalid URL characters",
			value:         types.StringValue("https://exa mple.com"),
			expectError:   true,
			errorContains: "not a valid URL",
		},
		{
			name:        "null value - no validation",
			value:       types.StringNull(),
			expectError: false,
		},
		{
			name:        "unknown value - no validation",
			value:       types.StringUnknown(),
			expectError: false,
		},
		{
			name:        "localhost URL",
			value:       types.StringValue("https://localhost:6443"),
			expectError: false,
		},
		{
			name:        "IP address URL",
			value:       types.StringValue("https://192.168.1.1:6443"),
			expectError: false,
		},
		{
			name:        "IPv6 URL",
			value:       types.StringValue("https://[::1]:6443"),
			expectError: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := validator.StringRequest{
				Path:        path.Root("test"),
				ConfigValue: tc.value,
			}
			resp := &validator.StringResponse{}

			v.ValidateString(ctx, req, resp)

			if tc.expectError {
				if !resp.Diagnostics.HasError() {
					t.Error("expected error, got none")
				}
				if tc.errorContains != "" {
					found := false
					for _, diag := range resp.Diagnostics.Errors() {
						if strings.Contains(diag.Summary(), tc.errorContains) ||
							strings.Contains(diag.Detail(), tc.errorContains) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got: %v", tc.errorContains, resp.Diagnostics)
					}
				}
			} else {
				if resp.Diagnostics.HasError() {
					t.Errorf("unexpected error: %v", resp.Diagnostics)
				}
			}
		})
	}
}

func TestURLValidatorDescriptions(t *testing.T) {
	ctx := context.Background()
	v := urlValidator{}

	desc := v.Description(ctx)
	if desc == "" {
		t.Error("expected non-empty description")
	}

	mdDesc := v.MarkdownDescription(ctx)
	if mdDesc == "" {
		t.Error("expected non-empty markdown description")
	}
}

func TestKubeconfigValidator(t *testing.T) {
	ctx := context.Background()
	v := kubeconfigValidator{}

	tests := []struct {
		name          string
		value         types.String
		expectError   bool
		errorContains string
	}{
		{
			name: "valid kubeconfig YAML",
			value: types.StringValue(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://example.com
  name: example
contexts:
- context:
    cluster: example
    user: admin
  name: example-context
current-context: example-context
users:
- name: admin
  user:
    token: abc123
`),
			expectError: false,
		},
		{
			name: "simple YAML map",
			value: types.StringValue(`
key1: value1
key2: value2
`),
			expectError: false,
		},
		{
			name: "YAML list",
			value: types.StringValue(`
- item1
- item2
- item3
`),
			expectError: false,
		},
		{
			name: "nested YAML structure",
			value: types.StringValue(`
metadata:
  name: test
  labels:
    app: myapp
spec:
  replicas: 3
`),
			expectError: false,
		},
		{
			name:        "plain string - valid YAML",
			value:       types.StringValue("key value"),
			expectError: false, // Plain strings are valid YAML
		},
		{
			name: "invalid YAML - bad indentation",
			value: types.StringValue(`
key1: value1
  key2: value2
`),
			expectError:   true,
			errorContains: "not valid YAML",
		},
		{
			name:          "invalid YAML - unclosed quote",
			value:         types.StringValue(`key: "value`),
			expectError:   true,
			errorContains: "not valid YAML",
		},
		{
			name:          "invalid YAML - duplicate keys",
			value:         types.StringValue("key: value1\nkey: value2"),
			expectError:   true,
			errorContains: "not valid YAML",
		},
		{
			name:        "empty string - valid YAML",
			value:       types.StringValue(""),
			expectError: false,
		},
		{
			name:        "null value - no validation",
			value:       types.StringNull(),
			expectError: false,
		},
		{
			name:        "unknown value - no validation",
			value:       types.StringUnknown(),
			expectError: false,
		},
		{
			name: "YAML with special characters",
			value: types.StringValue(`
message: "Hello, World!"
path: /usr/local/bin
regex: "^[a-z]+$"
`),
			expectError: false,
		},
		{
			name: "YAML with multiline string",
			value: types.StringValue(`
description: |
  This is a multiline
  string in YAML
  format
`),
			expectError: false,
		},
		{
			name: "YAML with anchors and aliases",
			value: types.StringValue(`
defaults: &defaults
  timeout: 30
  retries: 3

production:
  <<: *defaults
  environment: prod
`),
			expectError: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := validator.StringRequest{
				Path:        path.Root("kubeconfig"),
				ConfigValue: tc.value,
			}
			resp := &validator.StringResponse{}

			v.ValidateString(ctx, req, resp)

			if tc.expectError {
				if !resp.Diagnostics.HasError() {
					t.Error("expected error, got none")
				}
				if tc.errorContains != "" {
					found := false
					for _, diag := range resp.Diagnostics.Errors() {
						if strings.Contains(diag.Summary(), tc.errorContains) ||
							strings.Contains(diag.Detail(), tc.errorContains) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got: %v", tc.errorContains, resp.Diagnostics)
					}
				}
			} else {
				if resp.Diagnostics.HasError() {
					t.Errorf("unexpected error: %v", resp.Diagnostics)
				}
			}
		})
	}
}

func TestKubeconfigValidatorDescriptions(t *testing.T) {
	ctx := context.Background()
	v := kubeconfigValidator{}

	desc := v.Description(ctx)
	if desc == "" {
		t.Error("expected non-empty description")
	}
	if !strings.Contains(desc, "kubeconfig") {
		t.Errorf("expected description to mention 'kubeconfig', got: %s", desc)
	}

	mdDesc := v.MarkdownDescription(ctx)
	if mdDesc == "" {
		t.Error("expected non-empty markdown description")
	}
	if !strings.Contains(mdDesc, "kubeconfig") {
		t.Errorf("expected markdown description to mention 'kubeconfig', got: %s", mdDesc)
	}
}
