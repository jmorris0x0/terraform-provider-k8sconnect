// internal/k8sinline/resource/manifest/manifest_basic_test.go
package manifest_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline"
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
	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
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
					resource.TestCheckResourceAttr("k8sinline_manifest.test_exec", "yaml_body", testNamespaceYAML),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_exec", "id"),

					// ✅ Verify namespace actually exists in Kubernetes
					testAccCheckNamespaceExists(k8sClient, "acctest-exec"),
				),
			},
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, "acctest-exec"),
	})
}

func TestAccManifestResource_KubeconfigRaw(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigKubeconfigRaw,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_raw", "yaml_body", testNamespaceYAMLRaw),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_raw", "id"),
					testAccCheckNamespaceExists(k8sClient, "acctest-raw"),
				),
			},
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, "acctest-raw"),
	})
}

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

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigKubeconfigFile,
				ConfigVariables: config.Variables{
					"kubeconfig_path": config.StringVariable(tmpfile.Name()),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_file", "yaml_body", testNamespaceYAMLFile),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_file", "id"),
					testAccCheckNamespaceExists(k8sClient, "acctest-file"),
				),
			},
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, "acctest-file"),
	})
}

// Test different resource types
func TestAccManifestResource_Pod(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigPod,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_pod", "yaml_body", testPodYAML),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_pod", "id"),
					testAccCheckPodExists(k8sClient, "default", "acctest-pod"),
				),
			},
		},
		CheckDestroy: testAccCheckPodDestroy(k8sClient, "default", "acctest-pod"),
	})
}

// Alternative: Test namespace inference with ConfigMap (simpler than Pod)
func TestAccManifestResource_DefaultNamespaceInference(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Skip("TF_ACC_KUBECONFIG_RAW not set, skipping")
	}

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigDefaultNamespace,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_default_ns", "yaml_body", testConfigMapYAMLNoNamespace),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_default_ns", "id"),
					// Key test: ConfigMap with no namespace should end up in default
					testAccCheckConfigMapExists(k8sClient, "default", "acctest-config"),
				),
			},
		},
		CheckDestroy: testAccCheckConfigMapDestroy(k8sClient, "default", "acctest-config"),
	})
}

// Test constants
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

provider "k8sinline" {}

resource "k8sinline_manifest" "test_exec" {
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

const testNamespaceYAMLRaw = `apiVersion: v1
kind: Namespace
metadata:
  name: acctest-raw
`

const testAccManifestConfigKubeconfigRaw = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_raw" {
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

const testNamespaceYAMLFile = `apiVersion: v1
kind: Namespace
metadata:
  name: acctest-file
`

const testAccManifestConfigKubeconfigFile = `
variable "kubeconfig_path" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_file" {
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

provider "k8sinline" {}

resource "k8sinline_manifest" "test_pod" {
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

provider "k8sinline" {}

resource "k8sinline_manifest" "test_default_ns" {
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
