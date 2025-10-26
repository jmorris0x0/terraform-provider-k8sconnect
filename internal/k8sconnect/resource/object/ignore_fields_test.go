package object_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
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

  cluster = {
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

  cluster = {
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

  cluster = {
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

  cluster = {
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

  cluster = {
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

  cluster = {
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

  cluster = {
    kubeconfig = var.raw
  }
}
`, kubeconfig, yamlBody)
}

// TestAccObjectResource_IgnoreFieldsUnknown tests that ignore_fields works correctly
// when the value is unknown at plan time (computed from another resource).
// This validates ADR-011 smart projection logic handles unknown ignore_fields gracefully.
func TestAccObjectResource_IgnoreFieldsUnknown(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-unknown-ns-%d", time.Now().UnixNano()%1000000)
	sourceName := fmt.Sprintf("source-cm-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("test-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigIgnoreFieldsUnknown(ns, sourceName, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Both resources should be created successfully
					resource.TestCheckResourceAttrSet("k8sconnect_object.source", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.dependent", "id"),

					testhelpers.CheckConfigMapExists(k8sClient, ns, sourceName),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),

					// Verify ignore_fields was populated correctly after apply
					// (during plan it was unknown because source.id was unknown)
					resource.TestCheckResourceAttr("k8sconnect_object.dependent", "ignore_fields.#", "1"),
					resource.TestCheckResourceAttr("k8sconnect_object.dependent", "ignore_fields.0", "spec.replicas"),
				),
			},
			// Step 2: Re-apply to verify no drift
			{
				Config: testAccManifestConfigIgnoreFieldsUnknown(ns, sourceName, deployName),
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
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigIgnoreFieldsUnknown(namespace, sourceName, deployName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    kubeconfig = var.raw
  }
}

# Source resource - its ID will be unknown at plan time during CREATE
resource "k8sconnect_object" "source" {
  depends_on = [k8sconnect_object.namespace]

  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  config: "replicas"
YAML

  cluster = {
    kubeconfig = var.raw
  }
}

# Dependent resource with ignore_fields referencing source's data
# At plan time during CREATE, source.managed_state_projection is unknown
# so the ignore_fields value is unknown, testing ADR-011 bootstrap handling
locals {
  # This local uses a computed value (source's projection), making it unknown at plan time
  field_to_ignore = try(jsondecode(k8sconnect_object.source.managed_state_projection["data.config"]), "spec.replicas")
}

resource "k8sconnect_object" "dependent" {
  depends_on = [k8sconnect_object.source]

  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 3
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

  cluster = {
    kubeconfig = var.raw
  }

  # ignore_fields uses a local that references source's managed_state_projection
  # During initial plan, source doesn't exist yet, so this is unknown
  ignore_fields = [local.field_to_ignore]
}
`, namespace, sourceName, namespace, deployName, namespace)
}

// TestAccObjectResource_UpdateWithIgnoreFieldsChange tests the complex scenario of:
// 1. Updating a resource (changing image)
// 2. While also removing a field from ignore_fields
// 3. When external controller has modified that field
// This validates ownership transition works correctly during updates (ADR-009)
func TestAccObjectResource_UpdateWithIgnoreFieldsChange(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-update-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("ignore-update-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment with ignore_fields
			{
				Config: testAccManifestConfigUpdateWithIgnoreFieldsChange(ns, deployName, "nginx:1.21", 3, true),
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
			// Step 2: Simulate HPA modifying replicas externally
			{
				PreConfig: func() {
					ctx := context.Background()
					err := ssaClient.ForceApplyDeploymentReplicasSSA(ctx, ns, deployName, 5, "hpa-controller")
					if err != nil {
						t.Fatalf("Failed to simulate HPA taking ownership: %v", err)
					}
					t.Logf("✓ Simulated hpa-controller scaling replicas to 5")
				},
				Config: testAccManifestConfigUpdateWithIgnoreFieldsChange(ns, deployName, "nginx:1.21", 3, true),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify HPA owns replicas
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.spec.replicas", "hpa-controller"),
					// Verify replicas is 5 (HPA's value)
					testhelpers.CheckDeploymentReplicaCount(k8sClient.(*kubernetes.Clientset), ns, deployName, 5),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			// Step 3: In SAME update, change image AND remove replicas from ignore_fields
			// This is the critical test: ownership reclamation during an update
			{
				Config: testAccManifestConfigUpdateWithIgnoreFieldsChange(ns, deployName, "nginx:1.22", 3, false),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify image was updated
					testhelpers.CheckDeploymentImage(k8sClient.(*kubernetes.Clientset), ns, deployName, "nginx:1.22"),
					// Verify replicas was reclaimed and reset to 3
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.spec.replicas", "k8sconnect"),
					testhelpers.CheckDeploymentReplicaCount(k8sClient.(*kubernetes.Clientset), ns, deployName, 3),
					// Verify ignore_fields is now empty
					resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.#", "0"),
				),
			},
			// Step 4: Verify no drift on next plan
			{
				Config: testAccManifestConfigUpdateWithIgnoreFieldsChange(ns, deployName, "nginx:1.22", 3, false),
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
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigUpdateWithIgnoreFieldsChange(namespace, name, image string, replicas int, withIgnoreFields bool) string {
	ignoreFieldsLine := ""
	if withIgnoreFields {
		ignoreFieldsLine = `ignore_fields = ["spec.replicas"]`
	}

	return fmt.Sprintf(`
variable "raw" { type = string }

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
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
        image: %s
YAML

  cluster = {
    kubeconfig = var.raw
  }

  %s
}
`, namespace, name, namespace, replicas, image, ignoreFieldsLine)
}

// TestAccObjectResource_IgnoreFieldsJSONPathPredicate tests that JSONPath predicates
// work correctly in ignore_fields, specifically during UPDATE operations.
//
// This test also verifies the field ownership prediction bug fix where dry-run with
// force=true doesn't predict ownership takeover. The test runs multiple kubectl patch
// iterations to reliably catch the race condition (see FIELD_OWNERSHIP_PREDICTION_BUG.md).
//
// Environment-aware iterations:
// - CI: 5 iterations (race window is larger due to resource contention)
// - Local: 15 iterations (race window is smaller on fast machines)
// - With TEST_RACE_DELAY=1: adds artificial delay to widen race window
func TestAccObjectResource_IgnoreFieldsJSONPathPredicate(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-jsonpath-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("ignore-jsonpath-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	// Detect environment and configure iterations to catch race condition
	isCI := os.Getenv("CI") != ""
	delayStr := os.Getenv("TEST_RACE_DELAY")
	var delayMs int
	if delayStr != "" {
		var err error
		delayMs, err = strconv.Atoi(delayStr)
		if err != nil || delayMs <= 0 {
			delayMs = 75 // Default to 75ms if invalid value
		}
	}

	iterations := 5 // Default for CI
	if !isCI {
		iterations = 15 // More iterations needed locally due to faster machines
		t.Logf("🏠 Running locally: using %d iterations to catch race condition (vs 5 in CI)", iterations)
	}

	if delayMs > 0 {
		t.Logf("⏱️  Artificial delay enabled (TEST_RACE_DELAY=%d) to widen race window", delayMs)
	}

	// Build test steps: initial creation + multiple patch-apply cycles
	testSteps := []resource.TestStep{
		// Step 1: Create deployment with JSONPath predicate in ignore_fields
		{
			Config: testAccManifestConfigIgnoreFieldsJSONPath(ns, deployName, "managed-value", "external-value"),
			ConfigVariables: config.Variables{
				"raw": config.StringVariable(raw),
			},
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
				testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				// Verify ignore_fields contains JSONPath predicate
				resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.#", "1"),
				resource.TestCheckResourceAttr("k8sconnect_object.test", "ignore_fields.0",
					"spec.template.spec.containers[?(@.name=='app')].env[?(@.name=='EXTERNAL_VAR')].value"),
			),
		},
	}

	// Add multiple kubectl patch → terraform apply cycles to catch race condition
	testSteps = append(testSteps, generateIgnoreFieldsPatchApplyCycles(
		t, ssaClient, k8sClient, ns, deployName, raw, iterations, delayMs)...)

	// Final steps: verify no drift and test terraform update
	testSteps = append(testSteps, []resource.TestStep{
		// Verify no drift after all the patch cycles
		{
			Config: testAccManifestConfigIgnoreFieldsJSONPath(ns, deployName, "managed-value", "external-value"),
			ConfigVariables: config.Variables{
				"raw": config.StringVariable(raw),
			},
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				},
			},
		},
		// Update MANAGED_VAR in terraform, verify EXTERNAL_VAR still preserved
		{
			Config: testAccManifestConfigIgnoreFieldsJSONPath(ns, deployName, "managed-value-updated", "external-value"),
			ConfigVariables: config.Variables{
				"raw": config.StringVariable(raw),
			},
			Check: resource.ComposeTestCheckFunc(
				// MANAGED_VAR should be updated
				testhelpers.CheckDeploymentEnvVar(k8sClient.(*kubernetes.Clientset), ns, deployName,
					"app", "MANAGED_VAR", "managed-value-updated"),
				// EXTERNAL_VAR should STILL be preserved at kubectl's value (not reset to "external-value")
				testhelpers.CheckDeploymentEnvVar(k8sClient.(*kubernetes.Clientset), ns, deployName,
					"app", "EXTERNAL_VAR", fmt.Sprintf("kubectl-external-%d", iterations)),
			),
		},
	}...)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps:        testSteps,
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

// generateIgnoreFieldsPatchApplyCycles creates multiple test steps that patch with kubectl
// and then apply with terraform. This exercises the race condition where dry-run doesn't
// predict forced ownership takeover. Multiple iterations increase the probability of
// catching the race condition.
func generateIgnoreFieldsPatchApplyCycles(
	t *testing.T,
	ssaClient *testhelpers.SSATestClient,
	k8sClient kubernetes.Interface,
	ns, deployName, raw string,
	iterations int,
	delayMs int,
) []resource.TestStep {
	steps := []resource.TestStep{}

	for i := 0; i < iterations; i++ {
		iteration := i + 1
		steps = append(steps, resource.TestStep{
			PreConfig: func() {
				ctx := context.Background()

				// Modify MANAGED_VAR (not ignored - should be reverted)
				err := ssaClient.ForceApplyDeploymentEnvVarSSA(ctx, ns, deployName, "app",
					"MANAGED_VAR", fmt.Sprintf("kubectl-managed-%d", iteration), "kubectl-patch")
				if err != nil {
					t.Fatalf("Iteration %d: Failed to modify MANAGED_VAR: %v", iteration, err)
				}

				// Modify EXTERNAL_VAR (ignored - should be preserved)
				err = ssaClient.ForceApplyDeploymentEnvVarSSA(ctx, ns, deployName, "app",
					"EXTERNAL_VAR", fmt.Sprintf("kubectl-external-%d", iteration), "kubectl-patch")
				if err != nil {
					t.Fatalf("Iteration %d: Failed to modify EXTERNAL_VAR: %v", iteration, err)
				}

				// Optional: artificial delay to widen race window for local testing
				if delayMs > 0 {
					time.Sleep(time.Duration(delayMs) * time.Millisecond)
				}

				t.Logf("✓ Iteration %d/%d: Patched env vars with kubectl", iteration, iterations)
			},
			Config: testAccManifestConfigIgnoreFieldsJSONPath(ns, deployName, "managed-value", "external-value"),
			ConfigVariables: config.Variables{
				"raw": config.StringVariable(raw),
			},
			Check: resource.ComposeTestCheckFunc(
				// After apply, MANAGED_VAR should be reset to terraform's value
				testhelpers.CheckDeploymentEnvVar(k8sClient.(*kubernetes.Clientset), ns, deployName,
					"app", "MANAGED_VAR", "managed-value"),
				// EXTERNAL_VAR should be preserved at kubectl's value from this iteration
				testhelpers.CheckDeploymentEnvVar(k8sClient.(*kubernetes.Clientset), ns, deployName,
					"app", "EXTERNAL_VAR", fmt.Sprintf("kubectl-external-%d", iteration)),
			),
		})
	}

	return steps
}

func testAccManifestConfigIgnoreFieldsJSONPath(namespace, name, managedValue, externalValue string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
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
  replicas: 2
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
        image: nginx:1.21
        env:
        - name: MANAGED_VAR
          value: %s
        - name: EXTERNAL_VAR
          value: %s
YAML

  cluster = {
    kubeconfig = var.raw
  }

  # Use JSONPath predicate to ignore only EXTERNAL_VAR
  ignore_fields = ["spec.template.spec.containers[?(@.name=='app')].env[?(@.name=='EXTERNAL_VAR')].value"]
}
`, namespace, name, namespace, managedValue, externalValue)
}
