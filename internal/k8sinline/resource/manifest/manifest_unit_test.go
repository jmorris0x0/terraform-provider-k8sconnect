// internal/k8sinline/resource/manifest/manifest_unit_test.go
package manifest

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseYAML(t *testing.T) {
	r := &manifestResource{}

	tests := []struct {
		name        string
		yaml        string
		expectError bool
		expectKind  string
		expectName  string
	}{
		{
			name: "valid namespace",
			yaml: `apiVersion: v1
kind: Namespace
metadata:
  name: test-namespace`,
			expectError: false,
			expectKind:  "Namespace",
			expectName:  "test-namespace",
		},
		{
			name: "missing apiVersion",
			yaml: `kind: Namespace
metadata:
  name: test-namespace`,
			expectError: true,
		},
		{
			name: "missing kind",
			yaml: `apiVersion: v1
metadata:
  name: test-namespace`,
			expectError: true,
		},
		{
			name: "missing name",
			yaml: `apiVersion: v1
kind: Namespace
metadata: {}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := r.parseYAML(tt.yaml)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if obj.GetKind() != tt.expectKind {
				t.Errorf("expected kind %q, got %q", tt.expectKind, obj.GetKind())
			}

			if obj.GetName() != tt.expectName {
				t.Errorf("expected name %q, got %q", tt.expectName, obj.GetName())
			}
		})
	}
}

func TestCreateK8sClient_KubeconfigRaw(t *testing.T) {
	r := &manifestResource{}

	// Minimal valid kubeconfig with insecure-skip-tls-verify
	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://example.com
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token`

	conn := clusterConnectionModel{
		Host:                 types.StringNull(),
		ClusterCACertificate: types.StringNull(),
		KubeconfigFile:       types.StringNull(),
		KubeconfigRaw:        types.StringValue(kubeconfig),
		Context:              types.StringNull(),
	}

	client, err := r.createK8sClient(conn)
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	if client == nil {
		t.Fatal("expected client but got nil")
	}
}

func TestCreateK8sClient_MultipleModesError(t *testing.T) {
	r := &manifestResource{}

	conn := clusterConnectionModel{
		Host:                 types.StringValue("https://example.com"),
		ClusterCACertificate: types.StringValue("test-ca"),
		KubeconfigFile:       types.StringNull(),
		KubeconfigRaw:        types.StringValue("test-kubeconfig"),
		Context:              types.StringNull(),
	}

	_, err := r.createK8sClient(conn)
	if err == nil {
		t.Fatal("expected error for multiple connection modes but got none")
	}

	expectedMsg := "cannot specify multiple connection modes"
	if err.Error() != expectedMsg {
		t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
	}
}

func TestCreateK8sClient_NoModeError(t *testing.T) {
	r := &manifestResource{}

	conn := clusterConnectionModel{
		Host:                 types.StringNull(),
		ClusterCACertificate: types.StringNull(),
		KubeconfigFile:       types.StringNull(),
		KubeconfigRaw:        types.StringNull(),
		Context:              types.StringNull(),
	}

	_, err := r.createK8sClient(conn)
	if err == nil {
		t.Fatal("expected error for no connection mode but got none")
	}

	expectedMsg := "must specify exactly one of: inline connection, kubeconfig_file, or kubeconfig_raw"
	if err.Error() != expectedMsg {
		t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
	}
}

func TestGetGVR(t *testing.T) {
	r := &manifestResource{}

	tests := []struct {
		name             string
		kind             string
		apiVersion       string
		expectedGroup    string
		expectedResource string
	}{
		{
			name:             "namespace",
			kind:             "Namespace",
			apiVersion:       "v1",
			expectedGroup:    "",
			expectedResource: "namespaces",
		},
		{
			name:             "deployment",
			kind:             "Deployment",
			apiVersion:       "apps/v1",
			expectedGroup:    "apps",
			expectedResource: "deployments",
		},
		{
			name:             "pod",
			kind:             "Pod",
			apiVersion:       "v1",
			expectedGroup:    "",
			expectedResource: "pods",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion(tt.apiVersion)
			obj.SetKind(tt.kind)

			gvr, err := r.getGVR(obj)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if gvr.Group != tt.expectedGroup {
				t.Errorf("expected group %q, got %q", tt.expectedGroup, gvr.Group)
			}

			if gvr.Resource != tt.expectedResource {
				t.Errorf("expected resource %q, got %q", tt.expectedResource, gvr.Resource)
			}
		})
	}
}

func TestGenerateID(t *testing.T) {
	r := &manifestResource{}

	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("Namespace")
	obj.SetName("test-namespace")

	conn := clusterConnectionModel{
		Host:                 types.StringValue("https://example.com"),
		ClusterCACertificate: types.StringValue("test-ca"),
		KubeconfigFile:       types.StringNull(),
		KubeconfigRaw:        types.StringNull(),
		Context:              types.StringNull(),
	}

	id1 := r.generateID(obj, conn)
	id2 := r.generateID(obj, conn)

	// Should be deterministic
	if id1 != id2 {
		t.Errorf("expected consistent ID generation, got %q and %q", id1, id2)
	}

	// Should be non-empty hex string
	if len(id1) == 0 {
		t.Error("expected non-empty ID")
	}

	// Test different inputs produce different IDs
	obj2 := &unstructured.Unstructured{}
	obj2.SetAPIVersion("v1")
	obj2.SetKind("Namespace")
	obj2.SetName("different-namespace")

	id3 := r.generateID(obj2, conn)
	if id1 == id3 {
		t.Error("expected different IDs for different objects")
	}
}
