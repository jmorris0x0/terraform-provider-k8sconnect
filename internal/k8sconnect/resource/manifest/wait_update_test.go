// internal/k8sconnect/resource/manifest/wait_update_test.go
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

// TestAccManifestResource_WaitForFieldChange tests changing wait_for.field to a different field path
// This verifies that status gets re-pruned to only include the new field
func TestAccManifestResource_WaitForFieldChange(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-change-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("field-change-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment, wait_for status.replicas
			{
				Config: testAccManifestConfigWaitForFieldUpdate(ns, deployName, "status.replicas"),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Status should only contain replicas
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.replicas", "1"),
					// Should NOT contain readyReplicas yet
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status.readyReplicas"),
				),
			},
			// Step 2: Change to wait_for status.readyReplicas
			// The old field (replicas) should be removed, new field (readyReplicas) should appear
			{
				Config: testAccManifestConfigWaitForFieldUpdate(ns, deployName, "status.readyReplicas"),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Status should now contain readyReplicas
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.readyReplicas", "1"),
					// Should NOT contain replicas anymore
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status.replicas"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigWaitForFieldUpdate(namespace, name, fieldPath string) string {
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
    field = "%s"
  }

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace, name, name, fieldPath)
}

// TestAccManifestResource_WaitTypeTransitionFieldToFieldValue tests changing from field to field_value
// field populates status, field_value doesn't - status should be removed
func TestAccManifestResource_WaitTypeTransitionFieldToFieldValue(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("field-to-value-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("field-to-value-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment with field (populates status)
			{
				Config: testAccManifestConfigFieldWaitUpdate(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Status should be populated
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.readyReplicas", "1"),
				),
			},
			// Step 2: Change to field_value (doesn't populate status)
			{
				Config: testAccManifestConfigFieldValueWaitUpdate(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Status should be removed
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}

func testAccManifestConfigFieldWaitUpdate(namespace, name string) string {
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
  }

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace, name, name)
}

func testAccManifestConfigFieldValueWaitUpdate(namespace, name string) string {
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
    field_value = {
      "status.readyReplicas" = "1"
    }
  }

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, name, namespace, name, name)
}

// TestAccManifestResource_WaitTypeTransitionFieldValueToField tests changing from field_value to field
// field_value doesn't populate status, field does - status should appear
func TestAccManifestResource_WaitTypeTransitionFieldValueToField(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("value-to-field-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("value-to-field-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create deployment with field_value (doesn't populate status)
			{
				Config: testAccManifestConfigFieldValueWaitUpdate(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Status should NOT be populated
					resource.TestCheckNoResourceAttr("k8sconnect_manifest.test", "status"),
				),
			},
			// Step 2: Change to field (populates status)
			{
				Config: testAccManifestConfigFieldWaitUpdate(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					// Status should now be populated
					resource.TestCheckResourceAttr("k8sconnect_manifest.test", "status.readyReplicas", "1"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
	})
}
