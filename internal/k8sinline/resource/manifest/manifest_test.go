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
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

  cluster_connection = {
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

  cluster_connection = {
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
  
  cluster_connection = {
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

  cluster_connection = {
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

  cluster_connection = {
    kubeconfig_raw = var.raw
    context        = "kind-oidc-e2e"  # Explicit context (connection change)
  }
}
`

func TestAccManifestResource_ForceDestroy(t *testing.T) {
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
				Config: testAccManifestConfigForceDestroy,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_force", "force_destroy", "true"),
					resource.TestCheckResourceAttr("k8sinline_manifest.test_force", "delete_timeout", "30s"),
					testAccCheckPVCExists(k8sClient, "default", "test-pvc-force"),
				),
			},
		},
		CheckDestroy: testAccCheckPVCDestroy(k8sClient, "default", "test-pvc-force"),
	})
}

func TestAccManifestResource_DeleteTimeout(t *testing.T) {
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
				Config: testAccManifestConfigDeleteTimeout,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sinline_manifest.test_timeout", "delete_timeout", "2m"),
					testAccCheckNamespaceExists(k8sClient, "acctest-timeout"),
				),
			},
		},
		CheckDestroy: testAccCheckNamespaceDestroy(k8sClient, "acctest-timeout"),
	})
}

// Helper functions for PVC testing (since PVCs commonly have finalizers)
func testAccCheckPVCExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("pvc %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified PVC %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

func testAccCheckPVCDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 20; i++ { // Longer wait for PVCs
			_, err := client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified PVC %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking PVC: %v", err)
			}
			time.Sleep(2 * time.Second)
		}
		return fmt.Errorf("PVC %s/%s still exists after deletion", namespace, name)
	}
}

// Test configurations
const testAccManifestConfigForceDestroy = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_force" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-pvc-force
  namespace: default
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
YAML

  delete_timeout = "30s"
  force_destroy = true

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

const testAccManifestConfigDeleteTimeout = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_timeout" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-timeout
YAML

  delete_timeout = "2m"

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

	k8sClient := createK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sinline": providerserver.NewProtocol6WithError(k8sinline.New()),
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
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_deferred_env", "id"),
					testAccCheckConfigMapExists(k8sClient, "default", "test-deferred-auth-env"),
					// Verify the random values made it into the exec env vars (not the YAML)
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_deferred_env", "cluster_connection.exec.env.TEST_SESSION_ID"),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_deferred_env", "cluster_connection.exec.env.TEST_TRACE_ID"),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_deferred_env", "cluster_connection.exec.env.TEST_RUN_ID"),
					// Verify the exec command and args are what we expect
					resource.TestCheckResourceAttr("k8sinline_manifest.test_deferred_env", "cluster_connection.exec.command", "sh"),
					resource.TestCheckResourceAttr("k8sinline_manifest.test_deferred_env", "cluster_connection.exec.args.#", "2"),
				),
			},
		},
		CheckDestroy: testAccCheckConfigMapDestroy(k8sClient, "default", "test-deferred-auth-env"),
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

resource "k8sinline_manifest" "test_deferred_env" {
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

func TestAccManifestResource_DriftDetection(t *testing.T) {
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
			// Step 1: Create initial ConfigMap
			{
				Config: testAccManifestConfigDriftDetectionInitial,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sinline_manifest.drift_test", "id"),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.drift_test", "managed_state_projection"),
					testAccCheckConfigMapExists(k8sClient, "default", "drift-test-cm"),
				),
			},
			// Step 2: Modify ConfigMap outside of Terraform (simulating drift)
			{
				PreConfig: func() {
					ctx := context.Background()

					// Modify the ConfigMap using the same field manager to avoid conflicts
					// This simulates drift without causing field manager conflicts
					dynamicClient, err := k8sclient.NewDynamicK8sClientFromKubeconfig([]byte(raw), "")
					if err != nil {
						t.Fatalf("Failed to create dynamic client: %v", err)
					}

					// Create modified object with same structure but different values
					modifiedCM := &unstructured.Unstructured{
						Object: map[string]interface{}{
							"apiVersion": "v1",
							"kind":       "ConfigMap",
							"metadata": map[string]interface{}{
								"name":      "drift-test-cm",
								"namespace": "default",
								"annotations": map[string]interface{}{
									"example.com/team": "platform-team", // Changed from backend-team
								},
							},
							"data": map[string]interface{}{
								"key1": "modified-outside-terraform", // Changed value
								"key2": "value2",                     // Unchanged
								"key3": "value3-modified",            // Changed value
								// Note: we're not adding/removing fields to avoid structural conflicts
							},
						},
					}

					// Apply with k8sinline field manager (same as Terraform uses)
					// This simulates drift that Terraform can correct
					err = dynamicClient.Apply(ctx, modifiedCM, k8sclient.ApplyOptions{
						FieldManager: "k8sinline",
						Force:        true,
					})
					if err != nil {
						t.Fatalf("Failed to apply modified ConfigMap: %v", err)
					}
					t.Log("✅ Modified ConfigMap outside of Terraform (simulating drift)")
				},
				Config: testAccManifestConfigDriftDetectionInitial,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				// This should show drift!
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// Step 3: Verify drift is corrected by apply
			{
				Config: testAccManifestConfigDriftDetectionInitial,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify ConfigMap is back to original state
					testAccCheckConfigMapData(k8sClient, "default", "drift-test-cm", map[string]string{
						"key1": "value1",
						"key2": "value2",
						"key3": "value3",
					}),
					// Verify annotation is back to original
					testAccCheckConfigMapAnnotation(k8sClient, "default", "drift-test-cm",
						"example.com/team", "backend-team"),
				),
			},
		},
		CheckDestroy: testAccCheckConfigMapDestroy(k8sClient, "default", "drift-test-cm"),
	})
}

const testAccManifestConfigDriftDetectionInitial = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "drift_test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: drift-test-cm
  namespace: default
  annotations:
    example.com/team: "backend-team"
data:
  key1: value1
  key2: value2
  key3: value3
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

func TestAccManifestResource_NoDriftWhenNoChanges(t *testing.T) {
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
			// Step 1: Create resource
			{
				Config: testAccManifestConfigNoDrift,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sinline_manifest.no_drift", "id"),
					testAccCheckConfigMapExists(k8sClient, "default", "no-drift-cm"),
				),
			},
			// Step 2: Run plan without any changes - should be empty
			{
				Config: testAccManifestConfigNoDrift,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // No drift expected!
			},
			// Step 3: Add field that we don't manage - should still show no drift
			{
				PreConfig: func() {
					ctx := context.Background()
					cm, err := k8sClient.CoreV1().ConfigMaps("default").Get(ctx, "no-drift-cm", metav1.GetOptions{})
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

					_, err = k8sClient.CoreV1().ConfigMaps("default").Update(ctx, cm, metav1.UpdateOptions{})
					if err != nil {
						t.Fatalf("Failed to update ConfigMap: %v", err)
					}
					t.Log("✅ Added unmanaged fields to ConfigMap")
				},
				Config: testAccManifestConfigNoDrift,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: false, // Still no drift - we don't manage those fields!
			},
		},
		CheckDestroy: testAccCheckConfigMapDestroy(k8sClient, "default", "no-drift-cm"),
	})
}

const testAccManifestConfigNoDrift = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "no_drift" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: no-drift-cm
  namespace: default
data:
  config: |
    setting1=value1
    setting2=value2
YAML

  cluster_connection = {
    kubeconfig_raw = var.raw
  }
}
`

func TestAccManifestResource_DriftDetectionNestedStructures(t *testing.T) {
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
			// Step 1: Create Deployment
			{
				Config: testAccManifestConfigDriftDetectionDeployment,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sinline_manifest.drift_deployment", "id"),
					testAccCheckDeploymentExists(k8sClient, "default", "drift-test-deployment"),
				),
			},
			// Step 2: Modify nested fields
			{
				PreConfig: func() {
					ctx := context.Background()
					dep, err := k8sClient.AppsV1().Deployments("default").Get(ctx, "drift-test-deployment", metav1.GetOptions{})
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

					_, err = k8sClient.AppsV1().Deployments("default").Update(ctx, dep, metav1.UpdateOptions{})
					if err != nil {
						t.Fatalf("Failed to update Deployment: %v", err)
					}
					t.Log("✅ Modified Deployment nested fields")
				},
				Config: testAccManifestConfigDriftDetectionDeployment,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // Should detect drift in image and replicas
			},
		},
		CheckDestroy: testAccCheckDeploymentDestroy(k8sClient, "default", "drift-test-deployment"),
	})
}

