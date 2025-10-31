package object_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

func TestAccObjectResource_Ownership(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ownership-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("ownership-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigOwnership(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					// ID should be 12 hex characters
					resource.TestMatchResourceAttr("k8sconnect_object.test_ownership", "id",
						regexp.MustCompile("^[a-f0-9]{12}$")),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					testhelpers.CheckOwnershipAnnotations(k8sClient, ns, cmName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
	})
}

func testAccManifestConfigOwnership(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "cm_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "ownership_namespace" {
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

resource "k8sconnect_object" "test_ownership" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: value
YAML

  cluster = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.ownership_namespace]
}`, namespace, cmName, namespace)
}

func TestAccObjectResource_OwnershipConflict(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ownership-conflict-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("conflict-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Create first resource
			{
				Config: testAccManifestConfigOwnershipFirst(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					testhelpers.CheckOwnershipAnnotations(k8sClient, ns, cmName),
				),
			},
			// Try to create second resource managing same ConfigMap - should fail
			{
				Config: testAccManifestConfigOwnershipBoth(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				ExpectError: regexp.MustCompile("already managed by a different k8sconnect resource"),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
	})
}

func testAccManifestConfigOwnershipFirst(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "cm_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "conflict_namespace" {
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

resource "k8sconnect_object" "first" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  owner: first
YAML

  cluster = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.conflict_namespace]
}`, namespace, cmName, namespace)
}

func testAccManifestConfigOwnershipBoth(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "cm_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "conflict_namespace" {
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

resource "k8sconnect_object" "first" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  owner: first
YAML

  cluster = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.conflict_namespace]
}

resource "k8sconnect_object" "second" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  owner: second
YAML

  cluster = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.conflict_namespace]
}`, namespace, cmName, namespace, cmName, namespace)
}

func TestAccObjectResource_OwnershipImport(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ownership-import-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("import-ownership-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with Terraform
			{
				Config: testAccManifestConfigOwnershipImport(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("k8sconnect_object.import_test", "id",
						regexp.MustCompile("^[a-f0-9]{12}$")),
					testhelpers.CheckOwnershipAnnotations(k8sClient, ns, cmName),
				),
			},
			// Step 2: Import the ConfigMap
			{
				Config: testAccManifestConfigOwnershipImport(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				ResourceName:      "k8sconnect_object.import_test",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("k3d-k8sconnect-test:%s:v1/ConfigMap:%s", ns, cmName),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"cluster",
					"yaml_body",
					"managed_state_projection",
					"delete_protection",
				},
			},
			// Step 3: Verify ownership annotations still exist after import
			{
				Config: testAccManifestConfigOwnershipImport(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("k8sconnect_object.import_test", "id",
						regexp.MustCompile("^[a-f0-9]{12}$")),
					testhelpers.CheckOwnershipAnnotations(k8sClient, ns, cmName),
				),
			},
		},
	})
}

func testAccManifestConfigOwnershipImport(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "cm_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "import_namespace" {
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

resource "k8sconnect_object" "import_test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: value
YAML

  cluster = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.import_namespace]
}`, namespace, cmName, namespace)
}

