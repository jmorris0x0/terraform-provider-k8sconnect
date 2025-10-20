package object_test

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

// TestAccObjectResource_GenericError tests the default/fallback error handler
// This ensures unknown errors still get classified with useful messages
func TestAccObjectResource_GenericError(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("generic-error-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccObjectResourceErrorNamespace(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
				),
			},
			// Step 2: Try to create resource with malformed YAML structure
			// This should trigger a generic error path
			{
				Config: testAccObjectResourceGenericErrorConfig(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				ExpectError: regexp.MustCompile("API Error|Invalid|error"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Helper functions

func testAccObjectResourceErrorNamespace(namespace string) string {
	return fmt.Sprintf(`
resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %[1]s
  YAML

  cluster_connection = {
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

func testAccObjectResourceGenericErrorConfig(namespace string) string {
	return fmt.Sprintf(`
resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %[1]s
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}

# Create a Pod with invalid spec (missing required fields)
# This should trigger validation errors
resource "k8sconnect_object" "invalid_pod" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Pod
    metadata:
      name: invalid-pod
      namespace: %[1]s
    spec:
      # Missing required 'containers' field - should cause error
      restartPolicy: Never
  YAML

  cluster_connection = {
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
