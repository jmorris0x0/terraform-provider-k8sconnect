// internal/k8sconnect/resource/manifest/manifest_ownership_test.go
package manifest_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
)

func TestAccManifestResource_Ownership(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigOwnership,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// ID should be 12 hex characters
					resource.TestMatchResourceAttr("k8sconnect_manifest.test_ownership", "id",
						regexp.MustCompile("^[a-f0-9]{12}$")),
					testAccCheckConfigMapExists(k8sClient, "default", "test-ownership"),
					testAccCheckOwnershipAnnotations(k8sClient, "default", "test-ownership"),
				),
			},
		},
		CheckDestroy: testAccCheckConfigMapDestroy(k8sClient, "default", "test-ownership"),
	})
}

const testAccManifestConfigOwnership = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_ownership" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-ownership
  namespace: default
data:
  key: value
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}`

func TestAccManifestResource_OwnershipConflict(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Create first resource
			{
				Config: testAccManifestConfigOwnershipFirst,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckConfigMapExists(k8sClient, "default", "test-conflict"),
					testAccCheckOwnershipAnnotations(k8sClient, "default", "test-conflict"),
				),
			},
			// Try to create second resource managing same ConfigMap - should fail
			{
				Config: testAccManifestConfigOwnershipBoth,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("resource managed by different k8sconnect resource"),
			},
		},
		CheckDestroy: testAccCheckConfigMapDestroy(k8sClient, "default", "test-conflict"),
	})
}

const testAccManifestConfigOwnershipFirst = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "first" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-conflict
  namespace: default
data:
  owner: first
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}`

const testAccManifestConfigOwnershipBoth = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "first" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-conflict
  namespace: default
data:
  owner: first
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "second" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-conflict
  namespace: default
data:
  owner: second
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}`

func TestAccManifestResource_OwnershipImport(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with Terraform
			{
				Config: testAccManifestConfigOwnershipImport,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("k8sconnect_manifest.import_test", "id",
						regexp.MustCompile("^[a-f0-9]{12}$")),
					testAccCheckOwnershipAnnotations(k8sClient, "default", "test-import-ownership"),
				),
			},
			// Step 2: Import the ConfigMap
			{
				Config: testAccManifestConfigOwnershipImport,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ResourceName:      "k8sconnect_manifest.import_test",
				ImportState:       true,
				ImportStateId:     "kind-oidc-e2e/default/ConfigMap/test-import-ownership",
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"imported_without_annotations",
					"cluster_connection",
					"yaml_body",
					"managed_state_projection",
					"delete_protection",
					"force_conflicts",
				},
			},
			// Step 3: Verify ownership annotations still exist after import
			{
				Config: testAccManifestConfigOwnershipImport,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("k8sconnect_manifest.import_test", "id",
						regexp.MustCompile("^[a-f0-9]{12}$")),
					testAccCheckOwnershipAnnotations(k8sClient, "default", "test-import-ownership"),
				),
			},
		},
	})
}

const testAccManifestConfigOwnershipImport = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "import_test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-import-ownership
  namespace: default
data:
  key: value
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}`

// Helper to check ownership annotations exist
func testAccCheckOwnershipAnnotations(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap: %v", err)
		}

		annotations := cm.GetAnnotations()
		if annotations == nil {
			return fmt.Errorf("ConfigMap has no annotations")
		}

		if _, ok := annotations["k8sconnect.terraform.io/terraform-id"]; !ok {
			return fmt.Errorf("ConfigMap missing ownership annotation k8sconnect.terraform.io/terraform-id")
		}

		return nil
	}
}