// TestAccObjectResource_FieldManagerConflict verifies that field ownership conflicts are detected and reported.
// Expected behavior:
//  1. When another field manager (e.g., kubectl) takes ownership of a field that's defined in our YAML
//  2. The provider should detect this conflict during planning
//  3. An error should be raised indicating which fields are conflicted and who owns them
//  4. The provider should warn about conflicts and force ownership (since we always use force=true)
//     a) A warning is shown listing all conflicting fields
//     b) Fields are taken over forcibly (may cause fights with other controllers)
//     c) Users should use ignore_fields to release ownership if they don't want to manage a field
//
// TestAccObjectResource_FieldManagerConflict verifies that field ownership conflicts are detected and reported.
func TestAccObjectResource_FieldManagerConflict(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-conflict-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("field-conflict-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	k8sClientset := k8sClient.(*kubernetes.Clientset)

	// Create our minimal SSA test client from the helpers
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment with Terraform
			{
				Config: testAccManifestConfig_FieldConflict(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 2),
				),
			},
			// Step 2: Use Server-Side Apply to transfer ownership to hpa-controller
			{
				PreConfig: func() {
					ctx := context.Background()

					// Use our minimal SSA client from helpers to properly transfer field ownership
					// This simulates what an HPA controller would actually do
					err := ssaClient.ApplyDeploymentReplicasSSA(ctx, ns, deployName, 3, "hpa-controller")
					if err != nil {
						t.Fatalf("Failed to apply with hpa-controller: %v", err)
					}

					// Give it a moment to process
					time.Sleep(2 * time.Second)

					// Verify the field ownership changed
					deploy, err := k8sClientset.AppsV1().Deployments(ns).Get(ctx, deployName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("Failed to get deployment: %v", err)
					}

					t.Logf("ManagedFields after hpa-controller SSA:")
					hasHPA := false
					for _, mf := range deploy.ManagedFields {
						t.Logf("  Manager: %s, Operation: %s", mf.Manager, mf.Operation)
						if mf.Manager == "hpa-controller" {
							hasHPA = true
							t.Logf("    ✓ hpa-controller took ownership via SSA")
						}
					}

					if !hasHPA {
						t.Fatalf("hpa-controller did not appear in managedFields after SSA")
					}

					// Verify replicas changed to 3
					if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 3 {
						t.Fatalf("Expected replicas to be 3, got %v", deploy.Spec.Replicas)
					}
				},
				// Now change replicas with Terraform - should take ownership and show warning
				Config: testAccManifestConfig_FieldConflictUpdate(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
				},
				Check: resource.ComposeTestCheckFunc(
					// Should be 4 because we forced it
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 4),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

// TestAccObjectResource_SharedOwnershipNoValueChange verifies SSA co-ownership behavior when values match.
// This test documents the Option A vs Option B decision from ADR-021.
//
// Scenario: HPA becomes co-owner of spec.replicas but applies the SAME value
// Expected behavior with current implementation (Option B - metadata-based):
//   - Detects manager list change ([k8sconnect] → [k8sconnect, hpa-controller])
//   - May show warning about co-ownership (even though value matches)
//
// Expected behavior if we switch to Option A (value-based):
//   - No warning (value unchanged, no actual drift)
//   - Silent co-ownership acceptance
//
// This test will PASS with either approach, but documents the behavior difference.
func TestAccObjectResource_SharedOwnershipNoValueChange(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("shared-owner-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("shared-owner-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	k8sClientset := k8sClient.(*kubernetes.Clientset)

	// Create our minimal SSA test client from the helpers
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment with Terraform (replicas: 2)
			{
				Config: testAccManifestConfig_SharedOwnership(ns, deployName, 2),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
					"replicas":    config.IntegerVariable(2),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 2),
				),
			},
			// Step 2: HPA applies SAME VALUE (replicas: 2) - becomes co-owner
			{
				PreConfig: func() {
					ctx := context.Background()

					// HPA applies replicas: 2 (SAME VALUE as Terraform)
					// This creates shared ownership: both k8sconnect and hpa-controller own spec.replicas
					err := ssaClient.ApplyDeploymentReplicasSSA(ctx, ns, deployName, 2, "hpa-controller")
					if err != nil {
						t.Fatalf("Failed to apply with hpa-controller: %v", err)
					}

					// Give it a moment to process
					time.Sleep(2 * time.Second)

					// Verify co-ownership was established
					deploy, err := k8sClientset.AppsV1().Deployments(ns).Get(ctx, deployName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("Failed to get deployment: %v", err)
					}

					t.Logf("ManagedFields after HPA SSA (same value):")
					hasK8sconnect := false
					hasHPA := false
					for _, mf := range deploy.ManagedFields {
						t.Logf("  Manager: %s, Operation: %s", mf.Manager, mf.Operation)
						if mf.Manager == "k8sconnect" {
							hasK8sconnect = true
						}
						if mf.Manager == "hpa-controller" {
							hasHPA = true
						}
					}

					if !hasK8sconnect {
						t.Fatalf("k8sconnect not in managedFields - expected co-ownership")
					}
					if !hasHPA {
						t.Fatalf("hpa-controller not in managedFields - expected co-ownership")
					}

					// Verify value is still 2 (unchanged)
					if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 2 {
						t.Fatalf("Expected replicas to remain 2, got %v", deploy.Spec.Replicas)
					}

					t.Logf("✓ Co-ownership established: both k8sconnect and hpa-controller own spec.replicas")
					t.Logf("✓ Value unchanged: replicas=2")
				},
				// Re-apply same config (no changes) - test if warning appears
				Config: testAccManifestConfig_SharedOwnership(ns, deployName, 2),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
					"replicas":    config.IntegerVariable(2),
				},
				Check: resource.ComposeTestCheckFunc(
					// Value should remain 2
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 2),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfig_FieldConflict(namespace, deployName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "deploy_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "field_conflict_namespace" {
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

resource "k8sconnect_object" "test_deployment" {
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
      app: field-conflict-test
  template:
    metadata:
      labels:
        app: field-conflict-test
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        resources:
          limits:
            cpu: "100m"
            memory: "128Mi"
          requests:
            cpu: "50m"
            memory: "64Mi"
YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.field_conflict_namespace]
}
`, namespace, deployName, namespace)
}

func testAccManifestConfig_FieldConflictUpdate(namespace, deployName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "deploy_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "field_conflict_namespace" {
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

resource "k8sconnect_object" "test_deployment" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 4
  selector:
    matchLabels:
      app: field-conflict-test
  template:
    metadata:
      labels:
        app: field-conflict-test
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        resources:
          limits:
            cpu: "100m"
            memory: "128Mi"
          requests:
            cpu: "50m"
            memory: "64Mi"
YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.field_conflict_namespace]
}
`, namespace, deployName, namespace)
}

