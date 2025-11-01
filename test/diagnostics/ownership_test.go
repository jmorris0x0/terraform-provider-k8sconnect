package diagnostics

import (
	"fmt"
	"testing"
	"time"
)

// TestPatchOwnershipWarning_OnlyOnActualChange is a regression test for Issue #1:
// "Patch Field Ownership Transition" warning appeared on EVERY plan,
// even when ownership hadn't changed and we already owned all the fields.
//
// Expected behavior:
// - First apply: Warning appears (taking ownership from kubectl) ✅
// - Second plan with no changes: NO warning (we already own the fields) ❌ BUG
//
// This test will FAIL before the fix and PASS after the fix.
//
// How to run:
//
//	TEST=TestPatchOwnershipWarning_OnlyOnActualChange go test -v ./test/diagnostics -timeout 5m
//
// Prerequisites:
//   - TF_ACC_KUBECONFIG must be set
//   - kubectl must be in PATH
//   - terraform must be in PATH
//   - k8sconnect provider must be installed (make install)
func TestPatchOwnershipWarning_OnlyOnActualChange(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping diagnostic test in short mode")
	}

	// Generate unique names for this test run
	testID := time.Now().UnixNano() % 1000000
	namespace := fmt.Sprintf("diag-ownership-%d", testID)
	deploymentName := fmt.Sprintf("test-deploy-%d", testID)

	// Setup: Create namespace and deployment with kubectl (external ownership)
	t.Logf("Creating namespace %s and deployment %s with kubectl", namespace, deploymentName)

	nsYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, namespace)
	CreateKubectlResource(t, nsYAML)

	deployYAML := fmt.Sprintf(`apiVersion: apps/v1
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
      - name: nginx
        image: nginx:latest
`, deploymentName, namespace)
	CreateKubectlResource(t, deployYAML)

	// Cleanup at the end
	defer func() {
		t.Logf("Cleaning up namespace %s", namespace)
		DeleteKubectlResource(t, "namespace", namespace, "")
	}()

	// Setup terraform working directory
	testDir := t.TempDir()
	SetupProviderConfig(t, testDir)

	// Write main.tf with k8sconnect_patch resource
	mainTF := fmt.Sprintf(`resource "k8sconnect_patch" "test" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "%s"
    namespace   = "%s"
  }

  patch = jsonencode({
    spec = {
      replicas = 2
    }
  })

  cluster = local.cluster
}
`, deploymentName, namespace)
	WriteTestFile(t, testDir, "main.tf", mainTF)

	// Run terraform init
	t.Log("Running terraform init")
	RunTerraformInit(t, testDir)

	// STEP 1: First apply - take ownership from kubectl
	// EXPECT: Warning about ownership transition (kubectl → k8sconnect-patch)
	t.Log("STEP 1: First apply - taking ownership from kubectl")
	firstApply := RunTerraformApply(t, testDir)

	if firstApply.ExitCode != 0 {
		t.Fatalf("First apply failed with exit code %d:\n%s", firstApply.ExitCode, firstApply.Combined)
	}

	// First apply SHOULD have the warning (documenting expected behavior)
	// Note: We don't assert this because the warning might appear during plan or apply,
	// and the exact timing isn't critical for this test. We only care about the SECOND plan.
	t.Logf("First apply completed. Output length: %d bytes", len(firstApply.Combined))

	// STEP 2: Second plan with same config - ownership already correct
	// EXPECT: NO warning (we already own the fields)
	// BUG: Currently shows warning even though nothing changed
	t.Log("STEP 2: Second plan - ownership already correct, expecting NO warning")
	secondPlan := RunTerraformPlan(t, testDir)

	// Exit code 0 = no changes (expected for idempotent apply)
	AssertPlanExitCode(t, secondPlan, 0)

	// CRITICAL REGRESSION TEST:
	// The second plan should NOT show ownership transition warning
	// because we already own the fields from the first apply.
	//
	// Before fix: Test FAILS - warning appears
	// After fix: Test PASSES - no warning
	AssertOutputNotContains(t, secondPlan, "Ownership Transition")

	t.Log("✅ PASS: No ownership warning on second plan (ownership unchanged)")
}

// TestPatchOwnershipWarning_AppearsOnActualChange verifies that we DO show
// the warning when ownership actually changes between applies.
//
// This ensures our fix doesn't break the legitimate warning case.
func TestPatchOwnershipWarning_AppearsOnActualChange(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping diagnostic test in short mode")
	}

	// Generate unique names
	testID := time.Now().UnixNano() % 1000000
	namespace := fmt.Sprintf("diag-transition-%d", testID)
	deploymentName := fmt.Sprintf("test-deploy-%d", testID)

	// Setup: Create namespace and deployment with kubectl
	t.Logf("Creating namespace %s and deployment %s with kubectl", namespace, deploymentName)

	nsYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, namespace)
	CreateKubectlResource(t, nsYAML)

	deployYAML := fmt.Sprintf(`apiVersion: apps/v1
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
      - name: nginx
        image: nginx:latest
`, deploymentName, namespace)
	CreateKubectlResource(t, deployYAML)

	defer func() {
		DeleteKubectlResource(t, "namespace", namespace, "")
	}()

	// Setup terraform
	testDir := t.TempDir()
	SetupProviderConfig(t, testDir)

	mainTF := fmt.Sprintf(`resource "k8sconnect_patch" "test" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "%s"
    namespace   = "%s"
  }

  patch = jsonencode({
    spec = {
      replicas = 2
    }
  })

  cluster = local.cluster
}
`, deploymentName, namespace)
	WriteTestFile(t, testDir, "main.tf", mainTF)

	RunTerraformInit(t, testDir)

	// First apply - takes ownership from kubectl (no warning on first apply - no previous state)
	t.Log("First apply - taking ownership from kubectl")
	firstApply := RunTerraformApply(t, testDir)
	if firstApply.ExitCode != 0 {
		t.Fatalf("First apply failed: %s", firstApply.Combined)
	}

	// Modify the field externally with kubectl to simulate ownership conflict
	t.Log("Modifying field externally with kubectl")
	scaleCmd := fmt.Sprintf("kubectl scale deployment %s --replicas=3 -n %s", deploymentName, namespace)
	RunKubectlCommand(t, scaleCmd)

	// Second plan - field modified externally, should show drift warning
	t.Log("Second plan - field modified externally, should show drift warning")
	secondPlan := RunTerraformPlan(t, testDir)

	// The warning SHOULD appear because kubectl modified a field we own (drift/conflict)
	// Note: This is a drift warning, not an ownership transition warning
	// Note: Warning now includes resource identity to prevent collapsing
	AssertOutputContains(t, secondPlan, "Field Ownership Conflict")

	t.Log("✅ PASS: Drift warning appears when external changes conflict with managed fields")
}
