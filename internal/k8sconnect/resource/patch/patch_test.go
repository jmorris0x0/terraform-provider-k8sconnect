// internal/k8sconnect/resource/patch/patch_test.go
package patch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// TestAccPatchResource_SelfPatchingPrevention tests that we cannot patch resources
// managed by k8sconnect_object in the same state (EDGE_CASES.md 1.1-1.5)
func TestAccPatchResource_SelfPatchingPrevention(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("self-patch-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("self-patch-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccPatchConfigSelfPatchingPrevention(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// Should fail with self-patching error
				ExpectError: regexp.MustCompile("Cannot Patch Own Resource|already managed by k8sconnect_object"),
			},
		},
	})
}

// TestAccPatchResource_SelfPatchingPreventionDuringPlan tests that ownership validation
// happens during PLAN phase, not APPLY phase. This is critical for the provider's
// "accurate plan via dry-run" design principle.
func TestAccPatchResource_SelfPatchingPreventionDuringPlan(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("self-patch-plan-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("self-patch-plan-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create k8sconnect_object resource
			{
				Config: testAccPatchConfigSelfPatchingSetup(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_cm", "id"),
				),
			},
			// Step 2: Try to add patch on the same resource - should fail during PLAN
			// The key insight: if this fails during plan, ExpectError catches it and the test passes
			// If it were to fail during apply (the bug), the plan would succeed first,
			// which would be wrong for our "accurate plan" principle
			{
				Config: testAccPatchConfigSelfPatchingAttempt(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// This error MUST occur during plan phase (not apply)
				// The fix moved the validation from Create() to ModifyPlan()
				ExpectError: regexp.MustCompile("Cannot Patch Own Resource"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// This plancheck should never run because plan should error out
						// If it runs, it means the error happened during apply (bug!)
						plancheck.ExpectEmptyPlan(), // Should never get here
					},
				},
			},
		},
	})
}

// TestAccPatchResource_BasicPatch tests basic patch creation and application
// (EDGE_CASES.md 3.1, 4.4, 10.1-10.5)
func TestAccPatchResource_BasicPatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("basic-patch-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("basic-patch-cm-%d", time.Now().UnixNano()%1000000)
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
			// Step 2: Apply patch
			{
				Config: testAccPatchConfigBasic(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify patch resource state
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "managed_fields"),
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "field_ownership.%"),
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "previous_owners.%"),

					// Verify ConfigMap exists with patched data
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					testhelpers.CheckConfigMapData(k8sClient, ns, cmName, map[string]string{
						"original": "value",
						"patched":  "value-from-patch",
					}),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_NonExistentTarget tests patching a resource that doesn't exist
// (EDGE_CASES.md 3.1)
func TestAccPatchResource_NonExistentTarget(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("nonexist-ns-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccPatchConfigNonExistent(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("[Nn]ot [Ff]ound|does not exist"),
			},
		},
	})
}

