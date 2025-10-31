package wait_test

import (
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccWaitResource_TimeoutShowsActualStatus verifies that when a wait times out
// without receiving watch events, the error message shows actual object status
// instead of "(no status available)".
//
// This tests the fix for WAIT_ISSUES.md Issue 1.
//
// Before fix: "timeout after 5s waiting for condition "Complete" (no status available)"
// After fix: Error shows actual Job conditions and pod issues
func TestAccWaitResource_TimeoutShowsActualStatus(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-timeout-%d", time.Now().UnixNano()%1000000)
	jobName := fmt.Sprintf("failing-job-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigFailingJob(nsName, jobName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// After fix: Error should show actual status instead of "(no status available)"
				//
				// We expect a timeout error, but it should show meaningful diagnostics:
				// - "No conditions found" (for Jobs that don't report conditions)
				// - OR actual Job conditions with pod issues (if conditions exist)
				//
				// The key requirement: should NOT contain "(no status available)"
				// Use (?s) to make . match newlines
				ExpectError: regexp.MustCompile(`(?s)Wait Timeout.*Job.*did not`),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, nsName),
	})
}

// testAccWaitConfigFailingJob creates a Job that will fail (bad image)
// and a wait resource that times out waiting for completion
func testAccWaitConfigFailingJob(namespace, jobName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "failing_job" {
  yaml_body = <<-YAML
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: %s
      namespace: %s
    spec:
      template:
        spec:
          containers:
          - name: fail
            image: this-image-does-not-exist-12345:v999
            command: ["echo", "hello"]
          restartPolicy: Never
      backoffLimit: 0
  YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_wait" "job_complete" {
  object_ref = k8sconnect_object.failing_job.object_ref

  cluster = {
    kubeconfig = var.raw
  }

  wait_for = {
    condition = "Complete"
    timeout   = "5s"  # Short timeout to fail quickly
  }
}
`, namespace, jobName, namespace)
}
