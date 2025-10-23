# Import Ownership Issue - Complete Investigation & Fix Guide

**Date:** 2025-10-22 (Updated 2025-10-23)
**Current Commit:** 9bc8702 "Improve import yaml_body cleaning"
**Status:** ✅ All tests passing at baseline | ❌ Import→Apply broken in real usage

---

## Executive Summary

**The Bug:** After importing a kubectl-created resource, the first `terraform apply` fails with "Provider produced inconsistent result after apply" errors related to field_ownership.

**Root Cause:** We add internal annotations during apply that aren't predicted during plan, causing unexpected field_ownership entries.

**Solution:** Filter our internal annotations (`k8sconnect.terraform.io/*`) from field_ownership tracking - they're implementation details, not user-managed fields.

**Files to Change:** `internal/k8sconnect/resource/object/field_ownership.go`

---

## CRITICAL: Original Motivation - Import with --generate-config

**⚠️ WARNING:** This bug was discovered while trying to use Terraform's `import` blocks with `--generate-config`. We MUST ensure this workflow works. Do NOT spend hours fixing basic import only to discover generate-config doesn't work.

### The Desired User Workflow

1. **User creates resource externally** (e.g., with kubectl):
   ```bash
   kubectl apply -f my-resource.yaml
   ```

2. **User writes an import block** in their Terraform config:
   ```hcl
   import {
     to = k8sconnect_object.my_resource
     id = "my-context:my-namespace:apps/v1/Deployment:my-deployment"
   }
   ```

3. **User generates config**:
   ```bash
   terraform plan -generate-config-out=generated.tf
   ```

   Terraform should populate `yaml_body` with the resource definition.

4. **User applies** to take ownership:
   ```bash
   terraform apply
   ```

### Open Questions That Must Be Answered

1. **What goes in `yaml_body` during generate-config?**
   - Option A: Only user-specified fields (minimal)
   - Option B: All fields including K8s defaults
   - Option C: Managed fields only (based on field ownership)

2. **How do we distinguish user fields from K8s defaults?**
   - Can we use `managedFields` to determine what kubectl originally specified?
   - Should we filter out server-added fields (like `status`, `metadata.uid`, etc.)?

3. **Test Requirements:**
   - All import tests should use import blocks + `--generate-config` pattern
   - Tests should verify the generated config is correct and apply works without drift

**This is not optional.** If generate-config doesn't work, the import feature is incomplete.

---

## Reproduction Steps (100% Reliable)

### Prerequisites
```bash
cd /Users/jonathan/code/terraform-provider-k8sconnect/scenarios/kind-validation
./reset.sh  # Creates kind cluster, generates kind-validation-config kubeconfig
terraform init
```

### Step-by-Step Reproduction

**1. Create a ConfigMap with kubectl (simulating pre-existing infrastructure):**

```bash
KUBECONFIG=kind-validation-config kubectl create namespace import-test
KUBECONFIG=kind-validation-config kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-import
  namespace: import-test
  labels:
    created-by: kubectl
data:
  key1: value1
  key2: value2
EOF
```

Verify field manager:
```bash
KUBECONFIG=kind-validation-config kubectl get configmap test-import -n import-test -o yaml | grep -A5 managedFields
```
Should show `manager: kubectl-client-side-apply`

**2. Create Terraform config for import:**

```hcl
# import-test.tf
resource "k8sconnect_object" "imported_configmap" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-import
      namespace: import-test
      labels:
        created-by: kubectl
    data:
      key1: value1
      key2: value2
  YAML

  cluster_connection = {
    kubeconfig = file("${path.module}/kind-validation-config")
  }
}
```

**3. Import the resource:**

```bash
terraform import 'k8sconnect_object.imported_configmap' 'kind-kind-validation:import-test:v1/ConfigMap:test-import'
```

✅ **Expected:** Import succeeds
✅ **Actual:** Import succeeds - correctly extracts kubectl ownership

**4. Plan after import:**

```bash
terraform plan -target='k8sconnect_object.imported_configmap'
```

✅ **Expected:** Plan succeeds, shows taking ownership from kubectl
✅ **Actual:** Plan succeeds, shows:
```
Warning: Field Ownership Override

Forcing ownership of fields managed by other controllers:
  - data.key1 (managed by "kubectl-client-side-apply")
  - data.key2 (managed by "kubectl-client-side-apply")
  - metadata.labels.created-by (managed by "kubectl-client-side-apply")
```

