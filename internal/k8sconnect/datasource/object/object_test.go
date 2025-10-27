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

func TestAccObjectDataSource_basic(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ds-test-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("config-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create resources and read with data source
			{
				Config: testAccObjectDataSourceConfig(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify the object resource created successfully
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					// Verify data source can read it
					resource.TestCheckResourceAttr("data.k8sconnect_object.test", "kind", "ConfigMap"),
					resource.TestCheckResourceAttr("data.k8sconnect_object.test", "api_version", "v1"),
					resource.TestCheckResourceAttr("data.k8sconnect_object.test", "name", cmName),
					resource.TestCheckResourceAttr("data.k8sconnect_object.test", "namespace", ns),
					// Verify outputs are populated
					resource.TestCheckResourceAttrSet("data.k8sconnect_object.test", "manifest"),
					resource.TestCheckResourceAttrSet("data.k8sconnect_object.test", "yaml_body"),
					// Verify we can access nested fields via .object attribute with dot notation
					resource.TestCheckOutput("test_data_key1", "value1"),
				),
			},
		},
	})
}

func testAccObjectDataSourceConfig(ns, name string) string {
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

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key1: value1
  key2: value2
YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}

# Now read it with the data source
data "k8sconnect_object" "test" {
  api_version = "v1"
  kind        = "ConfigMap"
  name        = var.name
  namespace   = var.namespace

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.test]
}

# Test that we can access nested fields via .object attribute
output "test_data_key1" {
  value = data.k8sconnect_object.test.object.data.key1
}
`, ns, name, ns)
}
