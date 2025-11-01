package diagnostics

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TerraformOutput captures the output and exit code of a terraform command
type TerraformOutput struct {
	Stdout   string
	Stderr   string
	Combined string
	ExitCode int
}

// RunTerraformInit runs terraform init in the given directory
func RunTerraformInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("terraform", "init", "-backend=false")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terraform init failed: %v\nOutput:\n%s", err, output)
	}
}

// RunTerraformApply runs terraform apply with auto-approve
func RunTerraformApply(t *testing.T, dir string) *TerraformOutput {
	t.Helper()
	cmd := exec.Command("terraform", "apply", "-auto-approve")
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("terraform apply failed: %v\nOutput:\n%s", err, output)
		}
	}

	return &TerraformOutput{
		Combined: string(output),
		ExitCode: exitCode,
	}
}

// RunTerraformPlan runs terraform plan and captures output
func RunTerraformPlan(t *testing.T, dir string) *TerraformOutput {
	t.Helper()
	cmd := exec.Command("terraform", "plan", "-detailed-exitcode")
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Only fail if it's not exit code 1 or 2 (which are expected)
			t.Logf("terraform plan output:\n%s", output)
		}
	}

	return &TerraformOutput{
		Combined: string(output),
		ExitCode: exitCode,
	}
}

// RunTerraformDestroy runs terraform destroy with auto-approve
func RunTerraformDestroy(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("terraform", "destroy", "-auto-approve")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("terraform destroy failed (non-fatal): %v\nOutput:\n%s", err, output)
	}
}

// AssertOutputContains fails the test if the output doesn't contain the substring
func AssertOutputContains(t *testing.T, output *TerraformOutput, substr string) {
	t.Helper()
	if !strings.Contains(output.Combined, substr) {
		t.Errorf("Expected output to contain %q but it didn't.\nFull output:\n%s", substr, output.Combined)
	}
}

// AssertOutputNotContains fails the test if the output contains the substring
// This is the key assertion for regression tests - we want to ensure warnings DON'T appear
func AssertOutputNotContains(t *testing.T, output *TerraformOutput, substr string) {
	t.Helper()
	if strings.Contains(output.Combined, substr) {
		// Find the context around the match for better error reporting
		lines := strings.Split(output.Combined, "\n")
		var contextLines []string
		for i, line := range lines {
			if strings.Contains(line, substr) {
				// Include 3 lines before and after for context
				start := i - 3
				if start < 0 {
					start = 0
				}
				end := i + 4
				if end > len(lines) {
					end = len(lines)
				}
				contextLines = lines[start:end]
				break
			}
		}

		t.Errorf("Expected output NOT to contain %q but it did.\nContext:\n%s\n\nFull output:\n%s",
			substr, strings.Join(contextLines, "\n"), output.Combined)
	}
}

// AssertPlanExitCode checks that terraform plan exited with the expected code
// 0 = no changes, 1 = error, 2 = changes present
func AssertPlanExitCode(t *testing.T, output *TerraformOutput, expectedCode int) {
	t.Helper()
	if output.ExitCode != expectedCode {
		t.Errorf("Expected plan exit code %d, got %d.\nOutput:\n%s",
			expectedCode, output.ExitCode, output.Combined)
	}
}

// WriteTestFile writes a .tf file to the test directory
func WriteTestFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write %s: %v", filename, err)
	}
}

// SetupProviderConfig writes the provider configuration for tests
func SetupProviderConfig(t *testing.T, dir string) {
	t.Helper()

	kubeconfig := os.Getenv("TF_ACC_KUBECONFIG")
	if kubeconfig == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set for diagnostics tests")
	}

	// Write versions.tf
	versions := `terraform {
  required_providers {
    k8sconnect = {
      source = "local/k8sconnect"
      version = "0.1.0"
    }
  }
}
`
	WriteTestFile(t, dir, "versions.tf", versions)

	// Write locals.tf with cluster connection (same pattern as test/examples)
	locals := fmt.Sprintf(`locals {
  cluster = {
    kubeconfig = %q
  }
}`, kubeconfig)
	WriteTestFile(t, dir, "locals.tf", locals)
}

// CreateKubectlResource creates a resource using kubectl apply
// This simulates an external resource that k8sconnect will interact with
func CreateKubectlResource(t *testing.T, yamlContent string) {
	t.Helper()

	// Write to temp file
	tmpfile := filepath.Join(t.TempDir(), "kubectl-resource.yaml")
	if err := os.WriteFile(tmpfile, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to write kubectl yaml: %v", err)
	}

	// Apply with kubectl
	cmd := exec.Command("kubectl", "apply", "-f", tmpfile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl apply failed: %v\nOutput:\n%s", err, output)
	}

	t.Logf("Created resource with kubectl:\n%s", output)
}

// DeleteKubectlResource deletes a resource using kubectl delete
func DeleteKubectlResource(t *testing.T, kind, name, namespace string) {
	t.Helper()

	args := []string{"delete", kind, name, "--ignore-not-found=true"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}

	cmd := exec.Command("kubectl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("kubectl delete warning (non-fatal): %v\nOutput:\n%s", err, output)
	}
}

// RunKubectlCommand runs an arbitrary kubectl command
func RunKubectlCommand(t *testing.T, command string) {
	t.Helper()

	// Split command into parts (simple split by spaces - works for our use cases)
	parts := strings.Fields(command)
	if len(parts) == 0 {
		t.Fatal("Empty kubectl command")
	}

	// First part should be "kubectl", rest are args
	if parts[0] != "kubectl" {
		t.Fatalf("Command must start with 'kubectl', got: %s", parts[0])
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl command failed: %v\nCommand: %s\nOutput:\n%s", err, command, output)
	}

	t.Logf("kubectl command succeeded: %s\nOutput: %s", command, output)
}