**5. Apply after import:**

```bash
terraform apply -target='k8sconnect_object.imported_configmap' -auto-approve
```

❌ **Expected:** Apply succeeds
❌ **Actual:** Apply FAILS with:

```
Error: Provider produced inconsistent result after apply

When applying changes to k8sconnect_object.imported_configmap, provider
produced an unexpected new value: .field_ownership: new element
"metadata.annotations.k8sconnect.terraform.io/created-at" has appeared.

Error: Provider produced inconsistent result after apply

When applying changes to k8sconnect_object.imported_configmap, provider
produced an unexpected new value: .field_ownership: new element
"metadata.annotations.k8sconnect.terraform.io/terraform-id" has appeared.
```

---

## Root Cause Analysis

### What Happens Step-by-Step

**During Import (`internal/k8sconnect/resource/object/import.go`):**
1. Read resource from Kubernetes
2. Extract field_ownership from `managedFields` (correctly shows kubectl-client-side-apply)
3. Store in Terraform state
4. Annotations `k8sconnect.terraform.io/created-at` and `terraform-id` **do NOT exist yet**

**During Plan (`internal/k8sconnect/resource/object/plan_modifier.go`):**
1. ModifyPlan executes server-side dry-run with yaml_body
2. Dry-run predicts taking ownership of data.key1, data.key2, metadata.labels
3. **Dry-run does NOT include our annotations** (they're not in yaml_body)
4. Predicted field_ownership: data.key1, data.key2, metadata.labels (owned by k8sconnect)
5. **Missing:** No prediction for annotation ownership

**During Apply (`internal/k8sconnect/resource/object/crud.go` Update function):**
1. Parse yaml_body to unstructured object
2. **ADD annotations** (lines ~87-91 in apply.go):
   ```go
   if obj.GetAnnotations() == nil {
       obj.SetAnnotations(make(map[string]string))
   }
   annotations := obj.GetAnnotations()
   annotations["k8sconnect.terraform.io/created-at"] = time.Now().Format(time.RFC3339)
   annotations["k8sconnect.terraform.io/terraform-id"] = data.Id.ValueString()
   ```
3. Apply with SSA force=true
4. Read back from Kubernetes
5. Extract field_ownership - **NOW INCLUDES ANNOTATION PATHS**
6. Return to Terraform

**Terraform's Consistency Check:**
- Compares planned field_ownership vs actual field_ownership
- Finds NEW elements that weren't in plan:
  - `metadata.annotations.k8sconnect.terraform.io/created-at`
  - `metadata.annotations.k8sconnect.terraform.io/terraform-id`
- **REJECTS as inconsistent** → Error

### Why This Bug Exists

The annotations are:
1. **Not in yaml_body** (user doesn't specify them)
2. **Added by our code** during apply
3. **Get field ownership** (because SSA tracks everything)
4. **Not predicted** in plan (dry-run only sees yaml_body)
5. **Appear unexpectedly** in post-apply state

This is a mismatch between what we predict (plan) and what we produce (apply).

---

## Current Code State

### Field Ownership Extraction

**Location:** `internal/k8sconnect/resource/object/field_ownership.go`

**Current Implementation:**
```go
// extractFieldOwnership parses managedFields and returns a map of field paths to managers
func extractFieldOwnership(obj *unstructured.Unstructured) map[string]string {
    result := make(map[string]string)

    managedFields := obj.GetManagedFields()
    for _, entry := range managedFields {
        manager := entry.Manager

        // Parse FieldsV1 (nested JSON structure like {"f:spec":{"f:replicas":{}}})
        if entry.FieldsV1 == nil {
            continue
        }

        fields := parseFieldsV1(entry.FieldsV1)
        for path := range fields {
            result[path] = manager
        }
    }

    return result
}
```

**Problem:** Extracts ALL field ownership, including our internal annotations.

### Where Annotations Are Added

**Location:** `internal/k8sconnect/resource/object/apply.go` (Update function, ~line 87)

```go
func (r *objectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
    // ... parse yaml_body to obj ...

    // Add our tracking annotations
    if obj.GetAnnotations() == nil {
        obj.SetAnnotations(make(map[string]string))
    }
    annotations := obj.GetAnnotations()
    annotations["k8sconnect.terraform.io/created-at"] = time.Now().Format(time.RFC3339)
    annotations["k8sconnect.terraform.io/terraform-id"] = data.Id.ValueString()

    // ... apply with SSA ...
}
```

Also in Create() function (~line 47).

---

## Solution: Filter Internal Annotations from field_ownership

### Why This Approach

**Terraform Convention:** Import should NEVER mutate cloud resources. Import is read-only in ALL mainstream providers (AWS, Azure, GCP, Kubernetes). Mutation happens during apply, not import.

**Key Insight:** Our `k8sconnect.terraform.io/*` annotations are **internal implementation details**, not user-managed fields. They're analogous to:
- AWS provider internal tags
- Azure provider metadata
- GCP provider labels added by Terraform

Tracking their field ownership adds no value and causes this bug. Users don't care who owns our internal bookkeeping annotations.

### The Fix

**File:** `internal/k8sconnect/resource/object/field_ownership.go`

**Function:** `extractFieldOwnership()`

**Change:** Filter out our internal annotation paths

**Implementation:**

```go
// extractFieldOwnership parses managedFields and returns a map of field paths to managers.
// It filters out k8sconnect's internal annotations, which are implementation details
// and should not be tracked as user-managed fields.
func extractFieldOwnership(obj *unstructured.Unstructured) map[string]string {
    result := make(map[string]string)

    managedFields := obj.GetManagedFields()
    for _, entry := range managedFields {
        manager := entry.Manager

        if entry.FieldsV1 == nil {
            continue
        }

        fields := parseFieldsV1(entry.FieldsV1)
        for path := range fields {
            // Skip our internal annotations - they're implementation details, not user-managed fields
            if strings.HasPrefix(path, "metadata.annotations.k8sconnect.terraform.io/") {
                continue
            }
            result[path] = manager
        }
    }

    return result
}
```

**Add import if not present:**
```go
import "strings"
```

### What This Fixes

1. **Import:** Still reads everything correctly, just doesn't track our annotations in field_ownership
2. **Plan:** Predicts ownership changes for user fields only (no annotation paths in field_ownership)
3. **Apply:** Adds annotations, but they don't appear in field_ownership
4. **Terraform Check:** Sees consistent field_ownership (no unexpected elements) ✅

### Side Effects

**Annotations still work:**
- They're still added to resources
- They're still in Kubernetes
- They're just not tracked in field_ownership

**What we lose:**
- Can't detect if another tool modifies our annotations
- Can't warn about annotation ownership conflicts

**Why that's okay:**
- No other tool should touch our internal annotations
- If they do, worst case: annotations get overwritten on next apply
- Not a user-facing issue

---

## Testing the Fix

### Manual Test (Exact Reproduction Steps Above)

After implementing the fix:

```bash
cd scenarios/kind-validation
./reset.sh
# ... create ConfigMap with kubectl (see reproduction steps) ...
# ... create import-test.tf (see reproduction steps) ...
terraform import 'k8sconnect_object.imported_configmap' 'kind-kind-validation:import-test:v1/ConfigMap:test-import'
terraform apply -target='k8sconnect_object.imported_configmap' -auto-approve
```

✅ **Expected after fix:** Apply succeeds (no inconsistent result error)

Then run:
```bash
terraform plan -target='k8sconnect_object.imported_configmap'
```

✅ **Expected:** "No changes" (no drift)

### Automated Test to Add

**File:** `internal/k8sconnect/resource/object/import_test.go`

**Add this test:**

```go
// TestAccObjectResource_ImportThenApplyNoDiff verifies the full import workflow:
// 1. Create resource with kubectl (external manager)
// 2. Import into Terraform
// 3. First apply after import - MUST succeed (was failing before fix)
// 4. Second apply - verify no drift
func TestAccObjectResource_ImportThenApplyNoDiff(t *testing.T) {
    t.Parallel()

    raw := os.Getenv("TF_ACC_KUBECONFIG")
    if raw == "" {
        t.Fatal("TF_ACC_KUBECONFIG must be set")
    }

    k8sClient := testhelpers.CreateK8sClient(t, raw)
    ns := fmt.Sprintf("import-apply-ns-%d", time.Now().UnixNano()%1000000)
    configMapName := fmt.Sprintf("kubectl-cm-%d", time.Now().UnixNano()%1000000)

    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
            "k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
        },
        Steps: []resource.TestStep{
            // Step 1: Create namespace with Terraform
            {
                Config: testAccObjectConfigNamespaceOnly(ns),
                ConfigVariables: config.Variables{
                    "raw":       config.StringVariable(raw),
                    "namespace": config.StringVariable(ns),
                },
                Check: resource.ComposeTestCheckFunc(
                    testhelpers.CheckNamespaceExists(k8sClient, ns),
                ),
            },
            // Step 2: Create ConfigMap with kubectl (external manager)
            {
                PreConfig: func() {
                    testhelpers.CreateConfigMapWithKubectl(t, ns, configMapName, map[string]string{
                        "created-by": "kubectl",
                        "test":       "import-then-apply",
                        "key1":       "value1",
                    })
                },
                Config: testAccObjectConfigNamespaceOnly(ns),
                ConfigVariables: config.Variables{
                    "raw":       config.StringVariable(raw),
                    "namespace": config.StringVariable(ns),
                },
                Check: resource.ComposeTestCheckFunc(
                    testhelpers.CheckConfigMapExists(k8sClient, ns, configMapName),
                    testhelpers.CheckFieldManager(k8sClient, ns, "ConfigMap", configMapName, "kubectl-client-side-apply"),
                ),
            },
            // Step 3: Import ConfigMap into Terraform
            {
                Config: testAccObjectConfigWithImportedConfigMap(ns, configMapName),
                ConfigVariables: config.Variables{
                    "raw":       config.StringVariable(raw),
                    "namespace": config.StringVariable(ns),
                    "name":      config.StringVariable(configMapName),
                },
                ResourceName:      "k8sconnect_object.imported_cm",
                ImportState:       true,
                ImportStateId:     fmt.Sprintf("k3d-k8sconnect-test:%s:v1/ConfigMap:%s", ns, configMapName),
                ImportStateVerify: true,
                ImportStateVerifyIgnore: []string{
                    "cluster_connection",
                    "yaml_body",
                    "managed_state_projection",
                    "delete_protection",
                    "force_conflicts",
                },
            },
            // Step 4: First apply after import - THIS IS THE CRITICAL TEST
            // Before fix: fails with "Provider produced inconsistent result"
            // After fix: succeeds
            {
                Config: testAccObjectConfigWithImportedConfigMap(ns, configMapName),
                ConfigVariables: config.Variables{
                    "raw":       config.StringVariable(raw),
                    "namespace": config.StringVariable(ns),
                    "name":      config.StringVariable(configMapName),
                },
                Check: resource.ComposeTestCheckFunc(
                    testhelpers.CheckConfigMapExists(k8sClient, ns, configMapName),
                    // Verify k8sconnect now owns the fields (took over from kubectl)
                    testhelpers.CheckFieldManager(k8sClient, ns, "ConfigMap", configMapName, "k8sconnect"),
                    // Verify our annotations were added
                    resource.TestCheckResourceAttrSet("k8sconnect_object.imported_cm", "id"),
                ),
            },
            // Step 5: Second apply - verify no drift
            {
                Config: testAccObjectConfigWithImportedConfigMap(ns, configMapName),
                ConfigVariables: config.Variables{
                    "raw":       config.StringVariable(raw),
                    "namespace": config.StringVariable(ns),
                    "name":      config.StringVariable(configMapName),
                },
                PlanOnly: true, // Just planning, should show no changes
            },
        },
        CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
    })
}

func testAccObjectConfigNamespaceOnly(namespace string) string {
    return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}
`, namespace)
}

