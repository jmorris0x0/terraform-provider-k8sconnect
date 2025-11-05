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

func TestAccObjectResource_Import(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	namespaceName := fmt.Sprintf("import-ns-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace with Terraform
			{
				Config: testAccManifestConfigImport(namespaceName),
				ConfigVariables: config.Variables{
					"raw":            config.StringVariable(raw),
					"namespace_name": config.StringVariable(namespaceName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_import", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_import", "yaml_body"),
					testhelpers.CheckNamespaceExists(k8sClient, namespaceName),
				),
			},
			// Step 2: Import the namespace
			{
				Config: testAccManifestConfigImport(namespaceName),
				ConfigVariables: config.Variables{
					"raw":            config.StringVariable(raw),
					"namespace_name": config.StringVariable(namespaceName),
				},
				ResourceName:      "k8sconnect_object.test_import",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("k3d-k8sconnect-test:v1/Namespace:%s", namespaceName),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"cluster",
					"yaml_body",
					"managed_state_projection",
					"delete_protection",
					"force_conflicts",
				},
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, namespaceName),
	})
}

func testAccManifestConfigImport(namespaceName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace_name" {
  type = string  
}

provider "k8sconnect" {}

resource "k8sconnect_object" "test_import" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    test: import
    created-by: terraform-test
YAML
  
  cluster = {
    kubeconfig = var.raw
  }
}
`, namespaceName)
}

func TestAccObjectResource_ImportWithManagedFields(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("import-fields-ns-%d", time.Now().UnixNano()%1000000)
	configMapName := fmt.Sprintf("import-fields-cm-%d", time.Now().UnixNano()%1000000)
	resourceName := "k8sconnect_object.test_import"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create resource with Terraform
			{
				Config: testAccManifestConfigImportWithFields(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(configMapName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, configMapName),
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttrSet(resourceName, "yaml_body"),
					resource.TestCheckResourceAttrSet(resourceName, "managed_state_projection.%"),
				),
			},
			// Step 2: Import the same resource
			{
				Config: testAccManifestConfigImportWithFields(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(configMapName),
				},
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("k3d-k8sconnect-test:%s:v1/ConfigMap:%s", ns, configMapName),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"cluster",                  // Import uses file, config uses raw
					"yaml_body",                // Formatting and annotations differ
					"managed_state_projection", // Import includes extra K8s fields
					"delete_protection",        // Only in import, not in config
					"force_conflicts",
				},
			},
		},
	})
}

func testAccManifestConfigImportWithFields(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "name" {
  type = string  
}

provider "k8sconnect" {}

resource "k8sconnect_object" "import_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML
  
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_import" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
      labels:
        test: import
        created-by: terraform-test
      annotations:
        test-annotation: value
    data:
      key1: value1
      key2: value2
      key3: value3
  YAML
  
  cluster = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.import_namespace]
}
`, namespace, name, namespace)
}

// TestAccObjectResource_ImportWithOwnershipConflict creates a resource with kubectl first,
// then applies with k8sconnect to verify ownership takeover via SSA force=true
func TestAccObjectResource_ImportWithOwnershipConflict(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("import-ownership-ns-%d", time.Now().UnixNano()%1000000)
	configMapName := fmt.Sprintf("kubectl-created-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace with Terraform
			{
				Config: testAccManifestConfigImportOwnershipConflictPrep(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Create ConfigMap with kubectl (different field manager)
			{
				PreConfig: func() {
					testhelpers.CreateConfigMapWithKubectl(t, ns, configMapName, map[string]string{
						"created-by": "kubectl",
						"test":       "ownership-conflict",
					})
				},
				Config: testAccManifestConfigImportOwnershipConflictPrep(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, configMapName),
					// Verify kubectl created it (kubectl apply uses client-side-apply by default)
					testhelpers.CheckFieldManager(k8sClient, ns, "ConfigMap", configMapName, "kubectl-client-side-apply"),
				),
			},
			// Step 3: Apply with k8sconnect - should ERROR (resource exists, must use import)
			{
				Config: testAccManifestConfigImportOwnershipConflict(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(configMapName),
				},
				ExpectError: regexp.MustCompile("Resource Already Exists"),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigImportOwnershipConflictPrep(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "import_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}
`, namespace)
}

func testAccManifestConfigImportOwnershipConflict(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "name" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "import_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_import" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
      labels:
        created-by: kubectl
        test: ownership-conflict
    data:
      key1: value1
      key2: value2
  YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.import_namespace]
}
`, namespace, name, namespace)
}

// TestAccObjectResource_AnnotationLossRecovery verifies that k8sconnect can recover
// when ownership annotations are removed from a managed resource
func TestAccObjectResource_AnnotationLossRecovery(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("annotation-loss-ns-%d", time.Now().UnixNano()%1000000)
	configMapName := fmt.Sprintf("annotation-loss-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccManifestConfigAnnotationLossPrep(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Create ConfigMap with k8sconnect
			{
				Config: testAccManifestConfigAnnotationLoss(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(configMapName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, configMapName),
					// Verify k8sconnect owns the resource
					testhelpers.CheckFieldManager(k8sClient, ns, "ConfigMap", configMapName, "k8sconnect"),
					testhelpers.CheckHasAnnotation(k8sClient, ns, "ConfigMap", configMapName, "k8sconnect.terraform.io/terraform-id"),
				),
			},
			// Step 3: Remove annotations manually, then verify recovery on next apply
			{
				PreConfig: func() {
					// Remove k8sconnect annotation using kubectl
					testhelpers.RemoveAnnotation(t, k8sClient, ns, "ConfigMap", configMapName, "k8sconnect.terraform.io/terraform-id")
				},
				Config: testAccManifestConfigAnnotationLoss(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(configMapName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, configMapName),
					// Verify k8sconnect reclaimed ownership
					testhelpers.CheckFieldManager(k8sClient, ns, "ConfigMap", configMapName, "k8sconnect"),
					testhelpers.CheckHasAnnotation(k8sClient, ns, "ConfigMap", configMapName, "k8sconnect.terraform.io/terraform-id"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigAnnotationLossPrep(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "test_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}
`, namespace)
}

func testAccManifestConfigAnnotationLoss(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "name" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "test_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_configmap" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
    data:
      test: annotation-loss-recovery
  YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.test_namespace]
}
`, namespace, name, namespace)
}
