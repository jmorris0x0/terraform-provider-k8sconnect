// internal/k8sinline/resource/manifest/manifest_ownership_test.go
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
	v1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline"
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
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigOwnership,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// ID should be 12 hex characters
					resource.TestMatchResourceAttr("k8sinline_manifest.test_ownership", "id",
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

provider "k8sinline" {}

resource "k8sinline_manifest" "test_ownership" {
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
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
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
				ExpectError: regexp.MustCompile("resource managed by different k8sinline resource"),
			},
		},
		CheckDestroy: testAccCheckConfigMapDestroy(k8sClient, "default", "test-conflict"),
	})
}

const testAccManifestConfigOwnershipFirst = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "first" {
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

provider "k8sinline" {}

resource "k8sinline_manifest" "first" {
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

resource "k8sinline_manifest" "second" {
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

	// Create unmanaged ConfigMap first
	ctx := context.Background()
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-import-ownership",
			Namespace: "default",
		},
		Data: map[string]string{
			"key": "value",
		},
	}
	_, err := k8sClient.CoreV1().ConfigMaps("default").Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test ConfigMap: %v", err)
	}

	// Clean up after test
	t.Cleanup(func() {
		k8sClient.CoreV1().ConfigMaps("default").Delete(ctx, "test-import-ownership", metav1.DeleteOptions{})
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			// Import existing resource
			{
				ResourceName:      "k8sinline_manifest.import_test",
				ImportState:       true,
				ImportStateId:     "kind-oidc-e2e/default/ConfigMap/test-import-ownership",
				ImportStateVerify: false, // Skip verify since import doesn't set all attributes
				Config:            testAccManifestConfigOwnershipImport,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
			},
			// After import, apply should add ownership annotations
			{
				Config: testAccManifestConfigOwnershipImport,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("k8sinline_manifest.import_test", "id",
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

provider "k8sinline" {}

resource "k8sinline_manifest" "import_test" {
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

		if _, ok := annotations["k8sinline.terraform.io/terraform-id"]; !ok {
			return fmt.Errorf("ConfigMap missing ownership annotation k8sinline.terraform.io/terraform-id")
		}

		return nil
	}
}