const testAccManifestConfigDriftDetectionDeployment = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "drift_deployment" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: drift-test-deployment
  namespace: default
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
}
`

func TestAccManifestResource_DriftDetectionArrays(t *testing.T) {
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
			// Step 1: Create Service with multiple ports
			{
				Config: testAccManifestConfigDriftDetectionService,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sinline_manifest.drift_service", "id"),
					testAccCheckServiceExists(k8sClient, "default", "drift-test-service"),
				),
			},
			// Step 2: Modify array elements
			{
				PreConfig: func() {
					ctx := context.Background()
					svc, err := k8sClient.CoreV1().Services("default").Get(ctx, "drift-test-service", metav1.GetOptions{})
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

					_, err = k8sClient.CoreV1().Services("default").Update(ctx, svc, metav1.UpdateOptions{})
					if err != nil {
						t.Fatalf("Failed to update Service: %v", err)
					}
					t.Log("✅ Modified Service ports array")
				},
				Config: testAccManifestConfigDriftDetectionService,
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // Should detect port change
			},
		},
		CheckDestroy: testAccCheckServiceDestroy(k8sClient, "default", "drift-test-service"),
	})
}

const testAccManifestConfigDriftDetectionService = `
variable "raw" {
  type = string
}

provider "k8sinline" {}

