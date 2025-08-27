// internal/k8sinline/resource/manifest/manifest_import_test.go
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
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline"
)

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

func TestAccManifestResource_ImportWithManagedFields(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	k8sClient := createK8sClient(t, raw)

	// Create a ConfigMap with multiple fields to test projection
	configMapName := "acctest-import-fields-" + fmt.Sprintf("%d", time.Now().Unix())

	// Create the ConfigMap directly in Kubernetes WITHOUT Terraform
	ctx := context.Background()
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: "default",
			Labels: map[string]string{
				"test":       "import",
				"created-by": "terraform-test",
			},
			Annotations: map[string]string{
				"test-annotation": "value",
			},
		},
		Data: map[string]string{
			"key1": "value1",
			"key2": "value2",
			"key3": "value3",
		},
	}

	_, err := k8sClient.CoreV1().ConfigMaps("default").Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test ConfigMap: %v", err)
	}

	// Ensure cleanup even if test fails
	defer func() {
		k8sClient.CoreV1().ConfigMaps("default").Delete(ctx, configMapName, metav1.DeleteOptions{})
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
				Config: testAccManifestConfigImportWithFields,
				ConfigVariables: config.Variables{
					"raw":  config.StringVariable(raw),
					"name": config.StringVariable(configMapName),
				},
				ResourceName:  "k8sinline_manifest.test_import",
				ImportState:   true,
				ImportStateId: fmt.Sprintf("kind-oidc-e2e/default/ConfigMap/%s", configMapName),

				ImportStateCheck: func(states []*terraform.InstanceState) error {
					t.Logf("DEBUG: ImportStateCheck called with %d states", len(states))

					for i, state := range states {
						t.Logf("DEBUG: State[%d] ID: %s", i, state.ID)
						t.Logf("DEBUG: State[%d] Ephemeral.Type: %s", i, state.Ephemeral.Type)
						t.Logf("DEBUG: State[%d] Attributes count: %d", i, len(state.Attributes))

						for key, value := range state.Attributes {
							if len(value) > 100 {
								t.Logf("DEBUG: State[%d].Attributes[%s] = %s... (truncated, %d bytes total)",
									i, key, value[:100], len(value))
							} else {
								t.Logf("DEBUG: State[%d].Attributes[%s] = %s", i, key, value)
							}
						}
					}

					if len(states) != 1 {
						return fmt.Errorf("expected 1 state, got %d", len(states))
					}
					state := states[0]

					// Verify that yaml_body was populated
					yamlBody := state.Attributes["yaml_body"]
					if yamlBody == "" {
						return fmt.Errorf("yaml_body should be populated after import")
					}

					// Verify the YAML contains expected content
					if !strings.Contains(yamlBody, configMapName) {
						return fmt.Errorf("yaml_body should contain ConfigMap name %q", configMapName)
					}
					if !strings.Contains(yamlBody, "key1: value1") {
						return fmt.Errorf("yaml_body should contain data fields")
					}

					// Verify server-generated fields were removed
					if strings.Contains(yamlBody, "uid:") {
						return fmt.Errorf("yaml_body should not contain server-generated uid field")
					}
					if strings.Contains(yamlBody, "resourceVersion:") {
						return fmt.Errorf("yaml_body should not contain server-generated resourceVersion field")
					}

					// Verify managed_state_projection was populated
					projection := state.Attributes["managed_state_projection"]
					if projection == "" {
						return fmt.Errorf("managed_state_projection should be populated after import")
					}

					// Verify projection contains expected fields
					// The projection should be a JSON string containing the managed fields
					if !strings.Contains(projection, "\"apiVersion\"") {
						return fmt.Errorf("managed_state_projection should contain apiVersion field")
					}
					if !strings.Contains(projection, "\"metadata\"") {
						return fmt.Errorf("managed_state_projection should contain metadata field")
					}
					if !strings.Contains(projection, "\"data\"") {
						return fmt.Errorf("managed_state_projection should contain data field")
					}

					// Log projection for debugging
					t.Logf("✅ Import successful with managed state projection:")
					t.Logf("   Projection size: %d bytes", len(projection))
					t.Logf("   Contains metadata: %v", strings.Contains(projection, "metadata"))
					t.Logf("   Contains data: %v", strings.Contains(projection, "data"))

					return nil
				},

				Check: resource.ComposeTestCheckFunc(
					func(s *terraform.State) error {
						t.Logf("DEBUG: Check function called")
						t.Logf("DEBUG: terraform.State has %d modules", len(s.Modules))

						for _, module := range s.Modules {
							t.Logf("DEBUG: Module path: %v", module.Path)
							t.Logf("DEBUG: Module has %d resources", len(module.Resources))
							for name, rs := range module.Resources {
								t.Logf("DEBUG: Resource %s type: %s", name, rs.Type)
								t.Logf("DEBUG: Resource %s provider: %s", name, rs.Provider)
								t.Logf("DEBUG: Resource %s primary ID: %s", name, rs.Primary.ID)
								t.Logf("DEBUG: Resource %s primary attributes count: %d", name, len(rs.Primary.Attributes))

								// Log key attributes
								if yamlBody, ok := rs.Primary.Attributes["yaml_body"]; ok {
									t.Logf("DEBUG: Resource %s yaml_body length: %d", name, len(yamlBody))
								}
								if projection, ok := rs.Primary.Attributes["managed_state_projection"]; ok {
									t.Logf("DEBUG: Resource %s managed_state_projection length: %d", name, len(projection))
								}
							}
						}

						// Check if our specific resource exists
						rs, ok := s.RootModule().Resources["k8sinline_manifest.test_import"]
						if !ok {
							t.Logf("DEBUG: Resource k8sinline_manifest.test_import NOT FOUND in state")
							t.Logf("DEBUG: Available resources: %v", s.RootModule().Resources)
							return fmt.Errorf("k8sinline_manifest.test_import not found in state")
						}

						t.Logf("DEBUG: Found resource in state with ID: %s", rs.Primary.ID)

						return nil
					},
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_import", "id"),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_import", "yaml_body"),
					resource.TestCheckResourceAttrSet("k8sinline_manifest.test_import", "managed_state_projection"),
					// Verify the ConfigMap still exists after import
					testAccCheckConfigMapExists(k8sClient, "default", configMapName),
				),
			},
		},
	})
}

const testAccManifestConfigImportWithFields = `
variable "raw" {
  type = string
}
variable "name" {
  type = string  
}

provider "k8sinline" {}

resource "k8sinline_manifest" "test_import" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: ${var.name}
      namespace: default
      labels:
        test: import
        created-by: terraform-test
      annotations:
        test-annotation: value
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
