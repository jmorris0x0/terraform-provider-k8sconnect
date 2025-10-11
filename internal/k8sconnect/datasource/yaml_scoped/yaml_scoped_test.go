// internal/k8sconnect/datasource/yaml_scoped/yaml_scoped_test.go
package yaml_scoped

import (
	"strings"
	"testing"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/yaml_common"
)

func TestCategorizeCRDs(t *testing.T) {
	d := &yamlScopedDataSource{}

	content := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: myresources.example.com
spec:
  group: example.com
  names:
    kind: MyResource
    plural: myresources
  scope: Namespaced
---
apiVersion: v1
kind: Namespace
metadata:
  name: test-ns
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deploy
  namespace: test-ns`

	docs, err := yaml_common.ParseDocuments(content, "test")
	if err != nil {
		t.Fatalf("failed to parse documents: %v", err)
	}

	crds, clusterScoped, namespaced, err := d.categorizeManifests(docs)
	if err != nil {
		t.Fatalf("categorization failed: %v", err)
	}

	// Verify CRD categorization
	if len(crds) != 1 {
		t.Errorf("expected 1 CRD, got %d", len(crds))
	}
	if _, exists := crds["apiextensions.k8s.io.customresourcedefinition.myresources.example.com"]; !exists {
		t.Error("CRD not found in crds map")
	}

	// Verify cluster-scoped categorization
	if len(clusterScoped) != 1 {
		t.Errorf("expected 1 cluster-scoped resource, got %d", len(clusterScoped))
	}
	if _, exists := clusterScoped["namespace.test-ns"]; !exists {
		t.Error("Namespace not found in cluster_scoped map")
	}

	// Verify namespaced categorization
	if len(namespaced) != 1 {
		t.Errorf("expected 1 namespaced resource, got %d", len(namespaced))
	}
	if _, exists := namespaced["apps.deployment.test-ns.test-deploy"]; !exists {
		t.Error("Deployment not found in namespaced map")
	}
}

func TestClusterScopedKinds(t *testing.T) {
	tests := []struct {
		name               string
		apiVersion         string
		kind               string
		expectClusterScope bool
	}{
		{"Namespace", "v1", "Namespace", true},
		{"namespace lowercase", "v1", "namespace", true},
		{"ClusterRole", "rbac.authorization.k8s.io/v1", "ClusterRole", true},
		{"ClusterRoleBinding", "rbac.authorization.k8s.io/v1", "ClusterRoleBinding", true},
		{"PersistentVolume", "v1", "PersistentVolume", true},
		{"StorageClass", "storage.k8s.io/v1", "StorageClass", true},
		{"Node", "v1", "Node", true},
		{"IngressClass", "networking.k8s.io/v1", "IngressClass", true},
		{"Deployment", "apps/v1", "Deployment", false},
		{"Service", "v1", "Service", false},
		{"ConfigMap", "v1", "ConfigMap", false},
		{"Pod", "v1", "Pod", false},
		{"Secret", "v1", "Secret", false},
		{"unknown kind", "example.com/v1", "UnknownKind", false},
		{"ValidatingAdmissionPolicy", "admissionregistration.k8s.io/v1", "ValidatingAdmissionPolicy", true},
		{"ValidatingAdmissionPolicyBinding", "admissionregistration.k8s.io/v1", "ValidatingAdmissionPolicyBinding", true},
		{"SelfSubjectReview", "authentication.k8s.io/v1", "SelfSubjectReview", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isClusterScopedKind(tt.apiVersion, tt.kind)
			if result != tt.expectClusterScope {
				t.Errorf("isClusterScopedKind(%q, %q) = %v, want %v", tt.apiVersion, tt.kind, result, tt.expectClusterScope)
			}
		})
	}
}

func TestMixedManifests(t *testing.T) {
	d := &yamlScopedDataSource{}

	content := `# CRD for custom resource
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: applications.example.com
spec:
  group: example.com
  names:
    kind: Application
    plural: applications
  scope: Namespaced
---
# Namespace must be created before namespaced resources
apiVersion: v1
kind: Namespace
metadata:
  name: app-system
---
# ClusterRole for RBAC
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: app-reader
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list"]
---
# Namespaced resources
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  namespace: app-system
data:
  key: value
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-server
  namespace: app-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: server
  template:
    metadata:
      labels:
        app: server
    spec:
      containers:
      - name: server
        image: nginx:latest
---
# Custom resource (namespaced)
apiVersion: example.com/v1
kind: Application
metadata:
  name: my-app
  namespace: app-system
spec:
  version: "1.0"`

	docs, err := yaml_common.ParseDocuments(content, "test")
	if err != nil {
		t.Fatalf("failed to parse documents: %v", err)
	}

	crds, clusterScoped, namespaced, err := d.categorizeManifests(docs)
	if err != nil {
		t.Fatalf("categorization failed: %v", err)
	}

	// Check counts
	if len(crds) != 1 {
		t.Errorf("expected 1 CRD, got %d", len(crds))
	}
	if len(clusterScoped) != 2 {
		t.Errorf("expected 2 cluster-scoped resources (Namespace + ClusterRole), got %d", len(clusterScoped))
	}
	if len(namespaced) != 3 {
		t.Errorf("expected 3 namespaced resources (ConfigMap + Deployment + Application), got %d", len(namespaced))
	}

	// Verify specific resources
	expectedCRDs := []string{"apiextensions.k8s.io.customresourcedefinition.applications.example.com"}
	for _, id := range expectedCRDs {
		if _, exists := crds[id]; !exists {
			t.Errorf("CRD %q not found", id)
		}
	}

	expectedClusterScoped := []string{
		"namespace.app-system",
		"rbac.authorization.k8s.io.clusterrole.app-reader",
	}
	for _, id := range expectedClusterScoped {
		if _, exists := clusterScoped[id]; !exists {
			t.Errorf("cluster-scoped resource %q not found", id)
		}
	}

	expectedNamespaced := []string{
		"configmap.app-system.app-config",
		"apps.deployment.app-system.app-server",
		"example.com.application.app-system.my-app",
	}
	for _, id := range expectedNamespaced {
		if _, exists := namespaced[id]; !exists {
			t.Errorf("namespaced resource %q not found", id)
		}
	}
}

func TestDuplicateHandlingAcrossCategories(t *testing.T) {
	d := &yamlScopedDataSource{}

	tests := []struct {
		name        string
		content     string
		expectedErr string
	}{
		{
			name: "duplicate CRDs",
			content: `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: myresources.example.com
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: myresources.example.com`,
			expectedErr: "duplicate resource ID",
		},
		{
			name: "duplicate Namespaces",
			content: `apiVersion: v1
kind: Namespace
metadata:
  name: test-ns
---
apiVersion: v1
kind: Namespace
metadata:
  name: test-ns`,
			expectedErr: "duplicate resource ID",
		},
		{
			name: "duplicate namespaced resources",
			content: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: default
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: default`,
			expectedErr: "duplicate resource ID",
		},
		{
			name: "duplicate across different categories (same name, different kind)",
			content: `apiVersion: v1
kind: Service
metadata:
  name: app
  namespace: default
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: default`,
			expectedErr: "", // Different kinds = different IDs, no error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs, err := yaml_common.ParseDocuments(tt.content, "test")
			if err != nil {
				t.Fatalf("failed to parse documents: %v", err)
			}

			_, _, _, err = d.categorizeManifests(docs)

			if tt.expectedErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !strings.Contains(err.Error(), tt.expectedErr) {
					t.Errorf("expected error containing %q, got: %v", tt.expectedErr, err)
				}
			}
		})
	}
}

