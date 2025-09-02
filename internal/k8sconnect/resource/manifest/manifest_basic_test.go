// internal/k8sconnect/resource/manifest/manifest_basic_test.go
package manifest_test

import (
	"fmt"
	"os"
	"testing"

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
	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")

	fmt.Println("HOST      =", os.Getenv("TF_ACC_K8S_HOST"))
	fmt.Println("CA prefix =", os.Getenv("TF_ACC_K8S_CA")[:20], "…")
	fmt.Println("CMD       =", os.Getenv("TF_ACC_K8S_CMD"))
	fmt.Println("RAW prefix=", os.Getenv("TF_ACC_KUBECONFIG_RAW")[:20], "…")

	if host == "" || ca == "" || cmd == "" || raw == "" {
		t.Fatal("TF_ACC_K8S_HOST, TF_ACC_K8S_CA, TF_ACC_K8S_CMD and TF_ACC_KUBECONFIG_RAW must be set")
	}

	// Create Kubernetes client for verification
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigBasic,
				ConfigVariables: config.Variables{
					"host": config.StringVariable(host),
					"ca":   config.StringVariable(ca),
					"cmd":  config.StringVariable(cmd),
					"raw":  config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// ✅ Verify Terraform state
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_exec", "yaml_body", testNamespaceYAML),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_exec", "id"),

					// ✅ Verify namespace actually exists in Kubernetes
					testhelpers.CheckNamespaceExists(k8sClient, "acctest-exec"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, "acctest-exec"),
	})
}

const testNamespaceYAML = `apiVersion: v1
kind: Namespace
metadata:
  name: acctest-exec
`

const testAccManifestConfigBasic = `
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

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_exec" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-exec
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
`

func TestAccManifestResource_KubeconfigRaw(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigKubeconfigRaw,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_raw", "yaml_body", testNamespaceYAMLRaw),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_raw", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, "acctest-raw"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, "acctest-raw"),
	})
}

const testNamespaceYAMLRaw = `apiVersion: v1
kind: Namespace
metadata:
  name: acctest-raw
`

const testAccManifestConfigKubeconfigRaw = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_raw" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-raw
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

func TestAccManifestResource_KubeconfigFile(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	// Write kubeconfig to temp file
	tmpfile, err := os.CreateTemp("", "kubeconfig*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigKubeconfigFile,
				ConfigVariables: config.Variables{
					"kubeconfig_path": config.StringVariable(tmpfile.Name()),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_file", "yaml_body", testNamespaceYAMLFile),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_file", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, "acctest-file"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, "acctest-file"),
	})
}

const testNamespaceYAMLFile = `apiVersion: v1
kind: Namespace
metadata:
  name: acctest-file
`

const testAccManifestConfigKubeconfigFile = `
variable "kubeconfig_path" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_file" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-file
YAML

  cluster_connection = {
    kubeconfig_file = var.kubeconfig_path
  }
}
`

// Test different resource types
func TestAccManifestResource_Pod(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigPod,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_pod", "yaml_body", testPodYAML),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_pod", "id"),
					testhelpers.CheckPodExists(k8sClient, "default", "acctest-pod"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckPodDestroy(k8sClient, "default", "acctest-pod"),
	})
}

const testPodYAML = `apiVersion: v1
kind: Pod
metadata:
  name: acctest-pod
  namespace: default
spec:
  containers:
  - name: test
    image: busybox:1.35
    command: ["sleep", "3600"]
`

const testAccManifestConfigPod = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_pod" {
  yaml_body = <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: acctest-pod
  namespace: default
spec:
  containers:
  - name: test
    image: busybox:1.35
    command: ["sleep", "3600"]
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

// Alternative: Test namespace inference with ConfigMap (simpler than Pod)
func TestAccManifestResource_DefaultNamespaceInference(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Skip("TF_ACC_KUBECONFIG_RAW not set, skipping")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigDefaultNamespace,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_manifest.test_default_ns", "yaml_body", testConfigMapYAMLNoNamespace),
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_default_ns", "id"),
					// Key test: ConfigMap with no namespace should end up in default
					testhelpers.CheckConfigMapExists(k8sClient, "default", "acctest-config"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, "default", "acctest-config"),
	})
}

const testConfigMapYAMLNoNamespace = `apiVersion: v1
kind: ConfigMap
metadata:
  name: acctest-config
data:
  key1: value1
`

const testAccManifestConfigDefaultNamespace = `
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_manifest" "test_default_ns" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: acctest-config
data:
  key1: value1
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

func TestAccManifestResource_DeferredAuthWithComputedEnvVars(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

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
				Config: testAccManifestConfigDeferredAuthWithExecEnv,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify the manifest was created successfully
					resource.TestCheckResourceAttrSet("k8sconnect_manifest.test_deferred_env", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, "default", "test-deferred-auth-env"),
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
		CheckDestroy: testhelpers.CheckConfigMapDestroy(k8sClient, "default", "test-deferred-auth-env"),
	})
}

const testAccManifestConfigDeferredAuthWithExecEnv = `
variable "raw" {
  type = string
}

# These create unknown values during plan
resource "random_string" "session_id" {
  length = 16
  special = false
}

resource "random_uuid" "trace_id" {}

resource "k8sconnect_manifest" "test_deferred_env" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-deferred-auth-env
  namespace: default
data:
  test: "auth-was-deferred"
  # Don't put unknown values in the YAML - that's not what we're testing
  static_key: "static_value"
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
    
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
}
`
