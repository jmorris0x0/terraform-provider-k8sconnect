// internal/k8sconnect/resource/object/crd_test.go
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

func TestAccObjectResource_CRDAndCRTogether(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	plural := fmt.Sprintf("testcrds%s", suffix)
	crdName := fmt.Sprintf("%s.example.com", plural)
	crName := fmt.Sprintf("test-instance-%d", time.Now().UnixNano()%1000000)
	ns := fmt.Sprintf("crd-test-ns-%d", time.Now().UnixNano()%1000000)

	// Create Kubernetes client for verification
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigCRDWithCR(crdName, plural, crName, ns),
				ConfigVariables: config.Variables{
					"kubeconfig": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify all resources were created
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_namespace", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_crd", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_cr", "id"),

					// Verify namespace exists
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigCRDWithCR(crdName, plural, crName, namespace string) string {
	return fmt.Sprintf(`
variable "kubeconfig" {
  type = string
}

# Create namespace first
resource "k8sconnect_object" "test_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %[4]s
  YAML

  cluster_connection = {
    kubeconfig = var.kubeconfig
  }
}

# Create the CRD
resource "k8sconnect_object" "test_crd" {
  yaml_body = <<-YAML
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: %[1]s
    spec:
      group: example.com
      names:
        kind: TestCRD
        plural: %[2]s
      scope: Namespaced
      versions:
      - name: v1
        served: true
        storage: true
        schema:
          openAPIV3Schema:
            type: object
            properties:
              spec:
                type: object
                properties:
                  foo:
                    type: string
  YAML

  cluster_connection = {
    kubeconfig = var.kubeconfig
  }
}

# Create the Custom Resource - this should succeed thanks to CRD retry
resource "k8sconnect_object" "test_cr" {
  yaml_body = <<-YAML
    apiVersion: example.com/v1
    kind: TestCRD
    metadata:
      name: %[3]s
      namespace: %[4]s
    spec:
      foo: bar
  YAML

  cluster_connection = {
    kubeconfig = var.kubeconfig
  }

  depends_on = [
    k8sconnect_object.test_crd,
    k8sconnect_object.test_namespace
  ]
}
`, crdName, plural, crName, namespace)
}

// TestAccObjectResource_NonCRDErrorFailsImmediately verifies that non-CRD errors
// (like validation errors, invalid fields, etc.) fail immediately without triggering
// the 30-second CRD retry logic. This ensures good UX by not making users wait
// 30s for simple mistakes.
func TestAccObjectResource_NonCRDErrorFailsImmediately(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("non-crd-err-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigNonCRDError(ns),
				ConfigVariables: config.Variables{
					"kubeconfig": config.StringVariable(raw),
				},
				// This should fail with a validation error (not CRD not found)
				// The error message should indicate field not declared in schema
				// Use (?s) to allow . to match newlines, in case error is wrapped
				ExpectError: regexp.MustCompile(`(?s)field.*not declared in schema|unknown field`),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigNonCRDError(namespace string) string {
	return fmt.Sprintf(`
variable "kubeconfig" {
  type = string
}

resource "k8sconnect_object" "test_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster_connection = {
    kubeconfig = var.kubeconfig
  }
}

# This ConfigMap has an invalid field that should be rejected immediately
# (not a CRD-not-found error, so no 30s retry)
resource "k8sconnect_object" "test_invalid" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: invalid-cm
      namespace: %s
    spec:
      # ConfigMaps don't have a spec field - this should fail validation
      invalidField: invalid
  YAML

  cluster_connection = {
    kubeconfig = var.kubeconfig
  }

  depends_on = [k8sconnect_object.test_namespace]
}
`, namespace, namespace)
}

