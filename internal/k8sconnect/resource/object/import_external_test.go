package object_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccObjectResource_ImportExternal tests importing a resource created outside terraform
//
// This test verifies that:
// 1. A resource created by kubectl (without k8sconnect annotations) can be imported
// 2. The import correctly preserves kubectl's field ownership
// 3. After import, terraform apply takes ownership and adds k8sconnect annotations
// 4. Subsequent applies work correctly without drift
//
// This validates the fix for the import ownership verification bug where Read() was
// calling verifyOwnership before Update() could add the k8sconnect annotations.
func TestAccObjectResource_ImportExternal(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("import-external-ns-%d", time.Now().UnixNano()%1000000)
	configMapName := fmt.Sprintf("external-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace with Terraform
			{
				Config: testAccManifestConfigImportExternalPrep(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Create ConfigMap with kubectl, then import it
			{
				PreConfig: func() {
					// Now namespace exists, create ConfigMap with kubectl
					testhelpers.CreateConfigMapWithKubectl(t, ns, configMapName, map[string]string{
						"created-by": "kubectl",
						"purpose":    "import-test",
					})
				},
				Config: testAccManifestConfigImportExternal(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(configMapName),
				},
				ResourceName:  "k8sconnect_object.external_import",
				ImportState:   true,
				ImportStateId: fmt.Sprintf("k3d-k8sconnect-test:%s:v1/ConfigMap:%s", ns, configMapName),
			},
			// Step 3: Apply config - should show no changes since we imported correctly
			{
				Config: testAccManifestConfigImportExternal(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(configMapName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.external_import", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.external_import", "yaml_body"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, configMapName),
					// After apply, should now be managed by k8sconnect
					testhelpers.CheckFieldManager(k8sClient, ns, "ConfigMap", configMapName, "k8sconnect"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigImportExternalPrep(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "import_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}
`, namespace)
}

func testAccManifestConfigImportExternal(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "name" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "import_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "external_import" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
    data:
      created-by: kubectl
      purpose: import-test
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.import_namespace]
}
`, namespace, name, namespace)
}
