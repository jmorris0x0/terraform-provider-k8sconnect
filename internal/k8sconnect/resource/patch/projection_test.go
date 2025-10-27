package patch_test

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

// TestAccPatchResource_ProjectionStrategicMerge tests that managed_state_projection
// is populated for strategic merge patches (SSA) during terraform plan
func TestAccPatchResource_ProjectionStrategicMerge(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("projection-ssa-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("projection-ssa-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and ConfigMap with external field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create ConfigMap with kubectl field manager using k8s client
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"original": "value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Apply strategic merge patch and verify projection is populated
			{
				Config: testAccPatchConfigProjectionSSA(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify patch resource state has projection
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "managed_state_projection.%"),

					// Verify specific fields are in projection
					resource.TestCheckResourceAttr("k8sconnect_patch.test", "managed_state_projection.data.patched", "value-from-patch"),
					resource.TestCheckResourceAttr("k8sconnect_patch.test", "managed_state_projection.data.another", "field"),

					// Verify managed_fields is populated
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "managed_fields"),

					// Verify ConfigMap actually has the patched data
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					testhelpers.CheckConfigMapData(k8sClient, ns, cmName, map[string]string{
						"original": "value",
						"patched":  "value-from-patch",
						"another":  "field",
					}),
				),
			},
			// Step 3: Update patch content and verify projection updates
			{
				Config: testAccPatchConfigProjectionSSAUpdate(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify projection updated
					resource.TestCheckResourceAttr("k8sconnect_patch.test", "managed_state_projection.data.patched", "updated-value"),
					resource.TestCheckResourceAttr("k8sconnect_patch.test", "managed_state_projection.data.another", "field"),
					resource.TestCheckResourceAttr("k8sconnect_patch.test", "managed_state_projection.data.new-field", "new-value"),

					// Verify ConfigMap has updated data
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched", "updated-value"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "new-field", "new-value"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_ProjectionJSONPatch tests that managed_state_projection
// is null for JSON patches (non-SSA) since they don't provide field ownership
func TestAccPatchResource_ProjectionJSONPatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("projection-json-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("projection-json-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and ConfigMap with external field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"original": "value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Apply JSON patch and verify projection is null
			{
				Config: testAccPatchConfigProjectionJSON(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify patch resource state exists
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),

					// Verify managed_state_projection is null (not set) for JSON patches
					resource.TestCheckNoResourceAttr("k8sconnect_patch.test", "managed_state_projection.%"),

					// Verify ConfigMap has the patched data
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched", "json-patch-value"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_ProjectionMergePatch tests that managed_state_projection
// is null for merge patches (non-SSA)
func TestAccPatchResource_ProjectionMergePatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("projection-merge-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("projection-merge-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and ConfigMap
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"original": "value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Apply merge patch and verify projection is null
			{
				Config: testAccPatchConfigProjectionMerge(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify patch resource state exists
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),

					// Verify managed_state_projection is null for merge patches
					resource.TestCheckNoResourceAttr("k8sconnect_patch.test", "managed_state_projection.%"),

					// Verify ConfigMap has the patched data
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched", "merge-patch-value"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Terraform config helpers for projection tests

func testAccPatchConfigProjectionSSA(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "test_ns" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = { kubeconfig = var.raw }
}

resource "k8sconnect_patch" "test" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  # Strategic merge patch (SSA) - should show projection
  patch = <<YAML
data:
  patched: value-from-patch
  another: field
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigProjectionSSAUpdate(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "test_ns" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = { kubeconfig = var.raw }
}

resource "k8sconnect_patch" "test" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  # Updated strategic merge patch - projection should update
  patch = <<YAML
data:
  patched: updated-value
  another: field
  new-field: new-value
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigProjectionJSON(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "test_ns" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = { kubeconfig = var.raw }
}

resource "k8sconnect_patch" "test" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  # JSON patch (non-SSA) - projection should be null
  json_patch = jsonencode([
    {
      op    = "add"
      path  = "/data/patched"
      value = "json-patch-value"
    }
  ])

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigProjectionMerge(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "test_ns" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = { kubeconfig = var.raw }
}

resource "k8sconnect_patch" "test" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  # Merge patch (non-SSA) - projection should be null
  merge_patch = jsonencode({
    data = {
      patched = "merge-patch-value"
    }
  })

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}
