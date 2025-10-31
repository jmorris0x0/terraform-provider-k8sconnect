# ADR-021: Ownership Transition Messaging

**Status:** In Progress
**Date:** 2025-10-28
**Decision Date:** 2025-01-30 (Option A: Value-Based Detection)
**Last Updated:** 2025-01-30
**Related ADRs:** ADR-005 (Managed Fields Strategy), ADR-020 (Managed Fields Display Strategy)

## Summary

This ADR defines a systematic approach to ownership transition messaging using a 16-row state machine (4 boolean dimensions).

**Completed:**
- ✅ **Phase 0: Data Structure Foundation** - Refactored ownership tracking from `map[string]string` to `map[string][]string` to properly handle SSA shared ownership. Eliminates "last manager wins" bug. All tests passing (2025-10-28).
- ✅ **Phase 1: Option A/B Decision** - Decided on Option A (value-based detection) for `external_changed` dimension (2025-01-30).

**Current State:**
- Ownership warnings are ad-hoc (scattered across resource implementations, no centralized logic)
- Data structure (`map[string][]string`) supports the chosen approach
- The 16-row classification system described below is PLANNED, not yet implemented

**Next Steps:**
1. ✅ ~~Decide Option A (value-based) vs Option B (metadata-based) for `external_changed` detection~~ **DECIDED: Option A**
2. Implement centralized classification module with 16-row table
3. Integrate into both resources (object, patch)
4. Add provider-level verbosity configuration

## Context

### Background: Shared Ownership Discovery

ADR-020 discovered a fundamental SSA behavior: when multiple managers apply the SAME value with `force=true`, Kubernetes creates **shared ownership** - both managers are listed as co-owners in managedFields. This is intentional SSA design for collaboration.

**Example:**
```
T-1: k8sconnect owns spec.replicas exclusively (value: 3)
T:   HPA evaluates, decides 3 replicas needed, applies with force=true
     K8s adds HPA as co-owner alongside k8sconnect
     Value unchanged: still 3
T+1: Both "k8sconnect" and "horizontal-pod-autoscaler" listed in managedFields
```

This creates a critical question for ownership transition messaging: **Should we warn when a new manager becomes a co-owner (even if value matches)?**

### The Problem

ADR-020 established that we track field ownership via the `managed_fields` computed attribute (tracking ALL field managers, not just k8sconnect). However, we had no systematic approach to:

1. **Classify all possible ownership transitions** - What are ALL the ways ownership can change?
2. **Determine appropriate messaging** - Which transitions need warnings vs notes vs silence?
3. **Handle multiple fields transitioning simultaneously** - A single resource can have dozens of fields undergoing different ownership transitions in one update
4. **Distinguish taking vs re-taking ownership** - Critical UX difference between claiming a field for the first time vs regaining ownership after drift

Without a systematic approach, we risked:
- Inconsistent messaging (same scenario shows different messages)
- Missing edge cases (forgotten transitions)
- Overwhelming users (separate message per field)
- Confusing UX ("taking ownership" when we already owned it)

### Key Insight: Four Boolean Dimensions

After analysis, we discovered that ALL ownership transitions can be described by exactly four independent boolean dimensions:

```
(prev_owned, now_owned, config_changed, external_changed)
```

**prev_owned**: Did k8sconnect own this field in previous Terraform state (T-1)?
**now_owned**: Does k8sconnect own this field after current operation (T+1)?
**config_changed**: Did user modify `yaml_body` or `ignore_fields` between T-1 and T?
**external_changed**: Did external field manager change ownership/value between T-1 and T?

This creates a **16-state transition matrix** (2^4) that mathematically covers every possible ownership transition scenario.

### The Multi-Field Problem

A single Kubernetes resource can have dozens of fields, each undergoing different transitions:

```yaml
spec:
  replicas: 3           # Row 13: Re-taking (drift)
  selector:
    app: web           # Row 12: Hold (no change)
  template:
    spec:
      containers:
      - image: nginx:1.19  # Row 14: Normal update
      - image: busybox     # Row 6: Taking (new field)
```

Four fields, three different transition types in ONE update! Emitting a separate diagnostic message for each field would be overwhelming and confusing.

## Decision

**We implement a centralized ownership transition classification system with resource-level message aggregation.**

### Core Components

#### 1. 16-Row Transition Table

All ownership transitions classified by boolean tuple at **plan time** (using dry-run predictions):

