// internal/k8sinline/resource/manifest/manifest_lifecycle_test.go
package manifest_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline"
)

// Test delete protection functionality
func TestAccManifestResource_DeleteProtection(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create resource with delete protection enabled
			{
				Config: testAccManifestConfigDeleteProtectionEnabled,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_protected", "delete_protection", "true"),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_protected", "id"),
					testAccCheckNamespaceExists(k8sClient, "acctest-protected"),
				),
			},
			// Step 2: Try to destroy - should fail due to protection
			{
				Config: testAccManifestConfigDeleteProtectionProviderOnly,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("Delete Protection Enabled"),
			},
			// Step 3: Disable protection
			{
				Config: testAccManifestConfigDeleteProtectionDisabled,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_protected", "delete_protection", "false"),
					testAccCheckNamespaceExists(k8sClient, "acctest-protected"),
				),
			},
			// Step 4: Now destroy should succeed
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, "acctest-protected"),
	})
}

const testAccManifestConfigDeleteProtectionEnabled = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_protected" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-protected
YAML

  delete_protection = true

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigDeleteProtectionDisabled = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_protected" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-protected
YAML

  delete_protection = false

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

func TestAccManifestResource_ConnectionChange(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create with kubeconfig_raw
			{
				Config: testAccManifestConfigConnectionChange1,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_conn_change", "id"),
					testAccCheckNamespaceExists(k8sClient, "acctest-conn-change"),
					// TODO: Add check that ownership annotation exists on the K8s resource
				),
			},
			// Step 2: Change connection method (same cluster)
			{
				Config: testAccManifestConfigConnectionChange2,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("connection change would move resource to a different cluster"),
			},
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, "acctest-conn-change"),
	})
}

const testAccManifestConfigConnectionChange1 = `
variable "raw" { type = string }
provider "k8sinline" {}

resource "k8sinline_manifest" "test_conn_change" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-conn-change
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigConnectionChange2 = `
variable "raw" { type = string }
provider "k8sinline" {}

resource "k8sinline_manifest" "test_conn_change" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-conn-change
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
    context        = "kind-oidc-e2e"  # Explicit context (connection change)
  }
}
`

func TestAccManifestResource_ForceDestroy(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigForceDestroy,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_force", "force_destroy", "true"),
					resource.TestCheckResourceAttr("k8sinline_manifest.test_force", "delete_timeout", "30s"),
					testAccCheckPVCExists(k8sClient, "default", "test-pvc-force"),
				),
			},
		},
		CheckDestroy: testAccCheckPVCDestroy(k8sClient, "default", "test-pvc-force"),
	})
}

const testAccManifestConfigForceDestroy = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_force" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-pvc-force
  namespace: default
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
}
`

func TestAccManifestResource_DeleteTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigDeleteTimeout,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_timeout", "delete_timeout", "2m"),
					testAccCheckNamespaceExists(k8sClient, "acctest-timeout"),
				),
			},
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, "acctest-timeout"),
	})
}

const testAccManifestConfigDeleteTimeout = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_timeout" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-timeout
YAML

  delete_timeout = "2m"

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigDeleteProtectionProviderOnly = `
variable "raw" {
  type = string
}

provider "k8sinline" {}
`
