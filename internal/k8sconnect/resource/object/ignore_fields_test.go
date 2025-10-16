// internal/k8sconnect/resource/object/ignore_fields_test.go
package object_test

import (
	"context"
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
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"k8s.io/client-go/kubernetes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// Helper function to create a boolean pointer
func boolPtr(b bool) *bool {
	return &b
}

func TestAccObjectResource_IgnoreFields(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-fields-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("ignore-test-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	k8sClientset := k8sClient.(*kubernetes.Clientset)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment with ignore_fields - verify it's accepted and works
			{
				Config: testAccManifestConfigIgnoreFields(ns, deployName, 3),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(deployName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.ignore_test", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 3),
					// Verify we own spec.selector (but not spec.replicas which is ignored)
					resource.TestCheckResourceAttr("k8sconnect_object.ignore_test", "field_ownership.spec.selector", "k8sconnect"),
				),
			},
			// Step 2: Re-apply without changes - should show no drift
			{
				Config: testAccManifestConfigIgnoreFields(ns, deployName, 3),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(deployName),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

// TestAccObjectResource_IgnoreFieldsTransition tests the critical workflow:
// 1. Create resource WITHOUT ignore_fields
// 2. External controller takes field ownership (simulated with SSA)
// 3. Provider forces ownership back (with warning)
// 4. Add ignore_fields to release ownership
// 5. Verify no drift occurs when field is ignored
func TestAccObjectResource_IgnoreFieldsTransition(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-transition-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("ignore-transition-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment WITHOUT ignore_fields
			{
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, false, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 2: Simulate external controller taking ownership, then force it back
			{
				PreConfig: func() {
					ctx := context.Background()
					err := ssaClient.ForceApplyDeploymentReplicasSSA(ctx, ns, deployName, 5, "hpa-controller")
					if err != nil {
						t.Fatalf("Failed to simulate HPA taking ownership: %v", err)
					}
					t.Logf("✓ Simulated hpa-controller taking ownership of spec.replicas")
				},
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, false, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// We should have forced ownership back and reset replicas to 3
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.spec.replicas", "k8sconnect"),
					testhelpers.CheckDeploymentReplicaCount(k8sClient.(*kubernetes.Clientset), ns, deployName, 3),
				),
			},
			// Step 3: Add ignore_fields - releases ownership to hpa-controller
			{
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, true, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.#", "1"),
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.0", "spec.replicas"),
				),
			},
			// Step 4: Verify no drift even though replicas differ
			{
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, true, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

func testAccManifestConfigIgnoreFields(namespace, name string, replicas int) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "name" { type = string }

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: ${var.namespace}
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "ignore_test" {
  depends_on = [k8sconnect_object.namespace]

  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${var.name}
  namespace: ${var.namespace}
spec:
  replicas: %d
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  # Ignore spec.replicas - HPA will manage this
  ignore_fields = ["spec.replicas"]
}
`, replicas)
}

// TestAccObjectResource_IgnoreFieldsRemoveWhileOwned tests removing ignore_fields
// when another controller owns the field - we force ownership back
func TestAccObjectResource_IgnoreFieldsRemoveWhileOwned(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-remove-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("ignore-remove-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create with ignore_fields
			{
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, true, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 2: HPA takes ownership
			{
				PreConfig: func() {
					ctx := context.Background()
					err := ssaClient.ForceApplyDeploymentReplicasSSA(ctx, ns, deployName, 5, "hpa-controller")
					if err != nil {
						t.Fatalf("Failed to simulate HPA taking ownership: %v", err)
					}
					t.Logf("✓ Simulated hpa-controller taking ownership of spec.replicas")
				},
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, true, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify field_ownership shows hpa-controller owns spec.replicas
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.spec.replicas", "hpa-controller"),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			// Step 3: REMOVE ignore_fields - we force ownership back
			{
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, false, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// We should have forced ownership back and reset replicas to 3
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.spec.replicas", "k8sconnect"),
					testhelpers.CheckDeploymentReplicaCount(k8sClient.(*kubernetes.Clientset), ns, deployName, 3),
				),
			},
		},
	})
}

// TestAccObjectResource_IgnoreFieldsModifyList tests modifying the ignore_fields list
// This test verifies adding/removing fields from ignore_fields works correctly
func TestAccObjectResource_IgnoreFieldsModifyList(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-modify-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("ignore-modify-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create with one ignored field
			{
				Config: testAccManifestConfigIgnoreFieldsConfigMap(ns, cmName, []string{"data.key1"}, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.#", "1"),
					// Verify field_ownership includes key2 (but not key1 which is ignored)
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.data.key2", "k8sconnect"),
				),
			},
			// Step 2: Use SSA to simulate external controller taking ownership of data.key2
			{
				PreConfig: func() {
					ctx := context.Background()
					// Use SSA with FORCE to transfer ownership to external-controller (like other tests do)
					err := ssaClient.ForceApplyConfigMapDataSSA(ctx, ns, cmName, map[string]string{
						"key2": "externally-modified",
					}, "external-controller")
					if err != nil {
						t.Fatalf("Failed to apply with external-controller: %v", err)
					}
					t.Logf("✓ external-controller took ownership of data.key2 via SSA")
				},
				Config: testAccManifestConfigIgnoreFieldsConfigMap(ns, cmName, []string{"data.key1", "data.key2"}, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.#", "2"),
					// Verify field_ownership shows key2 is owned by external-controller
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.data.key2", "external-controller"),
				),
			},
			// Step 3: REMOVE one field from ignore list - should reclaim it
			{
				Config: testAccManifestConfigIgnoreFieldsConfigMap(ns, cmName, []string{"data.key2"}, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.#", "1"),
					// Verify key1 is back to expected value
					testhelpers.CheckConfigMapData(k8sClient, ns, cmName, map[string]string{
						"key1": "value1",
					}),
					// Verify field_ownership shows k8sconnect owns key1 again
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.data.key1", "k8sconnect"),
					// key2 should still be owned by external-controller
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.data.key2", "external-controller"),
				),
			},
		},
	})
}

// TestAccObjectResource_IgnoreFieldsModifyListError tests removing a field from ignore_fields
// when an external controller owns it - we force ownership back (Gap 1 from test coverage doc)
func TestAccObjectResource_IgnoreFieldsModifyListError(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-modify-error-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("ignore-modify-error-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create with both fields ignored
			{
				Config: testAccManifestConfigIgnoreFieldsConfigMap(ns, cmName, []string{"data.key1", "data.key2"}, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.#", "2"),
				),
			},
			// Step 2: External controller takes ownership of data.key2
			{
				PreConfig: func() {
					ctx := context.Background()
					// Use SSA with FORCE to transfer ownership to external-controller
					err := ssaClient.ForceApplyConfigMapDataSSA(ctx, ns, cmName, map[string]string{
						"key2": "externally-owned",
					}, "external-controller")
					if err != nil {
						t.Fatalf("Failed to apply with external-controller: %v", err)
					}
					t.Logf("✓ external-controller took ownership of data.key2 via SSA")
				},
				Config: testAccManifestConfigIgnoreFieldsConfigMap(ns, cmName, []string{"data.key1", "data.key2"}, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify field_ownership shows external-controller owns data.key2
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.data.key2", "external-controller"),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			// Step 3: REMOVE data.key2 from ignore list - we force ownership back
			{
				Config: testAccManifestConfigIgnoreFieldsConfigMap(ns, cmName, []string{"data.key1"}, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// We should have forced ownership back and set key2 to expected value
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.data.key2", "k8sconnect"),
					testhelpers.CheckConfigMapData(k8sClient, ns, cmName, map[string]string{
						"key2": "value2",
					}),
				),
			},
		},
	})
}

// TestAccObjectResource_IgnoreFieldsRemoveWhenOwned tests removing ignore_fields
// when WE still own the field - should succeed cleanly (Gap 3 from test coverage doc)
func TestAccObjectResource_IgnoreFieldsRemoveWhenOwned(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-noop-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("ignore-noop-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create with ignore_fields
			{
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, true, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.#", "1"),
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.0", "spec.replicas"),
				),
			},
			// Step 2: REMOVE ignore_fields immediately (no external controller took over) - should succeed
			{
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, false, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.#", "0"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Verify field_ownership shows k8sconnect owns spec.replicas again
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.spec.replicas", "k8sconnect"),
				),
			},
		},
	})
}

func testAccManifestConfigIgnoreFieldsTransition(namespace, name string, replicas int, withIgnoreFields bool, forceConflicts *bool) string {
	ignoreFieldsLine := ""
	if withIgnoreFields {
		ignoreFieldsLine = `ignore_fields = ["spec.replicas"]`
	}

	// Note: forceConflicts parameter is kept for compatibility but ignored (force is always true now)

	return fmt.Sprintf(`
variable "raw" { type = string }

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test" {
  depends_on = [k8sconnect_object.namespace]

  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  %s
}
`, namespace, name, namespace, replicas, ignoreFieldsLine)
}

func testAccManifestConfigIgnoreFieldsConfigMap(namespace, name string, ignoreFields []string, forceConflicts *bool) string {
	ignoreFieldsLine := ""
	if len(ignoreFields) > 0 {
		fields := make([]string, len(ignoreFields))
		for i, f := range ignoreFields {
			fields[i] = fmt.Sprintf(`"%s"`, f)
		}
		ignoreFieldsLine = fmt.Sprintf("ignore_fields = [%s]", strings.Join(fields, ", "))
	}

	// Note: forceConflicts parameter is kept for compatibility but ignored (force is always true now)

	return fmt.Sprintf(`
variable "raw" { type = string }

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test" {
  depends_on = [k8sconnect_object.namespace]

  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key1: value1
  key2: value2
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  %s
}
`, namespace, name, namespace, ignoreFieldsLine)
}

// TestAccObjectResource_IgnoreFieldsValidation tests that validation blocks
// attempts to ignore provider internal annotations
func TestAccObjectResource_IgnoreFieldsValidation(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-validation-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("ignore-validation-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Test 1: Block ignoring created-at annotation
			{
				Config: testAccManifestConfigIgnoreFieldsConfigMap(ns, cmName, []string{
					"metadata.annotations.k8sconnect.terraform.io/created-at",
				}, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("Cannot ignore provider internal annotations"),
			},
			// Test 2: Block ignoring terraform-id annotation
			{
				Config: testAccManifestConfigIgnoreFieldsConfigMap(ns, cmName, []string{
					"metadata.annotations.k8sconnect.terraform.io/terraform-id",
				}, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("Cannot ignore provider internal annotations"),
			},
			// Test 3: Block any annotation under our namespace
			{
				Config: testAccManifestConfigIgnoreFieldsConfigMap(ns, cmName, []string{
					"metadata.annotations.k8sconnect.terraform.io/something-else",
				}, nil),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("Cannot ignore provider internal annotations"),
			},
		},
	})
}

// TestAccObjectResource_YAMLBodyValidation tests that validation blocks
// server-managed fields and provider internal annotations in yaml_body
func TestAccObjectResource_YAMLBodyValidation(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Test 1: Block provider annotation in yaml_body
			{
				Config: testAccManifestConfigWithYAMLBody(raw, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
  annotations:
    k8sconnect.terraform.io/created-at: "2025-01-01"
data:
  key: value
`),
				ExpectError: regexp.MustCompile("Provider internal annotations not allowed in yaml_body"),
			},
			// Test 2: Block uid in yaml_body
			{
				Config: testAccManifestConfigWithYAMLBody(raw, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
  uid: abc-123
data:
  key: value
`),
				ExpectError: regexp.MustCompile("Server-managed fields not allowed in yaml_body"),
			},
			// Test 3: Block resourceVersion in yaml_body
			{
				Config: testAccManifestConfigWithYAMLBody(raw, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
  resourceVersion: "12345"
data:
  key: value
`),
				ExpectError: regexp.MustCompile("Server-managed fields not allowed in yaml_body"),
			},
			// Test 4: Block managedFields in yaml_body
			{
				Config: testAccManifestConfigWithYAMLBody(raw, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
  managedFields:
    - manager: kubectl
data:
  key: value
`),
				ExpectError: regexp.MustCompile("Server-managed fields not allowed in yaml_body"),
			},
			// Test 5: Block status in yaml_body
			{
				Config: testAccManifestConfigWithYAMLBody(raw, `
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  containers:
  - name: nginx
    image: nginx
status:
  phase: Running
`),
				ExpectError: regexp.MustCompile("Server-managed fields not allowed in yaml_body"),
			},
		},
	})
}

func testAccManifestConfigWithYAMLBody(kubeconfig, yamlBody string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
  default = %q
}

resource "k8sconnect_object" "test" {
  yaml_body = <<YAML
%s
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}
`, kubeconfig, yamlBody)
}