| # | prev | now | config | external | Possible? | Scenario | Message Level |
|---|------|-----|--------|----------|-----------|----------|---------------|
| 0 | F | F | F | F | ✅ YES | Unmanaged Stable | Silent |
| 1 | F | F | F | T | ✅ YES | Unmanaged External Change | Silent |
| 2 | F | F | T | F | ✅ RARE | Config Change, Still Unmanaged | Silent |
| 3 | F | F | T | T | ✅ YES | Unmanaged, Both Changed | Silent |
| 4 | F | T | F | F | ❌ IMPOSSIBLE | Spontaneous Gain | N/A |
| 5 | F | T | F | T | ❌ IMPOSSIBLE | External Causes Gain? | N/A |
| 6 | F | T | T | F | ✅ YES | **TAKING (first time)** | Silent |
| 7 | F | T | T | T | ✅ YES | **TAKING with Conflict** | Warning |
| 8 | T | F | F | F | ❌ IMPOSSIBLE | Spontaneous Loss | N/A |
| 9 | T | F | F | T | ❌ IMPOSSIBLE | Loss with force=true | N/A |
| 10 | T | F | T | F | ✅ YES | **INTENTIONAL RELEASE** | Silent |
| 11 | T | F | T | T | ✅ YES | **RELEASE + External** | Silent |
| 12 | T | T | F | F | ✅ YES | **HOLD (normal)** | Silent |
| 13 | T | T | F | T | ✅ YES | **RE-TAKING (drift)** | Warning |
| 14 | T | T | T | F | ✅ YES | **HOLD with Update** | Silent |
| 15 | T | T | T | T | ✅ YES | **UPDATE + Conflict** | Warning |

**Statistics:**
- Total states: 16 (2^4)
- Possible states: 11
- Impossible states: 5 (rows 4, 5, 8, 9)
- Silent states: 8 (rows 0, 1, 2, 3, 6, 10, 11, 12, 14)
- Warning states: 3 (rows 7, 13, 15 - **conflict scenarios**)

**Key Insights**:
- Row 9 (losing ownership despite force=true) is IMPOSSIBLE. Dry-run with force=true predicts we regain ownership, so now_owned=true (not Row 9).
- **Warn on conflicts**: Rows 7, 13, 15 all have `external_changed=true AND now_owned=true` → we're asserting ownership despite external controller activity → potential persistent drift if controllers fight.

#### 2. Conflict Detection Pattern

**All warnings share a common pattern**: `external_changed=true AND now_owned=true`

This means: "An external controller was managing/modifying this field, and we're asserting ownership anyway."

**The three conflict scenarios:**

**Row 7: TAKING with Conflict**
- `(false, true, true, true)` - Taking field from external controller
- User removed from ignore_fields, but external controller was actively managing it
- Example: Remove spec.replicas from ignore_fields while HPA is scaling
- **Risk**: HPA will keep trying to manage replicas → persistent drift ping-pong
- Message: "⚠️ Conflict: Taking ownership from active controller"

**Row 13: RE-TAKING (drift)**
- `(true, true, false, true)` - Regaining field after external took it
- We owned it, external modified it, we're reverting
- Example: kubectl manually changed replicas, terraform reverts
- **Risk**: If external controller keeps modifying, endless ping-pong
- Message: "⚠️ Drift detected - reverting external changes"

**Row 15: UPDATE + Conflict**
- `(true, true, true, true)` - Updating while external also changing
- User updated yaml_body, but external also modified the same field
- Example: User changes image, kubectl also changed it, we overwrite
- **Risk**: External controller might revert our change → potential fight
- Message: "⚠️ Conflict: Overwriting external changes during update"

**Why warn?** All three scenarios indicate potential **persistent drift** - another controller is actively managing fields we're claiming. User needs to either:
1. Add field to `ignore_fields` (let other controller manage it), OR
2. Disable the other controller (let Terraform manage it exclusively)

#### 3. Resource-Level Aggregation

**Implementation:**

```go
// Step 1: Per-field classification
type ConflictType int
const (
    TakingConflict ConflictType = iota  // Row 7
    DriftConflict                       // Row 13
    UpdateConflict                      // Row 15
    NoConflict                          // All other rows
)

func ClassifyConflict(prev, now, config, external bool) ConflictType {
    // Pattern: external=true AND now=true = conflict!
    if !external || !now {
        return NoConflict
    }

    // Row 7: (false, true, true, true) = Taking from external
    if !prev && config {
        return TakingConflict
    }

    // Row 13: (true, true, false, true) = Drift revert
    if prev && !config {
        return DriftConflict
    }

    // Row 15: (true, true, true, true) = Update + conflict
    if prev && config {
        return UpdateConflict
    }

    return NoConflict
}

// Step 2: Aggregate conflicts by type
type ConflictDetection struct {
    takingConflicts []FieldChange  // Row 7
    driftConflicts  []FieldChange  // Row 13
    updateConflicts []FieldChange  // Row 15
}

// Step 3: Format warnings
func (c *ConflictDetection) FormatWarnings() []string {
    var warnings []string

    if len(c.driftConflicts) > 0 {
        warnings = append(warnings, c.formatDriftWarning())
    }
    if len(c.takingConflicts) > 0 {
        warnings = append(warnings, c.formatTakingWarning())
    }
    if len(c.updateConflicts) > 0 {
        warnings = append(warnings, c.formatUpdateWarning())
    }

    return warnings
}
```

