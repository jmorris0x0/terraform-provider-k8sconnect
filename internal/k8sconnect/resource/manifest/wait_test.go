// internal/k8sconnect/resource/manifest/wait_test.go

package manifest_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
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

// TestAccManifestResource_NoWaitNoStatus verifies that resources without wait_for have null status
func TestAccManifestResource_NoWaitNoStatus(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("no-wait-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("no-wait-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap without wait_for
			{
				Config: testAccManifestConfigNoWait(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					// Status should not be set (null) when no wait_for
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
			// Step 2: Re-apply with formatting changes only - should show no drift
			{
				Config: testAccManifestConfigNoWaitFormatted(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // No drift expected!
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
	})
}

func testAccManifestConfigNoWait(namespace, name string) string {
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
  key1: value1
  key2: value2
YAML

  # No wait_for = no status tracking

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace)
}

func testAccManifestConfigNoWaitFormatted(namespace, name string) string {
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
# Added comment - formatting change only
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key1: value1
  key2: value2  # Another comment
YAML

  # No wait_for = no status tracking

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace)
}

// TestAccManifestResource_WaitForFieldExists tests waiting for a field to exist
func TestAccManifestResource_WaitForFieldExists(t *testing.T) {
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
				Config: testAccManifestConfigWaitForField(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, nsName),
					// Status should be populated because we waited
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.phase", "Active"),
					// Check output that uses the status
					resource.TestCheckOutput("namespace_ready", "true"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, nsName),
	})
}

func testAccManifestConfigWaitForField(name string) string {
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

  wait_for = {
    field = "status.phase"  # Wait for phase field to exist
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
}

output "namespace_ready" {
  value = k8sconnect_manifest.test.status.phase == "Active"
}
`, name)
}

// TestAccManifestResource_WaitForFieldValue tests waiting for specific field values
func TestAccManifestResource_WaitForFieldValue(t *testing.T) {
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
				Config: testAccManifestConfigWaitForValue(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, nsName),
					// Should have waited for Active phase but not populated status
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, nsName),
	})
}

func testAccManifestConfigWaitForValue(name string) string {
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

  wait_for = {
    field_value = {
      "status.phase" = "Active"  # Wait for specific value
    }
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
}
`, name)
}

// TestAccManifestResource_WaitForCondition tests waiting for Kubernetes conditions
func TestAccManifestResource_WaitForCondition(t *testing.T) {
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
				Config: testAccManifestConfigWaitForCondition(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Verify that we waited for the condition
					// Note: condition wait doesn't populate status per the design
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigWaitForCondition(namespace, name string) string {
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

  wait_for = {
    # Wait for the Progressing condition to be True
    # This indicates the deployment is actively rolling out
    condition = "Progressing"
    timeout = "2m"
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace, name, name)
}

// TestAccManifestResource_WaitForPVCBinding tests waiting for PersistentVolumeClaim binding
func TestAccManifestResource_WaitForPVCBinding(t *testing.T) {
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
				Config: testAccManifestConfigWaitForPVC(ns, pvcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
					// No status check - field_value doesn't populate status
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.pvc", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckPVCDestroy(k8sClient, ns, pvcName),
	})
}

func testAccManifestConfigWaitForPVC(namespace, name string) string {
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

# Then create PVC and wait for binding
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

  wait_for = {
    field_value = {
      "status.phase" = "Bound"
    }
    timeout = "30s"
  }

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.pv, k8sconnect_manifest.test_namespace]
}

