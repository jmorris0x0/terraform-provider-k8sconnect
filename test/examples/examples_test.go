package examples

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

func TestExamples(t *testing.T) {
	// Get kubeconfig from environment
	kubeconfig := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if kubeconfig == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set for examples tests")
	}

	// Find all example directories
	examplesDir := "../../examples"
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("Failed to read examples directory: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		exampleDir := filepath.Join(examplesDir, entry.Name())

		// Check if directory contains .tf files
		tfFiles, _ := filepath.Glob(filepath.Join(exampleDir, "*.tf"))
		if len(tfFiles) == 0 {
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			t.Parallel()
			testExampleDir(t, exampleDir, kubeconfig)
		})
	}
}

func testExampleDir(t *testing.T, exampleDir string, kubeconfig string) {
	// Create temp directory for test
	testDir := t.TempDir()

	// Generate a single hash for this entire test to ensure consistency
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Int63())))
	testHash := hex.EncodeToString(h.Sum(nil))[:8]

	// Copy ALL files from example directory
	err := filepath.Walk(exampleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path from example dir
		relPath, err := filepath.Rel(exampleDir, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(testDir, relPath)

		if info.IsDir() {
			// Create directory
			return os.MkdirAll(destPath, 0755)
		}

		// Copy file
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// Apply isolation to .tf files and non-template YAML files
		// Use the SAME hash for all files in this test
		if strings.HasSuffix(path, ".tf") {
			content = isolateExampleWithHash(content, testHash)
		} else if (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) &&
			!strings.Contains(path, "template") {
			// Only isolate YAML files that aren't templates
			content = isolateExampleWithHash(content, testHash)
		}

		return os.WriteFile(destPath, content, 0644)
	})

	if err != nil {
		t.Fatalf("Failed to copy example files: %v", err)
	}

	// Write test infrastructure files
	writeTestFiles(t, testDir, kubeconfig)

	// Run Terraform commands
	runTerraform(t, testDir, "init", "-backend=false")
	runTerraform(t, testDir, "plan", "-out=tfplan")
	runTerraform(t, testDir, "apply", "tfplan")

	// Idempotency check
	cmd := exec.Command("terraform", "plan", "-detailed-exitcode")
	cmd.Dir = testDir
	output, _ := cmd.CombinedOutput()

	exitCode := cmd.ProcessState.ExitCode()
	if exitCode == 2 {
		t.Errorf("Example not idempotent! Second plan shows changes:\n%s", output)
	} else if exitCode == 1 {
		t.Errorf("Second plan failed:\n%s", output)
	}

	// Cleanup
	runTerraform(t, testDir, "destroy", "-auto-approve")
}

func isolateExampleWithHash(content []byte, hash string) []byte {
	result := string(content)

	// Only isolate "example" namespace, not others like "demo"
	// Use the provided hash to ensure consistency across all files
	result = strings.ReplaceAll(result, `name: example`, fmt.Sprintf(`name: example-%s`, hash))
	result = strings.ReplaceAll(result, `namespace: example`, fmt.Sprintf(`namespace: example-%s`, hash))

	return []byte(result)
}

// Backward compatibility wrapper (not used anymore but kept for reference)
func isolateExample(content []byte) []byte {
	// Generate a hash for this call
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Int63())))
	hash := hex.EncodeToString(h.Sum(nil))[:8]

	return isolateExampleWithHash(content, hash)
}

func writeTestFiles(t *testing.T, dir string, kubeconfig string) {
	// Write versions.tf (still needed for required_providers)
	versions := `terraform {
  required_providers {
    k8sconnect = {
      source  = "local/k8sconnect"
      version = "0.1.0"
    }
  }
}`
	os.WriteFile(filepath.Join(dir, "versions.tf"), []byte(versions), 0644)

	// Write variables.tf
	variables := `variable "cluster_connection" {
  description = "Kubernetes cluster connection"
  type = object({
    kubeconfig_raw = string
  })
}`
	os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(variables), 0644)

	// Write terraform.tfvars
	tfvars := fmt.Sprintf(`cluster_connection = {
  kubeconfig_raw = %q
}`, kubeconfig)
	os.WriteFile(filepath.Join(dir, "terraform.tfvars"), []byte(tfvars), 0644)
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
