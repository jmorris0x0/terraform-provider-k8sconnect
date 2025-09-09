// internal/k8sconnect/resource/manifest/wait_status_test.go

package manifest_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

func TestAccManifestResource_StatusNamespace(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("acctest-status-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigStatusNamespace(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, nsName),
					resource.TestCheckResourceAttr(
						"k8sconnect_manifest.test",
						"status.phase",
						"Active",
					),
					resource.TestCheckOutput("namespace_ready", "true"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, nsName),
	})
}

func testAccManifestConfigStatusNamespace(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }

  track_status = true
}

output "namespace_ready" {
  value = k8sconnect_manifest.test.status.phase == "Active"
}
`, name)
}

// Test Default Behavior (No Status Tracking on ConfigMap)
func TestAccManifestResource_NoStatusByDefault(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	cmName := fmt.Sprintf("track-status-cm-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap without track_status
			{
				Config: testAccManifestConfigNoTracking(cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, "default", cmName),
					// Status should not be set (null)
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
			// Step 2: Re-apply with formatting changes only - should show no drift
			{
				Config: testAccManifestConfigNoTrackingFormatted(cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // No drift expected!
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, "default", cmName),
	})
}

func testAccManifestConfigNoTracking(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  key1: value1
  key2: value2
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name)
}

func testAccManifestConfigNoTrackingFormatted(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
# Added comment - formatting change only
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  key1: value1
  key2: value2  # Another comment
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name)
}

// Test Toggle Status Tracking
func TestAccManifestResource_ToggleStatusTracking(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("toggle-status-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace without track_status
			{
				Config: testAccManifestConfigToggleOff(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, nsName),
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
			// Step 2: Enable track_status
			{
				Config: testAccManifestConfigToggleOn(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "track_status", "true"),
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.phase", "Active"),
				),
			},
			// Step 3: Disable track_status again
			{
				Config: testAccManifestConfigToggleOff(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "track_status"),
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, nsName),
	})
}

func testAccManifestConfigToggleOff(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  # track_status not set (defaults to false)

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name)
}

func testAccManifestConfigToggleOn(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  track_status = true

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name)
}

// Test ConfigMap with Status Tracking (empty status)
func TestAccManifestResource_ConfigMapWithStatusTracking(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	cmName := fmt.Sprintf("cm-with-status-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with track_status = true
			{
				Config: testAccManifestConfigConfigMapWithTracking(cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, "default", cmName),
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "track_status", "true"),
					// ConfigMaps have no status subresource, so status should be empty map
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.%", "0"),
				),
			},
			// Step 2: Re-apply - should not show constant drift
			{
				Config: testAccManifestConfigConfigMapWithTracking(cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // No drift even with tracking enabled
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, "default", cmName),
	})
}

func testAccManifestConfigConfigMapWithTracking(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  config: |
    setting1: value1
    setting2: value2
YAML

  track_status = true

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name)
}
