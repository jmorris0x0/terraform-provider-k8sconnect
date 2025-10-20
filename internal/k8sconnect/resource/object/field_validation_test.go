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

// TestAccObjectResource_FieldValidationError_Single tests that field validation errors
// (typos in field names) are caught during plan phase with clear error messages
func TestAccObjectResource_FieldValidationError_Single(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-val-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccObjectResourceFieldValidationNamespace(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
				),
			},
			// Step 2: Try to create deployment with typo in field name
			// Should fail with clear field validation error during PLAN phase
			{
				Config: testAccObjectResourceFieldValidationSingleError(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				// Expect field validation error mentioning the typo
				ExpectError: regexp.MustCompile("Field Validation Failed|unknown field"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccObjectResource_FieldValidationError_Multiple tests multiple field validation errors
func TestAccObjectResource_FieldValidationError_Multiple(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-val-multi-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccObjectResourceFieldValidationNamespace(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
				),
			},
			// Step 2: Try to create deployment with multiple typos
			{
				Config: testAccObjectResourceFieldValidationMultipleErrors(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				// Expect field validation error mentioning multiple fields
				ExpectError: regexp.MustCompile("Field Validation Failed|unknown field"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccObjectResource_FieldValidationError_Update tests field validation during UPDATE
func TestAccObjectResource_FieldValidationError_Update(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-val-update-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccObjectResourceFieldValidationNamespace(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
				),
			},
			// Step 2: Create valid deployment
			{
				Config: testAccObjectResourceFieldValidationValidDeployment(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.deployment", "id"),
				),
			},
			// Step 3: Update with typo in field name
			// Should fail with field validation error during UPDATE
			{
				Config: testAccObjectResourceFieldValidationUpdateError(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				ExpectError: regexp.MustCompile("Field Validation Failed|unknown field"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Test configuration functions

func testAccObjectResourceFieldValidationNamespace(namespace string) string {
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

func testAccObjectResourceFieldValidationSingleError(namespace string) string {
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

# This should fail - "replica" is a typo (should be "replicas")
resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: test-deployment
      namespace: %[1]s
    spec:
      replica: 3  # TYPO: should be "replicas"
      selector:
        matchLabels:
          app: test
      template:
        metadata:
          labels:
            app: test
        spec:
          containers:
          - name: nginx
            image: nginx:1.27
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

func testAccObjectResourceFieldValidationMultipleErrors(namespace string) string {
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

# This should fail - multiple typos
resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: test-deployment
      namespace: %[1]s
    spec:
      replica: 3  # TYPO: should be "replicas"
      selector:
        matchLabels:
          app: test
      template:
        metadata:
          labels:
            app: test
        spec:
          container:  # TYPO: should be "containers" (plural)
          - name: nginx
            image: nginx:1.27
            imagePullPolice: Always  # TYPO: should be "imagePullPolicy"
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

func testAccObjectResourceFieldValidationValidDeployment(namespace string) string {
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

resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: test-deployment
      namespace: %[1]s
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: test
      template:
        metadata:
          labels:
            app: test
        spec:
          containers:
          - name: nginx
            image: nginx:1.27
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

func testAccObjectResourceFieldValidationUpdateError(namespace string) string {
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

# Update with typo - should fail
resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: test-deployment
      namespace: %[1]s
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: test
      template:
        metadata:
          labels:
            app: test
        spec:
          containers:
          - name: nginx
            image: nginx:1.27
            imagePullPolice: Always  # TYPO: should be "imagePullPolicy"
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
