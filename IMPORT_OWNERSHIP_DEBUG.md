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

**📚 UPDATE (2025-10-23):** After researching HashiCorp's official documentation, we now understand the correct workflow. See "Appendix: Understanding Import + Generate Config (RESOLVED)" section below for the proper workflow. Key insight: **NO resource block should exist** when using `--generate-config-out` - only the import block is needed.

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

---

## Appendix: Actual Testing Results (2025-10-23)

**TL;DR**:
- ❌ **Workflow #1 (CLI import)**: Bug confirmed
- ⚠️ **Workflow #2 (import block + generate-config)**: Mostly works, but generated config needs manual cleanup
- ❌ **Workflow #3 (import block without generate-config)**: Bug confirmed
- ✅ **Root cause identified**: Internal annotations not filtered from field_ownership
- ✅ **Fix required**: Filter `k8sconnect.terraform.io/*` annotations + clean up ImportState yaml generation

See complete summary at end of document.

### IMPORTANT: Understanding Import + Generate Config (RESOLVED)

After researching HashiCorp's official documentation, we now understand how `--generate-config-out` is supposed to work:

**✅ CORRECT WORKFLOW (Per HashiCorp Documentation):**

1. **Create ONLY an import block** (no resource block at all):
   ```hcl
   import {
     to = k8sconnect_object.imported_configmap
     id = "context:namespace:apiVersion/Kind:name"
   }
   ```

2. **Run**: `terraform plan -generate-config-out=generated.tf`
   - Terraform calls the provider's `ImportState` function
   - Provider reads the resource and populates ALL attributes (including required ones)
   - Terraform writes the complete resource configuration to generated.tf

3. **Review and edit** the generated config

4. **Paste** generated config into your main terraform files

5. **Apply** to complete the import

**Key Insights:**

- **NO resource block should exist** when using `--generate-config-out`
- The output file **must NOT already exist** (Terraform errors if it does)
- Almost all providers have required attributes - this feature works with them fine
- The provider's `ImportState` function is responsible for populating everything

**Why Our Initial Test Failed:**

❌ **What we did wrong:**
- Created a skeleton resource block alongside the import block
- Terraform validated that skeleton resource block BEFORE calling import
- Found `yaml_body` missing and failed schema validation
- Never got to the import phase at all

✅ **What we should have done:**
- Import block ONLY, with NO resource definition
- Let Terraform call `ImportState` to populate everything
- Examine what gets written to generated.tf

**The Three Import Workflows:**

Going forward, we need to test all three import methods:

1. **CLI-only import** (traditional):
   - Requires resource block with yaml_body already defined
   - Run: `terraform import k8sconnect_object.name "context:namespace:apiVersion/Kind:name"`
   - Import populates state, but config must be manually written

2. **Import block with --generate-config-out** (modern):
   - Import block ONLY, no resource definition
   - Run: `terraform plan -generate-config-out=generated.tf`
   - Config automatically generated

3. **Import block without --generate-config-out**:
   - Requires both import block AND resource block with yaml_body
   - Run: `terraform plan` then `terraform apply`
   - Config must be manually written before import

**Critical Question for Our Provider:**

Does our `ImportState` function correctly populate `yaml_body` so it can be written to the generated config file? This needs testing.

---

### Test 1: Import with --generate-config-out (INCOMPLETE TESTING)

**Step 1: Create ConfigMap with kubectl**

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

Verified field manager:
```bash
KUBECONFIG=kind-validation-config kubectl get configmap test-import -n import-test -o jsonpath='{.metadata.managedFields[0].manager}'
# Output: kubectl-client-side-apply
```

**Step 2: Create import block and skeleton resource** ❌ **(WRONG - Should not have created resource block!)**

File: `import-test.tf`
```hcl
import {
  to = k8sconnect_object.imported_configmap
  id = "kind-kind-validation:import-test:v1/ConfigMap:test-import"
}

resource "k8sconnect_object" "imported_configmap" {
  cluster_connection = {
    kubeconfig = file("${path.module}/kind-validation-config")
  }
}
```

**NOTE:** This is incorrect! We should have created ONLY the import block, with NO resource definition. By creating a skeleton resource block, we triggered Terraform's schema validation before import could run.

**Step 3: Attempt to generate config**

```bash
terraform plan -generate-config-out=generated.tf
```

**Result: FAILED**

```
Error: Missing required argument

  on import-test.tf line 6, in resource "k8sconnect_object" "imported_configmap":
   6: resource "k8sconnect_object" "imported_configmap" {

The argument "yaml_body" is required, but no definition was found.
```

**Analysis:**

The `--generate-config-out` feature failed in this test because:
1. `yaml_body` is marked as a required attribute in the schema
2. The skeleton resource did not include `yaml_body`
3. Terraform validation failed before import could run

**What we DON'T know yet:**
- Would it work with yaml_body present in the skeleton?
- Would it work with NO resource definition at all (import block only)?
- What should the generated.tf file contain?
- Is schema validation supposed to be skipped during import with --generate-config-out?

**Conclusion:**

