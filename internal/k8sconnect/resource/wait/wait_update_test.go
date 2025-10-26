package wait_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccWaitResource_UpdateWaitConfiguration tests updating the wait_for configuration
// This verifies that the Update method correctly re-performs the wait with new settings
func TestAccWaitResource_UpdateWaitConfiguration(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("wait-update-%d", time.Now().UnixNano()%1000000)
	configMapName := fmt.Sprintf("wait-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Step 1: Wait for field "data.key1" to exist
				Config: testAccWaitConfigUpdateStep1(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					resource.TestCheckResourceAttr("k8sconnect_wait.test", "object_ref.kind", "ConfigMap"),
					resource.TestCheckResourceAttr("k8sconnect_wait.test", "wait_for.field", "data.key1"),
				),
			},
			{
				// Step 2: Update to wait for different field "data.key2"
				// This triggers Update() which re-performs the wait
				Config: testAccWaitConfigUpdateStep2(ns, configMapName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					resource.TestCheckResourceAttr("k8sconnect_wait.test", "object_ref.kind", "ConfigMap"),
					resource.TestCheckResourceAttr("k8sconnect_wait.test", "wait_for.field", "data.key2"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

// TestAccWaitResource_UpdateFieldToCondition tests updating from waiting for a field to waiting for a condition
func TestAccWaitResource_UpdateFieldToCondition(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("wait-update-cond-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("deploy-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Step 1: Wait for replicas field
				Config: testAccWaitConfigUpdateFieldStep(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					resource.TestCheckResourceAttr("k8sconnect_wait.test", "wait_for.field", "status.replicas"),
				),
			},
			{
				// Step 2: Change to wait for condition
				// This is a common update pattern - first wait for basic field, then wait for condition
				Config: testAccWaitConfigUpdateConditionStep(ns, deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					resource.TestCheckResourceAttr("k8sconnect_wait.test", "wait_for.condition", "Available"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

// TestAccWaitResource_UpdateTimeout tests updating the timeout value
func TestAccWaitResource_UpdateTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("wait-update-timeout-%d", time.Now().UnixNano()%1000000)
	podName := fmt.Sprintf("pod-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Step 1: Wait with shorter timeout
				Config: testAccWaitConfigUpdateTimeoutStep1(ns, podName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					resource.TestCheckResourceAttr("k8sconnect_wait.test", "wait_for.timeout", "1m"),
				),
			},
			{
				// Step 2: Update to longer timeout
				Config: testAccWaitConfigUpdateTimeoutStep2(ns, podName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					resource.TestCheckResourceAttr("k8sconnect_wait.test", "wait_for.timeout", "2m"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

// Config templates

func testAccWaitConfigUpdateStep1(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "cm" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[2]s
  namespace: %[1]s
data:
  key1: "value1"
  key2: "value2"
YAML

  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_object.cm.object_ref

  wait_for = {
    field = "data.key1"
  }

  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.cm]
}
`, namespace, cmName)
}

func testAccWaitConfigUpdateStep2(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "cm" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[2]s
  namespace: %[1]s
data:
  key1: "value1"
  key2: "value2"
YAML

  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_object.cm.object_ref

  wait_for = {
    field = "data.key2"  # Changed from key1 to key2
  }

  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.cm]
}
`, namespace, cmName)
}

func testAccWaitConfigUpdateFieldStep(namespace, deployName string) string {
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
      name: %[1]s
  YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "deploy" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: %[2]s
      namespace: %[1]s
    spec:
      replicas: 1
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
            image: public.ecr.aws/nginx/nginx:1.25
  YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = %[2]q
    namespace   = %[1]q
  }

  wait_for = {
    field = "status.replicas"
  }

  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.deploy]
}
`, namespace, deployName)
}

func testAccWaitConfigUpdateConditionStep(namespace, deployName string) string {
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
      name: %[1]s
  YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "deploy" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: %[2]s
      namespace: %[1]s
    spec:
      replicas: 1
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
            image: nginx:1.25
  YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = %[2]q
    namespace   = %[1]q
  }

  wait_for = {
    condition = "Available"
  }

  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.deploy]
}
`, namespace, deployName)
}

func testAccWaitConfigUpdateTimeoutStep1(namespace, podName string) string {
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
      name: %[1]s
  YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "pod" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Pod
    metadata:
      name: %[2]s
      namespace: %[1]s
    spec:
      containers:
      - name: nginx
        image: nginx:1.25
  YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = {
    api_version = "v1"
    kind        = "Pod"
    name        = %[2]q
    namespace   = %[1]q
  }

  wait_for = {
    condition = "Ready"
    timeout   = "1m"
  }

  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.pod]
}
`, namespace, podName)
}

func testAccWaitConfigUpdateTimeoutStep2(namespace, podName string) string {
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
      name: %[1]s
  YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "pod" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Pod
    metadata:
      name: %[2]s
      namespace: %[1]s
    spec:
      containers:
      - name: nginx
        image: nginx:1.25
  YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = {
    api_version = "v1"
    kind        = "Pod"
    name        = %[2]q
    namespace   = %[1]q
  }

  wait_for = {
    condition = "Ready"
    timeout   = "2m"  # Changed from 1m to 2m
  }

  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.pod]
}
`, namespace, podName)
}
