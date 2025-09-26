// internal/k8sconnect/resource/manifest/basic_test.go
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

func TestAccManifestResource_Basic(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	cmd := os.Getenv("TF_ACC_K8S_CMD")
	raw := os.Getenv("TF_ACC_KUBECONFIG")

	fmt.Println("HOST      =", os.Getenv("TF_ACC_K8S_HOST"))
	fmt.Println("CA prefix =", os.Getenv("TF_ACC_K8S_CA")[:20], "…")
	fmt.Println("CMD       =", os.Getenv("TF_ACC_K8S_CMD"))
	fmt.Println("RAW prefix=", os.Getenv("TF_ACC_KUBECONFIG")[:20], "…")

	if host == "" || ca == "" || cmd == "" || raw == "" {
		t.Fatal("TF_ACC_K8S_HOST, TF_ACC_K8S_CA, TF_ACC_K8S_CMD and TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("basic-exec-ns-%d", time.Now().UnixNano()%1000000)

	// Create Kubernetes client for verification
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigBasic(ns),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"cmd":       config.StringVariable(cmd),
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					// ✅ Verify Terraform state
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_exec", "yaml_body", testNamespaceYAML(ns)),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_exec", "id"),

					// ✅ Verify namespace actually exists in Kubernetes
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testNamespaceYAML(namespace string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, namespace)
}

func testAccManifestConfigBasic(namespace string) string {
	return fmt.Sprintf(`
variable "host" {
  type = string
}
variable "ca" {
  type = string
}
variable "cmd" {
  type = string
}
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_exec" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = {
    host                   = var.host
    cluster_ca_certificate = var.ca

    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = var.cmd
      args        = ["hello"]
    }
  }
}
`, namespace)
}

func TestAccManifestResource_KubeconfigRaw(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("kubeconfig-raw-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigKubeconfigRaw(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_raw", "yaml_body", testNamespaceYAMLRaw(ns)),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_raw", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testNamespaceYAMLRaw(namespace string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, namespace)
}

func testAccManifestConfigKubeconfigRaw(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_raw" {
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
`, namespace)
}

func testNamespaceYAMLFile(namespace string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, namespace)
}

// Test different resource types
func TestAccManifestResource_Pod(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("pod-test-ns-%d", time.Now().UnixNano()%1000000)
	podName := fmt.Sprintf("test-pod-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigPod(ns, podName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"pod_name":  config.StringVariable(podName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_pod", "yaml_body", testPodYAML(ns, podName)),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_pod", "id"),
					testhelpers.CheckPodExists(k8sClient, ns, podName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckPodDestroy(k8sClient, ns, podName),
	})
}

func testPodYAML(namespace, podName string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  containers:
  - name: test
    image: public.ecr.aws/docker/library/busybox:latest
    command: ["sleep", "3600"]
`, podName, namespace)
}

func testAccManifestConfigPod(namespace, podName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "pod_name" {
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

resource "k8sconnect_manifest" "test_pod" {
  yaml_body = <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  containers:
  - name: test
    image: public.ecr.aws/docker/library/busybox:latest
    command: ["sleep", "3600"]
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, podName, namespace)
}

// Alternative: Test namespace inference with ConfigMap (simpler than Pod)
func TestAccManifestResource_DefaultNamespaceInference(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Skip("TF_ACC_KUBECONFIG not set, skipping")
	}

	cmName := fmt.Sprintf("acctest-config-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigDefaultNamespace(cmName),
				ConfigVariables: config.Variables{
					"raw":     config.StringVariable(raw),
					"cm_name": config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_default_ns", "yaml_body", testConfigMapYAMLNoNamespace(cmName)),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_default_ns", "id"),
					// Key test: ConfigMap with no namespace should end up in default
					testhelpers.CheckConfigMapExists(k8sClient, "default", cmName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, "default", cmName),
	})
}

func testConfigMapYAMLNoNamespace(cmName string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
data:
  key1: value1
`, cmName)
}

func testAccManifestConfigDefaultNamespace(cmName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "cm_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_default_ns" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
data:
  key1: value1
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}
`, cmName)
}

func TestAccManifestResource_DeferredAuthWithComputedEnvVars(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("deferred-auth-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("deferred-auth-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		ExternalProviders: map[string]resource.ExternalProvider{
			"random": {
				Source:            "hashicorp/random",
				VersionConstraint: "~> 3.5",
			},
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigDeferredAuthWithExecEnv(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify the manifest was created successfully
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_deferred_env", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
					// Verify the random values made it into the exec env vars (not the YAML)
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_deferred_env", "cluster_connection.exec.env.TEST_SESSION_ID"),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_deferred_env", "cluster_connection.exec.env.TEST_TRACE_ID"),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_deferred_env", "cluster_connection.exec.env.TEST_RUN_ID"),
					// Verify the exec command and args are what we expect
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_deferred_env", "cluster_connection.exec.command", "sh"),
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_deferred_env", "cluster_connection.exec.args.#", "2"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
	})
}

func testAccManifestConfigDeferredAuthWithExecEnv(namespace, cmName string) string {
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

# These create unknown values during plan
resource "random_string" "session_id" {
  length = 16
  special = false
}

resource "random_uuid" "trace_id" {}

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

resource "k8sconnect_manifest" "test_deferred_env" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  test: "auth-was-deferred"
  # Don't put unknown values in the YAML - that's not what we're testing
  static_key: "static_value"
YAML

  cluster_connection = {
    kubeconfig = var.raw
    
    # This exec block contains env vars that are unknown during plan
    # They're harmless TEST_* vars that won't affect actual authentication
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "sh"
      args        = ["-c", "kubectl config view --raw"]
      
      # These env vars will be unknown during plan, forcing deferred auth
      env = {
        TEST_SESSION_ID = random_string.session_id.result
        TEST_TRACE_ID   = random_uuid.trace_id.result
        TEST_RUN_ID     = "deferred-${random_uuid.trace_id.result}"
      }
    }
  }
  
  depends_on = [k8sconnect_manifest.test_namespace]
}
`, namespace, cmName, namespace)
}
