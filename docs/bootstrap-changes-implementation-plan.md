# Bootstrap Changes Implementation Plan

## Overview

This morning we attempted to implement all 6 UX improvement changes at once. Tests exploded, debugging became impossible, and we reverted everything. This document provides a safe, incremental approach to implementing the same changes.

**Current Status:**
- ✅ Change #2 (remove force_conflicts) - COMPLETED today
- ⏳ Changes #1, #3, #4, #5, #6 - TO BE IMPLEMENTED

## The Core Goal

**Make `managed_state_projection` the single source of truth for reviewing changes.**

Currently users see TWO diffs:
1. `yaml_body` - what you wrote in HCL
2. `managed_state_projection` - what will actually happen in cluster

**We want ONE diff:**
- Hide `yaml_body` (sensitive)
- Show accurate `managed_state_projection` in ALL scenarios

**The Problem:** Currently `managed_state_projection` shows "(known after apply)" for CREATE operations, even when the cluster exists and we COULD show an accurate diff via dry-run.

## The 6 Changes Recap

| # | Change | Goal | Lines Changed | Risk |
|---|--------|------|---------------|------|
| #1 | yaml_body sensitive | Hide yaml_body, show only projection | 1 line | Low (cosmetic) |
| #2 | Remove force_conflicts | Auto-force with warnings | ~50 lines | ✅ DONE |
| #3 | Smart projection for CREATE | Use dry-run when cluster exists, yaml when it doesn't | ~40 lines | Medium |
| #4 | Enhanced connection check | Detect if cluster exists (enables #3) | ~10 lines | Low |
| #5 | Preserve projection during CREATE | Avoid recalculating during apply (consistency) | ~15 lines | Medium |
| #6 | Field ownership update after CREATE | Fix null bug | 1 line | Low |

## The Three Scenarios

### Current Behavior (BROKEN)

| Scenario | Current Behavior | What We Want |
|----------|------------------|--------------|
| **CREATE + cluster doesn't exist** | projection = unknown ✅ | projection = yaml fallback ✅ |
| **CREATE + cluster exists** | projection = unknown ❌ | projection = dry-run (accurate!) ✅ |
| **UPDATE** | projection = dry-run ✅ | projection = dry-run ✅ |

**The bug:** Line 248-252 in `plan_modifier.go`:
```go
if isCreate {
    plannedData.ManagedStateProjection = types.StringUnknown()  // Always unknown!
    return true  // Never tries dry-run
}
```

This means even when cluster exists and we COULD show accurate diff, we show "(known after apply)" instead.

### Why This Matters

**Example:** User adds new deployment to existing cluster:

**Current (bad):**
```hcl
+ managed_state_projection = (known after apply)  # Unhelpful!
+ yaml_body = <<-EOT
    apiVersion: apps/v1
    kind: Deployment
    ...
  EOT
```
User sees yaml but not actual diff (K8s defaults, etc.)

**After fix (good):**
```hcl
+ managed_state_projection = {
    apiVersion: apps/v1
    kind: Deployment
    spec:
      replicas: 2
      template:
        spec:
          containers:
          - name: nginx
            resources:
              requests:
                cpu: 50m      # ← K8s added default
              limits:
                cpu: 100m     # ← User specified
    ...
  }
```
User sees EXACTLY what will be created, including K8s defaults.

## What Went Wrong This Morning

### The Stashed Code Analysis

**Files changed simultaneously:**
```
manifest.go
  ├─ yaml_body sensitive (#1)
  └─ force_conflicts default (#2)

plan_modifier.go
  ├─ Rewrote connection validation (#4 integration)
  ├─ Early return for CREATE (#3)
  └─ Yaml fallback projection (#3)

crud.go
  ├─ Preserve projection logic (#5)
  └─ Field ownership update (#6)

connection.go
  └─ Enhanced connection check (#4)
```

### The Fatal Interaction: #3 + #5

