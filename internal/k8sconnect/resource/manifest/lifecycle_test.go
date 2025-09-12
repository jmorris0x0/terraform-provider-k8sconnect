// internal/k8sconnect/resource/manifest/lifecycle_test.go
package manifest_test

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

// Test delete protection functionality
func TestAccManifestResource_DeleteProtection(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("delete-protected-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create resource with delete protection enabled
			{
				Config: testAccManifestConfigDeleteProtectionEnabled(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_protected", "delete_protection", "true"),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_protected", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Try to destroy - should fail due to protection
			{
				Config: testAccManifestConfigDeleteProtectionProviderOnly(),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("Delete Protection Enabled"),
			},
			// Step 3: Disable protection
			{
				Config: testAccManifestConfigDeleteProtectionDisabled(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_protected", "delete_protection", "false"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 4: Now destroy should succeed
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigDeleteProtectionEnabled(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_protected" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  delete_protection = true

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, namespace)
}

func testAccManifestConfigDeleteProtectionDisabled(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_protected" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  delete_protection = false

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, namespace)
}

func TestAccManifestResource_ConnectionChange(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("conn-change-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create with kubeconfig_raw
			{
				Config: testAccManifestConfigConnectionChange1(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_conn_change", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					// TODO: Add check that ownership annotation exists on the K8s resource
				),
			},
			// Step 2: Change connection method (same cluster)
			{
				Config: testAccManifestConfigConnectionChange2(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				ExpectError: regexp.MustCompile("connection change would move resource to a different cluster"),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigConnectionChange1(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_conn_change" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, namespace)
}

func testAccManifestConfigConnectionChange2(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_conn_change" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
    context        = "k3d-oidc-e2e"  # Explicit context (connection change)
  }
}
`, namespace)
}

func TestAccManifestResource_ForceDestroy(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("force-destroy-ns-%d", time.Now().UnixNano()%1000000)
	pvcName := fmt.Sprintf("force-destroy-pvc-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigForceDestroy(ns, pvcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"pvc_name":  config.StringVariable(pvcName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_force", "force_destroy", "true"),
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_force", "delete_timeout", "30s"),
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckPVCDestroy(k8sClient, ns, pvcName),
	})
}

func testAccManifestConfigForceDestroy(namespace, pvcName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "pvc_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "force_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "test_force" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
YAML

  delete_timeout = "30s"
  force_destroy = true

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.force_namespace]
}
`, namespace, pvcName, namespace)
}

func TestAccManifestResource_DeleteTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("delete-timeout-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigDeleteTimeout(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_timeout", "delete_timeout", "2m"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigDeleteTimeout(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_timeout" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  delete_timeout = "2m"

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, namespace)
}

func testAccManifestConfigDeleteProtectionProviderOnly() string {
	return `
variable "raw" {
  type = string
}

provider "k8sconnect" {}
`
}
