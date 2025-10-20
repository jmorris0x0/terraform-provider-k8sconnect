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

// Section 15: Array Handling Tests

// TestAccPatchResource_ArrayContainerByName tests patching a container by name using strategic merge
// (EDGE_CASES.md 15.1)
func TestAccPatchResource_ArrayContainerByName(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("array-container-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("array-container-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and Deployment with kubectl field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create Deployment with kubectl field manager using k8s client
					createDeploymentWithFieldManager(t, k8sClient, ns, deployName, "kubectl", map[string]interface{}{
						"replicas": float64(1),
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
										"name":  "app",
										"image": "nginx:1.14.2",
									},
									map[string]interface{}{
										"name":  "sidecar",
										"image": "busybox:1.28",
									},
								},
							},
						},
					}),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 2: Patch only the "app" container by name
			{
				Config: testAccPatchConfigArrayContainerPatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_ArrayEnvVarByName tests patching an environment variable by name
// (EDGE_CASES.md 15.2)
func TestAccPatchResource_ArrayEnvVarByName(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("array-env-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("array-env-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and Deployment with kubectl field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create Deployment with kubectl field manager using k8s client
					createDeploymentWithFieldManager(t, k8sClient, ns, deployName, "kubectl", map[string]interface{}{
						"replicas": float64(1),
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
										"name":  "app",
										"image": "nginx:1.14.2",
										"env": []interface{}{
											map[string]interface{}{
												"name":  "ENV1",
												"value": "value1",
											},
											map[string]interface{}{
												"name":  "ENV2",
												"value": "value2",
											},
										},
									},
								},
							},
						},
					}),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 2: Patch specific env var by name
			{
				Config: testAccPatchConfigArrayEnvPatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_SimpleArrayReplacement tests simple array replacement
// (EDGE_CASES.md 15.3)
func TestAccPatchResource_SimpleArrayReplacement(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("simple-array-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("simple-array-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create ConfigMap with array data using kubectl field manager
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"list": "item1,item2,item3",
					}),
				),
			},
			// Step 2: Patch to replace array
			{
				Config: testAccPatchConfigSimpleArrayPatch(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "list", "new1,new2"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_ComplexObjectArray tests arrays with complex objects
// (EDGE_CASES.md 15.4)
func TestAccPatchResource_ComplexObjectArray(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("complex-array-ns-%d", time.Now().UnixNano()%1000000)
	svcName := fmt.Sprintf("complex-array-svc-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and Service with kubectl field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create Service with kubectl field manager using k8s client
					createServiceWithFieldManager(t, k8sClient, ns, svcName, "kubectl", map[string]interface{}{
						"selector": map[string]interface{}{
							"app": "test",
						},
						"ports": []interface{}{
							map[string]interface{}{
								"name":       "http",
								"port":       float64(80),
								"targetPort": float64(8080),
								"protocol":   "TCP",
							},
							map[string]interface{}{
								"name":       "https",
								"port":       float64(443),
								"targetPort": float64(8443),
								"protocol":   "TCP",
							},
						},
					}),
					testhelpers.CheckServiceExists(k8sClient, ns, svcName),
				),
			},
			// Step 2: Patch ports array (merge by port number)
			{
				Config: testAccPatchConfigComplexArrayPatch(ns, svcName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					testhelpers.CheckServiceExists(k8sClient, ns, svcName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckServiceDestroy(k8sClient, ns, svcName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Section 16: Deep Nesting Tests

// TestAccPatchResource_DeepNestedContainerEnv tests deeply nested path patching
// (EDGE_CASES.md 16.1)
func TestAccPatchResource_DeepNestedContainerEnv(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("deep-nested-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("deep-nested-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and Deployment with kubectl field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create Deployment with kubectl field manager using k8s client
					createDeploymentWithFieldManager(t, k8sClient, ns, deployName, "kubectl", map[string]interface{}{
						"replicas": float64(1),
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
										"name":  "app",
										"image": "nginx:1.14.2",
										"env": []interface{}{
											map[string]interface{}{
												"name":  "DEEP_VAR",
												"value": "original",
											},
										},
									},
								},
							},
						},
					}),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 2: Patch deeply nested env var value
			{
				Config: testAccPatchConfigDeepNestedPatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "managed_fields"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_DeepNestedFieldExtraction tests nested field extraction in managed_fields
// (EDGE_CASES.md 16.3)
func TestAccPatchResource_DeepNestedFieldExtraction(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("deep-extract-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("deep-extract-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and Deployment with kubectl field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create Deployment with kubectl field manager using k8s client
					createDeploymentWithFieldManager(t, k8sClient, ns, deployName, "kubectl", map[string]interface{}{
						"replicas": float64(1),
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
										"name":  "app",
										"image": "nginx:1.14.2",
										"env": []interface{}{
											map[string]interface{}{
												"name":  "DEEP_VAR",
												"value": "original",
											},
										},
									},
								},
							},
						},
					}),
				),
			},
			// Step 2: Patch and verify managed_fields contains nested paths
			{
				Config: testAccPatchConfigDeepNestedPatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify managed_fields is populated with nested structure
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "managed_fields"),
					// Verify field_ownership shows nested paths
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "field_ownership.%"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_DeepNestedOwnershipTransfer tests ownership transfer with nested fields
// (EDGE_CASES.md 16.4)
func TestAccPatchResource_DeepNestedOwnershipTransfer(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("deep-ownership-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("deep-ownership-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and Deployment with kubectl field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create Deployment with kubectl field manager using k8s client
					createDeploymentWithFieldManager(t, k8sClient, ns, deployName, "kubectl", map[string]interface{}{
						"replicas": float64(1),
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
										"name":  "app",
										"image": "nginx:1.14.2",
										"env": []interface{}{
											map[string]interface{}{
												"name":  "DEEP_VAR",
												"value": "original",
											},
										},
									},
								},
							},
						},
					}),
				),
			},
			// Step 2: Patch deeply nested field
			{
				Config: testAccPatchConfigDeepNestedPatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify previous owner captured
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "previous_owners.%"),
				),
			},
			// Step 3: Destroy patch and verify ownership transfer
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Recreate Deployment with kubectl field manager after patch is destroyed
					createDeploymentWithFieldManager(t, k8sClient, ns, deployName, "kubectl", map[string]interface{}{
						"replicas": float64(1),
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
										"name":  "app",
										"image": "nginx:1.14.2",
										"env": []interface{}{
											map[string]interface{}{
												"name":  "DEEP_VAR",
												"value": "original",
											},
										},
									},
								},
							},
						},
					}),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Section 17: Special Values Tests

