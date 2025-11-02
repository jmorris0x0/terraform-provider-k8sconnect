package patch_test

import (
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccPatchResource_EmptyPatch tests that empty patch content produces a clean error
// Similar to Bug #1 for k8sconnect_object - verify patch doesn't crash on empty content
func TestAccPatchResource_EmptyPatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("empty-patch-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccPatchEmptyNamespace(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
					// Create a ConfigMap to patch
					createConfigMapWithFieldManager(t, k8sClient, ns, "test-config", "kubectl", map[string]string{
						"key1": "value1",
					}),
				),
			},
			// Step 2: Try to create patch with empty patch content
			// Should get a clean validation error, not a crash
			{
				Config: testAccPatchEmptyPatchConfig(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				// Should get a clean error about empty/missing patch content
				ExpectError: regexp.MustCompile("no patch content|patch.*empty|must not be empty|at least one"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

func testAccPatchEmptyNamespace(namespace string) string {
	return fmt.Sprintf(`
resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %[1]s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

variable "raw" {
  type = string
}

variable "namespace" {
  type = string
}
`, namespace)
}

func testAccPatchEmptyPatchConfig(namespace string) string {
	return fmt.Sprintf(`
resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %[1]s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

# Try to patch with empty patch content
resource "k8sconnect_patch" "empty_patch" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "test-config"
    namespace   = "%[1]s"
  }

  patch = ""

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}

variable "raw" {
  type = string
}

variable "namespace" {
  type = string
}
`, namespace)
}
