package diagnostics

import (
	"fmt"
	"testing"
	"time"
)

// TestWaitTimeout_VeryShortTimeout tests wait behavior with absurdly short timeouts
// to understand if there's minimum validation needed.
//
// This is investigation for Issue #6 from SOAKTEST.md.
//
// Expected: Timeout error should appear quickly
// Actual: TBD - need to test with resource that's NOT already ready
func TestWaitTimeout_VeryShortTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping diagnostic test in short mode")
	}

	// Generate unique names
	testID := time.Now().UnixNano() % 1000000
	namespace := fmt.Sprintf("diag-timeout-%d", testID)
	deploymentName := fmt.Sprintf("timeout-deploy-%d", testID)

	// Create namespace with kubectl
	t.Logf("Creating namespace %s", namespace)
	nsYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, namespace)
	CreateKubectlResource(t, nsYAML)

	defer func() {
		t.Logf("Cleaning up namespace %s", namespace)
		DeleteKubectlResource(t, "namespace", namespace, "")
	}()

	// Setup terraform
	testDir := t.TempDir()
	SetupProviderConfig(t, testDir)

	// Create deployment that will take a while to roll out
	// Use a bad image so it gets stuck in ImagePullBackOff
	mainTF := fmt.Sprintf(`
resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: %s
      namespace: %s
    spec:
      replicas: 1
      selector:
        matchLabels:
          app: test
      template:
        metadata:
          labels:
            app: test
        spec:
          containers:
          - name: app
            image: this-image-does-not-exist:v1.0.0
  YAML

  cluster = local.cluster
}

# Wait with absurdly short timeout
resource "k8sconnect_wait" "short_timeout" {
  object_ref = k8sconnect_object.deployment.object_ref

  wait_for = {
    rollout = true
    timeout = "100ms"  # Way too short for any real deployment
  }

  cluster = local.cluster
}
`, deploymentName, namespace)
	WriteTestFile(t, testDir, "main.tf", mainTF)

	RunTerraformInit(t, testDir)

	// Apply - this should timeout
	t.Log("Running terraform apply - expecting timeout error")
	applyOutput := RunTerraformApply(t, testDir)

	// The test is to see what ACTUALLY happens with a 100ms timeout
	// Document the behavior:

	if applyOutput.ExitCode == 0 {
		t.Logf("UNEXPECTED: Apply succeeded with exit code 0")
		t.Logf("This means 100ms timeout did NOT trigger a timeout error")
		t.Logf("Possible explanations:")
		t.Logf("  1. Wait completed before checking (resource already ready)")
		t.Logf("  2. Minimum timeout enforced internally")
		t.Logf("  3. Timeout granularity issue (rounds to nearest second?)")
		t.Log("Full output:")
		t.Log(applyOutput.Combined)
		t.Fatal("Expected timeout error but got success - investigate further!")
	} else {
		t.Logf("Apply failed with exit code %d (expected)", applyOutput.ExitCode)

		// Check if it's a timeout error
		if AssertOutputContainsHelper(applyOutput, "Wait Timeout") || AssertOutputContainsHelper(applyOutput, "timeout") {
			t.Log("✅ Timeout error detected as expected")

			// Check how long it actually waited
			// Look for timing information in the output
			t.Log("Timeout error message:")
			t.Log(applyOutput.Combined)

			// Success - the timeout worked as expected
			t.Log("CONCLUSION: Very short timeout (100ms) DOES trigger timeout error when resource not ready")
		} else {
			// Different error
			t.Log("ERROR: Different error than expected timeout:")
			t.Log(applyOutput.Combined)
			t.Fatal("Expected timeout error but got different error")
		}
	}
}

// Helper to check if output contains string (returns bool, doesn't fail test)
func AssertOutputContainsHelper(output *TerraformOutput, substr string) bool {
	return len(output.Combined) > 0 && contains(output.Combined, substr)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
