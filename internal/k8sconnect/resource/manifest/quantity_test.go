// internal/k8sconnect/resource/manifest/quantity_test.go
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

func TestAccManifestResource_QuantityNormalization(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("quantity-norm-ns-%d", time.Now().UnixNano()%1000000)
	quotaName := fmt.Sprintf("test-quantities-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigResourceQuota(ns, quotaName),
				ConfigVariables: config.Variables{
					"raw":        config.StringVariable(raw),
					"namespace":  config.StringVariable(ns),
					"quota_name": config.StringVariable(quotaName),
				},
				Check: resource.ComposeTestCheckFunc(
					// Initial apply should succeed
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_quota", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_quota", "managed_state_projection"),
				),
			},
			{
				// CRITICAL TEST: Re-apply with same config should show no changes
				Config: testAccManifestConfigResourceQuota(ns, quotaName),
				ConfigVariables: config.Variables{
					"raw":        config.StringVariable(raw),
					"namespace":  config.StringVariable(ns),
					"quota_name": config.StringVariable(quotaName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // Verifies no drift from quantity normalization!
			},
		},
		CheckDestroy: testhelpers.CheckResourceQuotaDestroy(k8sClient, ns, quotaName),
	})
}

func testAccManifestConfigResourceQuota(namespace, quotaName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "quota_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "quota_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "test_quota" {
  yaml_body = <<YAML
apiVersion: v1
kind: ResourceQuota
metadata:
  name: %s
  namespace: %s
spec:
  hard:
    requests.memory: "2Gi"      # K8s normalizes to "2147483648"
    requests.cpu: "1000m"       # K8s normalizes to "1"
    requests.storage: "10Gi"    # K8s normalizes to "10737418240"
    persistentvolumeclaims: "4" # Should stay as-is
    limits.memory: "4096Mi"     # K8s normalizes to "4294967296"
    limits.cpu: "2"             # Should stay as-is
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }

  delete_timeout = "30s" # ResourceQuotas delete quickly
  
  depends_on = [k8sconnect_manifest.quota_namespace]
}
`, namespace, quotaName, namespace)
}

func TestAccManifestResource_PVCQuantityNormalization(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("pvc-quantity-ns-%d", time.Now().UnixNano()%1000000)
	pvcName := fmt.Sprintf("test-pvc-quantity-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigPVCQuantity(ns, pvcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"pvc_name":  config.StringVariable(pvcName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_pvc_quantity", "id"),
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
				),
			},
			{
				// Re-apply should show no changes despite quantity normalization
				Config: testAccManifestConfigPVCQuantity(ns, pvcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"pvc_name":  config.StringVariable(pvcName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

func testAccManifestConfigPVCQuantity(namespace, pvcName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "pvc_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "pvc_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "test_pvc_quantity" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: "5Gi"  # This MUST NOT show as drift when K8s stores as bytes
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.pvc_namespace]
}
`, namespace, pvcName, namespace)
}

func TestAccManifestResource_ContainerResourcesNormalization(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("container-resources-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("test-resources-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigDeploymentResources(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_deployment", "id"),
				),
			},
			{
				// Verify no drift from CPU/memory normalization
				Config: testAccManifestConfigDeploymentResources(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigDeploymentResources(namespace, deployName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "deploy_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "deploy_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}

resource "k8sconnect_manifest" "test_deployment" {
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
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: nginx
        image: nginx:1.20
        resources:
          requests:
            memory: "64Mi"    # Normalizes to bytes
            cpu: "250m"       # Normalizes to "0.25"
          limits:
            memory: "128Mi"   # Normalizes to bytes
            cpu: "500m"       # Normalizes to "0.5"
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.deploy_namespace]
}
`, namespace, deployName, namespace)
}
