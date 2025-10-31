package wait_test

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

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// TestAccWaitResource_RetryAfterTimeout verifies that when a wait times out,
// re-running terraform apply retries the wait operation and succeeds if the
// condition is now met.
//
// This tests the critical retry behavior documented in WAIT_ISSUES.md Issue 2.
//
// Test flow:
// 1. Create ConfigMap without the expected field
// 2. Create wait resource that times out (field doesn't exist)
// 3. Verify apply fails with timeout error
// 4. Update ConfigMap to add the expected field (simulates fixing transient issue)
// 5. Re-run terraform apply
// 6. Verify wait succeeds on retry
func TestAccWaitResource_RetryAfterTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-retry-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("retry-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Step 1: Initial apply - wait will timeout because field doesn't exist
				Config: testAccWaitConfigRetryTimeout(nsName, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// Expect timeout error on first apply
				ExpectError: regexp.MustCompile(`(?s)Wait Operation Failed.*timeout`),
			},
			{
				// Step 2: Retry apply - wait should succeed now
				// PreConfig runs BEFORE terraform apply, allowing us to fix the condition
				PreConfig: func() {
					// Update ConfigMap to add the field we're waiting for
					// This simulates fixing a transient issue (e.g., image pull completes)
					if err := addDataFieldToConfigMap(k8sClient, nsName, cmName); err != nil {
						t.Fatalf("Failed to add data field: %v", err)
					}
				},
				Config: testAccWaitConfigRetryTimeout(nsName, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// No ExpectError - this should succeed
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, nsName, cmName),
					// Verify wait resource exists in state
					resource.TestCheckResourceAttrSet("k8sconnect_wait.retry_test", "id"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, nsName),
	})
}

func testAccWaitConfigRetryTimeout(namespace, name string) string {
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

resource "k8sconnect_object" "configmap" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
    data:
      initial: "value"
  YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_wait" "retry_test" {
  object_ref = k8sconnect_object.configmap.object_ref

  cluster = {
    kubeconfig = var.raw
  }

  wait_for = {
    field   = "data.ready"
    timeout = "5s"
  }
}
`, namespace, name, namespace)
}

// addDataFieldToConfigMap updates a ConfigMap to add the data.ready field
// This simulates fixing a transient issue between retry attempts
func addDataFieldToConfigMap(client kubernetes.Interface, namespace, name string) error {
	ctx := context.Background()

	// Get current ConfigMap
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	// Add data field (this is what we're waiting for)
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data["ready"] = "true"

	// Update the ConfigMap
	_, err = client.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ConfigMap: %w", err)
	}

	fmt.Printf("✅ Added data.ready field to ConfigMap %s/%s\n", namespace, name)
	return nil
}
