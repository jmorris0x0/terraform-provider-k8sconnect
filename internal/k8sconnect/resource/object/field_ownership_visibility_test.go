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

// TestFieldOwnershipTransitionVisibility validates that ownership transitions
// are visible in the Terraform plan diff. This is a CRITICAL feature that took
// hundreds of hours to build.
//
// This test MUST fail if any fix hides the ownership transition.
//
// Scenario:
// 1. Create a deployment with k8sconnect (k8sconnect owns all fields)
// 2. External kubectl patches spec.replicas using SSA with force=true (kubectl-patch takes ownership)
// 3. Run terraform plan (should show field_ownership transition: "kubectl-patch" -> "k8sconnect")
// 4. Apply terraform (k8sconnect takes ownership back)
//
// The critical requirement: Step 3 MUST show the ownership transition in the plan output.
func TestFieldOwnershipTransitionVisibility(t *testing.T) {
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
					// Verify k8sconnect owns spec.replicas initially
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.spec.replicas", "k8sconnect"),
				),
			},
			// Step 2: External kubectl patches spec.replicas and takes ownership
			// Then plan (should show ownership transition)
			{
				PreConfig: func() {
					ctx := context.Background()
					// Use kubectl with SSA force=true to take ownership of spec.replicas
					err := ssaClient.ForceApplyDeploymentReplicasSSA(ctx, ns, deployName, 3, "kubectl-patch")
					if err != nil {
						t.Fatalf("Failed to patch deployment with kubectl: %v", err)
					}
					t.Logf("✓ kubectl-patch took ownership of spec.replicas")
				},
				Config: testAccConfigFieldOwnershipVisibility(ns, deployName, 2),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// CRITICAL: This is the visibility test
				// The plan MUST show that field_ownership will change from "kubectl-patch" to "k8sconnect"
				// If this fails, it means the visibility feature is broken
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// Custom plan check that verifies ownership transition is visible
						&expectFieldOwnershipTransition{
							resourceAddress: "k8sconnect_object.test",
							fieldPath:       "spec.replicas",
							oldOwner:        "kubectl-patch",
							newOwner:        "k8sconnect",
						},
					},
				},
				Check: resource.ComposeTestCheckFunc(
					// After apply, k8sconnect should own spec.replicas again
					resource.TestCheckResourceAttr("k8sconnect_object.test", "field_ownership.spec.replicas", "k8sconnect"),
					// Replicas should be back to 2 (terraform's value)
					testhelpers.CheckDeploymentReplicaCount(k8sClient.(*kubernetes.Clientset), ns, deployName, 2),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

// expectFieldOwnershipTransition is a custom plan check that verifies ownership
// transitions are visible in the plan output.
//
// This is the guardian of the visibility feature. If this check fails, it means
// someone broke the field ownership visibility.
type expectFieldOwnershipTransition struct {
	resourceAddress string
	fieldPath       string
	oldOwner        string
	newOwner        string
}

func (e *expectFieldOwnershipTransition) CheckPlan(ctx context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	// Get the planned resource
	for _, rs := range req.Plan.ResourceChanges {
		if rs.Address == e.resourceAddress {
			// Found our resource
			// Check if field_ownership is changing
			if rs.Change == nil || rs.Change.After == nil {
				resp.Error = fmt.Errorf("resource %s has no planned changes", e.resourceAddress)
				return
			}

			// Get the field_ownership attribute from the change
			afterMap, ok := rs.Change.After.(map[string]interface{})
			if !ok {
				resp.Error = fmt.Errorf("resource %s after value is not a map", e.resourceAddress)
				return
			}

			fieldOwnership, ok := afterMap["field_ownership"].(map[string]interface{})
			if !ok {
				resp.Error = fmt.Errorf("field_ownership attribute not found or not a map in resource %s", e.resourceAddress)
				return
			}

			// Check the specific field's ownership
			actualOwner, ok := fieldOwnership[e.fieldPath].(string)
			if !ok {
				resp.Error = fmt.Errorf("field_ownership[%s] not found in resource %s", e.fieldPath, e.resourceAddress)
				return
			}

			// The critical check: After the plan, the ownership should be the NEW owner
			// But we also need to verify the TRANSITION is visible, which means
			// the plan should show it changing FROM oldOwner TO newOwner

			// Check if we're in a state where the plan is showing the transition
			// The planned value should be the new owner
			if actualOwner != e.newOwner {
				resp.Error = fmt.Errorf(
					"VISIBILITY FAILURE: Plan does not predict ownership transfer.\n"+
						"Expected field_ownership[%s] to transition to %q, but plan shows %q.\n"+
						"This means the visibility feature is broken - users cannot see ownership transfers.",
					e.fieldPath, e.newOwner, actualOwner)
				return
			}

			// Now verify the BEFORE state shows the old owner
			// We need to check the raw plan output to see the transition
			beforeMap, ok := rs.Change.Before.(map[string]interface{})
			if !ok {
				resp.Error = fmt.Errorf("resource %s before value is not a map", e.resourceAddress)
				return
			}

			beforeFieldOwnership, ok := beforeMap["field_ownership"].(map[string]interface{})
			if !ok {
				resp.Error = fmt.Errorf("field_ownership attribute not found in before state of resource %s", e.resourceAddress)
				return
			}

			beforeOwner, ok := beforeFieldOwnership[e.fieldPath].(string)
			if !ok {
				resp.Error = fmt.Errorf("field_ownership[%s] not found in before state of resource %s", e.fieldPath, e.resourceAddress)
				return
			}

			if beforeOwner != e.oldOwner {
				resp.Error = fmt.Errorf(
					"VISIBILITY FAILURE: Before state does not show current ownership.\n"+
						"Expected field_ownership[%s] to be %q (current owner), but state shows %q.\n"+
						"This means the plan is not showing the real current state.",
					e.fieldPath, e.oldOwner, beforeOwner)
				return
			}

			// SUCCESS: The plan shows the transition from oldOwner to newOwner
			// This is what the visibility feature is all about
			return
		}
	}

	resp.Error = fmt.Errorf("resource %s not found in plan", e.resourceAddress)
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

  cluster_connection = {
    kubeconfig = var.raw
  }
}
`, namespace, deployName, namespace, replicas)
}