**The Problem:**

Change #3 (plan_modifier.go) modified CREATE to return YAML-based projection:
```go
// CREATE operations now return YAML-based projection (not unknown)
plannedData.ManagedStateProjection = types.StringValue(projectionJSON)
```

Change #5 (crud.go) added logic to preserve plan projection:
```go
// If projection is set, preserve it (don't recalculate)
if !data.ManagedStateProjection.IsNull() && !data.ManagedStateProjection.IsUnknown() {
    // Keep it!
}
```

**When a test failed:**
- Is #3 setting projection incorrectly?
- Is #5 preserving it incorrectly?
- Are they interacting in an unexpected way?
- **Which one do I debug first?**

**With both changes at once: IMPOSSIBLE TO DEBUG**

### Test Explosion Cascade

Example: `TestAccManifestResource_Basic` (CREATE operation)

**Before changes:**
```
Plan: projection = (known after apply)
Apply: projection calculated from cluster
State: projection = {...actual fields...}
```

**After all 6 changes:**
```
Plan: projection = {...yaml fields...}  ← #3 changed this
Apply: projection preserved from plan   ← #5 changed this
State: projection = {...yaml fields...}
```

**Test failures across 3 categories:**
1. ❌ Expected: Projection is unknown during plan
   - Got: Projection has actual YAML content (#3)

2. ❌ Expected: yaml_body shows in output
   - Got: yaml_body is (sensitive value) (#1)

3. ❌ Expected: force_conflicts is null
   - Got: force_conflicts = true (#2)

**Multiple failure categories made debugging impossible.**

## Why Tests Don't Catch This Bug

**Current tests only check state AFTER apply, not PLAN output.**

Tests use:
- `resource.TestCheckResourceAttrSet()` - checks state has a value
- `plancheck.ExpectEmptyPlan()` - checks if plan has changes

**But NO tests check:**
- Whether attributes show "(known after apply)" vs actual values during plan
- What `managed_state_projection` contains in the plan phase

**Example test that WOULD catch this:**

Update `TestAccManifestResource_Basic` in `basic_test.go` to add a step that:
1. Creates a namespace first (cluster exists)
2. Then adds a ConfigMap (CREATE with existing cluster)
3. Uses `ConfigPlanChecks` with custom plan check
4. Verifies `managed_state_projection` is populated (not unknown) in plan

**Why this catches the bug:**
- Current code sets projection to unknown for ALL CREATEs
- Test would fail because plan shows "(known after apply)"
- After fix, test passes because plan shows actual JSON projection

**Recommendation:** Add this validation to an existing test (like `basic_test.go`) rather than creating a new test file. The test framework makes custom plan checks somewhat complex, so may want to tackle this after the main fixes are working.

## The Safe Implementation Sequence

### Dependency Chain

```
#4 (connection check) ← Foundation
  ↓
#6 (field ownership fix) ← Independent bug fix
  ↓
#3 (skip dry-run) ← Uses #4, enables #5
  ↓
#5 (preserve projection) ← Optimizes #3
  ↓
#1 (yaml_body sensitive) ← Cosmetic polish
```

---

## Day 1: #4 Enhanced Connection Check

**What:** Detect if cluster actually exists (host is known vs "known after apply")

**Files:** `internal/k8sconnect/resource/manifest/connection.go`

**Changes:**
```go
// Enhanced isConnectionReady() to check if host is known
func (r *manifestResource) isConnectionReady(obj types.Object) bool {
    if obj.IsNull() || obj.IsUnknown() {
        return false
    }

    // Convert to connection model to check if host is known
    conn, err := auth.ObjectToConnectionModel(context.Background(), obj)
    if err != nil {
        return false
    }

    // Check if host is known (required for connection)
    // Host is the primary field needed to connect - if it's unknown, we can't connect
    return !conn.Host.IsNull() && !conn.Host.IsUnknown()
}
```

**Implementation Steps:**
```bash
# Extract just connection.go from stash
git show stash@{0}:internal/k8sconnect/resource/manifest/connection.go > /tmp/connection_new.go

# Review the diff
diff internal/k8sconnect/resource/manifest/connection.go /tmp/connection_new.go

# Apply it
cp /tmp/connection_new.go internal/k8sconnect/resource/manifest/connection.go

# Test
make test

# Commit
git add internal/k8sconnect/resource/manifest/connection.go
git commit -m "feat: Enhanced connection check for bootstrap detection (#4)

Adds check for host field to isConnectionReady() to detect when cluster
doesn't exist yet (host is 'known after apply'). This enables smart
decisions about whether to attempt dry-run or use yaml fallback.

Part of bootstrap UX improvement series."
```

**Risk:** Very Low (isolated change, no behavioral impact yet)

**Tests Affected:** Minimal (connection validation only)

**Success Criteria:** All tests pass, no behavioral changes

---

## Day 2: #6 Field Ownership Fix

**What:** Fix null field_ownership bug by calling updateFieldOwnershipData after CREATE

**Files:** `internal/k8sconnect/resource/manifest/crud.go`

**Changes:**
```go
// After successful creation, before state save
// Line ~72 (after projection update, before state save)

// 8a. Update field ownership from created resource
r.updateFieldOwnershipData(ctx, &data, rc.Object)
```

**Implementation Steps:**
```bash
# Edit crud.go manually - find the section after projection update
# Add the single line:
# r.updateFieldOwnershipData(ctx, &data, rc.Object)

# Test
make test

# Commit
git add internal/k8sconnect/resource/manifest/crud.go
git commit -m "fix: Update field_ownership after CREATE (#6)

CREATE operations were not calling updateFieldOwnershipData, resulting
in field_ownership being null after resource creation. This adds the
missing call to populate field ownership from the created resource.

Fixes: field_ownership null after CREATE"
```

**Risk:** Low (bug fix, makes tests better)

**Tests Affected:** Tests that check field_ownership after CREATE (should now pass)

**Success Criteria:** All tests pass, field_ownership populated after CREATE

---

## Day 3: #3 Smart Projection for CREATE

**What:** CREATE operations do dry-run when ALL values known, yaml fallback when ANY unknown

**Files:** `internal/k8sconnect/resource/manifest/plan_modifier.go`

**The Logic:**

For CREATE operations, we need ALL of these to be true to do dry-run:
1. ✅ `yaml_body` is not unknown (attribute exists)
2. ✅ `yaml_body` does NOT contain `${` interpolations
3. ✅ `yaml_body` is not empty
4. ✅ `cluster_connection` is ready (host + auth known)
5. ✅ `ignore_fields` is not unknown (null is OK)

If ALL true → dry-run for accurate projection (shows K8s defaults!)
If ANY false → fallback (unknown if can't parse, yaml JSON if parseable)

**Changes (3 sections):**

**Section 1: Smart CREATE detection (lines ~41-75)**
```go
// Parse the desired YAML first
yamlStr := plannedData.YAMLBody.ValueString()

// Check if YAML is ready (no interpolations)
if plannedData.YAMLBody.IsUnknown() {
    // Entire attribute unknown
    plannedData.ManagedStateProjection = types.StringUnknown()
    diags := resp.Plan.Set(ctx, &plannedData)
    resp.Diagnostics.Append(diags...)
    return
}

if strings.Contains(yamlStr, "${") {
    // Contains unresolved interpolations - can't parse or dry-run
    tflog.Debug(ctx, "YAML contains interpolations, setting projection to unknown")
    plannedData.ManagedStateProjection = types.StringUnknown()
    diags := resp.Plan.Set(ctx, &plannedData)
    resp.Diagnostics.Append(diags...)
    return
}

if yamlStr == "" {
    // Empty YAML (shouldn't happen but be safe)
    plannedData.ManagedStateProjection = types.StringUnknown()
    diags := resp.Plan.Set(ctx, &plannedData)
    resp.Diagnostics.Append(diags...)
    return
}

// For CREATE: Check if we can do dry-run (all values known)
if isCreateOperation(req) {
    connectionReady := r.isConnectionReady(plannedData.ClusterConnection)
    ignoreFieldsReady := !plannedData.IgnoreFields.IsUnknown()

    canDryRun := connectionReady && ignoreFieldsReady

    if !canDryRun {
        // Some values unknown - use yaml fallback
        tflog.Debug(ctx, "CREATE with unknown values - using yaml fallback for projection")
        // Will use yaml fallback in calculateProjection
    } else {
        tflog.Debug(ctx, "CREATE with all values known - will attempt dry-run")
        // Will do dry-run in executeDryRunAndProjection
    }
}

// For UPDATE: Connection must be ready
if !isCreateOperation(req) && !r.isConnectionReady(plannedData.ClusterConnection) {
    tflog.Debug(ctx, "UPDATE with unknown connection - setting projection to unknown")
    plannedData.ManagedStateProjection = types.StringUnknown()
    diags := resp.Plan.Set(ctx, &plannedData)
    resp.Diagnostics.Append(diags...)
    return
}
```

**Section 2: Smart dry-run decision in executeDryRunAndProjection**
```go
func (r *manifestResource) executeDryRunAndProjection(...) bool {
    isCreate := isCreateOperation(req)

    // For CREATE: Decide if we can do dry-run
    if isCreate {
        connectionReady := r.isConnectionReady(plannedData.ClusterConnection)
        ignoreFieldsReady := !plannedData.IgnoreFields.IsUnknown()
        canDryRun := connectionReady && ignoreFieldsReady

        if !canDryRun {
            // Unknown values - use yaml fallback
            tflog.Debug(ctx, "CREATE with unknown values - using yaml fallback")
            return r.calculateProjection(ctx, req, plannedData, desiredObj, nil, nil, resp)
        }

        // All known - continue to dry-run below!
        tflog.Debug(ctx, "CREATE with known values - doing dry-run")
    }

    // Do dry-run (for UPDATE always, for CREATE when canDryRun)
    client, err := r.setupDryRunClient(ctx, plannedData, resp)
    if err != nil {
        return false
    }

    // Execute dry-run...
}
```

**Section 3: Smart projection calculation (lines ~267-320)**
```go
func (r *manifestResource) calculateProjection(...) bool {
    isCreate := isCreateOperation(req)

    // If no dry-run result (yaml fallback path), use parsed YAML
    if dryRunResult == nil {
        tflog.Debug(ctx, "Using yaml fallback for projection (no dry-run)")

        // Apply ignore_fields filtering
        objToProject := desiredObj.DeepCopy()
        if ignoreFields := getIgnoreFields(ctx, plannedData); ignoreFields != nil {
            objToProject = removeFieldsFromObject(objToProject, ignoreFields)
            tflog.Debug(ctx, "Applied ignore_fields to yaml projection", map[string]interface{}{
                "ignored_count": len(ignoreFields),
            })
        }

        // Convert parsed yaml to JSON for projection
        projectionJSON, err := toJSON(objToProject.Object)
        if err != nil {
            resp.Diagnostics.AddError("JSON Conversion Failed", fmt.Sprintf("Failed to convert yaml to projection: %s", err))
            return false
        }

        plannedData.ManagedStateProjection = types.StringValue(projectionJSON)
        tflog.Debug(ctx, "Populated projection from parsed yaml", map[string]interface{}{
            "projection_size": len(projectionJSON),
        })
        return true
    }

    // We have dry-run result - use field ownership
    tflog.Debug(ctx, "Using dry-run result for projection")

    // Extract ownership from dry-run result
    paths := extractOwnedPaths(ctx, dryRunResult.GetManagedFields(), desiredObj.Object)

    // Apply ignore_fields filtering
    if ignoreFields := getIgnoreFields(ctx, plannedData); ignoreFields != nil {
        paths = filterIgnoredPaths(paths, ignoreFields)
    }

    // Project the dry-run result
    projection, err := projectFields(dryRunResult.Object, paths)
    if err != nil {
        resp.Diagnostics.AddError("Projection Failed", err.Error())
        return false
    }

    // Convert to JSON
    projectionJSON, err := toJSON(projection)
    if err != nil {
        resp.Diagnostics.AddError("JSON Conversion Failed", err.Error())
        return false
    }

    plannedData.ManagedStateProjection = types.StringValue(projectionJSON)
    return true
}
```

**Implementation Steps:**
```bash
# Extract plan_modifier.go from stash and apply changes manually
# OR extract specific sections

# Test (EXPECT FAILURES)
make test

# Fix test expectations:
# - Change projection checks from "(known after apply)" to actual content
# - Update assertions to expect yaml-based projection during CREATE

# Example test fixes:
# BEFORE:
# resource.TestCheckResourceAttr(..., "managed_state_projection", "(known after apply)")
#
# AFTER:
# resource.TestCheckResourceAttrSet(..., "managed_state_projection")
# // Or verify it contains expected fields

# Commit
git add internal/k8sconnect/resource/manifest/plan_modifier.go
git commit -m "feat: Smart projection for CREATE operations (#3)

CREATE operations now intelligently choose between dry-run and yaml fallback:
- When ALL values known (yaml, connection, ignore_fields): Do dry-run for accurate projection
- When ANY values unknown: Use yaml fallback

This provides accurate diffs when possible (cluster exists, no interpolations)
while gracefully handling bootstrap scenarios (cluster being created).

UPDATE operations always use dry-run (connection must be ready).

Part of UX improvement series."
```

**Risk:** Medium (behavioral change, test assertions need updates)

**Tests Affected:** CREATE operations - projection may now show actual content instead of unknown

**Success Criteria:**
- CREATE with existing cluster and known values → shows dry-run projection (accurate!)
- CREATE with unknown values (bootstrap, interpolations) → shows yaml fallback or unknown
- UPDATE still requires connection and uses dry-run
- No "inconsistent plan" errors

**IMPORTANT:** Do NOT apply #5 (preserve projection) yet!

---

## Day 4: #5 Preserve Projection During CREATE

**What:** Avoid recalculating projection during apply when plan already has one

**Files:** `internal/k8sconnect/resource/manifest/crud.go`

**Changes:**
```go
// Around line 56, replace the projection update section:

// 8. Update projection BEFORE state save
// If plan already has a projection (from yaml fallback during bootstrap), preserve it
// to avoid inconsistent plan errors. It will be updated on next refresh with accurate dry-run.
if !data.ManagedStateProjection.IsNull() && !data.ManagedStateProjection.IsUnknown() {
    tflog.Debug(ctx, "Preserving projection from plan to avoid inconsistent plan error", map[string]interface{}{
        "projection_size": len(data.ManagedStateProjection.ValueString()),
    })
    // Keep the plan's projection - don't recalculate
    // Note: rc.Data points to data, so the projection is already set
} else {
    // No projection in plan (shouldn't happen with our changes, but handle it)
    if err := r.updateProjection(rc); err != nil {
        // Projection failed - save state with recovery flag (ADR-006)
        handleProjectionFailure(ctx, rc, resp.Private, &resp.State, &resp.Diagnostics, "created", err)
        return
    }
}
```

**Implementation Steps:**
```bash
# Edit crud.go manually - replace projection update section

# Test
make test

# Should mostly work since #3 already adjusted test expectations

# Commit
git add internal/k8sconnect/resource/manifest/crud.go
git commit -m "feat: Preserve projection during CREATE (#5)

Avoid recalculating projection during apply when plan already has a
yaml-based fallback projection. This prevents inconsistent plan errors
and improves performance for CREATE operations.

Projection will be updated on next refresh with accurate dry-run results.

Part of bootstrap UX improvement series."
```

**Risk:** Medium (but #3 already working reduces it)

**Tests Affected:** Should be minimal (tests already fixed for #3)

**Success Criteria:**
- CREATE preserves plan projection
- No inconsistent plan errors
- Performance improvement (skip projection recalculation)

---

## Day 5: #1 yaml_body Sensitive

**What:** Hide yaml_body from plan output (users review managed_state_projection)

**Files:** `internal/k8sconnect/resource/manifest/manifest.go`

**Changes:**
```go
"yaml_body": schema.StringAttribute{
    Required:    true,
    Sensitive:   true,  // ← ADD THIS
    Description: "UTF-8 encoded, single-document Kubernetes YAML. Multi-doc files will fail validation. Hidden from plan output - review managed_state_projection for changes.",
    // ... validators
},
```

**Implementation Steps:**
```bash
# Edit manifest.go - add Sensitive: true to yaml_body

# Test (EXPECT FAILURES in output assertions)
make test

# Fix test assertions:
# Remove checks for yaml_body content in plan output
# Or expect "(sensitive value)"

# Commit
git add internal/k8sconnect/resource/manifest/manifest.go
git commit -m "feat: Mark yaml_body as sensitive (#1)

Hide yaml_body from plan output to eliminate dual-diff confusion.
Users review managed_state_projection for cluster changes and use
git diff for config changes.

Part of bootstrap UX improvement series."
```

**Risk:** Low (cosmetic, only affects test assertions)

**Tests Affected:** Test output checks that expect yaml_body content

**Success Criteria:**
- yaml_body shows "(sensitive value)" in plan
- managed_state_projection shows changes
- All tests pass with updated assertions

---

## What NOT to Do

### ❌ Don't Apply the Whole Stash
```bash
# NO: git stash pop
# This brings back all 6 changes at once
```

### ❌ Don't Combine #3 and #5
```bash
# NO: Apply plan_modifier.go AND crud.go preservation together
# You already tried this - debugging nightmare
```

### ❌ Don't Do #1 Early
```bash
# NO: yaml_body sensitive first
# Breaks test assertions, adds noise while debugging logic
```

### ❌ Don't Skip #4
```bash
# NO: Jump straight to #3
# #3 needs #4's enhanced connection check to work properly
```

## Success Metrics

After completing all 5 remaining changes:

- ✅ CREATE operations work without cluster existing during plan
- ✅ No "inconsistent plan" errors during bootstrap
- ✅ managed_state_projection shows yaml-based fallback for CREATE
- ✅ Field ownership populated after CREATE
- ✅ yaml_body hidden from plan output
- ✅ All acceptance tests passing
- ✅ ADRs document the approach

## Emergency Rollback

If any day's change causes too much pain:

```bash
# Rollback that day's change
git revert HEAD

# Or if not committed yet
git restore <file>

# Re-evaluate the approach
# Maybe that change needs to be split further
```

## Key Lessons

1. **One change at a time** - Even if they're related
2. **Test between each** - Catch issues early
3. **Expect test failures** - That's OK, just fix them for that one change
4. **Commit frequently** - Safe rollback points
5. **Dependencies matter** - Do foundation (#4) before features (#3, #5)
6. **Cosmetic last** - Logic first (#3-#6), polish last (#1)

## Timeline Estimate

- Day 1: #4 - 2 hours (low risk)
- Day 2: #6 - 1 hour (bug fix)
- Day 3: #3 - 4 hours (test fixes)
- Day 4: #5 - 2 hours (builds on #3)
- Day 5: #1 - 2 hours (test assertions)

**Total: ~11 hours spread over 5 days**

Compare to this morning: 6 hours, ended in revert 😅

---

**Remember:** Slow is smooth, smooth is fast. One change at a time.
