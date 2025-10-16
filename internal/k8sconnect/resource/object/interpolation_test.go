// internal/k8sconnect/resource/object/interpolation_test.go
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

// TestAccObjectResource_UnknownInterpolation tests that yaml_body handles
// interpolations that reference computed/unknown values from other resources
func TestAccObjectResource_UnknownInterpolation(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("interp-ns-%d", time.Now().UnixNano()%1000000)
	svcName := fmt.Sprintf("interp-svc-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// This tests the bug: using a computed value from one resource
				// in the yaml_body of another resource should work
				Config: testAccManifestConfigInterpolation(ns, svcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"svc_name":  config.StringVariable(svcName),
				},
				Check: resource.ComposeTestCheckFunc(
					// Both resources should be created successfully
					resource.TestCheckResourceAttrSet("k8sconnect_object.source", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.dependent", "id"),

					// The dependent ConfigMap should contain the interpolated ID
					testhelpers.CheckConfigMapExists(k8sClient, ns, "dependent-config"),
					// The ConfigMap data should contain the source's ID
					testhelpers.CheckConfigMapFieldSet(k8sClient, ns, "dependent-config", "data.source_id"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigInterpolation(namespace, svcName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "svc_name" {
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

  cluster_connection = {
    kubeconfig = var.raw
  }
}

# Source resource with a computed ID that won't be known until apply
resource "k8sconnect_object" "source" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: source-config
  namespace: %s
data:
  value: "test"
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.namespace]
}

# This is the critical test: using a computed value (the ID) from one resource
# in the yaml_body of another. During plan, the ID is unknown.
resource "k8sconnect_object" "dependent" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: dependent-config
  namespace: %s
data:
  # This interpolation uses a value not known at plan time
  source_id: "${k8sconnect_object.source.id}"
  static_value: "known-at-plan-time"
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.source]
}
`, namespace, namespace, namespace)
}

// TestAccObjectResource_ChainedInterpolation tests multiple levels of interpolation
func TestAccObjectResource_ChainedInterpolation(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("chain-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Test A -> B -> C dependency chain with interpolations
				Config: testAccManifestConfigChainedInterpolation(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					// All three resources should be created
					resource.TestCheckResourceAttrSet("k8sconnect_object.first", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.second", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.third", "id"),

					// Verify they exist in Kubernetes
					testhelpers.CheckConfigMapExists(k8sClient, ns, "first"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, "second"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, "third"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigChainedInterpolation(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
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

  cluster_connection = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "first" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: first
  namespace: %s
data:
  value: "first"
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_object" "second" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: second
  namespace: %s
data:
  first_id: "${k8sconnect_object.first.id}"
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.first]
}

resource "k8sconnect_object" "third" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: third
  namespace: %s
data:
  first_id: "${k8sconnect_object.first.id}"
  second_id: "${k8sconnect_object.second.id}"
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.second]
}
`, namespace, namespace, namespace, namespace)
}
