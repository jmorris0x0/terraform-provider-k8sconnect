// internal/k8sconnect/resource/manifest/manifest_ownership_test.go
package manifest_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
)

func TestAccManifestResource_Ownership(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigOwnership,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// ID should be 12 hex characters
					resource.TestMatchResourceAttr("k8sconnect_manifest.test_ownership", "id",
						regexp.MustCompile("^[a-f0-9]{12}$")),
					testAccCheckConfigMapExists(k8sClient, "default", "test-ownership"),
					testAccCheckOwnershipAnnotations(k8sClient, "default", "test-ownership"),
				),
			},
		},
		CheckDestroy: testAccCheckConfigMapDestroy(k8sClient, "default", "test-ownership"),
	})
}

const testAccManifestConfigOwnership = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_ownership" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-ownership
  namespace: default
data:
  key: value
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}`

func TestAccManifestResource_OwnershipConflict(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Create first resource
			{
				Config: testAccManifestConfigOwnershipFirst,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckConfigMapExists(k8sClient, "default", "test-conflict"),
					testAccCheckOwnershipAnnotations(k8sClient, "default", "test-conflict"),
				),
			},
			// Try to create second resource managing same ConfigMap - should fail
			{
				Config: testAccManifestConfigOwnershipBoth,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("resource managed by different k8sconnect resource"),
			},
		},
		CheckDestroy: testAccCheckConfigMapDestroy(k8sClient, "default", "test-conflict"),
	})
}

const testAccManifestConfigOwnershipFirst = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "first" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-conflict
  namespace: default
data:
  owner: first
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}`

const testAccManifestConfigOwnershipBoth = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "first" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-conflict
  namespace: default
data:
  owner: first
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "second" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-conflict
  namespace: default
data:
  owner: second
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}`

func TestAccManifestResource_OwnershipImport(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with Terraform
			{
				Config: testAccManifestConfigOwnershipImport,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("k8sconnect_manifest.import_test", "id",
						regexp.MustCompile("^[a-f0-9]{12}$")),
					testAccCheckOwnershipAnnotations(k8sClient, "default", "test-import-ownership"),
				),
			},
			// Step 2: Import the ConfigMap
			{
				Config: testAccManifestConfigOwnershipImport,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ResourceName:      "k8sconnect_manifest.import_test",
				ImportState:       true,
				ImportStateId:     "k3d-oidc-e2e/default/ConfigMap/test-import-ownership",
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
				Config: testAccManifestConfigOwnershipImport,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("k8sconnect_manifest.import_test", "id",
						regexp.MustCompile("^[a-f0-9]{12}$")),
					testAccCheckOwnershipAnnotations(k8sClient, "default", "test-import-ownership"),
				),
			},
		},
	})
}

const testAccManifestConfigOwnershipImport = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "import_test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-import-ownership
  namespace: default
data:
  key: value
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}`

// Helper to check ownership annotations exist
func testAccCheckOwnershipAnnotations(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap: %v", err)
		}

		annotations := cm.GetAnnotations()
		if annotations == nil {
			return fmt.Errorf("ConfigMap has no annotations")
		}

		if _, ok := annotations["k8sconnect.terraform.io/terraform-id"]; !ok {
			return fmt.Errorf("ConfigMap missing ownership annotation k8sconnect.terraform.io/terraform-id")
		}

		return nil
	}
}

func TestAccManifestResource_FieldManagerConflict(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)
	k8sClientset := k8sClient.(*kubernetes.Clientset)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment with Terraform
			{
				Config: testAccManifestConfig_FieldConflict(false),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckDeploymentExists(k8sClient, "default", "field-conflict-test"),
					testAccCheckDeploymentReplicaCount(k8sClientset, "default", "field-conflict-test", 2),
				),
			},
			// Step 2: Modify with kubectl to create field manager conflict
			{
				PreConfig: func() {
					// Scale deployment using kubectl to create a different field manager
					cmd := exec.Command("kubectl", "scale", "deployment", "field-conflict-test", "--replicas=3")
					if err := cmd.Run(); err != nil {
						t.Fatalf("Failed to scale deployment with kubectl: %v", err)
					}

					// Give it a moment to process
					time.Sleep(2 * time.Second)
				},
				Config: testAccManifestConfig_FieldConflict(false),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile(`Field Manager Conflict`),
			},

			// You could also add a new step to verify the plan warning:
			// Step 2b: Plan-only to check warning
			{
				Config: testAccManifestConfig_FieldConflictUpdate(false), // Try to change back to 4 replicas
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				// Unfortunately Terraform test framework doesn't easily let us check for warnings
				// But this step would trigger the warning in real usage
			},
			// Step 3: Try to change replicas back with Terraform (should warn about conflict)
			{
				Config: testAccManifestConfig_FieldConflictUpdate(false),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile(`Field Manager Conflict`),
			},
			// Step 4: Force the change
			{
				Config: testAccManifestConfig_FieldConflictUpdate(true),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Now should be 4 because we forced
					testAccCheckDeploymentReplicaCount(k8sClientset, "default", "field-conflict-test", 4),
				),
			},
		},
		CheckDestroy: testAccCheckDeploymentDestroy(k8sClient, "default", "field-conflict-test"),
	})
}

func testAccManifestConfig_FieldConflict(forceConflicts bool) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_deployment" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: field-conflict-test
  namespace: default
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
}
`, forceConflicts)
}

func testAccManifestConfig_FieldConflictUpdate(forceConflicts bool) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_deployment" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: field-conflict-test
  namespace: default
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
}
`, forceConflicts)
}

// Helper function to check deployment replica count
func testAccCheckDeploymentReplicaCount(client *kubernetes.Clientset, namespace, name string, expected int32) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment: %v", err)
		}

		if *deployment.Spec.Replicas != expected {
			return fmt.Errorf("expected %d replicas, got %d", expected, *deployment.Spec.Replicas)
		}

		return nil
	}
}
