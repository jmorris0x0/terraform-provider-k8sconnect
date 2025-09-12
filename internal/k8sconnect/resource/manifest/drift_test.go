// internal/k8sconnect/resource/manifest/drift_test.go
package manifest_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

func TestAccManifestResource_DriftDetection(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("drift-detection-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("drift-test-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create initial ConfigMap
			{
				Config: testAccManifestConfigDriftDetectionInitial(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.drift_test", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.drift_test", "managed_state_projection"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Modify ConfigMap outside of Terraform (simulatingulating drift)
			{
				PreConfig: func() {
					ctx := context.Background()

					// Get the current ConfigMap to preserve ownership annotations
					cm, err := k8sClient.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("Failed to get ConfigMap: %v", err)
					}

					// Preserve the ownership annotations
					existingAnnotations := cm.GetAnnotations()

					// Modify the ConfigMap data - these are fields we manage
					cm.Data = map[string]string{
						"key1": "modified-outside-terraform", // Changed value
						"key2": "value2",                     // Unchanged
						"key3": "value3-modified",            // Changed value
					}

					// Modify other annotations but preserve ownership
					if cm.Annotations == nil {
						cm.Annotations = make(map[string]string)
					}
					cm.Annotations["example.com/team"] = "platform-team" // Changed from backend-team

					// Preserve ownership annotations
					for k, v := range existingAnnotations {
						if strings.HasPrefix(k, "k8sconnect.terraform.io/") {
							cm.Annotations[k] = v
						}
					}

					// Use Update with FieldManager to ensure the change is tracked
					_, err = k8sClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{
						FieldManager: "manual-edit", // Different field manager to simulate external change
					})
					if err != nil {
						t.Fatalf("Failed to update ConfigMap: %v", err)
					}
					t.Log("✅ Modified ConfigMap outside of Terraform (simulating drift)")
				},
				Config: testAccManifestConfigDriftDetectionInitial(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // Should detect drift!
			},
			// Step 3: Verify drift is corrected by apply
			{
				Config: testAccManifestConfigDriftDetectionInitial(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify ConfigMap is back to original state
					testhelpers.CheckConfigMapData(k8sClient, ns, cmName, map[string]string{
						"key1": "value1",
						"key2": "value2",
						"key3": "value3",
					}),
					// Verify annotation is back to original
					testhelpers.CheckConfigMapAnnotation(k8sClient, ns, cmName,
						"example.com/team", "backend-team"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
	})
}

func testAccManifestConfigDriftDetectionInitial(namespace, cmName string) string {
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

resource "k8sconnect_manifest" "drift_namespace" {
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

resource "k8sconnect_manifest" "drift_test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
  annotations:
    example.com/team: "backend-team"
data:
  key1: value1
  key2: value2
  key3: value3
YAML

  force_conflicts = true  

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.drift_namespace]
}
`, namespace, cmName, namespace)
}

func TestAccManifestResource_NoDriftWhenNoChanges(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("no-drift-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("no-drift-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create resource
			{
				Config: testAccManifestConfigNoDrift(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.no_drift", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Run plan without any changes - should be empty
			{
				Config: testAccManifestConfigNoDrift(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // No drift expected!
			},
			// Step 3: Add field that we don't manage - should still show no drift
			{
				PreConfig: func() {
					ctx := context.Background()
					cm, err := k8sClient.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("Failed to get ConfigMap: %v", err)
					}

					// Initialize maps if nil
					if cm.Data == nil {
						cm.Data = make(map[string]string)
					}
					if cm.Labels == nil {
						cm.Labels = make(map[string]string)
					}

					// Add fields we don't manage
					cm.Data["unmanaged_key"] = "not-in-terraform"
					cm.Labels["added-by"] = "external-controller"

					_, err = k8sClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
					if err != nil {
						t.Fatalf("Failed to update ConfigMap: %v", err)
					}
					t.Log("✅ Added unmanaged fields to ConfigMap")
				},
				Config: testAccManifestConfigNoDrift(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // Still no drift - we don't manage those fields!
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
	})
}

func testAccManifestConfigNoDrift(namespace, cmName string) string {
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

resource "k8sconnect_manifest" "no_drift_namespace" {
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

resource "k8sconnect_manifest" "no_drift" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  config: |
    setting1=value1
    setting2=value2
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.no_drift_namespace]
}
`, namespace, cmName, namespace)
}

func TestAccManifestResource_DriftDetectionNestedStructures(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("drift-nested-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("drift-deployment-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create Deployment
			{
				Config: testAccManifestConfigDriftDetectionDeployment(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.drift_deployment", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 2: Modify nested fields
			{
				PreConfig: func() {
					ctx := context.Background()
					dep, err := k8sClient.AppsV1().Deployments(ns).Get(ctx, deployName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("Failed to get Deployment: %v", err)
					}

					// Modify container image
					dep.Spec.Template.Spec.Containers[0].Image = "nginx:1.22"
					// Modify replicas
					replicas := int32(5)
					dep.Spec.Replicas = &replicas
					// Add an env var
					dep.Spec.Template.Spec.Containers[0].Env = append(dep.Spec.Template.Spec.Containers[0].Env,
						v1.EnvVar{Name: "ADDED_VAR", Value: "added"})

					_, err = k8sClient.AppsV1().Deployments(ns).Update(ctx, dep, metav1.UpdateOptions{})
					if err != nil {
						t.Fatalf("Failed to update Deployment: %v", err)
					}
					t.Log("✅ Modified Deployment nested fields")
				},
				Config: testAccManifestConfigDriftDetectionDeployment(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // Should detect drift in image and replicas
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigDriftDetectionDeployment(namespace, deployName string) string {
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

resource "k8sconnect_manifest" "drift_deployment_namespace" {
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

resource "k8sconnect_manifest" "drift_deployment" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 3
  selector:
    matchLabels:
      app: drift-test
  template:
    metadata:
      labels:
        app: drift-test
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
        ports:
        - containerPort: 80
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.drift_deployment_namespace]
}
`, namespace, deployName, namespace)
}

func TestAccManifestResource_DriftDetectionArrays(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	ns := fmt.Sprintf("drift-arrays-ns-%d", time.Now().UnixNano()%1000000)
	svcName := fmt.Sprintf("drift-service-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create Service with multiple ports
			{
				Config: testAccManifestConfigDriftDetectionService(ns, svcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"svc_name":  config.StringVariable(svcName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.drift_service", "id"),
					testhelpers.CheckServiceExists(k8sClient, ns, svcName),
				),
			},
			// Step 2: Modify array elements
			{
				PreConfig: func() {
					ctx := context.Background()
					svc, err := k8sClient.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("Failed to get Service: %v", err)
					}

					// Change port number
					svc.Spec.Ports[0].Port = 8080
					// Add a new port (not in our YAML)
					svc.Spec.Ports = append(svc.Spec.Ports, v1.ServicePort{
						Name:     "metrics",
						Port:     9090,
						Protocol: v1.ProtocolTCP,
					})

					_, err = k8sClient.CoreV1().Services(ns).Update(ctx, svc, metav1.UpdateOptions{})
					if err != nil {
						t.Fatalf("Failed to update Service: %v", err)
					}
					t.Log("✅ Modified Service ports array")
				},
				Config: testAccManifestConfigDriftDetectionService(ns, svcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"svc_name":  config.StringVariable(svcName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // Should detect port change
			},
		},
		CheckDestroy: testhelpers.CheckServiceDestroy(k8sClient, ns, svcName),
	})
}

func testAccManifestConfigDriftDetectionService(namespace, svcName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "svc_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "drift_service_namespace" {
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

resource "k8sconnect_manifest" "drift_service" {
  yaml_body = <<YAML
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    app: drift-test
  ports:
  - name: http
    port: 80
    protocol: TCP
    targetPort: 80
  - name: https
    port: 443
    protocol: TCP
    targetPort: 443
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
  
  depends_on = [k8sconnect_manifest.drift_service_namespace]
}
`, namespace, svcName, namespace)
}
