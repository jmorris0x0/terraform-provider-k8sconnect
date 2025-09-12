// internal/k8sconnect/resource/manifest/auth_test.go
package manifest_test

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

func TestAccManifestResource_TokenAuth(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	token := os.Getenv("TF_ACC_K8S_TOKEN")
	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")

	if token == "" {
		t.Skip("TF_ACC_K8S_TOKEN not set")
	}

	ns := fmt.Sprintf("token-auth-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigTokenAuth(ns),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"token":     config.StringVariable(token),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigTokenAuth(namespace string) string {
	return fmt.Sprintf(`
variable "host" { type = string }
variable "ca" { type = string }
variable "token" { type = string }
variable "namespace" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    token                  = var.token
  }
}`, namespace)
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

	ns := fmt.Sprintf("client-cert-auth-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigClientCertAuth(ns),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"cert":      config.StringVariable(cert),
					"key":       config.StringVariable(key),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigClientCertAuth(namespace string) string {
	return fmt.Sprintf(`
variable "host" { type = string }
variable "ca" { type = string }
variable "cert" { type = string }
variable "key" { type = string }
variable "namespace" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    client_certificate     = var.cert
    client_key            = var.key
  }
}`, namespace)
}
