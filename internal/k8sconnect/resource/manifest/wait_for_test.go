// internal/k8sconnect/resource/manifest/wait_for_test.go

package manifest_test

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

// TestAccManifestResource_NoWaitNoStatus verifies that resources without wait_for have null status
func TestAccManifestResource_NoWaitNoStatus(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	cmName := fmt.Sprintf("no-wait-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap without wait_for
			{
				Config: testAccManifestConfigNoWait(cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, "default", cmName),
					// Status should not be set (null) when no wait_for
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status", ""),
				),
			},
			// Step 2: Re-apply with formatting changes only - should show no drift
			{
				Config: testAccManifestConfigNoWaitFormatted(cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // No drift expected!
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, "default", cmName),
	})
}

func testAccManifestConfigNoWait(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  key1: value1
  key2: value2
YAML

  # No wait_for = no status tracking

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name)
}

func testAccManifestConfigNoWaitFormatted(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
# Added comment - formatting change only
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  key1: value1
  key2: value2  # Another comment
YAML

  # No wait_for = no status tracking

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name)
}

// TestAccManifestResource_WaitForFieldExists tests waiting for a field to exist
func TestAccManifestResource_WaitForFieldExists(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-field-%d", time.Now().Unix())

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
    kubeconfig_raw = var.raw
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

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-value-%d", time.Now().Unix())

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
					// Should have waited for Active phase
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.phase", "Active"),
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
    kubeconfig_raw = var.raw
  }
}
`, name)
}

// TestAccManifestResource_WaitForCondition tests waiting for Kubernetes conditions
func TestAccManifestResource_WaitForCondition(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	cmName := fmt.Sprintf("wait-cond-%d", time.Now().Unix())

	// This test would need a resource that has conditions
	// For now, using a ConfigMap as a placeholder - in real implementation
	// this would be a CRD or other resource with conditions
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigWaitForCondition(cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, "default", cmName),
					// For a real test, check that condition was met
					// resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.conditions.0.type", "Ready"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, "default", cmName),
	})
}

func testAccManifestConfigWaitForCondition(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  test: value
YAML

  # In a real test, this would be used with a CRD
  # wait_for = {
  #   condition = "Ready"
  # }

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name)
}

// TestAccManifestResource_WaitForPVCBinding tests waiting for PersistentVolumeClaim binding
func TestAccManifestResource_WaitForPVCBinding(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	pvcName := fmt.Sprintf("wait-pvc-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigWaitForPVC(pvcName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckPVCExists(k8sClient, "default", pvcName),
					// Should have waited for Bound status
					resource.TestCheckResourceAttr("k8sconnect_manifest.pvc", "status.phase", "Bound"),
					// Should have volume name populated
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.pvc", "status.volume_name"),
					// Check output
					resource.TestCheckOutput("pvc_bound", "true"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckPVCDestroy(k8sClient, "default", pvcName),
	})
}

func testAccManifestConfigWaitForPVC(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

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
    kubeconfig_raw = var.raw
  }
}

# Then create PVC and wait for binding
resource "k8sconnect_manifest" "pvc" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: default
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
    kubeconfig_raw = var.raw
  }

  depends_on = [k8sconnect_manifest.pv]
}

output "pvc_bound" {
  value = k8sconnect_manifest.pvc.status.phase == "Bound"
}

output "volume_name" {
  value = k8sconnect_manifest.pvc.status.volume_name
}
`, name, name, name)
}

// TestAccManifestResource_AutoRollout tests automatic rollout waiting for Deployments
func TestAccManifestResource_AutoRollout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	deployName := fmt.Sprintf("auto-rollout-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigAutoRollout(deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, "default", deployName),
					// Should automatically wait for rollout and populate status
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.replicas", "2"),
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.readyReplicas", "2"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, "default", deployName),
	})
}

func testAccManifestConfigAutoRollout(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: default
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
        image: nginx:1.21
        ports:
        - containerPort: 80
YAML

  # No wait_for specified - should auto-wait for rollout

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name, name, name)
}

// TestAccManifestResource_DisableAutoRollout tests disabling automatic rollout waiting
func TestAccManifestResource_DisableAutoRollout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	deployName := fmt.Sprintf("no-rollout-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigDisableRollout(deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, "default", deployName),
					// Should NOT have status because rollout waiting was disabled
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status", ""),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, "default", deployName),
	})
}

func testAccManifestConfigDisableRollout(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: default
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
        image: nginx:1.21
        ports:
        - containerPort: 80
YAML

  wait_for = {
    rollout = false  # Explicitly disable rollout waiting
  }

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name, name, name)
}

// TestAccManifestResource_WaitTimeout tests timeout behavior
func TestAccManifestResource_WaitTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	cmName := fmt.Sprintf("wait-timeout-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigWaitTimeout(cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, "default", cmName),
					// Resource should exist even though wait timed out
				),
				// Expect a warning but not an error
				ExpectNonEmptyPlan: false,
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, "default", cmName),
	})
}

func testAccManifestConfigWaitTimeout(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  test: value
YAML

  wait_for = {
    field = "status.impossibleField"  # This will never exist
    timeout = "5s"  # Short timeout
  }

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name)
}

// TestAccManifestResource_WaitForMultipleValues tests waiting for multiple field values
func TestAccManifestResource_WaitForMultipleValues(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	deployName := fmt.Sprintf("multi-wait-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigMultipleValues(deployName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, "default", deployName),
					// Should wait for both conditions
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.replicas", "2"),
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.readyReplicas", "2"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, "default", deployName),
	})
}

func testAccManifestConfigMultipleValues(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: default
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
        image: nginx:1.21
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
    kubeconfig_raw = var.raw
  }
}
`, name, name, name)
}

// TestAccManifestResource_StatefulSetRollout tests automatic rollout for StatefulSets
func TestAccManifestResource_StatefulSetRollout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	stsName := fmt.Sprintf("sts-rollout-%d", time.Now().Unix())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigStatefulSet(stsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckStatefulSetExists(k8sClient, "default", stsName),
					// Should automatically wait and populate status
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.replicas", "1"),
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.readyReplicas", "1"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckStatefulSetDestroy(k8sClient, "default", stsName),
	})
}

func testAccManifestConfigStatefulSet(name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: %s
  namespace: default
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
        image: nginx:1.21
        ports:
        - containerPort: 80
YAML

  # No wait_for - should auto-wait for StatefulSet rollout

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`, name, name, name, name)
}
