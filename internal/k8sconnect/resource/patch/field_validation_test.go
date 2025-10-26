package patch_test

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

// TestAccPatchResource_FieldValidationError tests that field validation errors
// (typos in field names) are caught during plan phase with clear error messages
// Uses strategic merge patch since it uses SSA
func TestAccPatchResource_FieldValidationError(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("patch-field-val-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccPatchResourceFieldValidationNamespace(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
					// Create deployment with kubectl field manager
					createDeploymentWithFieldManager(t, k8sClient, ns, "test-deployment", "kubectl", map[string]interface{}{
						"replicas": int64(2),
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "test",
							},
						},
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"labels": map[string]interface{}{
									"app": "test",
								},
							},
							"spec": map[string]interface{}{
								"containers": []interface{}{
									map[string]interface{}{
										"name":  "nginx",
										"image": "nginx:1.27",
									},
								},
							},
						},
					}),
				),
			},
			// Step 2: Try to patch deployment with typo in field name
			// Should fail with clear field validation error
			{
				Config: testAccPatchResourceFieldValidationError(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				// Expect field validation error mentioning the typo
				ExpectError: regexp.MustCompile("Field Validation Failed|unknown field|field not declared in schema"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_FieldValidationError_Update tests field validation during UPDATE
func TestAccPatchResource_FieldValidationError_Update(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("patch-field-val-upd-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and deployment with kubectl
			{
				Config: testAccPatchResourceFieldValidationNamespace(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.namespace", "id"),
					// Create deployment with kubectl field manager
					createDeploymentWithFieldManager(t, k8sClient, ns, "test-deployment", "kubectl", map[string]interface{}{
						"replicas": int64(2),
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "test",
							},
						},
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"labels": map[string]interface{}{
									"app": "test",
								},
							},
							"spec": map[string]interface{}{
								"containers": []interface{}{
									map[string]interface{}{
										"name":  "nginx",
										"image": "nginx:1.27",
									},
								},
							},
						},
					}),
				),
			},
			// Step 2: Create valid patch
			{
				Config: testAccPatchResourceFieldValidationValidPatch(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.deployment_patch", "id"),
				),
			},
			// Step 3: Update patch with typo in field name
			// Should fail with field validation error during UPDATE
			{
				Config: testAccPatchResourceFieldValidationUpdateError(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				ExpectError: regexp.MustCompile("Field Validation Failed|unknown field|field not declared in schema"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Test configuration functions

func testAccPatchResourceFieldValidationError(namespace string) string {
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

# This should fail - "replica" is a typo (should be "replicas")
resource "k8sconnect_patch" "deployment_patch" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "test-deployment"
    namespace   = "%[1]s"
  }

  patch = <<-YAML
    spec:
      replica: 3  # TYPO: should be "replicas"
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

func testAccPatchResourceFieldValidationValidPatch(namespace string) string {
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

# Valid patch
resource "k8sconnect_patch" "deployment_patch" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "test-deployment"
    namespace   = "%[1]s"
  }

  patch = <<-YAML
    spec:
      replicas: 3
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

func testAccPatchResourceFieldValidationUpdateError(namespace string) string {
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

# Update patch with typo - should fail
resource "k8sconnect_patch" "deployment_patch" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "test-deployment"
    namespace   = "%[1]s"
  }

  patch = <<-YAML
    spec:
      replica: 5  # TYPO: should be "replicas"
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

// Helper functions

func testAccPatchResourceFieldValidationNamespace(namespace string) string {
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
