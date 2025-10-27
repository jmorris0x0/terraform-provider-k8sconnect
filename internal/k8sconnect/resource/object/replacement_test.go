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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccObjectResource_ReplacementRaceCondition verifies that when a resource is
// replaced (e.g., for_each key change) and both old and new instances map to the same
// K8s object, the delete of the old instance detects the replacement and skips deletion
// gracefully without timing out.
func TestAccObjectResource_ReplacementRaceCondition(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("replacement-race-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("replacement-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with k8sconnect_object
			{
				Config: testAccConfigReplacement_Initial(ns, cmName),
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
			// Step 2: Manually update terraform-id and remove resource from config
			// This simulates the race condition where SSA Apply happens during deletion
			{
				PreConfig: func() {
					// Manually update terraform-id annotation to simulate SSA replacement
					// This simulates what happens when a new Terraform instance (new for_each key)
					// creates/updates the same K8s object via SSA Apply
					cm, err := k8sClient.CoreV1().ConfigMaps(ns).Get(context.Background(), cmName, metav1.GetOptions{})
					if err != nil {
						panic(fmt.Errorf("failed to get ConfigMap: %w", err))
					}

					// Change the terraform-id to simulate replacement by different instance
					if cm.Annotations == nil {
						cm.Annotations = make(map[string]string)
					}
					cm.Annotations["k8sconnect.terraform.io/terraform-id"] = "replaced-id-000"

					_, err = k8sClient.CoreV1().ConfigMaps(ns).Update(context.Background(), cm, metav1.UpdateOptions{})
					if err != nil {
						panic(fmt.Errorf("failed to update ConfigMap annotations: %w", err))
					}
				},
				Config: testAccConfigReplacement_Removed(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					// ConfigMap should still exist because it was "replaced"
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					// Verify terraform-id is still the "replaced" value
					func(s *terraform.State) error {
						cm, err := k8sClient.CoreV1().ConfigMaps(ns).Get(context.Background(), cmName, metav1.GetOptions{})
						if err != nil {
							return fmt.Errorf("failed to get ConfigMap: %w", err)
						}

						if cm.Annotations["k8sconnect.terraform.io/terraform-id"] != "replaced-id-000" {
							return fmt.Errorf("expected terraform-id to be 'replaced-id-000', got: %s",
								cm.Annotations["k8sconnect.terraform.io/terraform-id"])
						}

						return nil
					},
				),
			},
		},
		CheckDestroy: func(s *terraform.State) error {
			// Manually clean up the ConfigMap that was left behind
			_ = k8sClient.CoreV1().ConfigMaps(ns).Delete(context.Background(), cmName, metav1.DeleteOptions{})
			return testhelpers.CheckNamespaceDestroy(k8sClient, ns)(s)
		},
	})
}

func testAccConfigReplacement_Initial(namespace, cmName string) string {
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

resource "k8sconnect_object" "replacement_namespace" {
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

resource "k8sconnect_object" "test_replacement" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: original-value
YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.replacement_namespace]
}`, namespace, cmName, namespace)
}

func testAccConfigReplacement_Removed(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "replacement_namespace" {
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
# ConfigMap resource removed - this triggers deletion
`, namespace)
}
