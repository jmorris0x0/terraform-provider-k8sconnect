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

// TestAccObjectResource_ImportInconsistentState tests the bug found in aggressive soak testing:
// Import works but subsequent apply fails with "Provider produced inconsistent result"
//
// BUG REPRODUCTION:
//  1. Create resource with kubectl (no k8sconnect annotations)
//  2. Import resource using import block
//  3. Apply - should add k8sconnect annotations
//  4. CURRENTLY FAILS: "Provider produced inconsistent result after apply"
//     Error: .field_ownership: new element "metadata.annotations.k8sconnect.terraform.io/created-at" has appeared
//
// ROOT CAUSE:
// During plan phase after import, the provider doesn't predict that k8sconnect annotations
// will be added to field_ownership. Then during apply, they are added, causing Terraform
// to detect an inconsistency between planned and actual state.
//
// FIX REQUIRED:
// The plan phase (ModifyPlan) needs to predict that k8sconnect annotations will be added
// for imported resources, so Terraform knows to expect them in the apply result.
func TestAccObjectResource_ImportInconsistentState(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("import-inconsistent-%d", time.Now().UnixNano()%1000000)
	configMapName := fmt.Sprintf("kubectl-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace with Terraform
			{
				Config: testAccConfigImportInconsistentPrep(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Create ConfigMap with kubectl (simulating existing resource)
			{
				PreConfig: func() {
					t.Logf("Creating ConfigMap %s/%s with kubectl", ns, configMapName)
					testhelpers.CreateConfigMapWithKubectl(t, ns, configMapName, map[string]string{
						"created-by": "kubectl",
						"purpose":    "import-inconsistent-test",
					})
				},
				Config: testAccConfigImportInconsistentWithImportBlock(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":  config.StringVariable(raw),
					"name": config.StringVariable(configMapName),
				},
				Check: resource.ComposeTestCheckFunc(
					// After import + apply, resource should be in state
					resource.TestCheckResourceAttrSet("k8sconnect_object.imported_cm", "id"),
					resource.TestCheckResourceAttr("k8sconnect_object.imported_cm", "yaml_body",
						testAccYAMLBody(configMapName, ns)),
					testhelpers.CheckConfigMapExists(k8sClient, ns, configMapName),
					// After apply, should now be managed by k8sconnect with annotations
					testhelpers.CheckFieldManager(k8sClient, ns, "ConfigMap", configMapName, "k8sconnect"),
					testhelpers.CheckHasAnnotation(k8sClient, ns, "ConfigMap", configMapName,
						"k8sconnect.terraform.io/created-at"),
					testhelpers.CheckHasAnnotation(k8sClient, ns, "ConfigMap", configMapName,
						"k8sconnect.terraform.io/terraform-id"),
				),
			},
			// Step 3: Subsequent apply should work without drift
			{
				Config: testAccConfigImportInconsistentWithImportBlock(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":  config.StringVariable(raw),
					"name": config.StringVariable(configMapName),
				},
				PlanOnly: true,
				Check: resource.ComposeTestCheckFunc(
					// Plan should show no changes
					resource.TestCheckResourceAttrSet("k8sconnect_object.imported_cm", "id"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccConfigImportInconsistentPrep(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
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

func testAccConfigImportInconsistentWithImportBlock(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "name" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
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

# Import block - this is what triggers the bug
import {
  to = k8sconnect_object.imported_cm
  id = "k3d-k8sconnect-test:%s:v1/ConfigMap:%s"
}

resource "k8sconnect_object" "imported_cm" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
    data:
      created-by: kubectl
      purpose: import-inconsistent-test
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}
`, namespace, namespace, name, name, namespace)
}

func testAccYAMLBody(name, namespace string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  created-by: kubectl
  purpose: import-inconsistent-test
`, name, namespace)
}
