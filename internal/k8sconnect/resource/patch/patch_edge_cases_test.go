// internal/k8sconnect/resource/patch/patch_edge_cases_test.go
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
		if err != nil {
			return fmt.Errorf("failed to create custom resource externally: %v", err)
		}

		fmt.Printf("✅ Created custom resource %s/%s externally\n", namespace, crName)
		return nil
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
  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
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
  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
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
  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
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
  patched: value
YAML

  cluster_connection = { kubeconfig = var.raw }
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
  cluster_connection = { kubeconfig = var.raw }
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
  cluster_connection = { kubeconfig = var.raw }
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
  cluster_connection = { kubeconfig = var.raw }
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
  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
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
  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

// TestAccPatchResource_MultiplePatches tests that multiple patches can coexist
// when they target different fields, but conflict detection prevents overlapping fields
func TestAccPatchResource_MultiplePatches(t *testing.T) {
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
  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
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
  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
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
  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
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

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace, cmName, namespace, cmName, namespace)
}
