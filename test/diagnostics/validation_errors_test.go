package diagnostics

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestBuiltInResourceValidation_NotCEL is a regression test for Bug #3:
// Built-in Kubernetes resource validation errors were incorrectly labeled as
// "CEL Validation Failed" when they should be "Field Validation Failed".
//
// Expected behavior:
//   - Built-in resources (v1, apps/v1, etc.) with validation errors should show
//     "Field Validation Failed" or similar OpenAPI schema validation error
//   - Should NOT mention "CEL" or "Common Expression Language"
//
// This test will FAIL if the bug is present (shows CEL) and PASS after fix.
//
// How to run:
//
//	TEST=TestBuiltInResourceValidation_NotCEL make test-diagnostics
//
// Prerequisites:
//   - TF_ACC_KUBECONFIG must be set
//   - kubectl must be in PATH
//   - terraform must be in PATH
//   - k8sconnect provider must be installed (make install)
func TestBuiltInResourceValidation_NotCEL(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping diagnostic test in short mode")
	}

	// Generate unique names for this test run
	testID := time.Now().UnixNano() % 1000000
	namespace := fmt.Sprintf("diag-validation-%d", testID)

	// Setup: Create namespace with kubectl
	t.Logf("Creating namespace %s", namespace)

	nsYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, namespace)
	CreateKubectlResource(t, nsYAML)

	// Cleanup at the end
	defer func() {
		t.Logf("Cleaning up namespace %s", namespace)
		DeleteKubectlResource(t, "namespace", namespace, "")
	}()

	// Setup terraform working directory
	testDir := t.TempDir()
	SetupProviderConfig(t, testDir)

	// Write main.tf with ConfigMap that has invalid label (spaces in key)
	// This should trigger OpenAPI schema validation, NOT CEL validation
	mainTF := fmt.Sprintf(`resource "k8sconnect_object" "invalid_label" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-config
      namespace: %s
      labels:
        invalid label with spaces: "value"
    data:
      test: value
  YAML

  cluster = local.cluster
}
`, namespace)
	WriteTestFile(t, testDir, "main.tf", mainTF)

	// Run terraform init
	t.Log("Running terraform init")
	RunTerraformInit(t, testDir)

	// Run terraform apply - this should FAIL with validation error
	t.Log("Running terraform apply (expecting validation error)")
	output := RunTerraformApply(t, testDir)

	// Verify apply failed (non-zero exit code)
	if output.ExitCode == 0 {
		t.Errorf("Expected terraform apply to fail with validation error, but it succeeded.\nOutput:\n%s", output.Combined)
	}

	// Verify error message contains validation-related text
	// (either "Field Validation" or "Invalid value" from K8s API)
	hasValidationError := false
	outputLower := strings.ToLower(output.Combined)
	for _, phrase := range []string{"field validation", "invalid value", "invalid"} {
		if strings.Contains(outputLower, phrase) {
			hasValidationError = true
			break
		}
	}
	if !hasValidationError {
		t.Errorf("Expected output to contain validation error message.\nOutput:\n%s", output.Combined)
	}

	// CRITICAL: Verify error message does NOT mention CEL
	// This is the key regression test - built-in resources should never show CEL errors
	AssertOutputNotContains(t, output, "CEL")
	AssertOutputNotContains(t, output, "Common Expression Language")
	AssertOutputNotContains(t, output, "CRD schema")

	t.Log("✅ Validation error correctly identified as OpenAPI schema validation, not CEL")
}
