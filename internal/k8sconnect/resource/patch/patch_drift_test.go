package patch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// TestAccPatchResource_DriftCorrection_StrategicMerge tests that strategic merge patches
// re-apply and correct drift when someone externally modifies a patched value
func TestAccPatchResource_DriftCorrection_StrategicMerge(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("drift-sm-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("drift-sm-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap with external field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"original": "original-value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 1: Apply patch
			{
				Config: testAccPatchConfigDriftStrategicMerge(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched-key", "patched-value"),
				),
			},
			// Step 2: Manually modify the patched field to simulate drift
			{
				Config: testAccPatchConfigDriftStrategicMerge(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Modify the patched field externally
					modifyConfigMapData(t, k8sClient, ns, cmName, "patched-key", "manually-changed-value"),
					// Verify drift exists
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched-key", "manually-changed-value"),
				),
			},
			// Step 3: Re-apply (terraform apply) should correct the drift
			{
				Config: testAccPatchConfigDriftStrategicMerge(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify patch was re-applied and drift was corrected
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched-key", "patched-value"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_DriftCorrection_JSONPatch tests that JSON patches
// re-apply and correct drift when someone externally modifies a patched value
func TestAccPatchResource_DriftCorrection_JSONPatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("drift-json-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("drift-json-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap with external field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"original": "original-value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 1: Apply JSON patch
			{
				Config: testAccPatchConfigDriftJSONPatch(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "json-patched", "json-value"),
				),
			},
			// Step 2: Manually modify the patched field to simulate drift
			{
				Config: testAccPatchConfigDriftJSONPatch(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Modify the patched field externally
					modifyConfigMapData(t, k8sClient, ns, cmName, "json-patched", "manually-modified"),
					// Verify drift exists
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "json-patched", "manually-modified"),
				),
			},
			// Step 3: Re-apply (terraform apply) should correct the drift
			{
				Config: testAccPatchConfigDriftJSONPatch(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify patch was re-applied and drift was corrected
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "json-patched", "json-value"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_DriftCorrection_MergePatch tests that merge patches
// re-apply and correct drift when someone externally modifies a patched value
func TestAccPatchResource_DriftCorrection_MergePatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("drift-merge-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("drift-merge-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap with external field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"original": "original-value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 1: Apply merge patch
			{
				Config: testAccPatchConfigDriftMergePatch(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "merge-patched", "merge-value"),
				),
			},
			// Step 2: Manually modify the patched field to simulate drift
			{
				Config: testAccPatchConfigDriftMergePatch(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Modify the patched field externally
					modifyConfigMapData(t, k8sClient, ns, cmName, "merge-patched", "drift-value"),
					// Verify drift exists
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "merge-patched", "drift-value"),
				),
			},
			// Step 3: Re-apply (terraform apply) should correct the drift
			{
				Config: testAccPatchConfigDriftMergePatch(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify patch was re-applied and drift was corrected
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "merge-patched", "merge-value"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Helper function to modify ConfigMap data externally (simulating manual change/drift)
func modifyConfigMapData(t *testing.T, client kubernetes.Interface, namespace, name, key, value string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Get current ConfigMap
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get configmap %s/%s: %v", namespace, name, err)
		}

		// Modify the data
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data[key] = value

		// Create unstructured for SSA
		unstructuredCM := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": namespace,
				},
				"data": cm.Data,
			},
		}

		cmBytes, err := json.Marshal(unstructuredCM.Object)
		if err != nil {
			return fmt.Errorf("failed to marshal configmap: %v", err)
		}

		// Apply with external field manager to simulate manual change
		_, err = client.CoreV1().ConfigMaps(namespace).Patch(
			ctx,
			name,
			types.ApplyPatchType,
			cmBytes,
			metav1.PatchOptions{
				FieldManager: "manual-operator", // Simulate external modification
				Force:        ptr(true),
			},
		)
		if err != nil {
			return fmt.Errorf("failed to modify configmap data: %v", err)
		}

		fmt.Printf("✅ Externally modified configmap %s/%s: %s=%s (simulating drift)\n", namespace, name, key, value)
		return nil
	}
}

// Terraform config helpers for drift tests

func testAccPatchConfigDriftStrategicMerge(namespace, cmName string) string {
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
  cluster_connection = { kubeconfig = var.raw }
}

resource "k8sconnect_patch" "test" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
data:
  patched-key: patched-value
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigDriftJSONPatch(namespace, cmName string) string {
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
  cluster_connection = { kubeconfig = var.raw }
}

resource "k8sconnect_patch" "test" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  json_patch = jsonencode([
    {
      op    = "add"
      path  = "/data/json-patched"
      value = "json-value"
    }
  ])

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigDriftMergePatch(namespace, cmName string) string {
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
  cluster_connection = { kubeconfig = var.raw }
}

resource "k8sconnect_patch" "test" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  merge_patch = jsonencode({
    data = {
      "merge-patched" = "merge-value"
    }
  })

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}
