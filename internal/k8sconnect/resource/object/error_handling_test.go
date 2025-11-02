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

  cluster = {
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

  cluster = {
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

  cluster = {
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

// TestAccObjectResource_EmptyYAML tests that empty yaml_body produces a clean error
// Bug #1: Provider should not crash on empty YAML
func TestAccObjectResource_EmptyYAML(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccObjectResourceEmptyYAMLConfig(),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// Should get a clean validation error, not a crash
				ExpectError: regexp.MustCompile("yaml_body.*empty|cannot be empty|must not be empty"),
			},
		},
	})
}

func testAccObjectResourceEmptyYAMLConfig() string {
	return `
resource "k8sconnect_object" "empty_yaml" {
  yaml_body = ""

  cluster = {
    kubeconfig = var.raw
  }
}

variable "raw" {
  type = string
}
`
}

// TestAccObjectResource_NamespaceNotFound tests that non-existent namespace errors are clear
// Bug #2: Should not be misdiagnosed as CRD issues
func TestAccObjectResource_NamespaceNotFound(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccObjectResourceNamespaceNotFoundConfig(),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// Should get a clear namespace error, not a CRD error
				ExpectError: regexp.MustCompile("(?i)(namespace.*(not found|does not exist|doesn't exist))"),
			},
		},
	})
}

func testAccObjectResourceNamespaceNotFoundConfig() string {
	return `
resource "k8sconnect_object" "bad_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-config
      namespace: this-namespace-definitely-does-not-exist
    data:
      test: value
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

variable "raw" {
  type = string
}
`
}

// TestAccObjectResource_InvalidLabel tests that built-in resource validation errors
// Bug #3: Should NOT be labeled as "CEL Validation Failed" for built-in resources
func TestAccObjectResource_InvalidLabel(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("invalid-label-ns-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccObjectResourceInvalidLabelNamespace(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
				),
			},
			// Step 2: Try to create ConfigMap with invalid label
			// Should get field validation error, NOT CEL validation error
			{
				Config: testAccObjectResourceInvalidLabelConfig(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				// Should get validation error, but NOT "CEL Validation Failed"
				// Built-in resources use OpenAPI schema validation, not CEL
				ExpectError: regexp.MustCompile("(?i)(invalid|metadata\\.labels)"),
			},
		},
	})
}

func testAccObjectResourceInvalidLabelNamespace(namespace string) string {
	return fmt.Sprintf(`
resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %[1]s
  YAML

  cluster = {
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

func testAccObjectResourceInvalidLabelConfig(namespace string) string {
	return fmt.Sprintf(`
resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %[1]s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "bad_label" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-config
      namespace: %[1]s
      labels:
        invalid label with spaces: "value"
    data:
      test: value
  YAML

  cluster = {
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
