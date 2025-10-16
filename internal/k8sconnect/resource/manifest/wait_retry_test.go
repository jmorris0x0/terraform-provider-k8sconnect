// internal/k8sconnect/resource/manifest/wait_retry_test.go
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

// TestAccManifestResource_WaitRetryAfterTimeout verifies that wait conditions
// retry on subsequent applies even when config doesn't change
func TestAccManifestResource_WaitRetryAfterTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("wait-retry-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("test-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with wait that will timeout
			// ConfigMap has status="pending", wait expects "ready"
			// We expect this to fail but save state
			{
				Config: testAccManifestConfigWaitRetry(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				// Wait will timeout - expect error but state should be saved
				ExpectError: regexp.MustCompile("Wait condition failed|timeout"),
			},
			// Step 2: Fix the condition externally, then apply again WITHOUT config change
			// This tests that wait retries even when config is unchanged
			{
				PreConfig: func() {
					// First verify ConfigMap was created in Step 1 (proves state was saved)
					t.Logf("Verifying ConfigMap exists from Step 1...")
					cm, err := k8sClient.CoreV1().ConfigMaps(ns).Get(context.Background(), cmName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("ConfigMap should exist from Step 1 (state should have been saved): %v", err)
					}
					t.Logf("ConfigMap exists: %s/%s with data: %v", ns, cmName, cm.Data)

					// Now update ConfigMap to have status="ready"
					t.Logf("Updating ConfigMap to status=ready...")
					updateConfigMapData(t, k8sClient, ns, cmName, map[string]string{
						"status": "ready",
					})
				},
				Config: testAccManifestConfigWaitRetry(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					// Wait should succeed this time
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_wait", "id"),
					// Status should be populated
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_wait", "status.status", "ready"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigWaitRetry(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "cm_name" { type = string }
provider "k8sconnect" {}

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

resource "k8sconnect_manifest" "test_wait" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  status: "pending"
YAML

  # Wait for status to be "ready" (won't happen until we fix it externally)
  wait_for = {
    field = "data.status"
    field_value = "ready"
    timeout = "2s"
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_manifest.namespace]
}
`, namespace, cmName, namespace)
}

// updateConfigMapData updates a ConfigMap's data directly via K8s API
func updateConfigMapData(t *testing.T, client kubernetes.Interface, namespace, name string, data map[string]string) {
	ctx := context.Background()

	cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get ConfigMap for update: %v", err)
	}

	cm.Data = data

	_, err = client.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	t.Logf("Updated ConfigMap %s/%s data to %v", namespace, name, data)
}
