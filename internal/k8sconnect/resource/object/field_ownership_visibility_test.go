package object_test

import (
	"context"
	"fmt"
	"os"
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

// TestAccFieldOwnershipTransitionVisibility validates that ownership transitions
// are visible via warnings during plan.
//
// With ADR-020, field ownership moved to private state to avoid consistency errors.
// Ownership transitions are now reported as WARNINGS during plan instead of state diffs.
//
// Scenario:
// 1. Create a deployment with k8sconnect (k8sconnect owns all fields)
// 2. External kubectl patches spec.replicas using SSA with force=true (kubectl-patch takes ownership)
// 3. Run terraform plan (should show ownership transition warning)
// 4. Apply terraform (k8sconnect takes ownership back)
//
// Note: Warnings appear in terraform plan output but are not directly testable via the testing framework.
// This test verifies the end-to-end behavior succeeds.
func TestAccFieldOwnershipTransitionVisibility(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ownership-visibility-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("ownership-visibility-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ssaClient := testhelpers.NewSSATestClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment with k8sconnect
			{
				Config: testAccConfigFieldOwnershipVisibility(ns, deployName, 2),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 2: External kubectl patches spec.replicas and takes ownership
			// Then plan+apply (will show ownership transition warning during plan)
			{
				PreConfig: func() {
					ctx := context.Background()
					// Use kubectl with SSA force=true to take ownership of spec.replicas
					err := ssaClient.ForceApplyDeploymentReplicasSSA(ctx, ns, deployName, 3, "kubectl-patch")
					if err != nil {
						t.Fatalf("Failed to patch deployment with kubectl: %v", err)
					}
					t.Logf("✓ kubectl-patch took ownership of spec.replicas")
					t.Logf("  Next plan will show ownership transition warning: kubectl-patch → k8sconnect")
				},
				Config: testAccConfigFieldOwnershipVisibility(ns, deployName, 2),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// Ownership transition triggers plan warning, but resource still applies successfully
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectNonEmptyPlan(),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					// After apply, replicas should be back to 2 (terraform's value)
					// and k8sconnect will have taken ownership back via force=true
					testhelpers.CheckDeploymentReplicaCount(k8sClient.(*kubernetes.Clientset), ns, deployName, 2),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccConfigFieldOwnershipVisibility(namespace, deployName string, replicas int) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

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
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
YAML

  cluster = {
    kubeconfig = var.raw
  }
}
`, namespace, deployName, namespace, replicas)
}