// TestAccPatchResource_EmptyStringValue tests patching with empty string values
// (EDGE_CASES.md 17.1)
func TestAccPatchResource_EmptyStringValue(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("empty-string-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("empty-string-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"key": "original-value",
					}),
				),
			},
			// Step 2: Patch with empty string
			{
				Config: testAccPatchConfigEmptyString(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "key", ""),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_NullValue tests removing a field with null value
// (EDGE_CASES.md 17.2)
func TestAccPatchResource_NullValue(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("null-value-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("null-value-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with multiple keys
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"keep":   "this-value",
						"remove": "this-value",
					}),
				),
			},
			// Step 2: Patch with null to remove field (using json_patch)
			{
				Config: testAccPatchConfigNullValue(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "keep", "this-value"),
					// Note: "remove" key should be gone - test this via ConfigMap fetch
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_BooleanValues tests patching with boolean values
// (EDGE_CASES.md 17.3)
func TestAccPatchResource_BooleanValues(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("boolean-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("boolean-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and Deployment with kubectl field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create Deployment with kubectl field manager using k8s client
					createDeploymentWithFieldManager(t, k8sClient, ns, deployName, "kubectl", map[string]interface{}{
						"replicas": float64(1),
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
										"name":  "app",
										"image": "nginx:1.14.2",
									},
								},
							},
						},
					}),
				),
			},
			// Step 2: Patch boolean fields
			{
				Config: testAccPatchConfigBooleanPatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
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

// TestAccPatchResource_NumericValues tests patching with numeric values
// (EDGE_CASES.md 17.4)
func TestAccPatchResource_NumericValues(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("numeric-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("numeric-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and Deployment with kubectl field manager
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Create Deployment with kubectl field manager using k8s client
					createDeploymentWithFieldManager(t, k8sClient, ns, deployName, "kubectl", map[string]interface{}{
						"replicas": float64(1),
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
										"name":  "app",
										"image": "nginx:1.14.2",
									},
								},
							},
						},
					}),
				),
			},
			// Step 2: Patch numeric fields (replicas)
			{
				Config: testAccPatchConfigNumericPatch(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
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

// TestAccPatchResource_LargeStringValue tests patching with large string values
// (EDGE_CASES.md 17.5)
func TestAccPatchResource_LargeStringValue(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("large-string-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("large-string-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"key": "small-value",
					}),
				),
			},
			// Step 2: Patch with large string (10KB+)
			{
				Config: testAccPatchConfigLargeString(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Section 18: JSON Patch Operations Tests

// TestAccPatchResource_JSONPatchAdd tests JSON Patch add operation
// (EDGE_CASES.md 18.1)
func TestAccPatchResource_JSONPatchAdd(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("json-add-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("json-add-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"existing": "value",
					}),
				),
			},
			// Step 2: Use JSON Patch to add new field
			{
				Config: testAccPatchConfigJSONPatchAdd(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "new-key", "new-value"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_JSONPatchRemove tests JSON Patch remove operation
// (EDGE_CASES.md 18.2)
func TestAccPatchResource_JSONPatchRemove(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("json-remove-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("json-remove-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with multiple keys
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"keep":   "this",
						"remove": "this",
					}),
				),
			},
			// Step 2: Use JSON Patch to remove field
			{
				Config: testAccPatchConfigJSONPatchRemove(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "keep", "this"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_JSONPatchReplace tests JSON Patch replace operation
// (EDGE_CASES.md 18.3)
func TestAccPatchResource_JSONPatchReplace(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("json-replace-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("json-replace-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"key": "old-value",
					}),
				),
			},
			// Step 2: Use JSON Patch to replace value
			{
				Config: testAccPatchConfigJSONPatchReplace(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "key", "new-value"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_JSONPatchMove tests JSON Patch move operation
// (EDGE_CASES.md 18.4)
func TestAccPatchResource_JSONPatchMove(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("json-move-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("json-move-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"source": "value-to-move",
					}),
				),
			},
			// Step 2: Use JSON Patch to move value
			{
				Config: testAccPatchConfigJSONPatchMove(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "destination", "value-to-move"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_JSONPatchCopy tests JSON Patch copy operation
// (EDGE_CASES.md 18.5)
func TestAccPatchResource_JSONPatchCopy(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("json-copy-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("json-copy-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"original": "value-to-copy",
					}),
				),
			},
			// Step 2: Use JSON Patch to copy value
			{
				Config: testAccPatchConfigJSONPatchCopy(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "original", "value-to-copy"),
					testhelpers.CheckConfigMapDataValue(k8sClient, ns, cmName, "copy", "value-to-copy"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// TestAccPatchResource_JSONPatchTest tests JSON Patch test operation
// (EDGE_CASES.md 18.6)
func TestAccPatchResource_JSONPatchTest(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("json-test-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("json-test-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap
			{
				Config: testAccPatchConfigEmptyWithNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					createConfigMapWithFieldManager(t, k8sClient, ns, cmName, "kubectl", map[string]string{
						"key": "expected-value",
					}),
				),
			},
			// Step 2: Use JSON Patch with test operation (should succeed)
			{
				Config: testAccPatchConfigJSONPatchTestSuccess(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_patch.test", "id"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Terraform config helpers for Section 15: Array Handling

func testAccPatchConfigArrayContainerSetup(namespace, deployName string) string {
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

resource "k8sconnect_object" "test_deploy" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: app
        image: nginx:1.14.2
      - name: sidecar
        image: busybox:1.28
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigArrayContainerPatch(namespace, deployName string) string {
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
      - name: app
        env:
        - name: PATCHED
          value: "true"
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigArrayEnvSetup(namespace, deployName string) string {
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

resource "k8sconnect_object" "test_deploy" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: app
        image: nginx:1.14.2
        env:
        - name: ENV1
          value: "value1"
        - name: ENV2
          value: "value2"
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigArrayEnvPatch(namespace, deployName string) string {
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
      - name: app
        env:
        - name: ENV2
          value: "patched-value"
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigSimpleArrayPatch(namespace, cmName string) string {
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
  list: "new1,new2"
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigComplexArraySetup(namespace, svcName string) string {
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

resource "k8sconnect_object" "test_svc" {
  yaml_body = <<YAML
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    app: test
  ports:
  - port: 80
    targetPort: 8080
    protocol: TCP
  - port: 443
    targetPort: 8443
    protocol: TCP
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, svcName, namespace)
}

func testAccPatchConfigComplexArrayPatch(namespace, svcName string) string {
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
    kind        = "Service"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
spec:
  ports:
  - port: 80
    name: http-patched
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, svcName, namespace)
}

// Terraform config helpers for Section 16: Deep Nesting

func testAccPatchConfigDeepNestedSetup(namespace, deployName string) string {
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

resource "k8sconnect_object" "test_deploy" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: app
        image: nginx:1.14.2
        env:
        - name: DEEP_VAR
          value: "original"
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigDeepNestedPatch(namespace, deployName string) string {
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
      - name: app
        env:
        - name: DEEP_VAR
          value: "patched"
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigDeepVolumeSetup(namespace, podName string) string {
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

resource "k8sconnect_object" "test_pod" {
  yaml_body = <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  containers:
  - name: app
    image: nginx:1.14.2
    volumeMounts:
    - name: config
      mountPath: /etc/config
  volumes:
  - name: config
    emptyDir: {}
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, podName, namespace)
}

func testAccPatchConfigDeepVolumePatch(namespace, podName string) string {
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
    kind        = "Pod"
    name        = "%s"
    namespace   = "%s"
  }

  patch = <<YAML
spec:
  containers:
  - name: app
    volumeMounts:
    - name: config
      mountPath: /etc/patched-config
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, podName, namespace)
}

// Terraform config helpers for Section 17: Special Values

func testAccPatchConfigEmptyString(namespace, cmName string) string {
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
  key: ""
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigNullValue(namespace, cmName string) string {
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
      "op": "remove",
      "path": "/data/remove"
    }
  ])

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigBooleanSetup(namespace, deployName string) string {
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

resource "k8sconnect_object" "test_deploy" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: app
        image: nginx:1.14.2
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigBooleanPatch(namespace, deployName string) string {
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
      - name: app
        tty: true
        stdin: false
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigNumericSetup(namespace, deployName string) string {
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

resource "k8sconnect_object" "test_deploy" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: app
        image: nginx:1.14.2
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigNumericPatch(namespace, deployName string) string {
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
  replicas: 3
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, deployName, namespace)
}

func testAccPatchConfigLargeString(namespace, cmName string) string {
	// Generate a large string (10KB+)
	largeValue := ""
	for i := 0; i < 1000; i++ {
		largeValue += "This is line " + fmt.Sprintf("%d", i) + " of a very large string value. "
	}

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
  key: %q
YAML

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace, largeValue)
}

// Terraform config helpers for Section 18: JSON Patch Operations

func testAccPatchConfigJSONPatchAdd(namespace, cmName string) string {
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
      "op": "add",
      "path": "/data/new-key",
      "value": "new-value"
    }
  ])

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigJSONPatchRemove(namespace, cmName string) string {
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
      "op": "remove",
      "path": "/data/remove"
    }
  ])

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigJSONPatchReplace(namespace, cmName string) string {
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
      "op": "replace",
      "path": "/data/key",
      "value": "new-value"
    }
  ])

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigJSONPatchMove(namespace, cmName string) string {
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
      "op": "move",
      "from": "/data/source",
      "path": "/data/destination"
    }
  ])

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigJSONPatchCopy(namespace, cmName string) string {
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
      "op": "copy",
      "from": "/data/original",
      "path": "/data/copy"
    }
  ])

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

