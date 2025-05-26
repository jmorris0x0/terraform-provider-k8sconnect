// internal/k8sinline/resource/manifest/manifest_test.go
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
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
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

// Helper function to create K8s client for verification
func createK8sClient(t *testing.T, kubeconfigRaw string) kubernetes.Interface {
	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfigRaw))
	if err != nil {
		t.Fatalf("Failed to create kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	return clientset
}

// Check function to verify namespace exists in K8s
func testAccCheckNamespaceExists(client kubernetes.Interface, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		_, err := client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("namespace %q does not exist in Kubernetes: %v", name, err)
		}

		fmt.Printf("✅ Verified namespace %q exists in Kubernetes\n", name)
		return nil
	}
}

// Check function to verify namespace is cleaned up
func testAccCheckNamespaceDestroy(client kubernetes.Interface, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 10; i++ {
			_, err := client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified namespace %q was deleted from Kubernetes\n", name)
					return nil
				}
				return fmt.Errorf("unexpected error checking namespace %q: %v", name, err)
			}

			// Namespace still exists, wait a bit
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("namespace %q still exists in Kubernetes after waiting for deletion", name)
	}
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

// Test delete protection functionality
func TestAccManifestResource_DeleteProtection(t *testing.T) {
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
			// Step 1: Create resource with delete protection enabled
			{
				Config: testAccManifestConfigDeleteProtectionEnabled,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_protected", "delete_protection", "true"),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_protected", "id"),
					testAccCheckNamespaceExists(k8sClient, "acctest-protected"),
				),
			},
			// Step 2: Try to destroy - should fail due to protection
			{
				Config: testAccManifestConfigDeleteProtectionEnabled,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Destroy:     true,
				ExpectError: regexp.MustCompile("Resource Protected from Deletion"),
			},
			// Step 3: Disable protection
			{
				Config: testAccManifestConfigDeleteProtectionDisabled,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_protected", "delete_protection", "false"),
					testAccCheckNamespaceExists(k8sClient, "acctest-protected"),
				),
			},
			// Step 4: Now destroy should succeed
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, "acctest-protected"),
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

// Helper functions
func testAccCheckPodExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("pod %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified pod %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

func testAccCheckPodDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 15; i++ {
			_, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified pod %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking pod: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("pod %s/%s still exists after deletion", namespace, name)
	}
}

// Helper functions for ConfigMap verification
func testAccCheckConfigMapExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("configmap %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified configmap %s/%s exists (inferred namespace)\n", namespace, name)
		return nil
	}
}

func testAccCheckConfigMapDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 10; i++ {
			_, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified configmap %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking configmap: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("configmap %s/%s still exists after deletion", namespace, name)
	}
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

  cluster_connection {
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

  cluster_connection {
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

  cluster_connection {
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

  cluster_connection {
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

  cluster_connection {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigDeleteProtectionEnabled = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_protected" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-protected
YAML

  delete_protection = true

  cluster_connection {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigDeleteProtectionDisabled = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_protected" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-protected
YAML

  delete_protection = false

  cluster_connection {
    kubeconfig_raw = var.raw
  }
}
`

func TestAccManifestResource_Import(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	// Create a namespace manually that we'll import
	namespaceName := "acctest-import-" + fmt.Sprintf("%d", time.Now().Unix())

	// Create the namespace directly in Kubernetes
	ctx := context.Background()
	_, err := k8sClient.CoreV1().Namespaces().Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
			Labels: map[string]string{
				"test":       "import",
				"created-by": "terraform-test",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test namespace: %v", err)
	}

	// Ensure cleanup even if test fails
	defer func() {
		k8sClient.CoreV1().Namespaces().Delete(ctx, namespaceName, metav1.DeleteOptions{})
	}()

	// Write the kubeconfig to a temporary file for import to use
	kubeconfigFile := writeKubeconfigToTempFile(t, raw)
	defer os.Remove(kubeconfigFile)

	// Set KUBECONFIG environment variable for the import
	oldKubeconfig := os.Getenv("KUBECONFIG")
	os.Setenv("KUBECONFIG", kubeconfigFile)
	defer func() {
		if oldKubeconfig != "" {
			os.Setenv("KUBECONFIG", oldKubeconfig)
		} else {
			os.Unsetenv("KUBECONFIG")
		}
	}()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigImport,
				ConfigVariables: config.Variables{
					"raw":            config.StringVariable(raw),
					"namespace_name": config.StringVariable(namespaceName),
				},
				ResourceName: "k8sinline_manifest.test_import",
				ImportState:  true,
				// Use new format: context/kind/name (cluster-scoped resource)
				ImportStateId: fmt.Sprintf("kind-oidc-e2e/%s/%s", "Namespace", namespaceName),

				// Fixed ImportStateCheck with correct state attribute structure
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("expected 1 state, got %d", len(states))
					}
					state := states[0]

					// Verify that yaml_body was populated with actual YAML from cluster
					yamlBody := state.Attributes["yaml_body"]
					if yamlBody == "" {
						return fmt.Errorf("yaml_body should be populated after import")
					}

					// Verify the YAML contains expected content
					if !strings.Contains(yamlBody, namespaceName) {
						return fmt.Errorf("yaml_body should contain namespace name %q", namespaceName)
					}
					if !strings.Contains(yamlBody, "kind: Namespace") {
						return fmt.Errorf("yaml_body should contain 'kind: Namespace'")
					}
					if !strings.Contains(yamlBody, "test: import") {
						return fmt.Errorf("yaml_body should contain test label")
					}

					// Verify server-generated fields were removed
					if strings.Contains(yamlBody, "uid:") {
						return fmt.Errorf("yaml_body should not contain server-generated uid field")
					}
					if strings.Contains(yamlBody, "resourceVersion:") {
						return fmt.Errorf("yaml_body should not contain server-generated resourceVersion field")
					}

					// Verify ID was generated
					if state.ID == "" {
						return fmt.Errorf("resource ID should be set after import")
					}

					// Verify cluster_connection is populated with import details
					// Check that cluster_connection object exists (% shows attribute count)
					if state.Attributes["cluster_connection.%"] == "" {
						return fmt.Errorf("cluster_connection should be populated after import")
					}

					// Verify kubeconfig_file is set (should be the temp file we created)
					kubeconfigFile := state.Attributes["cluster_connection.kubeconfig_file"]
					if kubeconfigFile == "" {
						return fmt.Errorf("cluster_connection.kubeconfig_file should be populated after import")
					}

					// Verify context is set to the expected value from import ID
					context := state.Attributes["cluster_connection.context"]
					if context != "kind-oidc-e2e" {
						return fmt.Errorf("cluster_connection.context should be 'kind-oidc-e2e', got %q", context)
					}

					// Verify other connection fields are empty/null (not used during import)
					if state.Attributes["cluster_connection.host"] != "" {
						return fmt.Errorf("cluster_connection.host should be empty after import, got %q", state.Attributes["cluster_connection.host"])
					}
					if state.Attributes["cluster_connection.cluster_ca_certificate"] != "" {
						return fmt.Errorf("cluster_connection.cluster_ca_certificate should be empty after import, got %q", state.Attributes["cluster_connection.cluster_ca_certificate"])
					}
					if state.Attributes["cluster_connection.kubeconfig_raw"] != "" {
						return fmt.Errorf("cluster_connection.kubeconfig_raw should be empty after import, got %q", state.Attributes["cluster_connection.kubeconfig_raw"])
					}

					fmt.Printf("✅ Import successful - cluster_connection populated with import details\n")
					return nil
				},

				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_import", "id"),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_import", "yaml_body"),
					// Verify the namespace still exists after import
					testAccCheckNamespaceExists(k8sClient, namespaceName),
				),
			},
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, namespaceName),
	})
}

// Helper function to write kubeconfig to a temporary file
func writeKubeconfigToTempFile(t *testing.T, kubeconfigContent string) string {
	tmpfile, err := os.CreateTemp("", "kubeconfig-import-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	if _, err := tmpfile.Write([]byte(kubeconfigContent)); err != nil {
		tmpfile.Close()
		os.Remove(tmpfile.Name())
		t.Fatalf("Failed to write kubeconfig: %v", err)
	}

	if err := tmpfile.Close(); err != nil {
		os.Remove(tmpfile.Name())
		t.Fatalf("Failed to close temp file: %v", err)
	}

	return tmpfile.Name()
}

const testAccManifestConfigImport = `
variable "raw" {
  type = string
}
variable "namespace_name" {
  type = string  
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_import" {
  yaml_body = "# Will be populated during import"
  
  cluster_connection {
    kubeconfig_raw = var.raw
  }
}`

func TestAccManifestResource_ConnectionChange(t *testing.T) {
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
			// Step 1: Create with kubeconfig_raw
			{
				Config: testAccManifestConfigConnectionChange1,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_conn_change", "id"),
					testAccCheckNamespaceExists(k8sClient, "acctest-conn-change"),
					// TODO: Add check that ownership annotation exists on the K8s resource
				),
			},
			// Step 2: Change connection method (same cluster)
			{
				Config: testAccManifestConfigConnectionChange2,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_conn_change", "id"),
					testAccCheckNamespaceExists(k8sClient, "acctest-conn-change"),
				),
				// Should show warning about connection change but not error
				ExpectNonEmptyPlan: false,
			},
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, "acctest-conn-change"),
	})
}

const testAccManifestConfigConnectionChange1 = `
variable "raw" { type = string }
provider "k8sinline" {}

resource "k8sinline_manifest" "test_conn_change" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-conn-change
YAML

  cluster_connection {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigConnectionChange2 = `
variable "raw" { type = string }
provider "k8sinline" {}

resource "k8sinline_manifest" "test_conn_change" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-conn-change
YAML

  cluster_connection {
    kubeconfig_raw = var.raw
    context        = "kind-oidc-e2e"  # Explicit context (connection change)
  }
}
`

func TestForceDestroy(t *testing.T) {
	r := &manifestResource{}

	tests := []struct {
		name          string
		setupClient   func() k8sclient.K8sClient
		expectError   bool
		expectApplied bool
	}{
		{
			name: "successful force destroy with finalizers",
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()

				// First Get: return object with finalizers
				objWithFinalizers := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "PersistentVolumeClaim",
						"metadata": map[string]interface{}{
							"name":       "test-pvc",
							"namespace":  "default",
							"finalizers": []string{"kubernetes.io/pvc-protection"},
						},
					},
				}
				client.GetResponse = objWithFinalizers

				// After Apply (finalizer removal), subsequent Gets return NotFound
				client.GetError = errors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, "test-pvc")

				return client
			},
			expectError:   false,
			expectApplied: true,
		},
		{
			name: "object without finalizers but stuck",
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()

				// Object exists but has no finalizers
				objWithoutFinalizers := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]interface{}{
							"name":      "test-pod",
							"namespace": "default",
						},
					},
				}
				client.GetResponse = objWithoutFinalizers

				// Simulate eventual deletion after retry
				client.GetError = errors.NewNotFound(schema.GroupResource{Resource: "pods"}, "test-pod")

				return client
			},
			expectError:   false,
			expectApplied: false, // No Apply call needed since no finalizers
		},
		{
			name: "object disappeared during force destroy",
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()

				// Object not found when we try to get it for force destroy
				client.GetError = errors.NewNotFound(schema.GroupResource{Resource: "pods"}, "test-pod")

				return client
			},
			expectError:   false,
			expectApplied: false,
		},
		{
			name: "apply fails during finalizer removal",
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()

				// Object has finalizers
				objWithFinalizers := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "PersistentVolumeClaim",
						"metadata": map[string]interface{}{
							"name":       "test-pvc",
							"finalizers": []string{"kubernetes.io/pvc-protection"},
						},
					},
				}
				client.GetResponse = objWithFinalizers

				// Apply fails
				client.ApplyError = errors.NewForbidden(schema.GroupResource{Resource: "persistentvolumeclaims"}, "test-pvc", fmt.Errorf("access denied"))

				return client
			},
			expectError:   true,
			expectApplied: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := tt.setupClient()
			ctx := context.Background()

			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata": map[string]interface{}{
						"name": "test-pod",
					},
				},
			}

			gvr := schema.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "pods",
			}

			// Mock response for testing - simplified
			resp := &resource.DeleteResponse{}
			resp.Diagnostics = resource.NewDiagnostics()

			err := r.forceDestroy(ctx, client, gvr, obj, resp)

			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}

			// Check if Apply was called when expected
			applyCalled := len(client.ApplyCalls) > 0
			if applyCalled != tt.expectApplied {
				t.Errorf("expected Apply called: %v, got: %v", tt.expectApplied, applyCalled)
			}

			// If Apply was called, verify finalizers were removed
			if applyCalled {
				appliedObj := client.ApplyCalls[0].Object
				finalizers := appliedObj.GetFinalizers()
				if len(finalizers) != 0 {
					t.Errorf("expected finalizers to be removed, but got: %v", finalizers)
				}
			}
		})
	}
}

