package doctest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMarkdownDocumentation(t *testing.T) {
	// Get kubeconfig from environment
	kubeconfig := os.Getenv("TF_ACC_KUBECONFIG")
	if kubeconfig == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set for documentation tests")
	}

	// List of documentation files to test
	docFiles := []string{
		"../../README.md",
		"../../docs/index.md",
		"../../docs/resources/manifest.md",
		"../../docs/resources/patch.md",
		"../../docs/data-sources/manifest.md",
		"../../docs/data-sources/yaml_split.md",
		"../../docs/data-sources/yaml_scoped.md",
	}

	// Extract all runnable examples
	examples, err := ExtractFromMultipleFiles(docFiles)
	if err != nil {
		t.Fatalf("Failed to extract examples: %v", err)
	}

	if len(examples) == 0 {
		t.Skip("No runnable examples found in documentation")
	}

	t.Logf("Found %d runnable examples in documentation", len(examples))

	// Run each example as a subtest
	for _, example := range examples {
		t.Run(example.Name, func(t *testing.T) {
			t.Parallel()
			testExample(t, example, kubeconfig)
		})
	}
}

func testExample(t *testing.T, example RunnableExample, kubeconfig string) {
	// Create temp directory for test
	testDir := t.TempDir()

	// Generate a unique hash for namespace isolation
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Int63())))
	testHash := hex.EncodeToString(h.Sum(nil))[:8]

	// Apply isolation to the code (includes test name in namespace)
	isolatedCode := isolateExample(example.Code, example.Name, testHash)

	// Auto-create any required namespaces
	finalCode := ensureNamespaceExists(isolatedCode)

	// Write main.tf with the example code
	mainTf := filepath.Join(testDir, "main.tf")
	if err := os.WriteFile(mainTf, []byte(finalCode), 0644); err != nil {
		t.Fatalf("Failed to write main.tf: %v", err)
	}

	// Write test infrastructure files
	writeTestFiles(t, testDir, kubeconfig)

	// Run Terraform commands
	t.Logf("Testing example from %s:%d", example.Source, example.LineNum)

	runTerraform(t, testDir, "init", "-backend=false")
	runTerraform(t, testDir, "plan", "-out=tfplan")
	runTerraform(t, testDir, "apply", "tfplan")

	// Idempotency check
	cmd := exec.Command("terraform", "plan", "-detailed-exitcode")
	cmd.Dir = testDir
	output, _ := cmd.CombinedOutput()

	exitCode := cmd.ProcessState.ExitCode()
	if exitCode == 2 {
		t.Errorf("Documentation example not idempotent! Second plan shows changes:\n%s", output)
	} else if exitCode == 1 {
		t.Errorf("Second plan failed:\n%s", output)
	}

	// Cleanup
	runTerraform(t, testDir, "destroy", "-auto-approve")
}

func isolateExample(content string, testName string, hash string) string {
	// Sanitize test name for kubernetes: lowercase, replace underscores with hyphens
	sanitizedTestName := strings.ToLower(testName)
	sanitizedTestName = strings.ReplaceAll(sanitizedTestName, "_", "-")

	// Create namespace suffix with test name and hash: example-wait-rollout-a1b2c3d4
	namespaceSuffix := fmt.Sprintf("%s-%s", sanitizedTestName, hash)

	// Isolate "example" namespace to avoid conflicts when running tests in parallel
	result := content
	result = strings.ReplaceAll(result, `name: example`, fmt.Sprintf(`name: example-%s`, namespaceSuffix))
	result = strings.ReplaceAll(result, `namespace: example`, fmt.Sprintf(`namespace: example-%s`, namespaceSuffix))

	// Also handle quoted versions that might appear in ConfigMaps or outputs
	result = strings.ReplaceAll(result, `"example"`, fmt.Sprintf(`"example-%s"`, namespaceSuffix))

	return result
}

// ensureNamespaceExists scans the code for namespace references and automatically
// prepends a namespace resource if one doesn't already exist
func ensureNamespaceExists(content string) string {
	// Check if namespace resource already exists in the code
	if strings.Contains(content, "kind: Namespace") || strings.Contains(content, `kind        = "Namespace"`) {
		return content
	}

	// Find namespace references
	var foundNamespace string

	// Scan for namespace: references
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.Contains(line, "namespace:") && !strings.Contains(line, "namespace   =") {
			// Extract namespace name
			parts := strings.Split(line, "namespace:")
			if len(parts) > 1 {
				ns := strings.TrimSpace(parts[1])
				// Remove quotes if present
				ns = strings.Trim(ns, `"`)
				if ns != "" && ns != "default" && ns != "kube-system" {
					foundNamespace = ns
					break
				}
			}
		}
	}

	// If we found a namespace that needs to be created, prepend it
	if foundNamespace != "" {
		// Use namespace name in resource name to avoid conflicts in parallel tests
		// Replace hyphens with underscores for valid Terraform identifiers
		resourceName := strings.ReplaceAll(foundNamespace, "-", "_")
		namespaceResource := fmt.Sprintf(`resource "k8sconnect_manifest" "ns_%s" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster_connection = var.cluster_connection
}

`, resourceName, foundNamespace)

		return namespaceResource + content
	}

	return content
}

func writeTestFiles(t *testing.T, dir string, kubeconfig string) {
	// Write versions.tf (required_providers block)
	versions := `terraform {
  required_providers {
    k8sconnect = {
      source  = "local/k8sconnect"
      version = "0.1.0"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "versions.tf"), []byte(versions), 0644); err != nil {
		t.Fatalf("Failed to write versions.tf: %v", err)
	}

	// Write variables.tf
	variables := `variable "cluster_connection" {
  description = "Kubernetes cluster connection"
  type = object({
    kubeconfig = string
  })
}`
	if err := os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(variables), 0644); err != nil {
		t.Fatalf("Failed to write variables.tf: %v", err)
	}

	// Write terraform.tfvars
	tfvars := fmt.Sprintf(`cluster_connection = {
  kubeconfig = %q
}`, kubeconfig)
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfvars"), []byte(tfvars), 0644); err != nil {
		t.Fatalf("Failed to write terraform.tfvars: %v", err)
	}
}

func runTerraform(t *testing.T, dir string, args ...string) {
	cmd := exec.Command("terraform", args...)
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()

	if testing.Verbose() {
		t.Logf("Command: terraform %s", strings.Join(args, " "))
		t.Logf("Output:\n%s", output)
	}

	if err != nil {
		t.Logf("Command: terraform %s", strings.Join(args, " "))
		t.Logf("Output:\n%s", output)
		t.Fatalf("terraform %s failed: %v", strings.Join(args, " "), err)
	}
}
