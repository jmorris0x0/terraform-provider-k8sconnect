// internal.k8sinline/resource/manifest/manifest_test.go

package manifest_test

import (
	"fmt"
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
		t.Fatal("Environment variables TF_ACC_K8S_HOST, TF_ACC_K8S_CA, TF_ACC_K8S_CMD, and TF_ACC_KUBECONFIG_RAW must be set for acceptance tests")
	}

	config := fmt.Sprintf(testAccManifestConfigBasic, host, ca, cmd, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (provider.Provider, error){
			"k8sinline": func() (provider.Provider, error) {
				return k8sinline.NewProvider(), nil
			},
		},
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`TODO: implement`),
			},
		},
	})
}

const testAccManifestConfigBasic = `
provider "k8sinline" {}

resource "k8sinline_manifest" "test_exec" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-exec
YAML

  cluster_connection {
    host                   = "%s"
    cluster_ca_certificate = "%s"

    exec = {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "%s"
      args        = ["hello"]
    }
  }
}
`
