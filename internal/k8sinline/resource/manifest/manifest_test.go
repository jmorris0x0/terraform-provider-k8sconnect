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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

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

  cluster_connection {
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

  cluster_connection {
    kubeconfig_raw = var.raw
  }
}
`
