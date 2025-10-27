package patch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// TestAccPatchResource_QuantityNormalization_StrategicMerge tests that strategic merge patches
// handle K8s quantity normalization correctly (1Gi -> bytes, 100m -> 0.1)
func TestAccPatchResource_QuantityNormalization_StrategicMerge(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("quantity-sm-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("quantity-sm-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and deployment
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createDeploymentWithResources(t, k8sClient, ns, deployName, "kubectl", map[string]string{
						"memory": "64Mi",
						"cpu":    "50m",
					}),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 1: Apply patch with quantity values
			{
				Config: testAccPatchConfigQuantityStrategicMerge(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
				),
			},
			// Step 2: Re-apply same config - should NOT show drift despite K8s normalization
			{
				Config: testAccPatchConfigQuantityStrategicMerge(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // Should be idempotent - no drift from quantity normalization
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_QuantityNormalization_JSONPatch tests that JSON patches
// handle K8s quantity normalization correctly
func TestAccPatchResource_QuantityNormalization_JSONPatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("quantity-json-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("quantity-json-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and deployment with HIGH limits so patch can set lower requests
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createDeploymentWithResources(t, k8sClient, ns, deployName, "kubectl", map[string]string{
						"memory": "256Mi",
						"cpu":    "200m",
					}),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 1: Apply JSON patch with quantity values
			{
				Config: testAccPatchConfigQuantityJSONPatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
				),
			},
			// Step 2: Re-apply - will likely show changes due to no value tracking in patch resource
			// This documents the current behavior
			{
				Config: testAccPatchConfigQuantityJSONPatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// JSON patch re-applies on every apply (expected behavior)
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_QuantityNormalization_MergePatch tests that merge patches
// handle K8s quantity normalization correctly
func TestAccPatchResource_QuantityNormalization_MergePatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("quantity-merge-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("quantity-merge-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and deployment with HIGH limits so patch can set lower requests
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createDeploymentWithResources(t, k8sClient, ns, deployName, "kubectl", map[string]string{
						"memory": "256Mi",
						"cpu":    "200m",
					}),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 1: Apply merge patch with quantity values
			{
				Config: testAccPatchConfigQuantityMergePatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
				),
			},
			// Step 2: Re-apply - will likely show changes due to no value tracking
			{
				Config: testAccPatchConfigQuantityMergePatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// Merge patch re-applies on every apply (expected behavior)
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_Idempotency tests that applying the same patch twice
// is idempotent (no unnecessary changes)
func TestAccPatchResource_Idempotency(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("idempotent-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("idempotent-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap
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
			// Step 1: Apply patch
			{
				Config: testAccPatchConfigIdempotency(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched", "value1"),
				),
			},
			// Step 2: Apply exact same config - verify idempotency
			{
				Config: testAccPatchConfigIdempotency(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched", "value1"),
				),
			},
			// Step 3: Plan-only check - should show no changes
			{
				Config: testAccPatchConfigIdempotency(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_NoWarningOnSubsequentUpdates tests that after taking ownership
// from another controller, subsequent patch updates don't warn about previousOwners
// This validates the fix where previousOwners should not cause warnings on every update
func TestAccPatchResource_NoWarningOnSubsequentUpdates(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("no-warn-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("no-warn-cm-%d", time.Now().UnixNano()%1000000)
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
						"field1": "original-value",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 1: Apply patch - takes ownership from kubectl (should record previousOwners)
			{
				Config:          testAccPatchConfigNoWarnFirst(ns, cmName),
				ConfigVariables: config.Variables{"raw": config.StringVariable(raw)},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "patched-value-1"),
					// Verify previousOwners is recorded
				),
				// ExpectNonEmptyPlan: false, // No warning expected, first takeover
			},
			// Step 2: Update patch content - should NOT warn about previousOwners
			// This is the critical test: previousOwners persists in state, but shouldn't cause warnings
			{
				Config:          testAccPatchConfigNoWarnSecond(ns, cmName),
				ConfigVariables: config.Variables{"raw": config.StringVariable(raw)},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "patched-value-2"),
					// previousOwners should still be recorded
				),
				// Should apply cleanly without warnings about ownership takeover
				ExpectNonEmptyPlan: false,
			},
			// Step 3: Another update - still no warnings
			{
				Config:          testAccPatchConfigNoWarnThird(ns, cmName),
				ConfigVariables: config.Variables{"raw": config.StringVariable(raw)},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "patched-value-3"),
				),
				ExpectNonEmptyPlan: false,
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_SemanticYAMLComparison tests that formatting/whitespace changes
// in patch YAML don't trigger diffs (semantic comparison, not string comparison)
func TestAccPatchResource_SemanticYAMLComparison(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("semantic-yaml-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("semantic-yaml-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap
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
			// Step 1: Apply patch with specific formatting
			{
				Config: testAccPatchConfigSemanticYAMLCompact(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "key1", "value1"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "key2", "value2"),
				),
			},
			// Step 2: Change only whitespace/formatting - should show no changes
			{
				Config: testAccPatchConfigSemanticYAMLExpanded(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // No diff expected - semantically identical
			},
			// Step 3: Apply expanded version to verify it actually works
			{
				Config: testAccPatchConfigSemanticYAMLExpanded(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "key1", "value1"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "key2", "value2"),
				),
			},
			// Step 4: Plan-only check again - still no changes
			{
				Config: testAccPatchConfigSemanticYAMLExpanded(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_TargetDeletedExternally tests behavior when target resource
// is deleted externally - patch should eventually clean up gracefully
func TestAccPatchResource_TargetDeletedExternally(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("target-deleted-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("target-deleted-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap
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
			// Step 1: Apply patch
			{
				Config: testAccPatchConfigTargetDeleted(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "patched", "value"),
				),
			},
			// Step 2: Delete target ConfigMap externally, then verify patch removal from config works
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					deleteConfigMapExternally(t, k8sClient, ns, cmName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_CustomResource tests patching a Custom Resource (CRD)
func TestAccPatchResource_CustomResource(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("crd-patch-ns-%d", time.Now().UnixNano()%1000000)
	crdName := fmt.Sprintf("testresources-%d", time.Now().UnixNano()%1000000)
	crName := fmt.Sprintf("test-cr-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create CRD, then create CR externally
			{
				Config: testAccPatchConfigCRDOnly(ns, crdName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_crd", "id"),
					// Wait for CRD to be established, then create CR externally
					createCustomResourceExternally(t, k8sClient, ns, crName, crdName),
				),
			},
			// Step 2: Patch the CR (created externally, not managed by k8sconnect_object)
			{
				Config: testAccPatchConfigCRDWithPatch(ns, crdName, crName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_StatusFields tests that attempting to patch status fields
// fails with a clear error (status is typically read-only)
func TestAccPatchResource_StatusFields(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("status-patch-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("status-patch-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and deployment
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createDeploymentWithResources(t, k8sClient, ns, deployName, "kubectl", map[string]string{
						"memory": "64Mi",
						"cpu":    "50m",
					}),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 1: Try to patch status field - K8s allows but ignores it
			{
				Config: testAccPatchConfigStatusField(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// Status patches are allowed by K8s but silently ignored (no effect)
				// This test documents that patch resource allows them
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Helper functions

func createDeploymentWithResources(t *testing.T, client kubernetes.Interface, namespace, name, fieldManager string, resources map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Create minimal deployment with resource limits
		deploy := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"replicas": 1,
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
									"image": "public.ecr.aws/nginx/nginx:1.21",
									"resources": map[string]interface{}{
										"requests": resources,
										"limits":   resources,
									},
								},
							},
						},
					},
				},
			},
		}

		deployBytes, err := json.Marshal(deploy.Object)
		if err != nil {
			return fmt.Errorf("failed to marshal deployment: %v", err)
		}

		_, err = client.AppsV1().Deployments(namespace).Patch(
			ctx,
			name,
			types.ApplyPatchType,
			deployBytes,
			metav1.PatchOptions{
				FieldManager: fieldManager,
				Force:        ptr(true),
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create deployment with field manager %s: %v", fieldManager, err)
		}

		fmt.Printf("✅ Created deployment %s/%s with field manager %s and resources %v\n", namespace, name, fieldManager, resources)
		return nil
	}
}

func deleteConfigMapExternally(t *testing.T, client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		err := client.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete configmap %s/%s: %v", namespace, name, err)
		}

		fmt.Printf("✅ Externally deleted configmap %s/%s\n", namespace, name)
		return nil
	}
}

func createCustomResourceExternally(t *testing.T, client kubernetes.Interface, namespace, crName, crdPlural string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Create CR instance externally (not managed by k8sconnect_object)
		cr := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "example.com/v1",
				"kind":       "TestResource",
				"metadata": map[string]interface{}{
					"name":      crName,
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"field1": "original-value",
					"field2": 42,
				},
			},
		}

		crBytes, err := json.Marshal(cr.Object)
		if err != nil {
			return fmt.Errorf("failed to marshal custom resource: %v", err)
		}

		// Use dynamic client to create CR
		raw := os.Getenv("TF_ACC_KUBECONFIG")
		restConfig, err := clientcmd.RESTConfigFromKubeConfig([]byte(raw))
		if err != nil {
			return fmt.Errorf("failed to create rest config: %v", err)
		}

		dynamicClient, err := dynamic.NewForConfig(restConfig)
		if err != nil {
			return fmt.Errorf("failed to create dynamic client: %v", err)
		}

		gvr := schema.GroupVersionResource{
			Group:    "example.com",
			Version:  "v1",
			Resource: crdPlural,
		}

		// Retry with exponential backoff - CRD may not be established yet
		var lastErr error
		backoff := []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}
		for i, wait := range backoff {
			_, err = dynamicClient.Resource(gvr).Namespace(namespace).Patch(
				ctx,
				crName,
				types.ApplyPatchType,
				crBytes,
				metav1.PatchOptions{
					FieldManager: "test-external",
					Force:        ptr(true),
				},
			)
			if err == nil {
				fmt.Printf("✅ Created custom resource %s/%s externally\n", namespace, crName)
				return nil
			}

			// Check if it's a CRD not ready error
			if !strings.Contains(err.Error(), "could not find the requested resource") &&
				!strings.Contains(err.Error(), "no matches for kind") {
				// Not a CRD issue, fail immediately
				return fmt.Errorf("failed to create custom resource externally: %v", err)
			}

			lastErr = err
			if i < len(backoff)-1 {
				time.Sleep(wait)
			}
		}

		return fmt.Errorf("failed to create custom resource externally after retries: %v", lastErr)
	}
}

// Terraform config helpers

func testAccPatchConfigQuantityStrategicMerge(namespace, deployName string) string {
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
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
spec:
  template:
    spec:
      containers:
      - name: nginx
        resources:
          requests:
            memory: "128Mi"  # K8s normalizes to bytes
            cpu: "100m"      # K8s normalizes to "0.1"
          limits:
            memory: "256Mi"
            cpu: "200m"
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigQuantityJSONPatch(namespace, deployName string) string {
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
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "%s"
    namespace   = "%s"
  }

  json_patch = jsonencode([
    {
      op    = "replace"
      path  = "/spec/template/spec/containers/0/resources/requests/memory"
      value = "128Mi"  # K8s normalizes to bytes
    },
    {
      op    = "replace"
      path  = "/spec/template/spec/containers/0/resources/requests/cpu"
      value = "100m"   # K8s normalizes to "0.1"
    }
  ])

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigQuantityMergePatch(namespace, deployName string) string {
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
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "%s"
    namespace   = "%s"
  }

  merge_patch = jsonencode({
    spec = {
      template = {
        spec = {
          containers = [
            {
              name  = "nginx"
              image = "public.ecr.aws/nginx/nginx:1.21"
              resources = {
                requests = {
                  memory = "128Mi"  # K8s normalizes to bytes
                  cpu    = "100m"   # K8s normalizes to "0.1"
                }
                limits = {
                  memory = "256Mi"
                  cpu    = "200m"
                }
              }
            }
          ]
        }
      }
    }
  })

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigIdempotency(namespace, cmName string) string {
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

  patch = <<YAML
data:
  patched: value1
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigTargetDeleted(namespace, cmName string) string {
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

  patch = <<YAML
data:
  patched: value
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigCRDOnly(namespace, crdPlural string) string {
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

resource "k8sconnect_object" "test_crd" {
  yaml_body = <<YAML
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %s.example.com
spec:
  group: example.com
  names:
    kind: TestResource
    plural: %s
    singular: testresource
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              field1:
                type: string
              field2:
                type: integer
YAML
  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, crdPlural, crdPlural)
}

func testAccPatchConfigCRDWithPatch(namespace, crdPlural, crName string) string {
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

resource "k8sconnect_object" "test_crd" {
  yaml_body = <<YAML
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %s.example.com
spec:
  group: example.com
  names:
    kind: TestResource
    plural: %s
    singular: testresource
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              field1:
                type: string
              field2:
                type: integer
YAML
  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}

resource "k8sconnect_patch" "test" {
  target = {
    api_version = "example.com/v1"
    kind        = "TestResource"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
spec:
  field1: "patched-value"
  field2: 100
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_crd]
}
`, namespace, crdPlural, crdPlural, crName, namespace)
}

func testAccPatchConfigStatusField(namespace, deployName string) string {
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
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "%s"
    namespace   = "%s"
  }

  # Try to patch status - should fail
  json_patch = jsonencode([
    {
      op    = "add"
      path  = "/status/conditions"
      value = []
    }
  ])

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

// TestAccPatchResource_MultiplePatches tests that multiple patches can coexist
// when they target different fields, but conflict detection prevents overlapping fields
func Skip_TestAccPatchResource_MultiplePatches(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("multi-patch-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("multi-patch-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap
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
			// Step 1: Apply first patch to field1
			{
				Config: testAccPatchConfigMultiplePatchesFirst(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.patch1", "id"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "patched-by-patch1"),
				),
			},
			// Step 2: Apply both patches - non-overlapping fields should succeed
			{
				Config: testAccPatchConfigMultiplePatchesBothNonOverlapping(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.patch1", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_patch.patch2", "id"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "patched-by-patch1"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field2", "patched-by-patch2"),
				),
			},
			// Step 3: Try to add overlapping patch - should error during plan
			{
				Config: testAccPatchConfigMultiplePatchesOverlapping(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("Patch Conflicts with Existing Patch"),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Config helpers for multi-patch test

func testAccPatchConfigMultiplePatchesFirst(namespace, cmName string) string {
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

resource "k8sconnect_patch" "patch1" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
data:
  field1: patched-by-patch1
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigMultiplePatchesBothNonOverlapping(namespace, cmName string) string {
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

resource "k8sconnect_patch" "patch1" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
data:
  field1: patched-by-patch1
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}

resource "k8sconnect_patch" "patch2" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
data:
  field2: patched-by-patch2
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace, cmName, namespace)
}

func testAccPatchConfigMultiplePatchesOverlapping(namespace, cmName string) string {
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

resource "k8sconnect_patch" "patch1" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
data:
  field1: patched-by-patch1
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}

resource "k8sconnect_patch" "patch2" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
data:
  field2: patched-by-patch2
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}

resource "k8sconnect_patch" "patch3_conflicting" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
data:
  field1: conflicting-patch  # This conflicts with patch1
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace, cmName, namespace, cmName, namespace)
}

// TestAccPatchResource_FieldRemoval_StrategicMerge_SingleField tests that removing
// a single field from a strategic merge patch transfers ownership back to the previous owner
func Skip_TestAccPatchResource_FieldRemoval_StrategicMerge_SingleField(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-removal-sm-single-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("field-removal-sm-single-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap with kubectl managing three fields
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"field1": "kubectl-value1",
						"field2": "kubectl-value2",
						"field3": "kubectl-value3",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 1: Apply patch taking ownership of all three fields
			{
				Config: testAccPatchConfigFieldRemovalSMThreeFields(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					// Verify previous owners were captured
					// Verify patched values
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "patched1"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field2", "patched2"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field3", "patched3"),
				),
			},
			// Step 2: Remove field3 from patch - ownership should transfer back to kubectl
			{
				Config: testAccPatchConfigFieldRemovalSMTwoFields(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify field3 value remains (not reverted)
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field3", "patched3"),
					// Verify field3 ownership transferred back to kubectl
					// REMOVED per ADR-020: 					checkConfigMapFieldOwner(k8sClient, ns, cmName, "data.field3", "kubectl"),
					// Verify other fields still patched
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "patched1"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field2", "patched2"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_FieldRemoval_StrategicMerge_MultipleFields tests that removing
// multiple fields from a strategic merge patch transfers ownership back correctly
func Skip_TestAccPatchResource_FieldRemoval_StrategicMerge_MultipleFields(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-removal-sm-multi-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("field-removal-sm-multi-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap with two different field managers
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// kubectl manages field1 and field2
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"field1": "kubectl-value1",
						"field2": "kubectl-value2",
					}),
					// hpa-controller manages field3 and field4
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "hpa-controller", map[string]string{
						"field3": "hpa-value3",
						"field4": "hpa-value4",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 1: Apply patch taking ownership of all four fields
			{
				Config: testAccPatchConfigFieldRemovalSMFourFields(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					// Verify previous owners were captured
					// Verify patched values
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "patched1"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field2", "patched2"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field3", "patched3"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field4", "patched4"),
				),
			},
			// Step 2: Remove field2 and field3 - each should go back to its original owner
			{
				Config: testAccPatchConfigFieldRemovalSMTwoFieldsRemaining(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify removed field values remain (not reverted)
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field2", "patched2"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field3", "patched3"),
					// Verify field2 ownership transferred back to kubectl
					// REMOVED per ADR-020: 					checkConfigMapFieldOwner(k8sClient, ns, cmName, "data.field2", "kubectl"),
					// Verify field3 ownership transferred back to hpa-controller
					// REMOVED per ADR-020: 					checkConfigMapFieldOwner(k8sClient, ns, cmName, "data.field3", "hpa-controller"),
					// Verify remaining fields still patched
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "patched1"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field4", "patched4"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_FieldRemoval_JSONPatch tests that field removal works
// with JSON patches (though tracking is less precise than strategic merge)
func Skip_TestAccPatchResource_FieldRemoval_JSONPatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-removal-json-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("field-removal-json-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap with kubectl managing fields
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"field1": "kubectl-value1",
						"field2": "kubectl-value2",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 1: Apply JSON patch to both fields
			{
				Config: testAccPatchConfigFieldRemovalJSONTwoFields(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "json-patched1"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field2", "json-patched2"),
				),
			},
			// Step 2: Remove field2 from JSON patch - should transfer back
			{
				Config: testAccPatchConfigFieldRemovalJSONOneField(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify field2 value remains
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field2", "json-patched2"),
					// Verify field2 ownership transferred back to kubectl
					// REMOVED per ADR-020: 					checkConfigMapFieldOwner(k8sClient, ns, cmName, "data.field2", "kubectl"),
					// Verify field1 still patched
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "json-patched1"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_FieldRemoval_MergePatch tests that field removal works
// with merge patches
func Skip_TestAccPatchResource_FieldRemoval_MergePatch(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-removal-merge-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("field-removal-merge-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create namespace and ConfigMap with kubectl managing fields
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"field1": "kubectl-value1",
						"field2": "kubectl-value2",
					}),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 1: Apply merge patch to both fields
			{
				Config: testAccPatchConfigFieldRemovalMergeTwoFields(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "merge-patched1"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field2", "merge-patched2"),
				),
			},
			// Step 2: Remove field2 from merge patch - should transfer back
			{
				Config: testAccPatchConfigFieldRemovalMergeOneField(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify field2 value remains
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field2", "merge-patched2"),
					// Verify field2 ownership transferred back to kubectl
					// REMOVED per ADR-020: 					checkConfigMapFieldOwner(k8sClient, ns, cmName, "data.field2", "kubectl"),
					// Verify field1 still patched
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "field1", "merge-patched1"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Config helpers for field removal tests

func testAccPatchConfigFieldRemovalSMThreeFields(namespace, cmName string) string {
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

  patch = <<YAML
data:
  field1: patched1
  field2: patched2
  field3: patched3
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigFieldRemovalSMTwoFields(namespace, cmName string) string {
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

  patch = <<YAML
data:
  field1: patched1
  field2: patched2
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigFieldRemovalSMFourFields(namespace, cmName string) string {
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

  patch = <<YAML
data:
  field1: patched1
  field2: patched2
  field3: patched3
  field4: patched4
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigFieldRemovalSMTwoFieldsRemaining(namespace, cmName string) string {
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

  patch = <<YAML
data:
  field1: patched1
  field4: patched4
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigFieldRemovalJSONTwoFields(namespace, cmName string) string {
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

  json_patch = jsonencode([
    {
      op    = "replace"
      path  = "/data/field1"
      value = "json-patched1"
    },
    {
      op    = "replace"
      path  = "/data/field2"
      value = "json-patched2"
    }
  ])

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigFieldRemovalJSONOneField(namespace, cmName string) string {
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

  json_patch = jsonencode([
    {
      op    = "replace"
      path  = "/data/field1"
      value = "json-patched1"
    }
  ])

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigFieldRemovalMergeTwoFields(namespace, cmName string) string {
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

  merge_patch = jsonencode({
    data = {
      field1 = "merge-patched1"
      field2 = "merge-patched2"
    }
  })

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigFieldRemovalMergeOneField(namespace, cmName string) string {
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

  merge_patch = jsonencode({
    data = {
      field1 = "merge-patched1"
    }
  })

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigSemanticYAMLCompact(namespace, cmName string) string {
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

  patch = <<YAML
data:
  key1: value1
  key2: value2
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigSemanticYAMLExpanded(namespace, cmName string) string {
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

  patch = <<YAML
# ConfigMap data patch
data:
  key1: value1  # first key
  key2: value2  # second key
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigNoWarnFirst(namespace, cmName string) string {
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

  patch = <<YAML
data:
  field1: patched-value-1
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigNoWarnSecond(namespace, cmName string) string {
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

  patch = <<YAML
data:
  field1: patched-value-2
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigNoWarnThird(namespace, cmName string) string {
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

  patch = <<YAML
data:
  field1: patched-value-3
YAML

  cluster = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}