func testAccManifestConfig_SharedOwnership(namespace, deployName string, replicas int) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "deploy_name" {
  type = string
}
variable "replicas" {
  type = number
}

provider "k8sconnect" {}

resource "k8sconnect_object" "shared_owner_namespace" {
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

resource "k8sconnect_object" "test_deployment" {
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
      app: shared-owner-test
  template:
    metadata:
      labels:
        app: shared-owner-test
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        resources:
          limits:
            cpu: "100m"
            memory: "128Mi"
          requests:
            cpu: "50m"
            memory: "64Mi"
YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.shared_owner_namespace]
}
`, namespace, deployName, namespace, replicas)
}

// TestAccObjectResource_DriftDetectionWithForceConflicts tests the scenario where:
// 1. We create a resource
// 2. External controller modifies it
// 3. We detect drift and correct it WITHOUT changing the config
// This is the scenario reported by the user where "inconsistent plan" error occurs
func TestAccObjectResource_DriftDetectionWithForceConflicts(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("drift-force-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("drift-force-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	k8sClientset := k8sClient.(*kubernetes.Clientset)
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	deploymentConfig := fmt.Sprintf(`
variable "raw" { type = string }

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = { kubeconfig = var.raw }
}

resource "k8sconnect_object" "deployment" {
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
      app: drift-test
  template:
    metadata:
      labels:
        app: drift-test
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        resources:
          limits:
            cpu: "100m"
            memory: "128Mi"
YAML
  cluster = { kubeconfig = var.raw }
}
`, ns, deployName, ns)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment - force_conflicts defaults to true
			{
				Config: deploymentConfig,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 2),
					resource.TestCheckResourceAttr("k8sconnect_object.deployment", "field_ownership.spec.replicas", "k8sconnect"),
				),
			},
			// Step 2: External controller modifies replicas (simulating kubectl edit or HPA)
			{
				PreConfig: func() {
					ctx := context.Background()
					err := ssaClient.ApplyDeploymentReplicasSSA(ctx, ns, deployName, 5, "kubectl-edit")
					if err != nil {
						t.Fatalf("Failed to apply with kubectl-edit: %v", err)
					}
					time.Sleep(1 * time.Second)

					// Verify ownership changed
					deploy, err := k8sClientset.AppsV1().Deployments(ns).Get(ctx, deployName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("Failed to get deployment: %v", err)
					}

					hasKubectlEdit := false
					for _, mf := range deploy.ManagedFields {
						if mf.Manager == "kubectl-edit" {
							hasKubectlEdit = true
							t.Logf("✓ kubectl-edit took ownership via SSA")
						}
					}
					if !hasKubectlEdit {
						t.Fatalf("kubectl-edit did not appear in managedFields")
					}
				},
				// Apply SAME config - just correcting drift
				Config: deploymentConfig,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// This should succeed and forcibly take ownership back
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 2),
					// Critical: field_ownership should update to show k8sconnect owns it again
					resource.TestCheckResourceAttr("k8sconnect_object.deployment", "field_ownership.spec.replicas", "k8sconnect"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

// TestAccObjectResource_MultipleFieldConflicts tests that when multiple fields have ownership conflicts,
// ALL conflicts are detected and shown in the warning (not just one).
// This reproduces the bug where only spec.replicas was shown but not other conflicted fields.
func TestAccObjectResource_MultipleFieldConflicts(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("multi-conflict-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("multi-conflict-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	k8sClientset := k8sClient.(*kubernetes.Clientset)
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	deploymentConfig := fmt.Sprintf(`
variable "raw" { type = string }

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = { kubeconfig = var.raw }
}