func testAccObjectConfigWithImportedConfigMap(namespace, name string) string {
    return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "name" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "imported_cm" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
      labels:
        created-by: kubectl
        test: import-then-apply
    data:
      key1: value1
  YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}
`, namespace, name, namespace)
}
```

This test exercises the EXACT user workflow that's currently broken.

---

## Running All Tests

After implementing the fix:

```bash
# From repo root
cd /Users/jonathan/code/terraform-provider-k8sconnect

# Build and install provider
make install

# Run ALL tests to ensure nothing broke
make test        # Unit tests (fast, no cluster)
make testacc     # Acceptance tests (requires k3d cluster)
make test-examples  # Example tests

# Specifically run the new test
TEST=TestAccObjectResource_ImportThenApplyNoDiff make testacc
```

All tests should pass ✅

---

## Why Existing Tests Didn't Catch This

**Current import tests:**
- `TestAccObjectResource_Import`
- `TestAccObjectResource_ImportWithManagedFields`
- `TestAccObjectResource_ImportWithOwnershipConflict`

**All use `ImportStateVerifyIgnore`:**
```go
ImportStateVerifyIgnore: []string{
    "cluster_connection",
    "yaml_body",
    "managed_state_projection",
    "delete_protection",
    "force_conflicts",
}
```

