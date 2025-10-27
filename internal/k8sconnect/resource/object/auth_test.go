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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

func TestAccObjectResource_TokenAuth(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	token := os.Getenv("TF_ACC_K8S_TOKEN")
	raw := os.Getenv("TF_ACC_KUBECONFIG")

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
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
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

resource "k8sconnect_object" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    token                  = var.token
  }
}`, namespace)
}

func TestAccObjectResource_ClientCertAuth(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	cert := os.Getenv("TF_ACC_K8S_CLIENT_CERT")
	key := os.Getenv("TF_ACC_K8S_CLIENT_KEY")
	raw := os.Getenv("TF_ACC_KUBECONFIG")

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
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
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

resource "k8sconnect_object" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    client_certificate     = var.cert
    client_key            = var.key
  }
}`, namespace)
}

// TestAccObjectResource_ContextSwitching verifies that multiple contexts in a kubeconfig
// work correctly. This is critical for multi-cluster scenarios.
func TestAccObjectResource_ContextSwitching(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	token := os.Getenv("TF_ACC_K8S_TOKEN")
	raw := os.Getenv("TF_ACC_KUBECONFIG")

	if host == "" || ca == "" || token == "" {
		t.Skip("TF_ACC_K8S_HOST, TF_ACC_K8S_CA, and TF_ACC_K8S_TOKEN must be set")
	}

	ns1 := fmt.Sprintf("context-a-ns-%d", time.Now().UnixNano()%1000000)
	ns2 := fmt.Sprintf("context-b-ns-%d", time.Now().UnixNano()%1000000)
	cm1 := fmt.Sprintf("context-a-cm-%d", time.Now().UnixNano()%1000000)
	cm2 := fmt.Sprintf("context-b-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	// Create a kubeconfig with two contexts (both pointing to same cluster for simplicity)
	multiContextKubeconfig := createMultiContextKubeconfig(host, ca, token)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigContextSwitching(ns1, ns2, cm1, cm2),
				ConfigVariables: config.Variables{
					"kubeconfig": config.StringVariable(multiContextKubeconfig),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify both namespaces were created
					testhelpers.CheckNamespaceExists(k8sClient, ns1),
					testhelpers.CheckNamespaceExists(k8sClient, ns2),
					// Verify both ConfigMaps were created
					testhelpers.CheckConfigMapExists(k8sClient, ns1, cm1),
					testhelpers.CheckConfigMapExists(k8sClient, ns2, cm2),
				),
			},
		},
		CheckDestroy: func(s *terraform.State) error {
			// Check both namespaces are destroyed
			if err := testhelpers.CheckNamespaceDestroy(k8sClient, ns1)(s); err != nil {
				return err
			}
			return testhelpers.CheckNamespaceDestroy(k8sClient, ns2)(s)
		},
	})
}

// createMultiContextKubeconfig creates a kubeconfig with two contexts
// Both contexts point to the same cluster for testing simplicity
func createMultiContextKubeconfig(host, ca, token string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: %s
    server: %s
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: context-a
- context:
    cluster: test-cluster
    user: test-user
  name: context-b
current-context: context-a
users:
- name: test-user
  user:
    token: %s
`, ca, host, token)
}

func testAccManifestConfigContextSwitching(ns1, ns2, cm1, cm2 string) string {
	return fmt.Sprintf(`
variable "kubeconfig" { type = string }

provider "k8sconnect" {}

# Create namespace using context-a
resource "k8sconnect_object" "ns_context_a" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    kubeconfig = var.kubeconfig
    context    = "context-a"
  }
}

# Create namespace using context-b
resource "k8sconnect_object" "ns_context_b" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    kubeconfig = var.kubeconfig
    context    = "context-b"
  }
}

# Create ConfigMap in ns1 using context-a
resource "k8sconnect_object" "test_context_a" {
  depends_on = [k8sconnect_object.ns_context_a]

  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  context: "a"
  test: "multi-context"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
    context    = "context-a"
  }
}

# Create ConfigMap in ns2 using context-b
resource "k8sconnect_object" "test_context_b" {
  depends_on = [k8sconnect_object.ns_context_b]

  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  context: "b"
  test: "multi-context"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
    context    = "context-b"
  }
}
`, ns1, ns2, cm1, ns1, cm2, ns2)
}
