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

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

func TestAccObjectResource_DriftDetection(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
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
					resource.TestCheckResourceAttrSet("k8sconnect_object.drift_test", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.drift_test", "managed_state_projection.%"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Modify ConfigMap outside of Terraform (simulating drift)
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
					// Modify ONLY the data that we still own (key2)
					// This simulates drift in fields we manage, not fields taken by another manager
					cm.Data = map[string]string{
						"key1": cm.Data["key1"], // Keep unchanged
						"key2": "drift-value",   // Change the field we own - this should show drift
						"key3": cm.Data["key3"], // Keep unchanged
					}
					// Keep ownership annotations
					cm.SetAnnotations(existingAnnotations)
					// Update using the same field manager to maintain ownership
					_, err = k8sClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{
						FieldManager: "k8sconnect", // Use same manager to keep ownership
					})
					if err != nil {
						t.Fatalf("Failed to update ConfigMap: %v", err)
					}

					cmAfter, _ := k8sClient.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
					t.Logf("ConfigMap after modification: data=%v", cmAfter.Data)
					t.Logf("Number of managedFields entries: %d", len(cmAfter.ManagedFields))
					for i, mf := range cmAfter.ManagedFields {
						t.Logf("ManagedField[%d]: manager=%s, operation=%s", i, mf.Manager, mf.Operation)
					}

					t.Log("✅ Modified ConfigMap with same field manager (simulating drift in owned fields)")
				},
				Config: testAccManifestConfigDriftDetectionInitial(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
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

resource "k8sconnect_object" "drift_namespace" {
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

resource "k8sconnect_object" "drift_test" {
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

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.drift_namespace]
}
`, namespace, cmName, namespace)
}

func TestAccObjectResource_NoDriftWhenNoChanges(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
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
					resource.TestCheckResourceAttrSet("k8sconnect_object.no_drift", "id"),
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

resource "k8sconnect_object" "no_drift_namespace" {
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

resource "k8sconnect_object" "no_drift" {
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
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.no_drift_namespace]
}
`, namespace, cmName, namespace)
}