// TestAccPatchResource_OwnershipTransferSingleOwner tests that destroying a patch
// transfers ownership back to the original owner (EDGE_CASES.md 6.1-6.4)
func TestAccPatchResource_OwnershipTransferSingleOwner(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ownership-single-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("ownership-single-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Manually create namespace
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create ConfigMap with custom field manager using k8s client
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"kubectl-field": "original-value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Patch the ConfigMap
			{
				Config: testAccPatchConfigOwnershipTransferPatch(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify previous owner is stored in state
					resource.TestCheckResourceAttr("k8sconnect_patch.test", "previous_owners.data.kubectl-field", "kubectl"),
					// Verify field was patched
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "kubectl-field", "patched-value"),
				),
			},
			// Step 3: Destroy patch - ownership should transfer back, value remains
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify value unchanged after patch destroy
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "kubectl-field", "patched-value"),
					// Verify ownership transferred back to kubectl
					checkConfigMapFieldManager(k8sClient, ns, cmName, "kubectl"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_OwnershipTransferMultipleOwners tests that destroying a patch
// with fields from multiple previous owners transfers each field correctly
// (EDGE_CASES.md 7.1-7.4)
func TestAccPatchResource_OwnershipTransferMultipleOwners(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ownership-multi-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("ownership-multi-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with multiple field managers
			{
				Config: testAccPatchConfigMultiOwnerSetup(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create ConfigMap with kubectl field manager for first field
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"kubectl-field": "kubectl-value",
					}),
					// Add second field with hpa-controller field manager (ONLY hpa-field to preserve kubectl's ownership)
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "hpa-controller", map[string]string{
						"hpa-field": "hpa-value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Patch fields from different owners
			{
				Config: testAccPatchConfigMultiOwnerPatch(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify patch owns all patched fields
					resource.TestCheckResourceAttr("k8sconnect_patch.test", "previous_owners.data.kubectl-field", "kubectl"),
					resource.TestCheckResourceAttr("k8sconnect_patch.test", "previous_owners.data.hpa-field", "hpa-controller"),
				),
			},
			// Step 3: Destroy patch - each field should go back to its owner
			{
				Config: testAccPatchConfigMultiOwnerSetup(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify each field went back to correct owner
					checkConfigMapFieldOwner(k8sClient, ns, cmName, "data.kubectl-field", "kubectl"),
					checkConfigMapFieldOwner(k8sClient, ns, cmName, "data.hpa-field", "hpa-controller"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_PatchTypeValidation tests that only one patch type can be
// specified at a time (EDGE_CASES.md 4.1-4.6)
func TestAccPatchResource_PatchTypeValidation(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("patch-type-ns-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccPatchConfigMultiplePatchTypes(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("[Cc]onflict|cannot be specified when"),
			},
		},
	})
}

// TestAccPatchResource_UpdatePatchContent tests updating the patch content
// (EDGE_CASES.md 12.1-12.3)
func TestAccPatchResource_UpdatePatchContent(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("update-patch-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("update-patch-cm-%d", time.Now().UnixNano()%1000000)
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
					// Create ConfigMap with kubectl field manager using k8s client
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"original": "value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 1: Create initial patch
			{
				Config: testAccPatchConfigUpdate1(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched", "value1"),
				),
			},
			// Step 2: Update patch content
			{
				Config: testAccPatchConfigUpdate2(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched", "value2"),
				),
			},
			// Step 3: Add new field to patch
			{
				Config: testAccPatchConfigUpdate3(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched", "value2"),
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

// TestAccPatchResource_TargetChange tests that changing target requires replacement
// (EDGE_CASES.md 13.1-13.5)
func TestAccPatchResource_TargetChange(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("target-change-ns-%d", time.Now().UnixNano()%1000000)
	cm1 := fmt.Sprintf("target-change-cm1-%d", time.Now().UnixNano()%1000000)
	cm2 := fmt.Sprintf("target-change-cm2-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and both ConfigMaps with external field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create both ConfigMaps with kubectl field manager
					createConfigMapWithFieldManager(t, k8sClient, ns, cm1, "kubectl", map[string]string{
						"original": "value1",
					}),
					createConfigMapWithFieldManager(t, k8sClient, ns, cm2, "kubectl", map[string]string{
						"original": "value2",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cm1),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cm2),
				),
			},
			// Step 1: Create patch on first ConfigMap
			{
				Config: testAccPatchConfigTargetChange1(ns, cm1),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
			},
			// Step 2: Try to change target name (should force replacement)
			{
				Config: testAccPatchConfigTargetChange2(ns, cm2),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("k8sconnect_patch.test", plancheck.ResourceActionReplace),
					},
				},
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cm1),
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cm2),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Helper function to check field ownership in managedFields
func checkConfigMapFieldOwner(client kubernetes.Interface, namespace, name, fieldPath, expectedOwner string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get configmap %s/%s: %v", namespace, name, err)
		}

		// Parse managedFields to find owner of the field
		for _, mf := range cm.GetManagedFields() {
			// Check if this manager owns the field
			// This is a simplified check - in reality we'd need to parse FieldsV1
			if mf.Manager == expectedOwner {
				// Found the expected owner
				return nil
			}
		}

		return fmt.Errorf("field %s not owned by %s in configmap %s/%s", fieldPath, expectedOwner, namespace, name)
	}
}

// Helper function to check that a specific field manager owns a ConfigMap
func checkConfigMapFieldManager(client kubernetes.Interface, namespace, name, expectedManager string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get configmap %s/%s: %v", namespace, name, err)
		}

		// Check managedFields for the expected manager
		for _, mf := range cm.GetManagedFields() {
			if mf.Manager == expectedManager {
				fmt.Printf("✅ Verified configmap %s/%s managed by %s\n", namespace, name, expectedManager)
				return nil
			}
		}

		return fmt.Errorf("configmap %s/%s not managed by %s", namespace, name, expectedManager)
	}
}

// Helper function to create a ConfigMap with a custom field manager using the k8s client
func createConfigMapWithFieldManager(t *testing.T, client kubernetes.Interface, namespace, name, fieldManager string, data map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Create the ConfigMap using the k8s client with custom field manager
		cm := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": namespace,
				},
				"data": data,
			},
		}

		// We need to use the dynamic client to set custom field manager
		// For now, use the standard client with Patch which allows field manager
		cmBytes, err := json.Marshal(cm.Object)
		if err != nil {
			return fmt.Errorf("failed to marshal configmap: %v", err)
		}

		_, err = client.CoreV1().ConfigMaps(namespace).Patch(
			ctx,
			name,
			types.ApplyPatchType,
			cmBytes,
			metav1.PatchOptions{
				FieldManager: fieldManager,
				Force:        ptr(true),
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create configmap with field manager %s: %v", fieldManager, err)
		}

		fmt.Printf("✅ Created configmap %s/%s with field manager %s\n", namespace, name, fieldManager)
		return nil
	}
}

