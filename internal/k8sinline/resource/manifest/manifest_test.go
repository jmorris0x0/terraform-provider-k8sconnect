// internal.k8sinline/resource/manifest/manifest_test.go
package manifest_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline"
)

func TestAccManifestResource_Basic(t *testing.T) {
	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	cmd := os.Getenv("TF_ACC_K8S_CMD")
	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")

	if host == "" || ca == "" || cmd == "" || raw == "" {
		t.Fatal("TF_ACC_K8S_HOST, TF_ACC_K8S_CA, TF_ACC_K8S_CMD, and TF_ACC_KUBECONFIG_RAW must be set for acceptance tests")
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (provider.Provider, error){
			"k8sinline": func() (provider.Provider, error) {
				return k8sinline.NewProvider(), nil
			},
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigBasic,
				ConfigVariables: map[string]interface{}{
					"host": host,
					"ca":   ca,
					"cmd":  cmd,
					"raw":  raw,
				},
				ExpectError: regexp.MustCompile(`TODO: implement`),
			},
		},
	})
}

const testAccManifestConfigBasic = `
variable "host" {
  type = string
}
variable "ca" {
  type = string
}
variable "cmd" {
  type = string
}
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_exec" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-exec
YAML

  cluster_connection {
    host                   = var.host
    cluster_ca_certificate = var.ca

    exec = {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = var.cmd
      args        = ["hello"]
    }
  }
}

resource "k8sinline_manifest" "test_raw" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-raw
YAML

  cluster_connection {
    kubeconfig_raw = var.raw
    context        = "default"
  }
}
`
