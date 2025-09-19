// test/examples/examples_test.go
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

// test/examples/examples_test.go - fix the glob pattern
func TestExamples(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping examples test in short mode")
	}

	kubeconfig := os.Getenv("TF_ACC_KUBECONFIG_RAW")
	if kubeconfig == "" {
		t.Fatal("TF_ACC_KUBECONFIG_RAW must be set")
	}

	// Fix: Remove trailing slash from glob pattern
	exampleDirs, err := filepath.Glob("../../examples/*")
	if err != nil {
		t.Fatal(err)
	}

	if len(exampleDirs) == 0 {
		t.Fatal("No examples found in examples/ directory")
	}

	for _, dir := range exampleDirs {
		// Skip if not a directory (like README.md)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}

		// Check if directory contains .tf files
		tfFiles, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
		if len(tfFiles) == 0 {
			continue
		}

		exampleName := filepath.Base(dir)
		t.Run(exampleName, func(t *testing.T) {
			t.Parallel()
			testExampleDir(t, dir, kubeconfig)
		})
	}
}

func testExampleDir(t *testing.T, exampleDir string, kubeconfig string) {
	// Create temp directory for test
	testDir := t.TempDir()

	// Copy all .tf files from example
	tfFiles, _ := filepath.Glob(filepath.Join(exampleDir, "*.tf"))
	for _, file := range tfFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}

		// Apply namespace isolation
		content = isolateExample(content)

		destFile := filepath.Join(testDir, filepath.Base(file))
		if err := os.WriteFile(destFile, content, 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Write test infrastructure files
	writeTestFiles(t, testDir, kubeconfig)

	// Run Terraform lifecycle
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

func isolateExample(content []byte) []byte {
	// Generate a short hash for uniqueness
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Int63())))
	hash := hex.EncodeToString(h.Sum(nil))[:8] // First 8 chars of hash

	result := string(content)

	// Replace "example" namespace with unique suffix
	result = strings.ReplaceAll(result, `name: example`, fmt.Sprintf(`name: example-%s`, hash))
	result = strings.ReplaceAll(result, `namespace: example`, fmt.Sprintf(`namespace: example-%s`, hash))

	return []byte(result)
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
    kubeconfig_raw = optional(string)
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
	if err != nil {
		t.Logf("Command: terraform %s", strings.Join(args, " "))
		t.Logf("Output:\n%s", output)
		t.Fatalf("terraform %s failed: %v", strings.Join(args, " "), err)
	}
}