Notice: **field_ownership is NOT in this list** (so it should be verified), BUT...

**The tests stop after import step** - they never do:
1. Import ✅ (tested)
2. Apply after import ❌ (NOT tested)
3. Second apply ❌ (NOT tested)

So the bug is invisible to our test suite. The new test fixes this gap.

---

## Alternative Approaches Considered (And Why They're Wrong)

### ❌ Option 1: Predict Annotations in Plan Modifier

```go
// Add predicted ownership for annotations we'll add
if isFirstApplyAfterImport(stateData) {
    plannedData.FieldOwnership["metadata.annotations.k8sconnect.terraform.io/created-at"] = "k8sconnect"
    plannedData.FieldOwnership["metadata.annotations.k8sconnect.terraform.io/terraform-id"] = "k8sconnect"
}
```

**Why it's wrong:**
- Hacky special-case logic
- Requires detecting "first apply after import" (fragile)
- Doesn't address root issue (annotations shouldn't be tracked)

### ❌ Option 2: Add Annotations During Import

```go
func (r *objectResource) ImportState(ctx context.Context, req ...) {
    // ... read resource ...

    // Add annotations immediately
    annotations := obj.GetAnnotations()
    annotations["k8sconnect.terraform.io/created-at"] = time.Now()
    annotations["k8sconnect.terraform.io/terraform-id"] = newId

    // Write back to Kubernetes
    client.ApplySSA(ctx, obj, force=true)
}
```

