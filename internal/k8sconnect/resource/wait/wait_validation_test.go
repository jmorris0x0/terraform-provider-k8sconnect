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
)

// TestAccWaitResource_NegativeTimeout tests that negative timeout values are rejected
// Issue #2 from SOAKTEST.md
func TestAccWaitResource_NegativeTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	namespace := fmt.Sprintf("negative-timeout-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigNegativeTimeout(namespace),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("(?i)timeout must be positive|invalid.*timeout|negative.*not allowed"),
			},
		},
	})
}

func testAccWaitConfigNegativeTimeout(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "test_namespace" {
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
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
  namespace: %s
data:
  key: value
YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.test_namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_object.test.object_ref

  cluster = {
    kubeconfig = var.raw
  }

  wait_for = {
    field = "metadata.name"
    timeout = "-5s"  # BUG: This should be rejected but isn't
  }
}
`, namespace, namespace)
}

// TestAccWaitResource_ZeroTimeout tests that zero timeout values are rejected
// Issue #4 from SOAKTEST.md
func TestAccWaitResource_ZeroTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	namespace := fmt.Sprintf("zero-timeout-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigZeroTimeout(namespace),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("(?i)timeout must be positive|invalid.*timeout|zero.*not allowed"),
			},
		},
	})
}

func testAccWaitConfigZeroTimeout(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "test_namespace" {
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
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
  namespace: %s
data:
  key: value
YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.test_namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_object.test.object_ref

  cluster = {
    kubeconfig = var.raw
  }

  wait_for = {
    field = "metadata.name"
    timeout = "0s"  # BUG: This should be rejected but isn't
  }
}
`, namespace, namespace)
}

// TestAccWaitResource_RolloutOnNonRolloutResource tests that rollout is rejected on non-rollout resources
// Issue #3 from SOAKTEST.md
func TestAccWaitResource_RolloutOnNonRolloutResource(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	namespace := fmt.Sprintf("rollout-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigRolloutOnConfigMap(namespace),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("(?i)rollout.*not supported|rollout.*only.*Deployment|ConfigMap.*does not support.*rollout"),
			},
		},
	})
}

func testAccWaitConfigRolloutOnConfigMap(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "test_namespace" {
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
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
  namespace: %s
data:
  key: value
YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.test_namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_object.test.object_ref

  cluster = {
    kubeconfig = var.raw
  }

  wait_for = {
    rollout = true  # BUG: This should be rejected for ConfigMap but isn't
  }
}
`, namespace, namespace)
}
