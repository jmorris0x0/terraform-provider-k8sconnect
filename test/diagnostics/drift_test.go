package diagnostics

import (
	"fmt"
	"testing"
	"time"
)

// TestDriftWarning_ConsistentAcrossResourceTypes is a regression test for Issue #2:
// "Inconsistent Drift Warnings" - drift warnings appeared for Deployment but not ConfigMap,
// even though drift was detected in both.
//
// Expected behavior:
// - Deployment with drift: Shows drift warning ✅
// - ConfigMap with drift: Shows drift warning ❌ BUG (currently no warning)
//
// This test will FAIL before the fix and PASS after the fix.
//
// How to run:
//
//	TEST=TestDriftWarning_ConsistentAcrossResourceTypes make test-diagnostics
//
// Prerequisites:
//   - TF_ACC_KUBECONFIG must be set
//   - kubectl must be in PATH
//   - terraform must be in PATH
//   - k8sconnect provider must be installed (make install)
func TestDriftWarning_ConsistentAcrossResourceTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping diagnostic test in short mode")
	}

	// Generate unique names
	testID := time.Now().UnixNano() % 1000000
	namespace := fmt.Sprintf("diag-drift-%d", testID)
	deploymentName := fmt.Sprintf("test-deploy-%d", testID)
	configMapName := fmt.Sprintf("test-cm-%d", testID)

	// Setup: Create namespace
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

	// Setup terraform working directory
	testDir := t.TempDir()
	SetupProviderConfig(t, testDir)

	// Create both a Deployment and ConfigMap using k8sconnect_object
	mainTF := fmt.Sprintf(`
resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: %s
      namespace: %s
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: test
      template:
        metadata:
          labels:
            app: test
        spec:
          containers:
          - name: nginx
            image: nginx:latest
  YAML

  cluster = local.cluster
}

resource "k8sconnect_object" "configmap" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
    data:
      key1: original-value
      key2: another-value
  YAML

  cluster = local.cluster
}
`, deploymentName, namespace, configMapName, namespace)
	WriteTestFile(t, testDir, "main.tf", mainTF)

	// Initialize and apply to create resources
	t.Log("Running terraform init")
	RunTerraformInit(t, testDir)

	t.Log("Running terraform apply to create resources")
	firstApply := RunTerraformApply(t, testDir)
	if firstApply.ExitCode != 0 {
		t.Fatalf("First apply failed with exit code %d:\n%s", firstApply.ExitCode, firstApply.Combined)
	}

	// Introduce drift for BOTH resources using kubectl
	t.Log("Introducing drift for Deployment (scale replicas)")
	scaleCmd := fmt.Sprintf("kubectl scale deployment %s --replicas=5 -n %s", deploymentName, namespace)
	RunKubectlCommand(t, scaleCmd)

	t.Log("Introducing drift for ConfigMap (modify data)")
	// Use kubectl apply to modify the ConfigMap
	driftConfigMapYAML := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key1: MODIFIED-BY-KUBECTL
  key2: another-value