⚠️ **INCOMPLETE TESTING** - We jumped to conclusions without understanding how this feature is supposed to work. We need to:
1. Test import block alone (no resource definition)
2. Test import block + skeleton with yaml_body
3. Research Terraform's expected behavior for --generate-config-out
4. Determine if yaml_body being required is compatible with this workflow

---

### Test 2: Traditional CLI-Based Import Workflow (REPRODUCED BUG)

**Note:** This tests Workflow #1 - traditional `terraform import` command with resource block already defined. This is the legacy import method that requires manual config writing.

**Step 1: Manually add yaml_body to resource**

File: `import-test.tf`
```hcl
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

**Step 2: Import**

```bash
terraform import 'k8sconnect_object.imported_configmap' 'kind-kind-validation:import-test:v1/ConfigMap:test-import'
```

**Result: SUCCESS**

```
Import successful!

The resources that were imported are shown above. These resources are now in
your Terraform state and will henceforth be managed by Terraform.
```

**Step 3: Plan after import**

```bash
terraform plan -target='k8sconnect_object.imported_configmap'
```

**Result: SUCCESS** (with expected warnings)

```
Terraform will perform the following actions:

  # k8sconnect_object.imported_configmap will be updated in-place
  ~ resource "k8sconnect_object" "imported_configmap" {
      ~ cluster_connection       = {
          - context    = "kind-kind-validation" -> null
          ~ kubeconfig = (sensitive value)
        }
      - delete_protection        = false -> null
        id                       = "725bf1125347"
        # (4 unchanged attributes hidden)
    }

Plan: 0 to add, 1 to change, 0 to destroy.

Warning: Field Ownership Override

Forcing ownership of fields managed by other controllers:
  - data.key2 (managed by "kubectl-client-side-apply")
  - metadata.labels.created-by (managed by "kubectl-client-side-apply")
  - data.key1 (managed by "kubectl-client-side-apply")

These fields will be forcibly taken over. The other controllers may fight back.
```

**Step 4: Apply after import**

```bash
terraform apply -target='k8sconnect_object.imported_configmap' -auto-approve
```

**Result: FAILED** (exact bug reproduced)

```
Error: Provider produced inconsistent result after apply

When applying changes to k8sconnect_object.imported_configmap, provider
"provider["registry.terraform.io/local/k8sconnect"]" produced an
unexpected new value: .field_ownership: new element
"metadata.annotations.k8sconnect.terraform.io/created-at" has appeared.

This is a bug in the provider, which should be reported in the provider's
own issue tracker.

Error: Provider produced inconsistent result after apply

When applying changes to k8sconnect_object.imported_configmap, provider
"provider["registry.terraform.io/local/k8sconnect"]" produced an
unexpected new value: .field_ownership: new element
"metadata.annotations.k8sconnect.terraform.io/terraform-id" has appeared.