resource "k8sinline_manifest" "drift_service" {
  yaml_body = <<YAML
apiVersion: v1
kind: Service
metadata:
  name: drift-test-service
  namespace: default
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
}
`

// Add these helper functions at the bottom of manifest_test.go

func testAccCheckConfigMapData(client kubernetes.Interface, namespace, name string, expectedData map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap: %v", err)
		}

		for key, expectedValue := range expectedData {
			actualValue, exists := cm.Data[key]
			if !exists {
				return fmt.Errorf("expected data key %q not found", key)
			}
			if actualValue != expectedValue {
				return fmt.Errorf("data key %q: expected %q, got %q", key, expectedValue, actualValue)
			}
		}
		return nil
	}
}

func testAccCheckConfigMapFieldNotExists(client kubernetes.Interface, namespace, name, field string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap: %v", err)
		}

		if _, exists := cm.Data[field]; exists {
			return fmt.Errorf("field %q should not exist but does", field)
		}
		return nil
	}
}

func testAccCheckDeploymentExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("deployment %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified deployment %s/%s exists\n", namespace, name)
		return nil
	}
}

func testAccCheckDeploymentDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 20; i++ {
			_, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified deployment %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking deployment: %v", err)
			}
			time.Sleep(2 * time.Second)
		}
		return fmt.Errorf("deployment %s/%s still exists after deletion", namespace, name)
	}
}

func testAccCheckServiceExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("service %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified service %s/%s exists\n", namespace, name)
		return nil
	}
}

func testAccCheckServiceDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 10; i++ {
			_, err := client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified service %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking service: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("service %s/%s still exists after deletion", namespace, name)
	}
}

func testAccCheckConfigMapAnnotation(client kubernetes.Interface, namespace, name, key, expectedValue string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap: %v", err)
		}

		actualValue, exists := cm.Annotations[key]
		if !exists {
			return fmt.Errorf("expected annotation %q not found", key)
		}
		if actualValue != expectedValue {
			return fmt.Errorf("annotation %q: expected %q, got %q", key, expectedValue, actualValue)
		}
		return nil
	}
}