**Example warning messages:**

```
⚠️ Drift detected - external changes will be reverted

The following fields were modified externally and will be reverted to your configuration:

  spec.replicas: 5 → 3 (modified by: kubectl)

To allow external management of this field, add it to ignore_fields.
```

```
⚠️ Ownership conflict - taking field from active controller

Removed from ignore_fields, but external controller is actively managing:

  spec.replicas (managed by: horizontal-pod-autoscaler)

Risk: HPA will continue trying to scale this deployment, causing persistent drift.
Consider: Add spec.replicas to ignore_fields to let HPA manage scaling.
```

```
⚠️ Ownership conflict - overwriting concurrent external changes

Updated yaml_body, but external controller also modified these fields:

  spec.template.spec.containers[0].image: "nginx:1.19" → "nginx:1.20"
  (external wanted: "nginx:1.18", you specified: "nginx:1.20")

Your configuration will be applied (overwriting external changes).
```

#### 4. Plan-Time Detection

**Timeline:**
```
T-1: Previous Terraform state (has managedFields from last apply)
     [External changes may occur]
T-plan: terraform plan
        1. Read current K8s state
        2. Detect external_changed: Compare T-1 vs current managedFields
        3. Do dry-run with force=true
        4. Dry-run response predicts T+1 managedFields
        5. Classify transitions using 16-row table
        6. Show warnings/notes in plan output ← USER SEES THEM HERE
     [User reviews plan, decides whether to apply]
T-apply: terraform apply (execute the planned changes)
```

**Why plan time?** Users need to see ownership warnings BEFORE applying so they can make informed decisions. Detection at apply time would be too late.

**Implementation location:** `plan_modifier.go` ModifyPlan() - after dry-run completes, before returning plan to user.

## Implementation Details

### Detection Logic

```go
// In plan_modifier.go ModifyPlan()
// After dry-run completes

prevOwned := stateManaged.isOwnedBy("k8sconnect", fieldPath)  // From T-1 state
nowOwned := dryRunManaged.isOwnedBy("k8sconnect", fieldPath)  // From dry-run prediction
configChanged := (plan.YamlBody != state.YamlBody) ||
                 (plan.IgnoreFields != state.IgnoreFields)
externalChanged := detectExternalChange(
    stateManaged,        // T-1 managedFields
    currentK8sManaged,   // Current K8s managedFields (before our apply)
    fieldPath,
)

// Classify conflict type
conflictType := ownership.ClassifyConflict(
    prevOwned,
    nowOwned,
    configChanged,
    externalChanged,
)

if conflictType != ownership.NoConflict {
    conflicts.AddField(conflictType, fieldPath, previousValue, currentValue, externalManager)
}

// After processing all fields
for _, warning := range conflicts.FormatWarnings() {
    resp.Diagnostics.AddWarning("Ownership Conflict", warning)
}
```

### Module Location

New module: `internal/k8sconnect/common/ownership/`

Files:
- `conflicts.go` - Conflict detection and warning formatting (all 3 types)
- `detection.go` - Helper functions for detecting external changes
- `conflicts_test.go` - Unit tests for all 16 rows (exhaustive!)

### Unit Test Coverage

The 16-row table enables **exhaustive unit testing**:

```go
func TestClassifyConflict(t *testing.T) {
    tests := []struct{
        name string
        prev, now, config, external bool
        want ConflictType
    }{
        // Row 0-6: No conflict
        {name: "unmanaged stable",
         prev: false, now: false, config: false, external: false,
         want: NoConflict},

        {name: "taking first time (no external)",
         prev: false, now: true, config: true, external: false,
         want: NoConflict},

        // Row 7: Taking with conflict
        {name: "taking from active external controller (Row 7)",
         prev: false, now: true, config: true, external: true,
         want: TakingConflict},

        // Row 13: Drift
        {name: "drift detected (Row 13)",
         prev: true, now: true, config: false, external: true,
         want: DriftConflict},

        // Row 15: Update with conflict
        {name: "update while external also changing (Row 15)",
         prev: true, now: true, config: true, external: true,
         want: UpdateConflict},

        // ... all 16 rows tested
    }
}
```