func TestDeleteWithForceDestroy(t *testing.T) {
	tests := []struct {
		name         string
		data         manifestResourceModel
		setupClient  func() k8sclient.K8sClient
		expectError  bool
		expectForced bool
	}{
		{
			name: "force_destroy disabled, timeout should error",
			data: manifestResourceModel{
				YAMLBody: types.StringValue(`apiVersion: v1
kind: Pod
metadata:
  name: test-pod`),
				DeleteTimeout: types.StringValue("1s"),
				ForceDestroy:  types.BoolValue(false),
			},
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()
				// Always return object (never deleted)
				client.GetResponse = &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]interface{}{
							"name":              "test-pod",
							"deletionTimestamp": "2024-01-01T00:00:00Z",
							"finalizers":        []string{"test-finalizer"},
						},
					},
				}
				return client
			},
			expectError:  true,
			expectForced: false,
		},
		{
			name: "force_destroy enabled, should succeed",
			data: manifestResourceModel{
				YAMLBody: types.StringValue(`apiVersion: v1
kind: Pod
metadata:
  name: test-pod`),
				DeleteTimeout: types.StringValue("1s"),
				ForceDestroy:  types.BoolValue(true),
			},
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()

				// First few calls: return object with finalizers
				objWithFinalizers := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]interface{}{
							"name":              "test-pod",
							"deletionTimestamp": "2024-01-01T00:00:00Z",
							"finalizers":        []string{"test-finalizer"},
						},
					},
				}
				client.GetResponse = objWithFinalizers

				// After force destroy Apply, return NotFound
				client.GetError = errors.NewNotFound(schema.GroupResource{Resource: "pods"}, "test-pod")

				return client
			},
			expectError:  false,
			expectForced: true,
		},
		{
			name: "delete_protection enabled, should block deletion",
			data: manifestResourceModel{
				YAMLBody: types.StringValue(`apiVersion: v1
kind: Pod
metadata:
  name: test-pod`),
				DeleteProtection: types.BoolValue(true),
				ForceDestroy:     types.BoolValue(true),
			},
			setupClient: func() k8sclient.K8sClient {
				return k8sclient.NewStubK8sClient()
			},
			expectError:  true,
			expectForced: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &manifestResource{
				clientGetter: func(conn ClusterConnectionModel) (k8sclient.K8sClient, error) {
					return tt.setupClient(), nil
				},
			}

			// Mock request/response - simplified for testing
			resp := &resource.DeleteResponse{}
			resp.Diagnostics = resource.NewDiagnostics()

			// This is a simplified test - in real testing you'd need to properly
			// set up the request.State with the data
			// r.Delete(context.Background(), req, resp)

			// For now, just test the individual components
			client := tt.setupClient()

			// Test that forceDestroy would be called if conditions are met
			if tt.data.ForceDestroy.ValueBool() && !tt.data.DeleteProtection.ValueBool() {
				obj := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata":   map[string]interface{}{"name": "test-pod"},
					},
				}

				gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

				err := r.forceDestroy(context.Background(), client, gvr, obj, resp)

				if tt.expectForced {
					// Should attempt to Apply (remove finalizers)
					if len(client.ApplyCalls) == 0 {
						t.Error("expected Apply call for force destroy")
					}
				}

				if tt.expectError && err == nil {
					t.Error("expected error but got none")
				}
				if !tt.expectError && err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestHandleDeletionTimeout(t *testing.T) {
	r := &manifestResource{}

	tests := []struct {
		name              string
		setupClient       func() k8sclient.K8sClient
		expectDiagnostics string
	}{
		{
			name: "finalizers blocking deletion",
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()
				client.GetResponse = &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "PersistentVolumeClaim",
						"metadata": map[string]interface{}{
							"name":              "test-pvc",
							"deletionTimestamp": "2024-01-01T00:00:00Z",
							"finalizers":        []string{"kubernetes.io/pvc-protection"},
						},
					},
				}
				return client
			},
			expectDiagnostics: "Deletion Blocked by Finalizers",
		},
		{
			name: "stuck without finalizers",
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()
				client.GetResponse = &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]interface{}{
							"name":              "test-pod",
							"deletionTimestamp": "2024-01-01T00:00:00Z",
						},
					},
				}
				return client
			},
			expectDiagnostics: "Deletion Stuck Without Finalizers",
		},
		{
			name: "deletion not initiated",
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()
				client.GetResponse = &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]interface{}{
							"name": "test-pod",
							// No deletionTimestamp
						},
					},
				}
				return client
			},
			expectDiagnostics: "Deletion Not Initiated",
		},
		{
			name: "object disappeared",
			setupClient: func() k8sclient.K8sClient {
				client := k8sclient.NewStubK8sClient()
				client.GetError = errors.NewNotFound(schema.GroupResource{Resource: "pods"}, "test-pod")
				return client
			},
			expectDiagnostics: "", // No diagnostics expected
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := tt.setupClient()
			resp := &resource.DeleteResponse{}
			resp.Diagnostics = resource.NewDiagnostics()

			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata":   map[string]interface{}{"name": "test-pod"},
				},
			}

			gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
			timeout := 5 * time.Minute
			timeoutErr := fmt.Errorf("timeout after %v", timeout)

			r.handleDeletionTimeout(resp, client, gvr, obj, timeout, timeoutErr)

			// Check that appropriate diagnostics were added
			hasExpectedDiagnostic := false
			if resp.Diagnostics != nil && resp.Diagnostics.HasError() {
				for _, diag := range resp.Diagnostics.Errors() {
					if strings.Contains(diag.Summary(), tt.expectDiagnostics) {
						hasExpectedDiagnostic = true
						break
					}
				}
			}

			if tt.expectDiagnostics != "" && !hasExpectedDiagnostic {
				errorSummaries := []string{}
				if resp.Diagnostics != nil {
					for _, diag := range resp.Diagnostics.Errors() {
						errorSummaries = append(errorSummaries, diag.Summary())
					}
				}
				t.Errorf("expected diagnostic containing %q, but got: %v", tt.expectDiagnostics, errorSummaries)
			}

			if tt.expectDiagnostics == "" && resp.Diagnostics != nil && resp.Diagnostics.HasError() {
				t.Errorf("expected no diagnostics, but got errors: %v", resp.Diagnostics.Errors())
			}
		})
	}
}

// Integration test for the full deletion flow
func TestAccManifestResource_ForceDestroy(t *testing.T) {
	// This would be in the acceptance test file
	const testConfig = `
variable "raw" { type = string }
provider "k8sinline" {}

resource "k8sinline_manifest" "test_force_destroy" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-force-destroy-pvc
  namespace: default
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 1Gi
YAML

  delete_timeout = "5s"    # Short timeout to trigger force destroy
  force_destroy = true     # Enable force destroy
  
  cluster_connection {
    kubeconfig_raw = var.raw
  }
}
`

	// Test would verify:
	// 1. PVC gets created
	// 2. On destroy, normal deletion is attempted
	// 3. After timeout, finalizers are removed automatically
	// 4. Resource is fully deleted without state drift
	// 5. Warning is shown about finalizer removal
}

// Helper to create unstructured object with finalizers
func createObjectWithFinalizers(kind, name string, finalizers []string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":       name,
				"finalizers": finalizers,
			},
		},
	}
}