resource "k8sconnect_object" "deployment" {
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
      app: multi-test
  template:
    metadata:
      labels:
        app: multi-test
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        resources:
          limits:
            cpu: "100m"
            memory: "128Mi"
          requests:
            cpu: "50m"
            memory: "64Mi"
YAML
  cluster = { kubeconfig = var.raw }
}
`, ns, deployName, ns)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment
			{
				Config: deploymentConfig,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 2),
				),
			},
			// Step 2: Multiple external controllers modify different fields
			{
				PreConfig: func() {
					ctx := context.Background()

					// HPA takes ownership of replicas
					err := ssaClient.ApplyDeploymentReplicasSSA(ctx, ns, deployName, 5, "hpa-controller")
					if err != nil {
						t.Fatalf("Failed to apply replicas with hpa-controller: %v", err)
					}

					// Resource controller changes CPU
					err = ssaClient.ApplyDeploymentCPULimitSSA(ctx, ns, deployName, "200m", "resource-controller")
					if err != nil {
						t.Fatalf("Failed to apply CPU with resource-controller: %v", err)
					}

					// Memory controller changes memory
					err = ssaClient.ApplyDeploymentMemoryLimitSSA(ctx, ns, deployName, "256Mi", "memory-controller")
					if err != nil {
						t.Fatalf("Failed to apply memory with memory-controller: %v", err)
					}

					time.Sleep(1 * time.Second)
					t.Logf("✓ Created 3 conflicts: replicas (hpa-controller), cpu (resource-controller), memory (memory-controller)")
				},
				// Apply SAME config - should detect and show ALL 3 conflicts in warning
				Config: deploymentConfig,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// Should succeed and correct all drift
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 2),
					// All fields should be owned by k8sconnect again
					resource.TestCheckResourceAttr("k8sconnect_object.deployment", "field_ownership.spec.replicas", "k8sconnect"),
					resource.TestCheckResourceAttr("k8sconnect_object.deployment", "field_ownership.spec.template.spec.containers[0].resources.limits.cpu", "k8sconnect"),
					resource.TestCheckResourceAttr("k8sconnect_object.deployment", "field_ownership.spec.template.spec.containers[0].resources.limits.memory", "k8sconnect"),
				),
				// All conflicts should be detected and corrected (we always force ownership)
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}
