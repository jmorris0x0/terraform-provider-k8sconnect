// internal/k8sconnect/resource/manifest/ownership_test.go
package manifest_test

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

func TestAccManifestResource_Ownership(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
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
					resource.TestMatchResourceAttr("k8sconnect_manifest.test_ownership", "id",
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

resource "k8sconnect_manifest" "ownership_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "test_ownership" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: value
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.ownership_namespace]
}`, namespace, cmName, namespace)
}

func TestAccManifestResource_OwnershipConflict(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
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
				ExpectError: regexp.MustCompile("resource managed by different k8sconnect resource"),
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

resource "k8sconnect_manifest" "conflict_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "first" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  owner: first
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.conflict_namespace]
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

resource "k8sconnect_manifest" "conflict_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "first" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  owner: first
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.conflict_namespace]
}

resource "k8sconnect_manifest" "second" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  owner: second
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.conflict_namespace]
}`, namespace, cmName, namespace, cmName, namespace)
}

func TestAccManifestResource_OwnershipImport(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
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
					resource.TestMatchResourceAttr("k8sconnect_manifest.import_test", "id",
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
				ResourceName:      "k8sconnect_manifest.import_test",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("k3d-k8sconnect-test/%s/ConfigMap/%s", ns, cmName),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"imported_without_annotations",
					"cluster_connection",
					"yaml_body",
					"managed_state_projection",
					"delete_protection",
					"force_conflicts",
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
					resource.TestMatchResourceAttr("k8sconnect_manifest.import_test", "id",
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

resource "k8sconnect_manifest" "import_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "import_test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: value
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.import_namespace]
}`, namespace, cmName, namespace)
}

// TestAccManifestResource_FieldManagerConflict verifies that field ownership conflicts are detected and reported.
// Expected behavior:
//  1. When another field manager (e.g., kubectl) takes ownership of a field that's defined in our YAML
//  2. The provider should detect this conflict during planning
//  3. An error should be raised indicating which fields are conflicted and who owns them
//  4. The error should provide clear resolution options:
//     a) Remove the conflicting field from your Terraform YAML
//     b) Set force_conflicts=true to forcibly take ownership (may cause fights with other controllers)
//     c) Future: Use ignore_field_changes to explicitly ignore the field
//
// Eventually make sure there is an error something like this:
// resp.Diagnostics.AddError(
//
//	"Field Ownership Conflict",
//	fmt.Sprintf("Cannot modify fields owned by other controllers:\n"+
//	    "  - %s (owned by %s)\n\n"+
//	    "Resolution options:\n"+
//	    "1. Remove the conflicting field from your Terraform configuration\n"+
//	    "2. Set force_conflicts = true to forcibly take ownership\n"+
//	    "   WARNING: The other controller may fight back, causing perpetual drift\n"+
//	    "3. (Future) Add field to ignore_field_changes when implemented",
//	    conflictDetails),
//
// )
// TestAccManifestResource_FieldManagerConflict verifies that field ownership conflicts are detected and reported.
func TestAccManifestResource_FieldManagerConflict(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
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
				Config: testAccManifestConfig_FieldConflict(ns, deployName, false),
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
				// Now try to change replicas with Terraform - should conflict
				Config: testAccManifestConfig_FieldConflictUpdate(ns, deployName, false),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
				},
				ExpectError: regexp.MustCompile(`Field Manager Conflict|Field Ownership Conflict`),
			},
			// Step 3: Force the change
			{
				Config: testAccManifestConfig_FieldConflictUpdate(ns, deployName, true),
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

func testAccManifestConfig_FieldConflict(namespace, deployName string, forceConflicts bool) string {
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

resource "k8sconnect_manifest" "field_conflict_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "test_deployment" {
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
        image: nginx:1.19
        resources:
          limits:
            cpu: "100m"
            memory: "128Mi"
          requests:
            cpu: "50m"
            memory: "64Mi"
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }

  force_conflicts = %t
  
  depends_on = [k8sconnect_manifest.field_conflict_namespace]
}
`, namespace, deployName, namespace, forceConflicts)
}

func testAccManifestConfig_FieldConflictUpdate(namespace, deployName string, forceConflicts bool) string {
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

resource "k8sconnect_manifest" "field_conflict_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "test_deployment" {
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
        image: nginx:1.19
        resources:
          limits:
            cpu: "100m"
            memory: "128Mi"
          requests:
            cpu: "50m"
            memory: "64Mi"
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }

  force_conflicts = %t
  
  depends_on = [k8sconnect_manifest.field_conflict_namespace]
}
`, namespace, deployName, namespace, forceConflicts)
}