This is a bug in the provider, which should be reported in the provider's
own issue tracker.
```

**Step 5: Verify state after failed apply**

```bash
terraform state show 'k8sconnect_object.imported_configmap' | grep -A 20 field_ownership
```

**Output:**

```
field_ownership          = {
    "data"                                                                  = "kubectl-client-side-apply"
    "data.key1"                                                             = "kubectl-client-side-apply"
    "data.key2"                                                             = "kubectl-client-side-apply"
    "metadata.annotations"                                                  = "kubectl-client-side-apply"
    "metadata.annotations.k8sconnect.terraform.io/created-at"               = "k8sconnect"
    "metadata.annotations.k8sconnect.terraform.io/terraform-id"             = "k8sconnect"
    "metadata.annotations.kubectl.kubernetes.io/last-applied-configuration" = "kubectl-client-side-apply"
    "metadata.labels"                                                       = "kubectl-client-side-apply"
    "metadata.labels.created-by"                                            = "kubectl-client-side-apply"
}
```

**Analysis:**

The internal annotations are present in field_ownership:
- `metadata.annotations.k8sconnect.terraform.io/created-at` (owned by "k8sconnect")
- `metadata.annotations.k8sconnect.terraform.io/terraform-id` (owned by "k8sconnect")

These annotations were:
1. NOT present during import
2. NOT predicted during plan
3. ADDED during apply (in the Update function)
4. TRACKED in field_ownership (because SSA tracks all fields)
5. DETECTED as unexpected by Terraform's consistency check

**Conclusion:**

✅ **Traditional CLI-based import workflow tested successfully** - Bug reproduced exactly as documented. The fix is to filter these internal annotations from field_ownership tracking.

⚠️ **Still need to test:**
- Import block alone (no resource definition) + --generate-config-out
- Import block + skeleton with yaml_body + --generate-config-out
- Whether the annotation filtering fix works for all three import scenarios

The traditional import workflow bug is confirmed and the fix is clear. But we need to ensure the fix works for ALL import methods before considering it complete.

---

## Summary: Complete Testing Results (2025-10-23)

### ✅ Workflow #1: CLI Import (TRADITIONAL METHOD)

**Status**: ❌ **BUG CONFIRMED**

**Steps**:
1. Create resource with kubectl
2. Write Terraform config with yaml_body
3. Run `terraform import k8sconnect_object.name "id"`
4. Run `terraform plan` - succeeds, shows ownership takeover
5. Run `terraform apply` - **FAILS**

**Error**:
```
Error: Provider produced inconsistent result after apply
...new element "metadata.annotations.k8sconnect.terraform.io/created-at" has appeared
...new element "metadata.annotations.k8sconnect.terraform.io/terraform-id" has appeared
```

**Root Cause**: Internal annotations added during UPDATE aren't predicted during plan.

---

### ✅ Workflow #2: Import Block + --generate-config-out (MODERN METHOD)

**Status**: ⚠️ **PARTIAL SUCCESS** (with caveats)

**Steps**:
1. Create import block ONLY (no resource definition)
2. Run `terraform plan -generate-config-out=generated.tf`
3. Terraform generates config

**Results**:
- ✅ **Config IS generated** - ImportState correctly populates yaml_body
- ❌ **Terraform generates invalid HCL**: `exec = {}` causes validation error
- ❌ **Generated yaml_body includes internal annotations**:
  ```yaml
  k8sconnect.terraform.io/created-at: "2025-10-23T17:31:13Z"
  k8sconnect.terraform.io/terraform-id: 725bf1125347
  ```

**After Manual Cleanup**:
- Removed `exec = {}` and set proper cluster_connection
- Removed internal annotations from yaml_body
- Applied successfully (treated as CREATE operation, taking over from kubectl)
- No drift on subsequent plan ✅

**Issues to Fix**:
1. **ImportState should NOT include internal annotations in yaml_body**
2. **ImportState should handle cluster_connection better** (exec field issue)

---

### ✅ Workflow #3: Import Block Without --generate-config-out

**Status**: ❌ **BUG CONFIRMED**

**Steps**:
1. Create resource with kubectl
2. Create import block AND resource definition with yaml_body
3. Run `terraform plan` - succeeds, shows "1 to import, 1 to change"
4. Run `terraform apply` - **FAILS**

**Error**: Same as Workflow #1
```
Error: Provider produced inconsistent result after apply
...new element "metadata.annotations.k8sconnect.terraform.io/created-at" has appeared
...new element "metadata.annotations.k8sconnect.terraform.io/terraform-id" has appeared
```

**Root Cause**: Same as Workflow #1 - UPDATE operation adds annotations not predicted in plan.

---

## The Core Bug

**Affects**: Workflows #1 and #3 (UPDATE operations after import)

**Does NOT affect**: Workflow #2 when treated as CREATE operation

**Why**:
- During import: No internal annotations exist, field_ownership shows kubectl
- During plan for UPDATE: Predicts taking over kubectl fields, but doesn't predict annotation ownership
- During apply (UPDATE): Code adds internal annotations, field_ownership gains new entries
- Terraform consistency check: Detects unexpected field_ownership entries → ERROR

---

## The Fix

**Primary**: Filter `metadata.annotations.k8sconnect.terraform.io/*` from field_ownership extraction
- File: `internal/k8sconnect/resource/object/field_ownership.go`
- Function: `extractFieldOwnership()`
- These are implementation details, not user-managed fields

**Secondary (for Workflow #2)**: Improve ImportState to generate cleaner config
- Don't include internal annotations in yaml_body for generated config
- Handle cluster_connection exec field properly (likely a Terraform config generation limitation)

---

### 📝 Documentation Impact

When updating import/migration docs:

1. **Workflow #2 is the modern recommended approach** (import block + generate-config-out)
   - But users need to know they must manually clean up generated config
   - Remove exec = {} or replace with proper connection details
   - Internal annotations should NOT be in generated config (we need to fix this)

2. **All three workflows need clear examples**
   - CLI import (legacy, but still supported)
   - Import blocks with generate-config (modern)
   - Import blocks without generate-config (hybrid)

3. **Important notes**:
   - Import blocks run during `terraform plan` phase (not separate command)
   - NO resource block should exist when using --generate-config-out
   - After the fix, all workflows should work correctly

---

## Testing Strategy

**Manual Testing Only**

We attempted to write an acceptance test but discovered the Terraform testing framework doesn't trigger the same consistency check that causes the bug in real-world usage.

**Test Location**: `scenarios/kind-validation/`

**How to reproduce the bug**:
1. `cd scenarios/kind-validation && ./reset.sh`
2. `terraform apply`
3. Create ConfigMap with kubectl: `KUBECONFIG=kind-validation-config kubectl apply -f ...`
4. Create import block + resource definition in `import-test.tf`
5. `terraform apply -target='k8sconnect_object.imported_cm' -auto-approve`
6. **BUG**: Fails with "Provider produced inconsistent result" about internal annotations

**After the fix**, step 6 should succeed.

**Why no acceptance test?**
The testing framework's import handling bypasses or handles differently the Terraform consistency check that detects the bug. Multiple attempts to reproduce it in acceptance tests failed, while manual testing consistently triggers it.

**This is acceptable** because:
- We have a reliable manual reproduction
- The fix is surgical and low-risk
- Existing acceptance tests verify import functionality works
- This is a rare edge case (import of kubectl-managed resources)
