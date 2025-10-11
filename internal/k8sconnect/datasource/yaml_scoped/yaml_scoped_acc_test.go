// internal/k8sconnect/datasource/yaml_scoped/yaml_scoped_acc_test.go
package yaml_scoped_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
)

func TestAccYamlScopedDataSource_Basic(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccYamlScopedConfigBasic,
				Check: resource.ComposeTestCheckFunc(
					// id should be set
					resource.TestCheckResourceAttrSet("data.k8sconnect_yaml_scoped.test", "id"),

					// CRDs - should have 1 entry
					resource.TestCheckResourceAttr("data.k8sconnect_yaml_scoped.test", "crds.%", "1"),
					resource.TestCheckResourceAttr(
						"data.k8sconnect_yaml_scoped.test",
						"crds.apiextensions.k8s.io.customresourcedefinition.databases.example.com",
						testCRDManifest,
					),

					// Cluster-scoped - should have 2 entries (Namespace + ClusterRole)
					resource.TestCheckResourceAttr("data.k8sconnect_yaml_scoped.test", "cluster_scoped.%", "2"),
					resource.TestCheckResourceAttr(
						"data.k8sconnect_yaml_scoped.test",
						"cluster_scoped.namespace.app-system",
						testNamespaceManifest,
					),
					resource.TestCheckResourceAttr(
						"data.k8sconnect_yaml_scoped.test",
						"cluster_scoped.rbac.authorization.k8s.io.clusterrole.app-admin",
						testClusterRoleManifest,
					),

					// Namespaced - should have 2 entries (ConfigMap + Deployment)
					resource.TestCheckResourceAttr("data.k8sconnect_yaml_scoped.test", "namespaced.%", "2"),
					resource.TestCheckResourceAttr(
						"data.k8sconnect_yaml_scoped.test",
						"namespaced.configmap.app-system.app-config",
						testConfigMapManifest,
					),
					resource.TestCheckResourceAttr(
						"data.k8sconnect_yaml_scoped.test",
						"namespaced.apps.deployment.app-system.app-server",
						testDeploymentManifest,
					),
				),
			},
		},
	})
}

func TestAccYamlScopedDataSource_CustomResources(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccYamlScopedConfigWithCR,
				Check: resource.ComposeTestCheckFunc(
					// CRD definition should be in crds
					resource.TestCheckResourceAttr("data.k8sconnect_yaml_scoped.test", "crds.%", "1"),

					// Custom resource INSTANCE should be in namespaced, not crds
					resource.TestCheckResourceAttr("data.k8sconnect_yaml_scoped.test", "namespaced.%", "1"),
					resource.TestCheckResourceAttr(
						"data.k8sconnect_yaml_scoped.test",
						"namespaced.example.com.database.default.my-db",
						testDatabaseCRManifest,
					),
				),
			},
		},
	})
}

func TestAccYamlScopedDataSource_EmptyCategories(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccYamlScopedConfigOnlyNamespaced,
				Check: resource.ComposeTestCheckFunc(
					// Only namespaced resources, others should be empty
					resource.TestCheckResourceAttr("data.k8sconnect_yaml_scoped.test", "crds.%", "0"),
					resource.TestCheckResourceAttr("data.k8sconnect_yaml_scoped.test", "cluster_scoped.%", "0"),
					resource.TestCheckResourceAttr("data.k8sconnect_yaml_scoped.test", "namespaced.%", "1"),
				),
			},
		},
	})
}

func TestAccYamlScopedDataSource_Errors(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config:      testAccYamlScopedConfigBothParams,
				ExpectError: regexp.MustCompile("Exactly one of 'content' or 'pattern' must be specified"),
			},
			{
				Config:      testAccYamlScopedConfigNeitherParam,
				ExpectError: regexp.MustCompile("Either 'content' or 'pattern' must be specified"),
			},
			{
				Config:      testAccYamlScopedConfigDuplicates,
				ExpectError: regexp.MustCompile("duplicate resource ID"),
			},
		},
	})
}

// Test manifests
const testCRDManifest = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: databases.example.com
spec:
  group: example.com
  names:
    kind: Database
    plural: databases
  scope: Namespaced`

const testNamespaceManifest = `apiVersion: v1
kind: Namespace
metadata:
  name: app-system`

const testClusterRoleManifest = `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: app-admin
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["*"]`

const testConfigMapManifest = `apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  namespace: app-system
data:
  key: value`

const testDeploymentManifest = `apiVersion: apps/v1
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
        image: public.ecr.aws/nginx/nginx:1.21`

const testDatabaseCRManifest = `apiVersion: example.com/v1
kind: Database
metadata:
  name: my-db
  namespace: default
spec:
  size: large`

// Test configurations
const testAccYamlScopedConfigBasic = `
data "k8sconnect_yaml_scoped" "test" {
  content = <<YAML
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: databases.example.com
spec:
  group: example.com
  names:
    kind: Database
    plural: databases
  scope: Namespaced
---
apiVersion: v1
kind: Namespace
metadata:
  name: app-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: app-admin
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["*"]
---
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
        image: public.ecr.aws/nginx/nginx:1.21
YAML
}
`

const testAccYamlScopedConfigWithCR = `
data "k8sconnect_yaml_scoped" "test" {
  content = <<YAML
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: databases.example.com
spec:
  group: example.com
  names:
    kind: Database
    plural: databases
  scope: Namespaced
---
apiVersion: example.com/v1
kind: Database
metadata:
  name: my-db
  namespace: default
spec:
  size: large
YAML
}
`

const testAccYamlScopedConfigOnlyNamespaced = `
data "k8sconnect_yaml_scoped" "test" {
  content = <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
  namespace: default
spec:
  containers:
  - name: nginx
    image: public.ecr.aws/nginx/nginx:1.21
YAML
}
`

const testAccYamlScopedConfigBothParams = `
data "k8sconnect_yaml_scoped" "test" {
  content = "apiVersion: v1\nkind: Namespace"
  pattern = "*.yaml"
}
`

const testAccYamlScopedConfigNeitherParam = `
data "k8sconnect_yaml_scoped" "test" {
}
`

const testAccYamlScopedConfigDuplicates = `
data "k8sconnect_yaml_scoped" "test" {
  content = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: duplicate
---
apiVersion: v1
kind: Namespace
metadata:
  name: duplicate
YAML
}
`