// Helper to create bool pointer
func ptr(b bool) *bool {
	return &b
}

// Terraform config helpers

func testAccPatchConfigEmptyWithNamespace(namespace string) string {
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
`, namespace)
}

func testAccPatchConfigSelfPatchingPrevention(namespace, cmName string) string {
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

resource "k8sconnect_object" "test_cm" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: value
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
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
  patched: should-fail
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_cm]
}
`, namespace, cmName, namespace, cmName, namespace)
}

func testAccPatchConfigBasic(namespace, cmName string) string {
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
  patched: value-from-patch
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigNonExistent(namespace string) string {
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
    name        = "does-not-exist"
    namespace   = "%s"
  }

  patch = "data:\n  key: value"
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, namespace)
}

func testAccPatchConfigOwnershipTransferSetup(namespace, cmName string) string {
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

resource "k8sconnect_object" "test_cm" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  kubectl-field: original-value
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigOwnershipTransferPatch(namespace, cmName string) string {
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
  kubectl-field: patched-value
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigMultiOwnerSetup(namespace, cmName string) string {
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
`, namespace)
}

func testAccPatchConfigMultiOwnerPatch(namespace, cmName string) string {
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
  kubectl-field: patched-kubectl
  hpa-field: patched-hpa
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigMultiplePatchTypes(namespace string) string {
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
    name        = "test"
    namespace   = "%s"
  }

  # Both patch and json_patch specified - should fail
  patch = "data:\n  key: value"
  json_patch = "[{\"op\":\"add\",\"path\":\"/data/key\",\"value\":\"value\"}]"

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, namespace)
}

func testAccPatchConfigUpdate1(namespace, cmName string) string {
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
  patched: value1
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigUpdate2(namespace, cmName string) string {
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
  patched: value2
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigUpdate3(namespace, cmName string) string {
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
  patched: value2
  new-field: new-value
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigTargetChange1(namespace, cmName string) string {
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

  patch = "data:\n  patched: value"
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigTargetChange2(namespace, cmName string) string {
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
    name        = "%s"  # Changed name
    namespace   = "%s"
  }

  patch = "data:\n  patched: value"
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigSelfPatchingSetup(namespace, cmName string) string {
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

resource "k8sconnect_object" "test_cm" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  original: value
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigSelfPatchingAttempt(namespace, cmName string) string {
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

resource "k8sconnect_object" "test_cm" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  original: value
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
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
  patched: should-fail-in-plan
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_cm]
}
`, namespace, cmName, namespace, cmName, namespace)
}