**Why it's wrong:**
- **Violates Terraform conventions** - import must be read-only
- No mainstream provider mutates resources during import (AWS, Azure, GCP, Kubernetes)
- Mutation should happen in apply, not import
- Would surprise users who expect import to just capture state

### ✅ Option 3: Filter Annotations from field_ownership (CORRECT)

This respects Terraform conventions and fixes the root cause.

---

## Commit Message Template

```
Fix: Filter internal annotations from field_ownership tracking

After importing a kubectl-created resource, the first terraform apply
would fail with "Provider produced inconsistent result" errors. The
plan modifier couldn't predict that we'd add k8sconnect.terraform.io
annotations during apply, causing unexpected field_ownership entries.

Solution: Filter our internal annotations from field_ownership tracking.
These are implementation details (like AWS provider internal tags), not
user-managed fields. Tracking their ownership added no value and caused
this inconsistency.

Changes:
- Modified extractFieldOwnership() to skip k8sconnect.terraform.io/* paths
- Added TestAccObjectResource_ImportThenApplyNoDiff to catch regressions

Fixes the import → plan → apply → verify workflow.
```

---

## Cleanup After Fix

Once the fix is verified working:

1. ✅ Verify manual test passes (scenarios/kind-validation)
2. ✅ Verify new automated test passes
3. ✅ Verify all existing tests still pass
4. ✅ Clean up scenarios/kind-validation/import-test.tf (temporary test file)
5. ✅ Archive or delete this document (IMPORT_OWNERSHIP_DEBUG.md)

---

## Quick Reference

**One-liner summary:** Filter `metadata.annotations.k8sconnect.terraform.io/*` from field_ownership extraction.

**File to edit:** `internal/k8sconnect/resource/object/field_ownership.go`

**Function to edit:** `extractFieldOwnership()`

**Line to add:**
```go
if strings.HasPrefix(path, "metadata.annotations.k8sconnect.terraform.io/") {
    continue
}
```

**Test to add:** `TestAccObjectResource_ImportThenApplyNoDiff` in `import_test.go`

**Expected result:** Import → Apply workflow succeeds without "inconsistent result" errors.
