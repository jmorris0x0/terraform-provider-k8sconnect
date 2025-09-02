// internal/k8sconnect/resource/manifest/manifest_formatting_test.go
package manifest_test

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

func TestAccManifestResource_NoUpdateOnFormattingChanges(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create initial resource
			{
				Config: testAccManifestConfigFormattingInitial,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, "default", "formatting-test"),
				),
			},
			// Step 2: Add only comments - should show no changes
			{
				Config: testAccManifestConfigFormattingComments,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			// Step 3: Add only whitespace - should show no changes
			{
				Config: testAccManifestConfigFormattingWhitespace,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			// Step 4: Both comments and whitespace - should show no changes
			{
				Config: testAccManifestConfigFormattingBoth,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			// Step 5: Real change - should show changes
			{
				Config: testAccManifestConfigFormattingRealChange,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, "default", "formatting-test",
						"key2", "value2-changed"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, "default", "formatting-test"),
	})
}

const testAccManifestConfigFormattingInitial = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: formatting-test
data:
  key1: value1
  key2: value2
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigFormattingComments = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
# This is a ConfigMap resource
apiVersion: v1
kind: ConfigMap
metadata:
  name: formatting-test  # Same name as before
data:
  key1: value1  # First value
  key2: value2  # Second value
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigFormattingWhitespace = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap


metadata:
  name: formatting-test
data:
  key1: value1
  
  key2: value2


YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigFormattingBoth = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
# ConfigMap with formatting changes
apiVersion: v1
kind: ConfigMap


metadata:
  name: formatting-test  # The name
  
data:
  key1: value1  # Value one
  
  
  key2: value2  # Value two


# End of file
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigFormattingRealChange = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: formatting-test
data:
  key1: value1
  key2: value2-changed
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`