Every possible ownership transition has a corresponding unit test verifying the correct conflict classification.

## Decision: Option A (Value-Based Detection) for external_changed

**Update (2025-01-30):** ✅ **DECIDED - Option A (Value-Based Detection)**

After comprehensive analysis of shared ownership scenarios, false positive/negative rates, UX implications, and implementation complexity, we have decided to use **Option A: Value-Based Detection**.

### The Decision

The boolean `external_changed` is defined as:

```go
external_changed = (currentFieldValue != previousStateValue)
```

**NOT:**
```go
external_changed = (currentManagers != previousManagers) OR (currentValue != previousValue)
```

This means `external_changed` is TRUE only when the field **value** actually changed, ignoring changes to the manager metadata when values match.

### Why Option A?

The critical ambiguity was whether to warn when external controllers become co-owners (even if value matches) versus only warning when values actually diverge.

### Shared Ownership Scenarios

**Scenario 1: HPA Becomes Co-Owner (Value Matches)**
```
T-1: k8sconnect owns spec.replicas exclusively (value: 3)
T:   HPA evaluates, decides 3 replicas needed
     HPA applies spec.replicas: 3 with force=true
     Both become co-owners (managedFields lists both)
     Value unchanged: 3
```

With Option A (value-based): `external_changed=false` → Row 12 (HOLD, silent)
With Option B (metadata-based): `external_changed=true` → Row 13 (warning)

**Scenario 2: HPA Scales Deployment**
```
T-1: k8sconnect owns spec.replicas exclusively (value: 3)
T:   HPA scales to 8 replicas
     HPA applies spec.replicas: 8 with force=true
     Value changed: 3 → 8
```

With Option A: `external_changed=true` → Row 13 (warning) ✓
With Option B: `external_changed=true` → Row 13 (warning) ✓

**Scenario 3: One-Time kubectl Patch (Value Matches)**
```
T-1: k8sconnect owns spec.image exclusively (value: "nginx:1.20")
T:   User runs: kubectl patch deployment foo -p '{"spec":{"template":{"spec":{"containers":[{"name":"app","image":"nginx:1.20"}]}}}}'
     Same value as yaml_body
     Both become co-owners
     Value unchanged: "nginx:1.20"
```

With Option A: `external_changed=false` → Row 12 (silent) ✓
With Option B: `external_changed=true` → Row 13 (warning) - **FALSE POSITIVE**

### Rationale for Choosing Option A

After comprehensive scenario analysis documented below, we chose **Option A (Value-Based Detection)** for the following reasons:

**1. Low False Positives**
- Doesn't warn on benign shared ownership (HPA becoming co-owner when value matches)
- Doesn't warn on one-time kubectl operations with matching values
- Supports GitOps dual-management patterns (Terraform + ArgoCD applying same config)
- Doesn't warn on normal Kubernetes self-healing operations

**2. High Actionability**
- Message shows actual value change: "spec.replicas: 5 → 3"
- User can see the problem and make informed decision
- Clear cause-and-effect relationship
- Aligns with Terraform's value-based drift detection mental model

**3. Better User Experience**
- Warnings only appear when there's real drift requiring user action
- No "crying wolf" on every plan when values match
- Clear, predictable behavior: "warn when values don't match"
- No confusing warnings about "drift" when values are actually correct

**4. Simpler Implementation**
- Just compare field values (already have both objects)
- No need to maintain manager list history complexity
- Fewer edge cases and conditions to handle
- Lower maintenance burden

**5. Acceptable Trade-Off**
- We give up: Early warning when HPA becomes co-owner (before it scales)
- This is acceptable because:
  - No actionable decision until HPA actually scales
  - User can't meaningfully respond to "HPA is managing this" when values match
  - When drift occurs, warning clearly identifies HPA as the cause
  - In practice, HPA will scale within minutes/hours if there's a problem

### Analysis of Both Approaches (Historical Context)

**Option A: Value-Based**