func TestAccObjectResource_DriftDetectionNestedStructures(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
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
					resource.TestCheckResourceAttrSet("k8sconnect_object.drift_deployment", "id"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 2: Modify nested fields
			{
				PreConfig: func() {
					ctx := context.Background()

					_, err := k8sClient.AppsV1().Deployments(ns).Patch(
						ctx, deployName,
						types.StrategicMergePatchType,
						[]byte(driftTestDeploymentPatch),
						metav1.PatchOptions{
							FieldManager: "k8sconnect",
						},
					)
					if err != nil {
						t.Fatalf("Failed to patch Deployment: %v", err)
					}
					t.Log("✅ Modified Deployment using patch (deterministic)")
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

const driftTestDeploymentPatch = `{
	"spec": {
		"replicas": 5,
		"template": {
			"spec": {
				"containers": [{
					"name": "nginx",
					"image": "public.ecr.aws/nginx/nginx:1.22"
				}]
			}
		}
	}
}`

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

resource "k8sconnect_object" "drift_deployment_namespace" {
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

resource "k8sconnect_object" "drift_deployment" {
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
        image: public.ecr.aws/nginx/nginx:1.21
        ports:
        - containerPort: 80
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.drift_deployment_namespace]
}
`, namespace, deployName, namespace)
}

func TestAccObjectResource_DriftDetectionArrays(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
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
					resource.TestCheckResourceAttrSet("k8sconnect_object.drift_service", "id"),
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

resource "k8sconnect_object" "drift_service_namespace" {
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

resource "k8sconnect_object" "drift_service" {
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
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.drift_service_namespace]
}
`, namespace, svcName, namespace)
}

// TestAccObjectResource_NodePortNoDrift verifies that nodePort doesn't cause drift with field ownership
func TestAccObjectResource_NodePortNoDrift(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("nodeport-test-%d", time.Now().UnixNano()%1000000)
	svcName := fmt.Sprintf("test-svc-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create LoadBalancer service with field ownership
			{
				Config: testAccServiceWithFieldOwnership(ns, svcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckServiceExists(k8sClient, ns, svcName),
				),
			},
			// Step 2: Verify refresh doesn't show nodePort drift
			{
				Config: testAccServiceWithFieldOwnership(ns, svcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // This is the key test - no drift!
			},
		},
		CheckDestroy: testhelpers.CheckServiceDestroy(k8sClient, ns, svcName),
	})
}

func testAccServiceWithFieldOwnership(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
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

  cluster_connection = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test" {
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
  - port: 9996
    targetPort: 8080
    protocol: TCP
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.test_namespace]
}
`, namespace, name, namespace)
}

// TODO Need to add apply to this as well
// And/Or, insert a Step 2.5 that tries to apply without force_conflicts and expects an error.
func TestAccObjectResource_CombinedDriftScenarios(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("combined-drift-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("combined-deploy-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("combined-cm-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create resources
			{
				Config: testAccCombinedDriftConfig(ns, deployName, cmName, false),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
					"cm_name":     config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Simulate BOTH types of drift
			// Note: We use force_conflicts=true here to avoid ERROR when HPA owns replicas field.
			// With force_conflicts, we get a WARNING instead, allowing plan to succeed.
			{
				PreConfig: func() {
					ctx := context.Background()

					// 1. Simulate HPA taking over replicas - use Patch with different field manager
					patchData := []byte(`{"spec":{"replicas":5}}`)
					_, err := k8sClient.AppsV1().Deployments(ns).Patch(ctx, deployName,
						types.StrategicMergePatchType,
						patchData,
						metav1.PatchOptions{
							FieldManager: "hpa-controller",
						})
					if err != nil {
						t.Fatalf("Failed to simulate HPA ownership: %v", err)
					}
					t.Log("✅ Simulated HPA taking ownership of replicas field")

					// 2. Modify ConfigMap data (value drift)
					cm, err := k8sClient.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("Failed to get ConfigMap: %v", err)
					}

					// Preserve the ownership annotations
					existingAnnotations := cm.GetAnnotations()

					// Modify the data
					cm.Data = map[string]string{
						"key1": "drift-value-1", // Changed
						"key2": "drift-value-2", // Changed
					}

					// Keep ownership annotations
					cm.SetAnnotations(existingAnnotations)

					// Update using same field manager (simulating value drift, not ownership change)
					_, err = k8sClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{
						FieldManager: "k8sconnect",
					})
					if err != nil {
						t.Fatalf("Failed to modify ConfigMap: %v", err)
					}
					t.Log("✅ Modified ConfigMap values (value drift)")

					// Log the state for debugging
					t.Logf("ConfigMap after modification: data=%v", cm.Data)

					// Check deployment managed fields
					deploy, _ := k8sClient.AppsV1().Deployments(ns).Get(ctx, deployName, metav1.GetOptions{})
					t.Logf("Deployment ManagedFields after HPA takeover:")
					for _, mf := range deploy.ManagedFields {
						t.Logf("  Manager: %s, Operation: %s", mf.Manager, mf.Operation)
					}
				},
				Config: testAccCombinedDriftConfig(ns, deployName, cmName, true), // force_conflicts=true to avoid error
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
					"cm_name":     config.StringVariable(cmName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // Should detect both drifts
			},
			// Step 3: Apply with force_conflicts to fix both
			{
				Config: testAccCombinedDriftConfig(ns, deployName, cmName, true),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
					"cm_name":     config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify ConfigMap is corrected
					testhelpers.CheckConfigMapData(k8sClient, ns, cmName, map[string]string{
						"key1": "value1",
						"key2": "value2",
					}),
					// Verify Deployment exists and has been updated
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
		),
	})
}

func testAccCombinedDriftConfig(namespace, deployName, cmName string, forceConflicts bool) string {
	// Note: forceConflicts parameter is kept for compatibility but ignored (force is always true now)

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
variable "cm_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
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

resource "k8sconnect_object" "deployment" {
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
      app: combined-test
  template:
    metadata:
      labels:
        app: combined-test
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_object" "configmap" {
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

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}
`, namespace, deployName, namespace, cmName, namespace)
}
