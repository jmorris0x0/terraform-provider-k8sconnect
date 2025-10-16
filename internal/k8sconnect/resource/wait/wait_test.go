// internal/k8sconnect/resource/wait/wait_test.go

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

// TestAccWaitResource_WaitForFieldExists tests waiting for a field to exist
func TestAccWaitResource_WaitForFieldExists(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-field-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigWaitForField(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, nsName),
					// Status should be populated on wait resource
					resource.TestCheckResourceAttr("k8sconnect_wait.test", "status.phase", "Active"),
					// Check output that uses the status from wait resource
					resource.TestCheckOutput("namespace_ready", "true"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, nsName),
	})
}

func testAccWaitConfigWaitForField(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
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

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_manifest.test.object_ref

  cluster_connection = {
    kubeconfig = var.raw
  }

  wait_for = {
    field = "status.phase"  # Wait for phase field to exist
  }
}

output "namespace_ready" {
  value = k8sconnect_wait.test.status.phase == "Active"
}
`, name)
}

// TestAccWaitResource_WaitForFieldValue tests waiting for specific field values
func TestAccWaitResource_WaitForFieldValue(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-value-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigWaitForValue(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, nsName),
					// field_value wait doesn't populate status per ADR-008
					resource.TestCheckNoResourceAttr("k8sconnect_wait.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, nsName),
	})
}

func testAccWaitConfigWaitForValue(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
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

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_manifest.test.object_ref

  cluster_connection = {
    kubeconfig = var.raw
  }

  wait_for = {
    field_value = {
      "status.phase" = "Active"  # Wait for specific value
    }
  }
}
`, name)
}

// TestAccWaitResource_WaitForCondition tests waiting for Kubernetes conditions
func TestAccWaitResource_WaitForCondition(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("wait-cond-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("wait-cond-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigWaitForCondition(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Condition wait doesn't populate status
					resource.TestCheckNoResourceAttr("k8sconnect_wait.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccWaitConfigWaitForCondition(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_namespace" {
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
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        ports:
        - containerPort: 80
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_manifest.test.object_ref

  cluster_connection = {
    kubeconfig = var.raw
  }

  wait_for = {
    # Wait for the Progressing condition to be True
    condition = "Progressing"
    timeout = "2m"
  }
}
`, namespace, name, namespace, name, name)
}

// TestAccWaitResource_ExplicitRollout tests explicit rollout waiting for Deployments
func TestAccWaitResource_ExplicitRollout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("explicit-rollout-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("explicit-rollout-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigExplicitRollout(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Rollout wait doesn't populate status
					resource.TestCheckNoResourceAttr("k8sconnect_wait.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccWaitConfigExplicitRollout(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_namespace" {
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
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 2
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        ports:
        - containerPort: 80
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_manifest.test.object_ref

  cluster_connection = {
    kubeconfig = var.raw
  }

  wait_for = {
    rollout = true
  }
}
`, namespace, name, namespace, name, name)
}

// TestAccWaitResource_WaitTimeout tests timeout behavior
func TestAccWaitResource_WaitTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("wait-timeout-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("wait-timeout-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigWaitTimeout(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					// ConfigMap should exist even though wait timed out
					// Status should be null since field doesn't exist
					resource.TestCheckNoResourceAttr("k8sconnect_wait.test", "status"),
				),
				ExpectError: regexp.MustCompile("Wait Operation Failed"),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
	})
}

func testAccWaitConfigWaitTimeout(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_namespace" {
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
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  test: value
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_manifest.test.object_ref

  cluster_connection = {
    kubeconfig = var.raw
  }

  wait_for = {
    field = "status.impossibleField"  # This will never exist
    timeout = "5s"  # Short timeout
  }
}
`, namespace, name, namespace)
}

// TestAccWaitResource_WaitForPVCBinding tests waiting for PersistentVolumeClaim binding
func TestAccWaitResource_WaitForPVCBinding(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("wait-pvc-ns-%d", time.Now().UnixNano()%1000000)
	pvcName := fmt.Sprintf("wait-pvc-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigWaitForPVC(ns, pvcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
					// field_value wait doesn't populate status per ADR-008
					resource.TestCheckNoResourceAttr("k8sconnect_wait.pvc", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckPVCDestroy(k8sClient, ns, pvcName),
	})
}

func testAccWaitConfigWaitForPVC(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_namespace" {
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

# First create a PersistentVolume
resource "k8sconnect_manifest" "pv" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: %s-pv
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Delete
  storageClassName: manual
  hostPath:
    path: /tmp/%s
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}

# Then create PVC
resource "k8sconnect_manifest" "pvc" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: manual
  resources:
    requests:
      storage: 1Gi
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.pv, k8sconnect_manifest.test_namespace]
}

# Separate wait resource waits for binding
resource "k8sconnect_wait" "pvc" {
  object_ref = k8sconnect_manifest.pvc.object_ref

  cluster_connection = {
    kubeconfig = var.raw
  }

  wait_for = {
    field_value = {
      "status.phase" = "Bound"
    }
    timeout = "30s"
  }
}
`, namespace, name, name, name, namespace)
}

// TestAccWaitResource_WaitForMultipleValues tests waiting for multiple field values
func TestAccWaitResource_WaitForMultipleValues(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("multi-wait-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("multi-wait-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigMultipleValues(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// field_value wait doesn't populate status per ADR-008
					resource.TestCheckNoResourceAttr("k8sconnect_wait.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccWaitConfigMultipleValues(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_namespace" {
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
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 2
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        ports:
        - containerPort: 80
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_manifest.test.object_ref

  cluster_connection = {
    kubeconfig = var.raw
  }

  wait_for = {
    field_value = {
      "status.replicas" = "2"
      "status.readyReplicas" = "2"
    }
  }
}
`, namespace, name, namespace, name, name)
}

// TestAccWaitResource_StatefulSetRollout tests explicit rollout for StatefulSets
func TestAccWaitResource_StatefulSetRollout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("sts-rollout-ns-%d", time.Now().UnixNano()%1000000)
	stsName := fmt.Sprintf("sts-rollout-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigStatefulSetRollout(ns, stsName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckStatefulSetExists(k8sClient, ns, stsName),
					// Rollout wait doesn't populate status
					resource.TestCheckNoResourceAttr("k8sconnect_wait.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckStatefulSetDestroy(k8sClient, ns, stsName),
	})
}

func testAccWaitConfigStatefulSetRollout(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_namespace" {
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
  yaml_body = <<YAML
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: %s
  namespace: %s
spec:
  serviceName: %s
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        ports:
        - containerPort: 80
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_manifest.test.object_ref

  cluster_connection = {
    kubeconfig = var.raw
  }

  wait_for = {
    rollout = true
  }
}
`, namespace, name, namespace, name, name, name)
}

// TestAccWaitResource_InvalidFieldPath tests error handling for invalid field paths
func TestAccWaitResource_InvalidFieldPath(t *testing.T) {
	t.Parallel()
	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}
	ns := fmt.Sprintf("invalid-path-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("invalid-path-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigInvalidPath(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				// Should fail due to invalid JSONPath syntax
				ExpectError: regexp.MustCompile("(?i)invalid.*path|parse error|unterminated"),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
	})
}

func testAccWaitConfigInvalidPath(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_namespace" {
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
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  test: value
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}

resource "k8sconnect_wait" "test" {
  object_ref = k8sconnect_manifest.test.object_ref

  cluster_connection = {
    kubeconfig = var.raw
  }

  wait_for = {
    field = "status.field["  # Invalid - unclosed bracket
    timeout = "5s"
  }
}
`, namespace, name, namespace)
}
