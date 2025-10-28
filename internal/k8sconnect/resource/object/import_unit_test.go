package object

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestParseImportID(t *testing.T) {
	r := &objectResource{}

	tests := []struct {
		name              string
		importID          string
		expectedContext   string
		expectedNamespace string
		expectedAPIVer    string
		expectedKind      string
		expectedName      string
		expectError       bool
		errorContains     string
	}{
		// Valid namespaced resources
		{
			name:              "basic namespaced deployment",
			importID:          "prod:default:apps/v1/Deployment:nginx",
			expectedContext:   "prod",
			expectedNamespace: "default",
			expectedAPIVer:    "apps/v1",
			expectedKind:      "Deployment",
			expectedName:      "nginx",
			expectError:       false,
		},
		{
			name:              "namespaced pod with core group",
			importID:          "dev:kube-system:v1/Pod:coredns",
			expectedContext:   "dev",
			expectedNamespace: "kube-system",
			expectedAPIVer:    "v1",
			expectedKind:      "Pod",
			expectedName:      "coredns",
			expectError:       false,
		},
		{
			name:              "namespaced service",
			importID:          "staging:app-ns:v1/Service:backend",
			expectedContext:   "staging",
			expectedNamespace: "app-ns",
			expectedAPIVer:    "v1",
			expectedKind:      "Service",
			expectedName:      "backend",
			expectError:       false,
		},
		{
			name:              "namespaced ingress with complex apiVersion",
			importID:          "prod:default:networking.k8s.io/v1/Ingress:my-ingress",
			expectedContext:   "prod",
			expectedNamespace: "default",
			expectedAPIVer:    "networking.k8s.io/v1",
			expectedKind:      "Ingress",
			expectedName:      "my-ingress",
			expectError:       false,
		},
		{
			name:              "namespaced CRD with complex group",
			importID:          "prod:my-ns:stable.example.com/v1/MyCustomResource:instance-1",
			expectedContext:   "prod",
			expectedNamespace: "my-ns",
			expectedAPIVer:    "stable.example.com/v1",
			expectedKind:      "MyCustomResource",
			expectedName:      "instance-1",
			expectError:       false,
		},
		{
			name:              "namespaced CRD with versioned group",
			importID:          "prod:operators:cert-manager.io/v1alpha2/Certificate:my-cert",
			expectedContext:   "prod",
			expectedNamespace: "operators",
			expectedAPIVer:    "cert-manager.io/v1alpha2",
			expectedKind:      "Certificate",
			expectedName:      "my-cert",
			expectError:       false,
		},

		// Valid cluster-scoped resources
		{
			name:              "cluster-scoped namespace",
			importID:          "prod:v1/Namespace:my-namespace",
			expectedContext:   "prod",
			expectedNamespace: "",
			expectedAPIVer:    "v1",
			expectedKind:      "Namespace",
			expectedName:      "my-namespace",
			expectError:       false,
		},
		{
			name:              "cluster-scoped clusterrole",
			importID:          "prod:rbac.authorization.k8s.io/v1/ClusterRole:admin",
			expectedContext:   "prod",
			expectedNamespace: "",
			expectedAPIVer:    "rbac.authorization.k8s.io/v1",
			expectedKind:      "ClusterRole",
			expectedName:      "admin",
			expectError:       false,
		},
		{
			name:              "cluster-scoped PV",
			importID:          "staging:v1/PersistentVolume:pv-001",
			expectedContext:   "staging",
			expectedNamespace: "",
			expectedAPIVer:    "v1",
			expectedKind:      "PersistentVolume",
			expectedName:      "pv-001",
			expectError:       false,
		},
		{
			name:              "cluster-scoped custom CRD",
			importID:          "prod:apiextensions.k8s.io/v1/CustomResourceDefinition:mycrds.example.com",
			expectedContext:   "prod",
			expectedNamespace: "",
			expectedAPIVer:    "apiextensions.k8s.io/v1",
			expectedKind:      "CustomResourceDefinition",
			expectedName:      "mycrds.example.com",
			expectError:       false,
		},

		// Edge cases with special characters
		{
			name:              "name with dashes",
			importID:          "prod:default:v1/ConfigMap:my-config-map",
			expectedContext:   "prod",
			expectedNamespace: "default",
			expectedAPIVer:    "v1",
			expectedKind:      "ConfigMap",
			expectedName:      "my-config-map",
			expectError:       false,
		},
		{
			name:              "name with dots",
			importID:          "prod:default:v1/Service:service.example.com",
			expectedContext:   "prod",
			expectedNamespace: "default",
			expectedAPIVer:    "v1",
			expectedKind:      "Service",
			expectedName:      "service.example.com",
			expectError:       false,
		},
		{
			name:              "namespace with dashes",
			importID:          "prod:kube-public:v1/ConfigMap:cluster-info",
			expectedContext:   "prod",
			expectedNamespace: "kube-public",
			expectedAPIVer:    "v1",
			expectedKind:      "ConfigMap",
			expectedName:      "cluster-info",
			expectError:       false,
		},
		{
			name:              "context with special chars",
			importID:          "gke_my-project_us-central1-a_my-cluster:default:v1/Pod:nginx",
			expectedContext:   "gke_my-project_us-central1-a_my-cluster",
			expectedNamespace: "default",
			expectedAPIVer:    "v1",
			expectedKind:      "Pod",
			expectedName:      "nginx",
			expectError:       false,
		},

		// Error cases: wrong number of parts
		{
			name:          "too few parts - only 1",
			importID:      "prod",
			expectError:   true,
			errorContains: "expected 3 or 4 colon-separated parts, got 1",
		},
		{
			name:          "too few parts - only 2",
			importID:      "prod:default",
			expectError:   true,
			errorContains: "expected 3 or 4 colon-separated parts, got 2",
		},
		{
			name:          "too many parts - 5",
			importID:      "prod:default:apps/v1/Deployment:nginx:extra",
			expectError:   true,
			errorContains: "expected 3 or 4 colon-separated parts, got 5",
		},
		{
			name:          "too many parts - 7",
			importID:      "prod:default:v1:Pod:nginx:extra:stuff",
			expectError:   true,
			errorContains: "expected 3 or 4 colon-separated parts, got 7",
		},

		// Error cases: empty parts
		{
			name:          "empty context in namespaced",
			importID:      ":default:apps/v1/Deployment:nginx",
			expectError:   true,
			errorContains: "context cannot be empty",
		},
		{
			name:          "empty context in cluster-scoped",
			importID:      ":v1/Namespace:my-ns",
			expectError:   true,
			errorContains: "context cannot be empty",
		},
		{
			name:          "empty kind in namespaced",
			importID:      "prod:default::nginx",
			expectError:   true,
			errorContains: "kind cannot be empty",
		},
		{
			name:          "empty kind in cluster-scoped",
			importID:      "prod::my-ns",
			expectError:   true,
			errorContains: "kind cannot be empty",
		},
		{
			name:          "empty name in namespaced",
			importID:      "prod:default:apps/v1/Deployment:",
			expectError:   true,
			errorContains: "name cannot be empty",
		},
		{
			name:          "empty name in cluster-scoped",
			importID:      "prod:v1/Namespace:",
			expectError:   true,
			errorContains: "name cannot be empty",
		},

		// Error cases: missing apiVersion
		{
			name:          "kind without apiVersion - namespaced",
			importID:      "prod:default:Deployment:nginx",
			expectError:   true,
			errorContains: "kind field must include apiVersion: apiVersion/kind",
		},
		{
			name:          "kind without apiVersion - cluster-scoped",
			importID:      "prod:Namespace:my-ns",
			expectError:   true,
			errorContains: "kind field must include apiVersion: apiVersion/kind",
		},
		{
			name:          "empty apiVersion before slash",
			importID:      "prod:default:/Deployment:nginx",
			expectError:   true,
			errorContains: "apiVersion cannot be empty",
		},
		{
			name:          "empty kind after slash",
			importID:      "prod:default:apps/v1/:nginx",
			expectError:   true,
			errorContains: "kind cannot be empty",
		},

		// Edge case: namespace can be empty string (for cluster-scoped with 4 parts)
		{
			name:              "empty namespace field in 4-part format",
			importID:          "prod::v1/Namespace:my-ns",
			expectedContext:   "prod",
			expectedNamespace: "",
			expectedAPIVer:    "v1",
			expectedKind:      "Namespace",
			expectedName:      "my-ns",
			expectError:       false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, ns, apiVer, kind, name, err := r.parseImportID(tc.importID)

			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errorContains)
				}
				if tc.errorContains != "" && !strings.Contains(err.Error(), tc.errorContains) {
					t.Fatalf("expected error containing %q, got %q", tc.errorContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if ctx != tc.expectedContext {
				t.Errorf("context: expected %q, got %q", tc.expectedContext, ctx)
			}
			if ns != tc.expectedNamespace {
				t.Errorf("namespace: expected %q, got %q", tc.expectedNamespace, ns)
			}
			if apiVer != tc.expectedAPIVer {
				t.Errorf("apiVersion: expected %q, got %q", tc.expectedAPIVer, apiVer)
			}
			if kind != tc.expectedKind {
				t.Errorf("kind: expected %q, got %q", tc.expectedKind, kind)
			}
			if name != tc.expectedName {
				t.Errorf("name: expected %q, got %q", tc.expectedName, name)
			}
		})
	}
}

