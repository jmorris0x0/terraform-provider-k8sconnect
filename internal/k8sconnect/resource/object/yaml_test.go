package object

import (
	"strings"
	"testing"
)

// TestParseYAML_MalformedAPIVersion verifies that parseYAML rejects apiVersion
// values with invalid format (e.g., too many slashes).
//
// Bug: "not/a/valid/version" passes parseYAML validation, then K8s returns
// misleading "Object 'Kind' is missing" error instead of identifying the bad
// apiVersion format. Soaktest Phase 1 UX issue.
func TestParseYAML_MalformedAPIVersion(t *testing.T) {
	r := &objectResource{}

	tests := []struct {
		name       string
		yaml       string
		wantErr    bool
		errContain string // if wantErr, error must contain this substring
	}{
		{
			name: "valid core API version",
			yaml: `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`,
			wantErr: false,
		},
		{
			name: "valid group API version",
			yaml: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test
`,
			wantErr: false,
		},
		{
			name: "valid CRD API version",
			yaml: `apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: test
`,
			wantErr: false,
		},
		{
			name: "malformed - too many slashes",
			yaml: `apiVersion: not/a/valid/version
kind: ConfigMap
metadata:
  name: test
`,
			wantErr:    true,
			errContain: "malformed",
		},
		{
			name: "malformed - two slashes",
			yaml: `apiVersion: apps/v1/extra
kind: Deployment
metadata:
  name: test
`,
			wantErr:    true,
			errContain: "malformed",
		},
		{
			name: "malformed - trailing slash",
			yaml: `apiVersion: apps/
kind: Deployment
metadata:
  name: test
`,
			wantErr:    true,
			errContain: "apiVersion",
		},
		{
			name: "malformed - leading slash",
			yaml: `apiVersion: /v1
kind: Deployment
metadata:
  name: test
`,
			wantErr:    true,
			errContain: "apiVersion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.parseYAML(tt.yaml)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Expected error but got nil")
				}
				if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("Error %q should contain %q", err.Error(), tt.errContain)
				}
			} else {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
			}
		})
	}
}