`, configMapName, namespace)
	CreateKubectlResource(t, driftConfigMapYAML)

	// Run terraform plan to detect drift
	t.Log("Running terraform plan - expecting drift warnings for BOTH resources")
	planOutput := RunTerraformPlan(t, testDir)

	// Exit code 2 means "changes detected" which is expected
	if planOutput.ExitCode != 2 {
		t.Logf("WARNING: Plan exit code was %d, expected 2 (changes detected)", planOutput.ExitCode)
	}

	// CRITICAL REGRESSION TEST:
	// Both resources have drift, so BOTH should show drift warnings.
	//
	// Before fix: Only Deployment shows warning, ConfigMap doesn't (INCONSISTENT)
	// After fix: Both show warnings (CONSISTENT)

	t.Log("Checking for Deployment drift warning...")
	hasDeploymentWarning := AssertOutputContainsHelper(planOutput, "Drift Detected") &&
		AssertOutputContainsHelper(planOutput, "k8sconnect_object.deployment")

	if !hasDeploymentWarning {
		t.Logf("WARNING: Deployment drift warning NOT found (unexpected)")
		t.Logf("Plan output:\n%s", planOutput.Combined)
	} else {
		t.Log("✅ Deployment drift warning found (expected)")
	}

	t.Log("Checking for ConfigMap drift warning...")
	// Each resource should have its OWN warning with specific field details
	// NOT collapsed into "(and more similar warning elsewhere)"
	// This ensures warnings are specific and actionable

	// Check if warnings are being collapsed
	hasCollapsedWarning := AssertOutputContainsHelper(planOutput, "more similar warning")

	if hasCollapsedWarning {
		t.Log("❌ ConfigMap drift warning IS COLLAPSED (BUG)")
		t.Log("ROOT CAUSE: Warning was collapsed by Terraform due to identical Summary")
		t.Log("FIX NEEDED: Include resource identity in warning Summary to prevent collapsing")
		t.Logf("Plan output:\n%s", planOutput.Combined)
		t.Fatal("REGRESSION TEST FAILED: ConfigMap drift warning lacks specific field details (collapsed). Issue #2 - warnings must be specific and actionable, not collapsed.")
	}

	// If not collapsed, verify both warnings show specific details
	hasConfigMapWarning := AssertOutputContainsHelper(planOutput, "k8sconnect_object.configmap") &&
		AssertOutputContainsHelper(planOutput, "data.key1")

	if !hasConfigMapWarning {
		t.Log("❌ ConfigMap warning missing expected details")
		t.Logf("Plan output:\n%s", planOutput.Combined)
		t.Fatal("ConfigMap drift warning doesn't show expected field details")
	}

	t.Log("✅ ConfigMap drift warning shows specific field details (not collapsed)")
	t.Log("✅ PASS: Both resources show SPECIFIC drift warnings with field details")
}

// TestDriftWarning_DeploymentOnly verifies that Deployment DOES show drift warning.
// This ensures we don't break the working case when fixing the ConfigMap case.
func TestDriftWarning_DeploymentOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping diagnostic test in short mode")
	}

	// Generate unique names
	testID := time.Now().UnixNano() % 1000000
	namespace := fmt.Sprintf("diag-drift-deploy-%d", testID)
	deploymentName := fmt.Sprintf("test-deploy-%d", testID)

	// Setup namespace
	t.Logf("Creating namespace %s", namespace)
	nsYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, namespace)
	CreateKubectlResource(t, nsYAML)

	defer func() {
		DeleteKubectlResource(t, "namespace", namespace, "")
	}()

	// Setup terraform
	testDir := t.TempDir()
	SetupProviderConfig(t, testDir)

	mainTF := fmt.Sprintf(`
resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: %s
      namespace: %s
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: test
      template:
        metadata:
          labels:
            app: test
        spec:
          containers:
          - name: nginx
            image: nginx:latest
  YAML

  cluster = local.cluster
}
`, deploymentName, namespace)
	WriteTestFile(t, testDir, "main.tf", mainTF)

	RunTerraformInit(t, testDir)

	t.Log("Creating deployment")
	firstApply := RunTerraformApply(t, testDir)
	if firstApply.ExitCode != 0 {
		t.Fatalf("Apply failed: %s", firstApply.Combined)
	}

	// Introduce drift
	t.Log("Introducing drift")
	scaleCmd := fmt.Sprintf("kubectl scale deployment %s --replicas=5 -n %s", deploymentName, namespace)
	RunKubectlCommand(t, scaleCmd)

	// Plan should show drift warning
	t.Log("Running plan")
	planOutput := RunTerraformPlan(t, testDir)

	// Should have drift warning
	AssertOutputContains(t, planOutput, "Drift Detected")

	t.Log("✅ PASS: Deployment drift warning appears as expected")
}