func TestInvalidYAMLHandling(t *testing.T) {
	d := &yamlScopedDataSource{}

	content := `apiVersion: v1
kind: Namespace
metadata:
  name: valid-ns
---
invalid: yaml: content: [
  missing: bracket
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deploy
  namespace: default`

	docs, err := yaml_common.ParseDocuments(content, "test")

	// ParseDocuments should return an error but still return all documents
	if err == nil {
		t.Error("expected parsing error for invalid YAML")
	}

	// Should have 3 documents (2 valid, 1 invalid)
	if len(docs) != 3 {
		t.Errorf("expected 3 documents, got %d", len(docs))
	}

	// categorizeManifests should fail when it encounters the invalid doc
	_, _, _, err = d.categorizeManifests(docs)
	if err == nil {
		t.Error("expected categorization to fail due to invalid YAML")
	}

	if !strings.Contains(err.Error(), "invalid YAML") {
		t.Errorf("error should mention invalid YAML: %v", err)
	}
}

func TestNamespacedCustomResources(t *testing.T) {
	d := &yamlScopedDataSource{}

	// Custom resources should be categorized as namespaced (not CRDs)
	content := `apiVersion: example.com/v1alpha1
kind: MyCustomResource
metadata:
  name: my-instance
  namespace: default
spec:
  foo: bar`

	docs, err := yaml_common.ParseDocuments(content, "test")
	if err != nil {
		t.Fatalf("failed to parse documents: %v", err)
	}

	crds, clusterScoped, namespaced, err := d.categorizeManifests(docs)
	if err != nil {
		t.Fatalf("categorization failed: %v", err)
	}

	if len(crds) != 0 {
		t.Errorf("custom resource instance should not be categorized as CRD, got %d CRDs", len(crds))
	}

	if len(clusterScoped) != 0 {
		t.Errorf("custom resource instance should not be cluster-scoped, got %d", len(clusterScoped))
	}

	if len(namespaced) != 1 {
		t.Errorf("expected 1 namespaced custom resource, got %d", len(namespaced))
	}

	if _, exists := namespaced["example.com.mycustomresource.default.my-instance"]; !exists {
		t.Error("custom resource not found in namespaced map")
	}
}