Pros:
- Only warns on actual drift (value mismatches)
- Low false positives (doesn't warn on benign shared ownership)
- Simple to implement (compare field values)
- Actionable (user sees: "field changed from X to Y")
- No controller list maintenance needed

Cons:
- Doesn't warn early about HPA (only warns when HPA actually scales, not when it becomes co-owner)
- Misses potential future conflicts (HPA is active but values match today, might drift tomorrow)

**Option B: Metadata-Based** (Rejected)

Pros:
- Early warning about active controllers (warns when HPA becomes co-owner)
- Catches potential conflicts before values diverge

Cons:
- False positives (warns on one-time kubectl operations)
- Noisy (warns even when values match and there's no actual problem)
- Less actionable ("New manager appeared" - so what?)
- Harder to understand ("Why is it warning? The value is correct!")

### Hybrid Approach Considered

We could track BOTH conditions:

```go
type FieldChange struct {
    valueChanged    bool  // Actual drift
    managersChanged bool  // Shared ownership appeared/changed
}
```

Then emit:
- **Warning** for value changes (actual drift)
- **Info/Note** for manager changes when value matches (FYI only)

But this:
1. Breaks the clean 4-boolean model (would need 5+ dimensions)
2. Adds complexity (32+ states instead of 16)
3. Questionable value (is the "FYI" useful?)

### User Perspective Analysis

**User with HPA (most common case):**
- Has HPA configured, forgets to add spec.replicas to ignore_fields
- What do they need to know?
  - When HPA SCALES (value changes): "spec.replicas changed 3 → 8 by HPA, add to ignore_fields" ✓
  - When HPA becomes co-owner but doesn't scale yet: Not actionable (no problem yet)

**User with one-time kubectl patch:**
- Runs kubectl patch once, value matches yaml_body
- What do they need to know?
  - Nothing! Values match, no drift, no problem ✓

**User with Flux/ArgoCD dual-management:**
- Both Terraform and GitOps managing same resource
- Values might match (same source) or diverge (conflict)
- What do they need to know?
  - When values diverge: "field changed, conflict detected" ✓
  - When values match: Acceptable pattern (both applying same config)

### Final Decision: Option A (Value-Based)

✅ **DECIDED (2025-01-30)**

Define `external_changed` as:
```go
external_changed = (currentValue != previousStateValue)
```

**Rationale:**
1. **Actionability** - Only warn when there's actual drift requiring user decision
2. **Low noise** - Don't warn on benign shared ownership or one-time operations
3. **Simplicity** - No need to maintain known-controller lists or complex logic
4. **Clear messaging** - "This field changed from X to Y, we're reverting"

**Trade-off accepted:** We won't warn early when HPA becomes co-owner (only when it actually scales). This is acceptable because:
- No actual problem until values diverge
- User can't act on "HPA is now co-owner" without drift
- When drift occurs, message clearly identifies HPA as the modifier

### Alternative Considered: Well-Known Controllers Detection (Rejected)

We considered using metadata-based detection ONLY for known controllers:

```go
knownControllers := []string{
    "horizontal-pod-autoscaler",
    "vertical-pod-autoscaler",
    "cluster-autoscaler",
    "deployment-controller",
    "argocd-application-controller",
    "flux-controller",
}

external_changed = (newManagerIsKnownController(managers)) OR (currentValue != previousValue)
```

This gives early warnings for controllers, but ignores one-time kubectl operations.

**Pros:**
- Best of both worlds (early HPA warning, no kubectl noise)

**Cons:**
- Requires maintaining controller list (will become stale)
- Arbitrary line between "controller" and "tool"
- What about custom operators? (Can't know all manager names)
- More complexity, fragile

**Verdict:** Not worth the complexity and maintenance burden. **Rejected in favor of pure value-based detection.**

### Decision Impact

✅ **DECIDED: Value-Based Detection (Option A)**

This decision means:
- `external_changed = (currentValue != previousStateValue)` for all fields
- No special handling for known controllers
- Warnings only when values actually diverge
- Shared ownership awareness is left for future optional diagnostic features

### Implementation Progress

**Completed: Data Structure Refactor (2025-10-28)**

We identified and fixed a fundamental bug in ownership tracking. The old code used `map[string]string` (single manager per field) which created "last manager wins" behavior when SSA co-ownership occurred.

**Changes made:**

1. **Data structure refactor** - Changed from `map[string]string` to `map[string][]string`:
   ```go
   // OLD (single manager):
   ownership := map[string]string{
       "spec.replicas": "k8sconnect",  // Can only track ONE manager
   }

   // NEW (all managers):
   ownership := map[string][]string{
       "spec.replicas": []string{"k8sconnect", "horizontal-pod-autoscaler"},  // Tracks ALL co-owners
   }
   ```

2. **Updated core functions**:
   - `ExtractAllManagedFields()` - Now returns `map[string][]string` with ALL co-owners
   - `getManagedFieldsFromPrivateState()` - Handles new data structure
   - `setManagedFieldsInPrivateState()` - Stores all managers
   - `checkOwnershipTransitions()` - Compares manager lists properly

3. **Common helper functions** - Added to `internal/k8sconnect/common/types.go`:
   - `StringSliceContains()` - Check if manager is in list
   - `StringSlicesEqual()` - Compare manager lists (order-independent)

4. **Silent state migration**:
   - Both object and patch resources gracefully migrate old `map[string]string` state
   - Migration happens on first read after upgrade
   - Debug-level logging only (invisible to users)

5. **Resources updated**:
   - `internal/k8sconnect/resource/object/` - Full refactor
   - `internal/k8sconnect/resource/patch/` - Full refactor
   - Both now properly track shared ownership

**Benefits:**
- ✅ Eliminates non-deterministic "last manager wins" behavior
- ✅ Properly represents three ownership states (exclusive k8sconnect, exclusive external, shared)
- ✅ Enables accurate shared ownership detection (foundation for Option A vs B decision)
- ✅ All tests passing (unit tests + acceptance tests)

**Current Code Behavior (Post-Refactor)**

The code now has the **data infrastructure** to implement either Option A or Option B:

1. **Private state storage** stores ALL managers for each field:
   ```go
   ownershipMap := extractAllManagedFields(rc.Object)  // Returns map[string][]string
   setManagedFieldsInPrivateState(ctx, resp.Private, ownershipMap)
   ```

2. **Ownership comparison capability** - Data structure supports both options:
   ```go
   prevOwnedByUs := common.StringSliceContains(previousOwners, "k8sconnect")
   nowOwnedByUs := common.StringSliceContains(currentOwners, "k8sconnect")

   // Can detect manager list changes (Option B):
   managersChanged := !common.StringSlicesEqual(previousOwners, currentOwners)

   // Can also compare values for Option A (when centralized system implemented):
   // valueChanged := (currentValue != previousValue)
   ```

3. **Current warning implementation**:
   - Ad-hoc warnings scattered across resource implementations
   - No centralized 16-row classification system yet
   - Simple ownership change detection without systematic categorization

**Decision Complete (2025-01-30):**

✅ Phase 1 is now complete. We have decided on **Option A (value-based detection)** for `external_changed`.

With both the data structure foundation and the architectural decision complete, the next steps are:
1. ✅ ~~**Decide** Option A (value-based) vs Option B (metadata-based) for `external_changed` detection~~ **DECIDED: Option A**
2. **Implement** centralized classification module with 16-row table
3. **Replace** ad-hoc warnings with systematic categorization

The data structure refactor and Option A decision provide a clear path forward for implementation.

## Rationale

### Why This Approach?

**1. Mathematical Completeness**
- Four boolean dimensions capture ALL possible scenarios
- 16-row table is exhaustive (2^4 states)
- Can prove we haven't missed any edge cases

**2. Centralized Logic**
- Single source of truth for all ownership messaging
- Consistent behavior across object, patch, wait resources
- Easy to maintain (change messages in one place)

**3. Testability**
- Unit tests for all 16 combinations (fast, no cluster needed)
- Deterministic classification (no ambiguity)
- Easy to verify correctness

**4. Resource-Level UX**
- One aggregated message per resource (not overwhelming)
- Groups related transitions together
- Verbosity control for different user preferences

**5. Plan-Time Visibility**
- User sees warnings BEFORE applying (critical for decision-making)
- Uses dry-run predictions (accurate T+1 state)
- No surprises during apply

### Why Not Alternatives?

**Scattered logic across resources:**
- Would lead to inconsistent messaging
- Hard to test (requires acceptance tests)
- Easy to forget edge cases
- Maintenance nightmare (6+ places to update)

**Apply-time detection:**
- Too late - user already committed
- Can't make informed decision
- Defeats purpose of ownership warnings

**Per-field messages:**
- Overwhelming (dozens of fields per resource)
- Hard to understand overall picture
- Clutters plan output

## Consequences

### Positive

1. **Complete coverage** - All ownership transitions handled systematically
2. **Consistent UX** - Same scenarios always produce same messages
3. **Testable** - Exhaustive unit test coverage
4. **Maintainable** - Single place to update messaging
5. **Scalable** - Handles resources with dozens of fields gracefully
6. **User control** - Verbosity levels for different preferences

### Negative

1. **Complexity** - Requires understanding 16-row state machine
2. **Implementation effort** - New module, aggregation logic, tests
3. **Breaking change** - Replaces ad-hoc ownership warnings (if any existed)

### Migration Path

**Phase 0: Data Structure Foundation** ✅ **COMPLETED (2025-10-28)**
- ✅ Refactor ownership tracking from `map[string]string` to `map[string][]string`
- ✅ Update `ExtractAllManagedFields()` to track all co-owners
- ✅ Add helper functions to `common/types.go`
- ✅ Implement silent state migration in both object and patch resources
- ✅ Update ownership transition detection to compare manager lists
- ✅ All tests passing (unit + acceptance)

**Phase 1:** ✅ **COMPLETED (2025-01-30)** - Decide Option A vs Option B
- ✅ Decided: Value-based (Option A) detection for `external_changed`
- ✅ Documented rationale and scenario analysis in ADR
- Next: Implement value comparison logic in Phase 2/3

**Phase 2:** Implement centralized classification module
- Create `internal/k8sconnect/common/ownership/` module
- Implement ClassifyConflict with 16-row table
- Implement ResourceOwnershipChanges aggregation
- Unit tests for all 16 rows

**Phase 3:** Integrate into object resource
- Update `object/plan_modifier.go` to use new module
- Test with existing object acceptance tests
- Verify message quality in real scenarios

**Phase 4:** Integrate into patch resource
- Update `patch/plan_modifier.go` to use new module
- Ensure consistency with object messaging

**Phase 5:** Provider configuration
- Add `managed_fields_verbosity` provider config
- Default to "full", allow "minimal" and "none"

## Handling Persistent Ownership Conflicts

### The Uncorrectable Drift Scenario

**Context:** What happens when an external controller actively manages a field that the user hasn't added to `ignore_fields`? This creates persistent drift:

1. User applies: k8sconnect claims field with force=true
2. External controller (HPA, operator, etc.) takes it back
3. User applies again: k8sconnect reclaims with force=true
4. Cycle repeats indefinitely

**Question:** Should we handle this differently than a one-time warning?

### Options Considered

**Option A: Warning Only** (✅ ADOPTED)

Show clear warning on each apply indicating the conflict:
```
⚠️ Ownership conflict detected for spec.replicas

This field is being actively managed by: horizontal-pod-autoscaler
But you are also managing it (not in ignore_fields).

This will cause persistent drift as both controllers fight for ownership.

Recommendation: Add spec.replicas to ignore_fields to let HPA manage scaling.
```

**Pros:**
- Simple, clear, actionable
- User decides what to do
- No automatic behavior that might surprise
- Warning appears every apply until resolved

**Cons:**
- Doesn't "solve" the problem automatically
- User must take action

**Option B: Automatic Detection + Suggestion**

Track history across multiple applies, escalate warning after detecting ping-pong pattern:
```
⚠️ Persistent ownership conflict detected (3 applies in a row)

Field spec.replicas keeps changing between you and horizontal-pod-autoscaler.

Would you like to add this to ignore_fields? (You'll need to do this manually)
```

**Pros:** More helpful context
**Cons:** More complex, requires history tracking, still requires manual action

**Option C: Error on Persistent Ping-Pong**

After N applies with same conflict, ERROR instead of warn.

**Pros:** Forces resolution
**Cons:** Too aggressive, breaks applies (possibly during deploy), user may not be able to fix immediately

### Decision: Option A (Warning Only) - Initial Implementation

**Status:** Adopted as the initial approach, but acknowledged as potentially insufficient for persistent conflicts.

**Rationale:**
1. **Clear warning is sufficient for most cases** - Row 13 (RE-TAKING drift) warns about external changes with clear recommendation
2. **User control** - User decides whether to add to ignore_fields, disable external controller, or accept the drift
3. **No surprises** - Doesn't break applies or automatically modify configuration
4. **Simple implementation** - No history tracking needed, just clear messaging

**Implementation:** Row 13 warnings should include:
- Field path that's conflicting
- External manager name (e.g., "horizontal-pod-autoscaler")
- Clear recommendation to use `ignore_fields`
- Link to documentation if needed

**Known Limitation:** For truly persistent conflicts (user and controller fighting over same field), seeing the same warning on every apply might not be the best UX. We should continue exploring better solutions.

### Rejected Approaches (For Now)

**History-based escalation (Option B)** - Adds complexity, still requires manual action. May revisit if clear use cases emerge.

**Automatic errors (Option C)** - Too aggressive for initial implementation. Users need flexibility during deploy cycles.

### Future Exploration

The persistent conflict scenario deserves better solutions than "warn forever":

**Possible approaches to explore:**
- Auto-detect persistent ping-pong and suggest more specific remediation
- Provider-level conflict resolution policies (fail vs warn vs ignore)
- Automatic addition to ignore_fields with user confirmation prompt
- Integration with external controller detection (detect HPA is installed, auto-suggest ignore_fields for replicas)
- "Conflict resolution mode" that temporarily allows user to see but not fix the conflict

**Key insight:** The problem isn't just *detecting* the conflict (we do that), it's *resolving* it in a way that respects both Terraform's declarative model and the reality of Kubernetes controllers.

This is an open problem. Option A gives us a working baseline while we learn from real-world usage what better solutions might look like.

## Future Considerations

### Message Customization

In the future, we could allow users to customize messages or add "Learn More" links to documentation explaining field ownership concepts.

### Field-Specific Rules

For well-known fields (like spec.replicas with HPA), we could show specialized messages: "HPA is managing replicas - add spec.replicas to ignore_fields if intentional".

Note: Ownership history tracking for ping-pong detection was considered but rejected (see "Handling Persistent Ownership Conflicts" above).

## Related Documentation

- ADR-005: Managed Fields Strategy - Why we track ownership
- ADR-020: Managed Fields Display Strategy - Private state + warnings decision
- ADR-011: Concise Diff Format - How ownership appears in diffs (may be superseded)
- `OWNERSHIP_TRANSITION_TABLE.md` - Detailed analysis of all 16 states

## Appendix: Complete 16-Row Table with Examples

### Row 0: (F, F, F, F) - Unmanaged Stable
**When:** Field in ignore_fields, nothing changes
**Message:** Silent
**Example:** spec.replicas in ignore_fields, HPA manages it, value stays 5

### Row 1: (F, F, F, T) - Unmanaged External Change
**When:** Field in ignore_fields, external modifies it
**Message:** Silent (expected behavior)
**Example:** spec.replicas in ignore_fields, HPA changes from 5 → 10

### Row 2: (F, F, T, F) - Config Change, Still Unmanaged
**When:** Modify yaml_body value of ignored field OR add to ignore_fields (redundant)
**Message:** Silent
**Example:** Field already ignored, add to ignore_fields again

### Row 3: (F, F, T, T) - Unmanaged, Both Changed
**When:** Add to ignore_fields + external modifies it simultaneously
**Message:** Silent
**Example:** Add spec.replicas to ignore_fields, HPA also modifies it

### Row 4: (F, T, F, F) - IMPOSSIBLE
**Why:** Can't gain ownership without config or external trigger

### Row 5: (F, T, F, T) - IMPOSSIBLE
**Why:** External change alone can't cause us to claim ownership

### Row 6: (F, T, T, F) - TAKING (first time)
**When:** Remove field from ignore_fields (no external drift)
**Message:** Note - "✓ Now managing field (removed from ignore_fields)"
**Example:** spec.replicas removed from ignore_fields, we claim it

### Row 7: (F, T, T, T) - TAKING with Conflict
**When:** Remove from ignore_fields + external modified + we overwrite
**Message:** Warning - "⚠️ Taking ownership (reverting external changes)"
**Example:** Remove spec.replicas from ignore_fields, kubectl changed it, we overwrite

### Row 8: (T, F, F, F) - IMPOSSIBLE
**Why:** Can't lose ownership without external or config trigger

### Row 9: (T, F, F, T) - IMPOSSIBLE
**Why:** Can't lose ownership with force=true when field still in yaml_body
**Explanation:** Dry-run with force=true predicts we regain ownership (now_owned=true)

### Row 10: (T, F, T, F) - INTENTIONAL RELEASE
**When:** Add field to ignore_fields
**Message:** Note - "✓ Released ownership (added to ignore_fields)"
**Example:** Add spec.replicas to ignore_fields, HPA now manages it

### Row 11: (T, F, T, T) - RELEASE + External
**When:** Add to ignore_fields + external manager takes it
**Message:** Note - "✓ Released ownership (now managed by kubectl)"
**Example:** Add spec.replicas to ignore_fields, HPA immediately claims it

### Row 12: (T, T, F, F) - HOLD (normal, most common)
**When:** No changes, maintaining ownership
**Message:** Silent
**Example:** Normal terraform apply with no drift, no config changes

### Row 13: (T, T, F, T) - RE-TAKING (drift revert)
**When:** External modified field, we revert with force=true
**Message:** Warning - "⚠️ Reverting external changes (drift detected)"
**Example:** kubectl changed spec.replicas from 3 → 5, terraform apply changes back to 3

### Row 14: (T, T, T, F) - HOLD with Config Update
**When:** User modified yaml_body, normal update
**Message:** Silent
**Example:** Change image from nginx:1.18 → nginx:1.19 in yaml_body

### Row 15: (T, T, T, T) - UPDATE + Conflict
**When:** User modified yaml_body + external also changed + we win
**Message:** Note - "Updated field (also reverted external changes)"
**Example:** User changes replicas 3 → 4, kubectl changed to 5, we apply 4 (our intent wins)
