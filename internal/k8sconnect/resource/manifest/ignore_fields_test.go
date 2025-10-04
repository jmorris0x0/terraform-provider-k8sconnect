// internal/k8sconnect/resource/manifest/ignore_fields_test.go
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
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"k8s.io/client-go/kubernetes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

func TestAccManifestResource_IgnoreFields(t *testing.T) {
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
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.ignore_test", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					testhelpers.CheckDeploymentReplicaCount(k8sClientset, ns, deployName, 3),
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

// TestAccManifestResource_IgnoreFieldsTransition tests the critical workflow:
// 1. Create resource WITHOUT ignore_fields
// 2. External controller takes field ownership (simulated with SSA)
// 3. Add ignore_fields to config
// 4. Verify conflict error goes away and no drift occurs
func TestAccManifestResource_IgnoreFieldsTransition(t *testing.T) {
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
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, false),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 2: Use SSA to forcibly take ownership of spec.replicas (simulating HPA)
			{
				PreConfig: func() {
					ctx := context.Background()
					err := ssaClient.ForceApplyDeploymentReplicasSSA(ctx, ns, deployName, 5, "hpa-controller")
					if err != nil {
						t.Fatalf("Failed to simulate HPA taking ownership: %v", err)
					}
					t.Logf("✓ Simulated hpa-controller taking ownership of spec.replicas")
				},
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, false),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("Field Ownership Conflict|Cannot modify fields owned by other controllers"),
			},
			// Step 3: Add ignore_fields - conflict should disappear
			{
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, true),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "ignore_fields.#", "1"),
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "ignore_fields.0", "spec.replicas"),
				),
			},
			// Step 4: Verify no drift even though replicas differ
			{
				Config: testAccManifestConfigIgnoreFieldsTransition(ns, deployName, 3, true),
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

resource "k8sconnect_manifest" "namespace" {
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

resource "k8sconnect_manifest" "ignore_test" {
  depends_on = [k8sconnect_manifest.namespace]

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

func testAccManifestConfigIgnoreFieldsTransition(namespace, name string, replicas int, withIgnoreFields bool) string {
	ignoreFieldsLine := ""
	if withIgnoreFields {
		ignoreFieldsLine = `ignore_fields = ["spec.replicas"]`
	}

	return fmt.Sprintf(`
variable "raw" { type = string }

resource "k8sconnect_manifest" "namespace" {
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

resource "k8sconnect_manifest" "test" {
  depends_on = [k8sconnect_manifest.namespace]

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
