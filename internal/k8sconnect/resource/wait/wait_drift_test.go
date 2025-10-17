// internal/k8sconnect/resource/wait/wait_drift_test.go
package wait_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
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
	"k8s.io/client-go/kubernetes"
)

// TestAccWaitResource_FieldDriftDetection tests that field waits refresh status
// and detect when the waited-for field value changes externally
func TestAccWaitResource_FieldDriftDetection(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("wait-drift-ns-%d", time.Now().UnixNano()%1000000)
	jobName := fmt.Sprintf("wait-drift-job-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 0: Create Job and wait for it to complete
			{
				Config: testAccWaitConfigFieldDrift(ns, jobName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckJobExists(k8sClient, ns, jobName),
					// Wait resource should capture the succeeded count
					resource.TestCheckResourceAttrSet("k8sconnect_wait.field_wait", "id"),
					checkJobStatusSucceeded(t, "k8sconnect_wait.field_wait", 1),
				),
			},
			// Step 1: Externally modify the Job status (simulate drift)
			{
				Config: testAccWaitConfigFieldDrift(ns, jobName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					modifyJobSucceededCount(t, k8sClient, ns, jobName, 999),
				),
			},
			// Step 2: Refresh should detect the drift
			{
				Config: testAccWaitConfigFieldDrift(ns, jobName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// EXPECTED: Wait resource status should reflect the modified count
					checkJobStatusSucceeded(t, "k8sconnect_wait.field_wait", 999),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckJobDestroy(k8sClient, ns, jobName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

// Helper function to modify Job succeeded count
func modifyJobSucceededCount(t *testing.T, client kubernetes.Interface, namespace, name string, count int32) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Get current Job
		job, err := client.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get job %s/%s: %v", namespace, name, err)
		}

		// Modify the status
		job.Status.Succeeded = count

		// Update status subresource
		_, err = client.BatchV1().Jobs(namespace).UpdateStatus(ctx, job, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to modify job status: %v", err)
		}

		t.Logf("✅ Externally modified job %s/%s succeeded count to %d (simulating drift)", namespace, name, count)
		return nil
	}
}

// Helper to check Job succeeded count in wait resource status
func checkJobStatusSucceeded(t *testing.T, resourceName string, expectedCount int32) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource not found: %s", resourceName)
		}

		// Status is stored as flattened attributes
		succeededAttr := rs.Primary.Attributes["status.succeeded"]
		if succeededAttr == "" {
			t.Logf("Available status attributes:")
			for k, v := range rs.Primary.Attributes {
				if k == "status" || k[:7] == "status." {
					t.Logf("  %s = %s", k, v)
				}
			}
			return fmt.Errorf("status.succeeded not found in state - expected %d", expectedCount)
		}

		count, err := strconv.ParseInt(succeededAttr, 10, 32)
		if err != nil {
			return fmt.Errorf("failed to parse succeeded count %q: %v", succeededAttr, err)
		}

		if int32(count) != expectedCount {
			return fmt.Errorf("Job succeeded count is %d, expected %d", count, expectedCount)
		}

		t.Logf("✅ Wait resource has correct Job succeeded count: %d", count)
		return nil
	}
}

// Terraform config for drift test
func testAccWaitConfigFieldDrift(namespace, jobName string) string {
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

resource "k8sconnect_object" "test_job" {
  yaml_body = <<YAML
apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
spec:
  template:
    spec:
      containers:
      - name: test
        image: busybox:1.28
        command: ["sh", "-c", "echo success && exit 0"]
      restartPolicy: Never
  backoffLimit: 1
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}

resource "k8sconnect_wait" "field_wait" {
  object_ref = k8sconnect_object.test_job.object_ref
  wait_for = {
    field = "status.succeeded"
    timeout = "60s"
  }
  cluster_connection = { kubeconfig = var.raw }
}
`, namespace, jobName, namespace)
}
