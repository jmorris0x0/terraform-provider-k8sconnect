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

// TestAccObjectResource_CELValidationError tests that CEL validation errors
// from CRDs are formatted clearly for the user
func TestAccObjectResource_CELValidationError(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	suffix := time.Now().UnixNano() % 1000000
	ns := fmt.Sprintf("cel-validation-ns-%d", suffix)
	testGroup := fmt.Sprintf("test-%d.com", suffix)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and CRD with CEL validation
			{
				Config: testAccObjectResourceCELValidationCRD(ns, testGroup),
				ConfigVariables: config.Variables{
					"raw":        config.StringVariable(raw),
					"namespace":  config.StringVariable(ns),
					"test_group": config.StringVariable(testGroup),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.crd", "id"),
				),
			},
			// Step 2: Try to create CR that violates CEL validation rule
			// Should fail with clear CEL validation error
			{
				Config: testAccObjectResourceCELValidationViolation(ns, testGroup),
				ConfigVariables: config.Variables{
					"raw":        config.StringVariable(raw),
					"namespace":  config.StringVariable(ns),
					"test_group": config.StringVariable(testGroup),
				},
				// Expect CEL validation error
				ExpectError: regexp.MustCompile("CEL Validation Failed"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccObjectResource_CELValidationError_Multiple tests multiple CEL validation errors
func TestAccObjectResource_CELValidationError_Multiple(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	suffix := time.Now().UnixNano() % 1000000
	ns := fmt.Sprintf("cel-multi-ns-%d", suffix)
	testGroup := fmt.Sprintf("test-%d.com", suffix)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and CRD with multiple CEL validation rules
			{
				Config: testAccObjectResourceCELMultipleRulesCRD(ns, testGroup),
				ConfigVariables: config.Variables{
					"raw":        config.StringVariable(raw),
					"namespace":  config.StringVariable(ns),
					"test_group": config.StringVariable(testGroup),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.crd", "id"),
				),
			},
			// Step 2: Try to create CR that violates multiple CEL rules
			{
				Config: testAccObjectResourceCELMultipleViolations(ns, testGroup),
				ConfigVariables: config.Variables{
					"raw":        config.StringVariable(raw),
					"namespace":  config.StringVariable(ns),
					"test_group": config.StringVariable(testGroup),
				},
				// Expect CEL validation error mentioning multiple errors
				ExpectError: regexp.MustCompile("CEL Validation Failed"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// testAccObjectResourceCELValidationCRD creates a CRD with CEL validation rules
func testAccObjectResourceCELValidationCRD(namespace, testGroup string) string {
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

resource "k8sconnect_object" "crd" {
  yaml_body = <<-YAML
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: widgets.%[2]s
    spec:
      group: %[2]s
      names:
        kind: Widget
        plural: widgets
        singular: widget
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
                  replicas:
                    type: integer
                    minimum: 1
                x-kubernetes-validations:
                - rule: "self.replicas <= 10"
                  message: "replicas cannot exceed 10"
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

variable "test_group" {
  type = string
}
`, namespace, testGroup)
}

// testAccObjectResourceCELValidationViolation tries to create CR that violates CEL rule
func testAccObjectResourceCELValidationViolation(namespace, testGroup string) string {
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

resource "k8sconnect_object" "crd" {
  yaml_body = <<-YAML
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: widgets.%[2]s
    spec:
      group: %[2]s
      names:
        kind: Widget
        plural: widgets
        singular: widget
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
                  replicas:
                    type: integer
                    minimum: 1
                x-kubernetes-validations:
                - rule: "self.replicas <= 10"
                  message: "replicas cannot exceed 10"
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}

# This should fail - replicas: 15 violates CEL rule (must be <= 10)
resource "k8sconnect_object" "widget" {
  yaml_body = <<-YAML
    apiVersion: %[2]s/v1
    kind: Widget
    metadata:
      name: test-widget
      namespace: %[1]s
    spec:
      replicas: 15
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.crd]
}

variable "raw" {
  type = string
}

variable "namespace" {
  type = string
}

variable "test_group" {
  type = string
}
`, namespace, testGroup)
}

// testAccObjectResourceCELMultipleRulesCRD creates a CRD with multiple CEL validation rules
func testAccObjectResourceCELMultipleRulesCRD(namespace, testGroup string) string {
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

resource "k8sconnect_object" "crd" {
  yaml_body = <<-YAML
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: gadgets.%[2]s
    spec:
      group: %[2]s
      names:
        kind: Gadget
        plural: gadgets
        singular: gadget
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
                  replicas:
                    type: integer
                    minimum: 1
                  maxReplicas:
                    type: integer
                    minimum: 1
                x-kubernetes-validations:
                - rule: "self.replicas <= 10"
                  message: "replicas cannot exceed 10"
                - rule: "self.replicas <= self.maxReplicas"
                  message: "replicas must be <= maxReplicas"
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

variable "test_group" {
  type = string
}
`, namespace, testGroup)
}

// testAccObjectResourceCELMultipleViolations tries to create CR that violates multiple CEL rules
func testAccObjectResourceCELMultipleViolations(namespace, testGroup string) string {
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

resource "k8sconnect_object" "crd" {
  yaml_body = <<-YAML
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: gadgets.%[2]s
    spec:
      group: %[2]s
      names:
        kind: Gadget
        plural: gadgets
        singular: gadget
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
                  replicas:
                    type: integer
                    minimum: 1
                  maxReplicas:
                    type: integer
                    minimum: 1
                x-kubernetes-validations:
                - rule: "self.replicas <= 10"
                  message: "replicas cannot exceed 10"
                - rule: "self.replicas <= self.maxReplicas"
                  message: "replicas must be <= maxReplicas"
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}

# This should fail - violates BOTH CEL rules:
# 1. replicas: 15 > 10 (exceeds max)
# 2. replicas: 15 > maxReplicas: 5 (violates comparison rule)
resource "k8sconnect_object" "gadget" {
  yaml_body = <<-YAML
    apiVersion: %[2]s/v1
    kind: Gadget
    metadata:
      name: test-gadget
      namespace: %[1]s
    spec:
      replicas: 15
      maxReplicas: 5
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.crd]
}

variable "raw" {
  type = string
}

variable "namespace" {
  type = string
}

variable "test_group" {
  type = string
}
`, namespace, testGroup)
}
