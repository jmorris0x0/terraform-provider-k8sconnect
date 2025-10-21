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

// TestAccWaitResource_ExtractSpecVolumeName tests extracting spec.volumeName from PVC
// This is the bug case from kind-validation scenario
func TestAccWaitResource_ExtractSpecVolumeName(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("wait-pvc-volume-%d", time.Now().UnixNano()%1000000)
	pvcName := fmt.Sprintf("test-pvc-%d", time.Now().UnixNano()%1000000)
	pvName := fmt.Sprintf("test-pv-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigPVCVolumeName(ns, pvcName, pvName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
					// Wait resource should populate result with full path structure
					// field="spec.volumeName" → stored as result.spec.volumeName
					resource.TestCheckResourceAttrSet("k8sconnect_wait.pvc_volume", "result.spec.volumeName"),
					// Verify the output value is set (uses the result)
					resource.TestCheckOutput("volume_name", pvName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckPVCDestroy(k8sClient, ns, pvcName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

func testAccWaitConfigPVCVolumeName(namespace, pvcName, pvName string) string {
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

resource "k8sconnect_object" "pv" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: %s
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
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}

resource "k8sconnect_object" "pvc" {
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
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.pv]
}

# Wait for PVC to be bound AND extract the volume name from spec
resource "k8sconnect_wait" "pvc_volume" {
  object_ref = k8sconnect_object.pvc.object_ref
  wait_for = {
    field = "spec.volumeName"  # Field is in .spec, NOT .status!
    timeout = "30s"
  }
  cluster_connection = { kubeconfig = var.raw }
}

# Use the volume name in an output (the failing use case from kind-validation)
# field="spec.volumeName" extracts to object.spec.volumeName (full path preserved)
output "volume_name" {
  value = k8sconnect_wait.pvc_volume.result.spec.volumeName
}
`, namespace, pvName, pvName, pvcName, namespace)
}

// TestAccWaitResource_ExtractSpecClusterIP tests extracting spec.clusterIP from Service
func TestAccWaitResource_ExtractSpecClusterIP(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("wait-svc-ip-%d", time.Now().UnixNano()%1000000)
	svcName := fmt.Sprintf("test-svc-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigServiceClusterIP(ns, svcName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckServiceExists(k8sClient, ns, svcName),
					// Wait resource should populate result with full path structure
					// field="spec.clusterIP" → stored as result.spec.clusterIP
					resource.TestCheckResourceAttrSet("k8sconnect_wait.svc_ip", "result.spec.clusterIP"),
					// Verify the output exists (uses the result)
					resource.TestCheckResourceAttrSet("k8sconnect_wait.svc_ip", "id"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckServiceDestroy(k8sClient, ns, svcName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

func testAccWaitConfigServiceClusterIP(namespace, svcName string) string {
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

resource "k8sconnect_object" "svc" {
  yaml_body = <<YAML
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  type: ClusterIP
  selector:
    app: test
  ports:
  - port: 80
    targetPort: 8080
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}

# Wait for Service to get clusterIP assigned
resource "k8sconnect_wait" "svc_ip" {
  object_ref = k8sconnect_object.svc.object_ref
  wait_for = {
    field = "spec.clusterIP"  # Field is in .spec, NOT .status!
    timeout = "30s"
  }
  cluster_connection = { kubeconfig = var.raw }
}

# Use the cluster IP in an output
# field="spec.clusterIP" extracts to object.spec.clusterIP (full path preserved)
output "cluster_ip" {
  value = k8sconnect_wait.svc_ip.result.spec.clusterIP
}
`, namespace, svcName, namespace)
}

// TestAccWaitResource_ExtractMetadataUID tests extracting metadata.uid
func TestAccWaitResource_ExtractMetadataUID(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("wait-uid-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("test-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigMetadataUID(ns, cmName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					// Wait resource should populate result with full path structure
					// field="metadata.uid" → stored as result.metadata.uid
					resource.TestCheckResourceAttrSet("k8sconnect_wait.cm_uid", "result.metadata.uid"),
					// Verify the output exists (uses the result)
					resource.TestCheckResourceAttrSet("k8sconnect_wait.cm_uid", "id"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

func testAccWaitConfigMetadataUID(namespace, cmName string) string {
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

resource "k8sconnect_object" "cm" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: value
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}

# Wait for ConfigMap to exist and extract its UID
resource "k8sconnect_wait" "cm_uid" {
  object_ref = k8sconnect_object.cm.object_ref
  wait_for = {
    field = "metadata.uid"  # Field is in .metadata, NOT .status!
    timeout = "30s"
  }
  cluster_connection = { kubeconfig = var.raw }
}

# Use the UID in an output
# field="metadata.uid" extracts to object.metadata.uid (full path preserved)
output "resource_uid" {
  value = k8sconnect_wait.cm_uid.result.metadata.uid
}
`, namespace, cmName, namespace)
}

// TestAccWaitResource_ExtractStatusLoadBalancerIngress tests extracting from .status
// This should already work, but included for completeness
func TestAccWaitResource_ExtractStatusLoadBalancerIngress(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("wait-lb-%d", time.Now().UnixNano()%1000000)
	svcName := fmt.Sprintf("test-lb-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigLoadBalancerIngress(ns, svcName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckServiceExists(k8sClient, ns, svcName),
					// k3d does support LoadBalancers - should populate result
					// Check that the ingress list has at least one element
					resource.TestCheckResourceAttrSet("k8sconnect_wait.lb_ingress", "result.status.loadBalancer.ingress.#"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckServiceDestroy(k8sClient, ns, svcName),
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
		),
	})
}

func testAccWaitConfigLoadBalancerIngress(namespace, svcName string) string {
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

resource "k8sconnect_object" "lb_svc" {
  yaml_body = <<YAML
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  type: LoadBalancer
  selector:
    app: test
  ports:
  - port: 9998
    targetPort: 8080
YAML
  cluster_connection = { kubeconfig = var.raw }
  depends_on = [k8sconnect_object.test_ns]
}

# Wait for LoadBalancer ingress - k3d supports LoadBalancers
resource "k8sconnect_wait" "lb_ingress" {
  object_ref = k8sconnect_object.lb_svc.object_ref
  wait_for = {
    field = "status.loadBalancer.ingress"
    timeout = "2m"
  }
  cluster_connection = { kubeconfig = var.raw }
}
`, namespace, svcName, namespace)
}