func TestClusterScopedCustomResources(t *testing.T) {
	d := &yamlScopedDataSource{}

	// Custom resource without namespace should be treated as namespaced (unknown scope)
	// This is a conservative choice - unknown resources default to namespaced
	content := `apiVersion: example.com/v1alpha1
kind: ClusterConfig
metadata:
  name: global-config
spec:
  setting: value`

	docs, err := yaml_common.ParseDocuments(content, "test")
	if err != nil {
		t.Fatalf("failed to parse documents: %v", err)
	}

	crds, clusterScoped, namespaced, err := d.categorizeManifests(docs)
	if err != nil {
		t.Fatalf("categorization failed: %v", err)
	}

	// Unknown custom resources without namespace default to namespaced category
	if len(crds) != 0 {
		t.Errorf("custom resource should not be categorized as CRD, got %d CRDs", len(crds))
	}

	if len(clusterScoped) != 0 {
		t.Errorf("custom resource should not be cluster-scoped without explicit knowledge, got %d", len(clusterScoped))
	}

	if len(namespaced) != 1 {
		t.Errorf("expected 1 custom resource in namespaced category, got %d", len(namespaced))
	}
}

func TestEdgeCases(t *testing.T) {
	d := &yamlScopedDataSource{}

	t.Run("resource without namespace metadata", func(t *testing.T) {
		content := `apiVersion: v1
kind: ConfigMap
metadata:
  name: no-namespace`

		docs, err := yaml_common.ParseDocuments(content, "test")
		if err != nil {
			t.Fatalf("failed to parse: %v", err)
		}

		_, _, namespaced, err := d.categorizeManifests(docs)
		if err != nil {
			t.Fatalf("categorization failed: %v", err)
		}

		// ConfigMap without namespace should still be categorized as namespaced
		id := "configmap.no-namespace"
		if _, exists := namespaced[id]; !exists {
			t.Errorf("expected ConfigMap with ID %q in namespaced category", id)
		}
	})

	t.Run("case insensitive kind matching", func(t *testing.T) {
		content := `apiVersion: v1
kind: NAMESPACE
metadata:
  name: test`

		docs, err := yaml_common.ParseDocuments(content, "test")
		if err != nil {
			t.Fatalf("failed to parse: %v", err)
		}

		_, clusterScoped, _, err := d.categorizeManifests(docs)
		if err != nil {
			t.Fatalf("categorization failed: %v", err)
		}

		// Should recognize NAMESPACE as cluster-scoped (case-insensitive)
		if len(clusterScoped) != 1 {
			t.Errorf("expected uppercase NAMESPACE to be recognized as cluster-scoped")
		}
	})
}

func TestRealWorldDependencyOrdering(t *testing.T) {
	d := &yamlScopedDataSource{}

	// Simulate a real deployment scenario with CRD, namespace, and application
	content := `# Step 1: Define CRD
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: databases.storage.example.com
spec:
  group: storage.example.com
  names:
    kind: Database
    plural: databases
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
---
# Step 2: Create namespace
apiVersion: v1
kind: Namespace
metadata:
  name: production
---
# Step 2: Create RBAC (cluster-scoped)
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: database-operator
rules:
- apiGroups: ["storage.example.com"]
  resources: ["databases"]
  verbs: ["*"]
---
# Step 3: Deploy operator
apiVersion: apps/v1
kind: Deployment
metadata:
  name: db-operator
  namespace: production
spec:
  replicas: 1
  selector:
    matchLabels:
      app: db-operator
  template:
    metadata:
      labels:
        app: db-operator
    spec:
      containers:
      - name: operator
        image: example.com/db-operator:v1
---
# Step 3: Create custom resource
apiVersion: storage.example.com/v1
kind: Database
metadata:
  name: main-db
  namespace: production
spec:
  size: large`

	docs, err := yaml_common.ParseDocuments(content, "test")
	if err != nil {
		t.Fatalf("failed to parse documents: %v", err)
	}

	crds, clusterScoped, namespaced, err := d.categorizeManifests(docs)
	if err != nil {
		t.Fatalf("categorization failed: %v", err)
	}

	// Verify dependency ordering categories
	if len(crds) != 1 {
		t.Errorf("expected 1 CRD (must apply first), got %d", len(crds))
	}

	if len(clusterScoped) != 2 {
		t.Errorf("expected 2 cluster-scoped resources (Namespace + ClusterRole, apply second), got %d", len(clusterScoped))
	}

	if len(namespaced) != 2 {
		t.Errorf("expected 2 namespaced resources (Deployment + Database CR, apply last), got %d", len(namespaced))
	}

	// Verify the custom resource instance is NOT in CRDs
	if _, exists := crds["storage.example.com.database.production.main-db"]; exists {
		t.Error("Database CR instance should not be in CRDs category")
	}

	// Verify it's in namespaced
	if _, exists := namespaced["storage.example.com.database.production.main-db"]; !exists {
		t.Error("Database CR instance should be in namespaced category")
	}
}