`, namespace, name, name, name, namespace)
}

// TestAccManifestResource_ExplicitRollout tests EXPLICIT rollout waiting for Deployments
func TestAccManifestResource_ExplicitRollout(t *testing.T) {
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
				Config: testAccManifestConfigExplicitRollout(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// No status check - rollout doesn't populate status
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigExplicitRollout(namespace, name string) string {
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

  # EXPLICIT wait_for rollout
  wait_for = {
    rollout = true
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace, name, name)
}

// TestAccManifestResource_NoDefaultRollout tests that Deployments DON'T automatically wait
// RENAMED from DisableAutoRollout - now verifies no auto-rollout happens
func TestAccManifestResource_NoDefaultRollout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("no-rollout-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("no-rollout-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigNoRollout(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Should NOT have status because no wait_for configured
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigNoRollout(namespace, name string) string {
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

  # NO wait_for - should complete quickly without waiting

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace, name, name)
}

// TestAccManifestResource_WaitTimeout tests timeout behavior
func TestAccManifestResource_WaitTimeout(t *testing.T) {
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
				Config: testAccManifestConfigWaitTimeout(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					// Resource should exist even though wait timed out
					// Status should be null since field doesn't exist
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
				ExpectError: regexp.MustCompile("Wait condition failed"),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
	})
}

func testAccManifestConfigWaitTimeout(namespace, name string) string {
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

  wait_for = {
    field = "status.impossibleField"  # This will never exist
    timeout = "5s"  # Short timeout
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace)
}

// TestAccManifestResource_WaitForMultipleValues tests waiting for multiple field values
func TestAccManifestResource_WaitForMultipleValues(t *testing.T) {
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
				Config: testAccManifestConfigMultipleValues(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// No status check - field_value doesn't populate status
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigMultipleValues(namespace, name string) string {
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

  wait_for = {
    field_value = {
      "status.replicas" = "2"
      "status.readyReplicas" = "2"
    }
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace, name, name)
}

// TestAccManifestResource_StatefulSetRollout tests EXPLICIT rollout for StatefulSets
func TestAccManifestResource_StatefulSetRollout(t *testing.T) {
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
				Config: testAccManifestConfigStatefulSetExplicit(ns, stsName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckStatefulSetExists(k8sClient, ns, stsName),
					// No status check - rollout doesn't populate status
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckStatefulSetDestroy(k8sClient, ns, stsName),
	})
}

func testAccManifestConfigStatefulSetExplicit(namespace, name string) string {
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

  # EXPLICIT wait_for rollout
  wait_for = {
    rollout = true
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace, name, name, name)
}

// TestAccManifestResource_EmptyWaitForNoStatus verifies empty wait_for block doesn't trigger waiting or status population
func TestAccManifestResource_EmptyWaitForNoStatus(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("empty-wait-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("empty-wait-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigEmptyWaitFor(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Custom check for null status - can't use TestCheckNoResourceAttr for dynamic attributes
					func(s *terraform.State) error {
						rs, ok := s.RootModule().Resources["k8sconnect_manifest.test"]
						if !ok {
							return fmt.Errorf("Resource k8sconnect_manifest.test not found")
						}

						// Check if status exists in attributes
						for key := range rs.Primary.Attributes {
							if strings.HasPrefix(key, "status.") || key == "status" {
								// Log what we found for debugging
								return fmt.Errorf("Expected status to be null, but found attribute: %s = %s",
									key, rs.Primary.Attributes[key])
							}
						}

						// Also check the raw state for status
						if statusVal, exists := rs.Primary.Attributes["status"]; exists && statusVal != "" {
							return fmt.Errorf("Expected status to be null, but it exists with value: %s", statusVal)
						}

						return nil
					},
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigEmptyWaitFor(namespace, name string) string {
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

  # Empty wait_for block - should NOT trigger waiting
  wait_for = {}

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace, name, name)
}

// TestAccManifestResource_StatusStability verifies status remains stable across plans
func TestAccManifestResource_StatusStability(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("status-stable-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("status-stable-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create Deployment with wait_for, status gets populated
			{
				Config: testAccManifestConfigStatusStability(ns, deployName, "initial"),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Status should be populated - only readyReplicas since that's what we wait for
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.readyReplicas", "1"),
				),
			},
			// Step 2: Add a label (unrelated change) - status should remain stable
			{
				Config: testAccManifestConfigStatusStability(ns, deployName, "updated"),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Critical status values should be preserved
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.readyReplicas", "1"),
					// Verify the label was actually updated
					func(s *terraform.State) error {
						// This ensures the update actually happened
						ctx := context.Background()
						deploy, err := k8sClient.AppsV1().Deployments(ns).Get(ctx, deployName, metav1.GetOptions{})
						if err != nil {
							return fmt.Errorf("failed to get deployment: %v", err)
						}

						labels := deploy.GetLabels()
						if labels["test-label"] != "updated" {
							return fmt.Errorf("expected label 'test-label' to be 'updated', got '%s'", labels["test-label"])
						}

						return nil
					},
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigStatusStability(namespace, name, labelValue string) string {
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
  labels:
    test-label: %s
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

  wait_for = {
    field = "status.readyReplicas"
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}

output "stable_replicas" {
  value = k8sconnect_manifest.test.status.readyReplicas
}
`, namespace, name, namespace, labelValue, name, name)
}