func TestValidateImportIDParts(t *testing.T) {
	r := &objectResource{}

	tests := []struct {
		name          string
		kubeContext   string
		kind          string
		resourceName  string
		expectValid   bool
		errorContains string
	}{
		{
			name:         "all parts valid",
			kubeContext:  "prod",
			kind:         "Deployment",
			resourceName: "nginx",
			expectValid:  true,
		},
		{
			name:         "valid with special chars in context",
			kubeContext:  "gke_project_zone_cluster",
			kind:         "Pod",
			resourceName: "my-pod",
			expectValid:  true,
		},
		{
			name:          "empty context",
			kubeContext:   "",
			kind:          "Deployment",
			resourceName:  "nginx",
			expectValid:   false,
			errorContains: "Missing Context",
		},
		{
			name:          "empty kind",
			kubeContext:   "prod",
			kind:          "",
			resourceName:  "nginx",
			expectValid:   false,
			errorContains: "Missing Kind",
		},
		{
			name:          "empty name",
			kubeContext:   "prod",
			kind:          "Deployment",
			resourceName:  "",
			expectValid:   false,
			errorContains: "Missing Name",
		},
		{
			name:          "all empty",
			kubeContext:   "",
			kind:          "",
			resourceName:  "",
			expectValid:   false,
			errorContains: "Missing Context", // First error encountered
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := &resource.ImportStateResponse{}

			valid := r.validateImportIDParts(tc.kubeContext, tc.kind, tc.resourceName, resp)

			if valid != tc.expectValid {
				t.Errorf("expected valid=%v, got %v", tc.expectValid, valid)
			}

			if tc.expectValid {
				if resp.Diagnostics.HasError() {
					t.Errorf("expected no errors, got: %v", resp.Diagnostics)
				}
			} else {
				if !resp.Diagnostics.HasError() {
					t.Error("expected error diagnostics, got none")
				}
				if tc.errorContains != "" {
					found := false
					for _, diag := range resp.Diagnostics.Errors() {
						if strings.Contains(diag.Summary(), tc.errorContains) || strings.Contains(diag.Detail(), tc.errorContains) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got diagnostics: %v", tc.errorContains, resp.Diagnostics)
					}
				}
			}
		})
	}
}
