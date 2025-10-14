# Dogfooding Results & Issues Found

This document tracks real-world findings from using k8sconnect in production-like scenarios.

## Test Environment
- **Date:** 2025-10-13
- **Cluster:** EKS test cluster via foobix provider
- **Module:** terraform-modules/foobix-cluster-aws converted from kubectl to k8sconnect
- **Resources:** ~74 k8sconnect_manifest resources (namespaces, PVs, Flux, reflector)

## Issues & Behaviors

### 1. Namespace Deletion Timeout - DEEP ANALYSIS COMPLETE ⚠️

**Status:** Root cause identified - multiple critical bugs found

**Observed Symptoms:**
- `k8sconnect_manifest.bootstrap_namespaces["oracle"]` hung during deletion
- Timed out after **exactly 10 minutes** (default timeout for Namespace resources)
- Error: "Deletion Stuck Without Finalizers"
- Manual inspection minutes later showed namespace was gone
- **Could not reproduce** on second clean destroy

**Root Causes Identified:**

**See DELETION_ANALYSIS.md for complete 500+ line deep dive**

**Critical Bug #1: Silent Error Handling (HIGH SEVERITY)**
- waitForDeletion() polls with Get() every 2 seconds for 10 minutes (300 requests)
- If Get() returns ANY error except NotFound, code silently continues waiting
- **ZERO logging** of non-NotFound errors - complete blind spot
- Could fail for 10 minutes straight with no indication

**Impact:** If API errors occur (network issues, throttling, auth problems):
- Never sees NotFound because getting errors instead
- Waits full 10 minutes silently
- Impossible to debug intermittent issues

**Critical Bug #2: No Progress Tracking (MEDIUM SEVERITY)**
- Only checks: "does resource exist?"
- Doesn't track: deletionTimestamp, finalizer count, cleanup progress
- Can't detect if deletion is stuck vs. progressing slowly
- No early failure on truly stuck deletions

**Why We Got "Deletion Stuck Without Finalizers":**
- Namespace was legitimately taking > 10 minutes to clean up 74+ resources
- Delete API succeeded (deletionTimestamp present)
- No finalizers blocking (checked by handleDeletionTimeout)
- Eventually finished after timeout (saw it gone minutes later)

**Why Couldn't Reproduce:**
- Second destroy: less cluster load, fewer resources, better network timing
- First destroy: interrupted state + more resources + network issues = slow cleanup

**Comprehensive Fix Designed:**

See DELETION_ANALYSIS.md for full implementation (200+ lines), includes:

1. **Error Logging & Recovery** ✅
   - Log every non-NotFound error with context
   - Track consecutive errors
   - Bail after 5 consecutive errors (assume deleted)
   - Include error details in timeout message

2. **Progress Tracking** ✅
   - Track deletionTimestamp appearance
   - Track finalizer count changes over time
   - Log progress updates
   - Detect stuck vs. slow deletions

3. **Adaptive Polling** ✅
   - Start at 1 second for fast deletes
   - Exponential backoff to 10 seconds when no progress
   - Reset to 1 second when progress detected
   - Reduces API load from 300 to ~100 requests

4. **Better Diagnostics** ✅
   - Duration tracking
   - Progress milestones
   - Detailed timeout messages with finalizer counts
   - Clear visibility into what's happening

**Recommendation:** Phased approach starting with minimal changes

**Phase 1 (Immediate):**
1. Increase namespace timeout: `case "Namespace": return 20 * time.Minute`
2. Add basic error logging to waitForDeletion() (~10 lines)

**Rationale:**
- **If Theory B** (slow cleanup): Fixed with timeout increase
- **If Theory A** (API errors): Next occurrence will have diagnostic logs
- Low risk, minimal code change
- Provides data to justify more complex fix if needed

**Phase 2 (After Validation):**
- If issue recurs with logs showing API errors → implement full enhanced version
- If no recurrence → declare victory with minimal change
- Don't over-engineer based on single unreproducible data point

**Full Enhanced Fix Available:**
See DELETION_ANALYSIS.md for complete 200+ line implementation with:
- Error logging & recovery (consecutive error bailout)
- Progress tracking (deletionTimestamp, finalizer counts)
- Adaptive polling (1s → 10s with backoff)
- Better diagnostics

