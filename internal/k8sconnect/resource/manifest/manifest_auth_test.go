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

func TestAccManifestResource_TokenAuth(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	token := os.Getenv("TF_ACC_K8S_TOKEN")
	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")

	if token == "" {
		t.Skip("TF_ACC_K8S_TOKEN not set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: `
variable "host" { type = string }
variable "ca" { type = string }
variable "token" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-token
YAML

  cluster_connection = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    token                  = var.token
  }
}`,
				ConfigVariables: config.Variables{
					"host":  config.StringVariable(host),
					"ca":    config.StringVariable(ca),
					"token": config.StringVariable(token),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, "acctest-token"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, "acctest-token"),
	})
}

func TestAccManifestResource_ClientCertAuth(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	cert := os.Getenv("TF_ACC_K8S_CLIENT_CERT")
	key := os.Getenv("TF_ACC_K8S_CLIENT_KEY")
	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")

	if cert == "" || key == "" {
		t.Skip("TF_ACC_K8S_CLIENT_CERT and TF_ACC_K8S_CLIENT_KEY not set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: `
variable "host" { type = string }
variable "ca" { type = string }
variable "cert" { type = string }
variable "key" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-cert
YAML

  cluster_connection = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    client_certificate     = var.cert
    client_key            = var.key
  }
}`,
				ConfigVariables: config.Variables{
					"host": config.StringVariable(host),
					"ca":   config.StringVariable(ca),
					"cert": config.StringVariable(cert),
					"key":  config.StringVariable(key),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, "acctest-cert"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, "acctest-cert"),
	})
}