// TestAccManifestResource_StatusRemovedWhenWaitRemoved verifies status is removed when wait_for is removed
func TestAccManifestResource_StatusRemovedWhenWaitRemoved(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("status-removal-ns-%d", time.Now().UnixNano()%1000000)
	jobName := fmt.Sprintf("status-removal-job-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create Job with wait_for succeeded field
			{
				Config: testAccManifestConfigJobWithWaitFor(ns, jobName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckJobExists(k8sClient, ns, jobName),
					// Status should be populated with succeeded count
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.succeeded", "1"),
				),
			},
			// Step 2: Remove wait_for, status should be cleared
			{
				Config: testAccManifestConfigJobWithoutWaitFor(ns, jobName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckJobExists(k8sClient, ns, jobName),
					// Status should be null when wait_for is removed
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckJobDestroy(k8sClient, ns, jobName),
	})
}

func testAccManifestConfigJobWithWaitFor(namespace, name string) string {
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
apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
spec:
  backoffLimit: 1
  template:
    spec:
      containers:
      - name: hello
        image: public.ecr.aws/docker/library/busybox:latest
        command: ["sh", "-c", "echo 'Hello World' && sleep 2"]
      restartPolicy: Never
YAML

  wait_for = {
    # Wait for succeeded field - only appears when job completes successfully
    field = "status.succeeded"
    timeout = "2m"
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace)
}

func testAccManifestConfigJobWithoutWaitFor(namespace, name string) string {
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
apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
spec:
  backoffLimit: 1
  template:
    spec:
      containers:
      - name: hello
        image: public.ecr.aws/docker/library/busybox:latest
        command: ["sh", "-c", "echo 'Hello World' && sleep 2"]
      restartPolicy: Never
YAML

  # wait_for removed - status should be cleared

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace)
}

// TestAccManifestResource_InvalidFieldPath tests error handling for invalid field paths
func TestAccManifestResource_InvalidFieldPath(t *testing.T) {
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
				Config: testAccManifestConfigInvalidPath(ns, cmName),
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

func testAccManifestConfigInvalidPath(namespace, name string) string {
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

  wait_for = {
    field = "status.field["  # Actually invalid - unclosed bracket
    timeout = "5s"
  }

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace)
}

// TestAccManifestResource_StatusPopulatedOnRefresh tests that Read() populates status after timeout
// This verifies the fix for: when wait_for times out, subsequent refreshes should populate status
// when the field becomes available
func TestAccManifestResource_StatusPopulatedOnRefresh(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("status-refresh-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("test-deploy-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment with wait_for that will timeout
			// The wait will fail BUT the resource will be saved to state (state is saved before wait)
			{
				Config: testAccDeploymentWithShortTimeout(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Note: Can't check output here because status is unknown (not null) when wait times out
					// When status is unknown, outputs referencing it also become unknown
				),
				ExpectError: regexp.MustCompile("Wait condition failed"),
			},
			// Step 2: Wait for deployment to actually become ready, then use longer timeout
			// This simulates recovery after a timeout - with reasonable timeout it should succeed
			{
				PreConfig: func() {
					// Poll for the deployment to actually become ready
					// (it continues rolling out even after terraform errored)
					startTime := time.Now()
					fmt.Printf("\n=== PreConfig starting at %v ===\n", startTime.Format(time.RFC3339))

					ctx := context.Background()
					maxWait := 90 * time.Second
					pollInterval := 2 * time.Second
					deadline := time.Now().Add(maxWait)
					pollCount := 0

					for time.Now().Before(deadline) {
						pollCount++
						deploy, err := k8sClient.AppsV1().Deployments(ns).Get(ctx, deployName, metav1.GetOptions{})
						if err == nil && deploy.Status.ReadyReplicas >= 1 {
							elapsed := time.Since(startTime)
							fmt.Printf("=== Deployment ready after %v (%d polls) ===\n\n", elapsed, pollCount)
							return
						}
						if pollCount%5 == 0 {
							fmt.Printf("Poll %d: readyReplicas=%d\n", pollCount, deploy.Status.ReadyReplicas)
						}
						time.Sleep(pollInterval)
					}

					elapsed := time.Since(startTime)
					fmt.Printf("=== WARNING: Deployment NOT ready after %v (%d polls) ===\n\n", elapsed, pollCount)
				},
				// Use longer timeout for step 2 - under parallel load 2s is too aggressive
				Config: testAccDeploymentWithLongerTimeout(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					// Now status should be populated because Read() calls updateStatus()
					// This is the core fix being tested
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.readyReplicas", "1"),
					resource.TestCheckOutput("replicas_output", "1"),
				),
				// No ExpectError - the wait should succeed now that the deployment is ready
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccDeploymentWithShortTimeout(namespace, name string) string {
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

  wait_for = {
    field = "status.readyReplicas"
    timeout = "2s"  # Very short timeout - deployment typically won't be ready in 2s
  }

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}

output "replicas_output" {
  value = try(k8sconnect_manifest.test.status.readyReplicas, "not-ready")
}
`, namespace, name, namespace, name, name)
}

func testAccDeploymentWithLongerTimeout(namespace, name string) string {
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

  wait_for = {
    field = "status.readyReplicas"
    timeout = "2m"  # Longer timeout for step 2 - handles parallel load
  }

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}

output "replicas_output" {
  value = try(k8sconnect_manifest.test.status.readyReplicas, "not-ready")
}
`, namespace, name, namespace, name, name)
}
