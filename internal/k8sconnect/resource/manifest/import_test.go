// internal/k8sconnect/resource/manifest/import_test.go
package manifest_test

import (
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"os"
	"testing"
	"time"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

func TestAccManifestResource_Import(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	namespaceName := fmt.Sprintf("import-ns-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace with Terraform
			{
				Config: testAccManifestConfigImport(namespaceName),
				ConfigVariables: config.Variables{
					"raw":            config.StringVariable(raw),
					"namespace_name": config.StringVariable(namespaceName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_import", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_import", "yaml_body"),
					testhelpers.CheckNamespaceExists(k8sClient, namespaceName),
				),
			},
			// Step 2: Import the namespace
			{
				Config: testAccManifestConfigImport(namespaceName),
				ConfigVariables: config.Variables{
					"raw":            config.StringVariable(raw),
					"namespace_name": config.StringVariable(namespaceName),
				},
				ResourceName:      "k8sconnect_manifest.test_import",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("k3d-oidc-e2e/%s/%s", "Namespace", namespaceName),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"imported_without_annotations",
					"cluster_connection",
					"yaml_body",
					"managed_state_projection",
					"delete_protection",
					"force_conflicts",
				},
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, namespaceName),
	})
}

func testAccManifestConfigImport(namespaceName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace_name" {
  type = string  
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_import" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    test: import
    created-by: terraform-test
YAML
  
  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, namespaceName)
}

func TestAccManifestResource_ImportWithManagedFields(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("import-fields-ns-%d", time.Now().UnixNano()%1000000)
	configMapName := fmt.Sprintf("import-fields-cm-%d", time.Now().UnixNano()%1000000)
	resourceName := "k8sconnect_manifest.test_import"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create resource with Terraform
			{
				Config: testAccManifestConfigImportWithFields(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(configMapName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, configMapName),
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttrSet(resourceName, "yaml_body"),
					resource.TestCheckResourceAttrSet(resourceName, "managed_state_projection"),
				),
			},
			// Step 2: Import the same resource
			{
				Config: testAccManifestConfigImportWithFields(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(configMapName),
				},
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("k3d-oidc-e2e/%s/ConfigMap/%s", ns, configMapName),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"imported_without_annotations", // This field is set during import
					"cluster_connection",           // Import uses file, config uses raw
					"yaml_body",                    // Formatting and annotations differ
					"managed_state_projection",     // Import includes extra K8s fields
					"delete_protection",            // Only in import, not in config
					"force_conflicts",
				},
			},
		},
	})
}

func testAccManifestConfigImportWithFields(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "name" {
  type = string  
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "import_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML
  
  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "test_import" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
      labels:
        test: import
        created-by: terraform-test
      annotations:
        test-annotation: value
    data:
      key1: value1
      key2: value2
      key3: value3
  YAML
  
  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.import_namespace]
}
`, namespace, name, namespace)
}