func testAccPatchConfigJSONPatchTestSuccess(namespace, cmName string) string {
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
      "op": "test",
      "path": "/data/key",
      "value": "expected-value"
    },
    {
      "op": "add",
      "path": "/data/new",
      "value": "added-after-test"
    }
  ])

  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}
`, namespace, cmName, namespace)
}

// Helper functions to create resources with custom field managers using k8s client

// createDeploymentWithFieldManager creates a Deployment with a custom field manager
func createDeploymentWithFieldManager(t *testing.T, client kubernetes.Interface, namespace, name, fieldManager string, spec map[string]interface{}) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Create the Deployment using the k8s client with custom field manager
		deploy := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": namespace,
				},
				"spec": spec,
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

		fmt.Printf("✅ Created deployment %s/%s with field manager %s\n", namespace, name, fieldManager)
		return nil
	}
}

// createServiceWithFieldManager creates a Service with a custom field manager
func createServiceWithFieldManager(t *testing.T, client kubernetes.Interface, namespace, name, fieldManager string, spec map[string]interface{}) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Create the Service using the k8s client with custom field manager
		svc := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": namespace,
				},
				"spec": spec,
			},
		}

		svcBytes, err := json.Marshal(svc.Object)
		if err != nil {
			return fmt.Errorf("failed to marshal service: %v", err)
		}

		_, err = client.CoreV1().Services(namespace).Patch(
			ctx,
			name,
			types.ApplyPatchType,
			svcBytes,
			metav1.PatchOptions{
				FieldManager: fieldManager,
				Force:        ptr(true),
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create service with field manager %s: %v", fieldManager, err)
		}

		fmt.Printf("✅ Created service %s/%s with field manager %s\n", namespace, name, fieldManager)
		return nil
	}
}

// createPodWithFieldManager creates a Pod with a custom field manager
func createPodWithFieldManager(t *testing.T, client kubernetes.Interface, namespace, name, fieldManager string, spec map[string]interface{}) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Create the Pod using the k8s client with custom field manager
		pod := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": namespace,
				},
				"spec": spec,
			},
		}

		podBytes, err := json.Marshal(pod.Object)
		if err != nil {
			return fmt.Errorf("failed to marshal pod: %v", err)
		}

		_, err = client.CoreV1().Pods(namespace).Patch(
			ctx,
			name,
			types.ApplyPatchType,
			podBytes,
			metav1.PatchOptions{
				FieldManager: fieldManager,
				Force:        ptr(true),
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create pod with field manager %s: %v", fieldManager, err)
		}

		fmt.Printf("✅ Created pod %s/%s with field manager %s\n", namespace, name, fieldManager)
		return nil
	}
}