**Confidence Levels:**
- Bug #1 exists (silent errors): 100%
- Bug #2 exists (no progress tracking): 100%
- Bug caused our specific timeout: 50% (two theories, zero evidence)
- Phase 1 prevents future issues: 50-75%
- Phase 1 enables diagnosis: 100%

### 2. cluster_lifecycle Pattern - Root Cause Identified ⚠️

**Status:** STILL NEEDED - Due to Foobix provider limitations, not kubectl/k8sconnect

**The Real Problem: YAML-Based API Design**

The foobix provider accepts cluster configuration as a single YAML string:
```hcl
resource "foobix_cluster" "this" {
  config = templatefile("${path.module}/templates/cluster.tpl.yaml", { ... })
}
```

**Why this is problematic:**
- Terraform cannot parse the YAML to understand which fields changed
- ANY change to the YAML (even non-destructive ones) marks downstream resources as tainted
- Creates cascade: `foobix_cluster` change → `wait_for_eks` → `cluster_connection` → all 74 k8sconnect resources

**The Dilemma:**
- **For destroy:** Need dependency chain `foobix_cluster.this.cluster_name` → k8s resources (ensures k8s destroys first)
- **For updates:** Need NO dependency (editing cluster YAML shouldn't cascade to k8s resources)
- Cannot have both with current architecture

**cluster_lifecycle modes solve this:**
- `"create"`: Initial bootstrap with all dependencies
- `"run"`: Break dependency chain (use `var.cluster_name` instead of `foobix_cluster.this.cluster_name`)
- `"destroy"`: Remove k8s resources from state before cluster destruction

**Test Results:**
- ✅ Successfully created cluster in "run" mode (dependency chain broken)
- ⚠️ Still need pattern to avoid cascading destroys on cluster YAML edits
- ❌ Cannot remove cluster_lifecycle pattern as initially hoped

**Impact on k8sconnect:**
This is NOT a k8sconnect limitation. It's a foobix provider design issue that affects any Terraform provider trying to manage k8s resources on foobix clusters.

## Positive Findings

### 1. Excellent Error Messages
- "Deletion Stuck Without Finalizers" error provided clear diagnostics
- Suggested kubectl commands to investigate
- Offered `force_destroy = true` solution
- Much better UX than kubectl provider's silent hanging

### 2. Clean Plan Output
- `cluster_connection` properly masked with `(sensitive value)`
- `host = (known after apply)` correctly shown for bootstrap scenario
- Read-only attributes (`field_ownership`, `managed_state_projection`, `status`) properly marked
- yaml_body displayed inline for verification

### 3. Bootstrap Flow Works
- All 74 resources planned successfully
- wait_for_eks → k8sconnect_manifest dependency chain correct
- Connection values flow properly through locals
- No errors during planning phase

### 4. Module Conversion Success
- Converted from kubectl provider with no issues
- Removed provider aliases cleanly
- Reflector submodule updated without problems
- No changes needed to templates/YAML content

## Gaps Identified

### 1. Data Source Limitations

**k8sconnect_manifest data source issues:**
- `object` attribute returns null (must use `jsondecode(manifest)`)
- No `wait_for` support (can't wait for LoadBalancer IP population)
- Unknown if handles "connection unknown during plan" gracefully
- Name inconsistent with resource (`k8sconnect_manifest` vs `k8sconnect_manifest`)

**Impact:** Cannot yet replace wait-for-lb module hack

**See DATASOURCE_DESIGN.md for complete design:**
- Rename to `k8sconnect_manifest` for consistency
- Add `existence_timeout` (wait for resource to appear)
- Add `wait_for` conditions (wait for fields to populate)
- Separate timeouts (5m existence, 10m per condition)
- Graceful "known after apply" during plan phase
- Bootstrap scenario support

**Needed implementations:**
1. Fix `object` attribute properly
2. Add `wait_for` support with timeout controls
3. Handle bootstrap scenario (resource doesn't exist yet)
4. Rename data source to `k8sconnect_manifest`

### 2. Flux Template Warnings

**Non-critical warning:** v1beta2 HelmRepository is deprecated, should upgrade to v1

**Location:** Flux GitOps templates in foobix-cluster-aws
**Impact:** Cosmetic warning only, doesn't affect functionality
**Fix:** Update Flux templates to use v1 API (separate from k8sconnect)

### 3. Warning Aggregation Loses Resource Context

**Issue:** Terraform aggregates warnings with identical summary text, resulting in messages like "(and 5 more similar warnings elsewhere)"

**Example output:**
```
│ Warning: Kubernetes API Warning
│
│   with module.k8sconnect_test.module.core_foobix_cluster.k8sconnect_manifest.this["root-kustomization"],
│   on .terraform/modules/k8sconnect_test/foobix-cluster-aws/main.tf line 211
│
│ The Kubernetes API server returned a warning:
│
│ v1beta2 HelmRepository is deprecated, upgrade to v1
│
│ (and 5 more similar warnings elsewhere)
```

**Problem:**
- Cannot see which other 5 resources triggered warnings
- Cannot tell if warnings are identical or slightly different
- Lose visibility into which resources need attention

**Current implementation:**
`internal/k8sconnect/resource/manifest/crud.go` - surfaceK8sWarnings() uses static summary:
```go
diagnostics.AddWarning(
    "Kubernetes API Warning",
    fmt.Sprintf("The Kubernetes API server returned a warning:\n\n%s", warning),
)
```

**Proposed fix:**
Include resource context in warning summary to prevent aggregation:
```go
diagnostics.AddWarning(
    fmt.Sprintf("Kubernetes API Warning (%s/%s)", obj.GetKind(), obj.GetName()),
    fmt.Sprintf("The Kubernetes API server returned a warning:\n\n%s", warning),
)
```

**Benefits:**
- Each resource's warning displayed separately
- Clear visibility into all affected resources
- Easier to track down and fix deprecation warnings

## Architecture Notes

### What k8sconnect Fixed
1. ✅ No provider-level config/aliases needed
2. ✅ Field ownership tracking available
3. ✅ SSA always-on
4. ✅ Better drift detection with managed_state_projection
5. ✅ Cleaner module interfaces

### What k8sconnect Cannot Fix
1. ❌ wait-for-eks hack (foobix provider limitation - doesn't expose cluster endpoint)
2. ❌ wait-for-lb hack (needs k8sconnect_manifest improvements first)

## Foobix Provider Improvements Needed

The YAML-based API design creates unnecessary dependency cascades. Potential solutions:

### Option 1: Expose Stable Output Attributes
Instead of only exposing the full YAML config, parse it and expose stable attributes:
```hcl
resource "foobix_cluster" "this" {
  config = "..." # YAML input

  # Computed stable outputs that don't change with every config update
  cluster_name   = "qa1"              # Parsed from YAML
  endpoint       = "https://..."      # Parsed from cluster status
  ca_certificate = "..."              # Parsed from cluster status
  stable_id      = "abc123"           # Unique ID that never changes
}
```

**Benefit:** Downstream resources depend on `stable_id` or `cluster_name`, not the entire config blob

### Option 2: Smart Change Detection
Provider could diff YAML changes and set `RequiresReplace = false` for non-destructive updates:
- Scaling nodes up/down: in-place update
- Adding tags: in-place update
- Changing cluster name: requires replace

**Benefit:** Only true destructive changes cascade downstream

### Option 3: Split Resource Types
Break monolithic resource into focused resources:
```hcl
resource "foobix_cluster" "this" {
  name   = var.cluster_name
  region = var.aws_region
}

resource "foobix_cluster_node_group" "workers" {
  cluster_id = foobix_cluster.this.id
  min        = 3
  max        = 5
}
```

**Benefit:** Changing node counts doesn't affect cluster resource

### Option 4: ignore_changes Lifecycle (Workaround)
```hcl
resource "foobix_cluster" "this" {
  config = templatefile(...)

  lifecycle {
    ignore_changes = [config]  # Prevents cascade but disables updates
  }
}
```

**Drawback:** Can't use Terraform to update cluster config at all

### Recommendation
**Option 1** is the most practical: expose `cluster_name` as a stable computed attribute. This single change would eliminate the need for cluster_lifecycle pattern entirely.

Contact Foobix team about implementing stable output attributes.

## Future Enhancements

### High Priority
1. Fix namespace deletion detection issue (if reproducible)
2. Implement k8sconnect_manifest.object attribute
3. Add wait_for support to k8sconnect_manifest data source

### Medium Priority
1. ✅ Include resource context in API warning summaries to prevent aggregation
2. ✅ Mark `cluster_connection.exec` block as sensitive in schema to reduce plan output noise
3. Better destroy-time error handling for "cluster doesn't exist" scenarios
4. Add more granular timeout controls

### Low Priority
1. Update Flux templates to v1 APIs
2. Document common conversion patterns from kubectl provider
