# ADR-020: Field Ownership Display Strategy

**Status:** Accepted
**Date:** 2025-10-26
**Decision Date:** 2025-10-26
**Related ADRs:** ADR-005 (Field Ownership Strategy)

## Context

### The Field Ownership Feature

ADR-005 established that we track field-level ownership from Kubernetes Server-Side Apply (SSA) managedFields and expose it to users via a `field_ownership` state attribute. This feature was difficult to build and provides visibility into which controller manages which fields.

**User Value:**
- See ownership transitions in Terraform plan diffs (e.g., "kubectl-patch" → "k8sconnect")
- Understand controller conflicts (HPA managing replicas, etc.)
- Use `ignore_fields` to avoid ownership fights
- Reference in `depends_on` for orchestration

### Discovery: Shared Ownership in SSA

While fixing a flaky test (TestAccObjectResource_IgnoreFieldsJSONPathPredicate failing 16% of time), we discovered a fundamental misunderstanding about SSA behavior:

**What we thought:** `force=true` always takes exclusive ownership
**Reality:** `force=true` creates **shared ownership** when values are identical

When multiple managers apply the same value with `force=true`, Kubernetes lists BOTH managers in the managedFields array as co-owners of that field. This is intentional SSA design for collaboration.

See `docs/investigation/ssa-shared-ownership-evidence.md` for complete analysis.

### The Architectural Problem

The `field_ownership` state attribute tracks WHO owns each field:

```hcl
field_ownership = {
  "spec.replicas" = "kubectl-patch"
}
```

**Dilemma:** When kubectl-patch and k8sconnect both apply `replicas: 3`, they become co-owners. But which manager appears "last" in the managedFields array depends on apply timestamps (not under our control).

**Result:** Flaky tests, "Provider produced inconsistent result after apply" errors in real usage.

### The Fundamental Conflict

**Requirement 1:** Show ownership transitions (key feature)
**Requirement 2:** No inconsistent plan errors (Terraform's contract)

**Reality:** These conflict when tracking external managers whose ownership can change outside Terraform's control.

Terraform's rule: State must only track what you control. Tracking external ownership violates this.

## Options Considered

### Option 1: Only Track k8sconnect Ownership

Track ONLY fields owned by k8sconnect, ignore all other managers completely.

**Pros:**
- Fully stable (no external drift)
- Simple implementation
- Fixes flaky tests

**Cons:**
- Loses ownership transition visibility entirely
- Can't see "kubectl-patch" → "k8sconnect" transitions
- Feature becomes much less valuable
- Breaking change with no upside for users


### Option 2: Move to Private State + Warnings

Track all ownership internally in private state (not in tfstate file). Emit warnings during plan when ownership changes detected.

**Pros:**
- Preserves some visibility (via warnings)
- Stable public state

**Cons:**
- Breaking change (users can't reference field_ownership)
- Loses `depends_on` capability
- Warnings less useful than diffs


### Option 3: Deterministic Shared Ownership Handling

This option has two variants with significantly different stability characteristics:

#### Option 3A: Track All Ownership with Deterministic Rules

Track ALL field ownership (including fields managed exclusively by external controllers), apply deterministic rules for shared ownership.

**Rules:**
- Exclusive ownership by external manager → report that manager
- Exclusive ownership by k8sconnect → report "k8sconnect"
- Shared ownership including k8sconnect → report "k8sconnect"
- Shared ownership NOT including k8sconnect → report alphabetically first manager

**Problem:** External managers can CHANGE without Terraform action.
- HPA takes ownership of spec.replicas → shows "hpa-controller"
- Flux also takes ownership (co-owners) → alphabetically first wins
- Value changes from "hpa-controller" to "flux" with no Terraform action
- **UNSTABLE** - same problem as original bug, just less frequent

#### Option 3B: Only Track k8sconnect Ownership

Track ONLY fields where k8sconnect is currently an owner (exclusive or shared). Ignore fields managed exclusively by external controllers.

**Rules:**
- k8sconnect is exclusive owner → report "k8sconnect"
- k8sconnect is a co-owner (shared ownership) → report "k8sconnect"
- k8sconnect is NOT an owner → field not present in field_ownership
- Always filter out ignore_fields even if we own them

**Transition visibility:**
- External manager exclusively owns spec.replicas → NOT tracked (field not present)
- k8sconnect applies with force=true → takes ownership (exclusive or shared)
- Field APPEARS in field_ownership with value "k8sconnect"
- User sees: field added to field_ownership (we now manage it)

**Visibility trade-off:**
- **Lost:** Can't see "kubectl-patch" → "k8sconnect" transition (don't see "kubectl-patch" before the fight)
- **Kept:** Can see when we START managing a field vs when we STOP managing it
- **Kept:** Can see shared ownership resolution (always shows k8sconnect deterministically)

**Pros:**
- Fully stable (only track what we control)
- Respects Terraform's contract (state = what we manage)
- Fixes flaky tests (deterministic for shared ownership)
- No breaking changes to schema
- Simpler logic (filter to k8sconnect ownership only)

**Cons:**
- Loses "pre-fight" visibility (can't see external ownership before we take it)
- Field appears/disappears from map rather than value changing
- Users must infer ownership fight from field appearing + managed_state_projection value change

### Option 4: Remove field_ownership Entirely

Nuclear option - remove the feature.

### Option 5: List of Managed Fields Instead of Map

Change schema from map to list:

```hcl
# Old
field_ownership = {"spec.replicas" = "k8sconnect"}

# New
managed_fields = ["spec.replicas"]
```

Only track WHAT we manage, not WHO else manages it.

**Cons:**
- Loses ALL ownership transition visibility
- Breaking change
- Redundant with managed_state_projection keys

## Decision

**We adopt Option 2: Move to Private State + Warnings**

Field ownership tracking will be moved entirely to private state. The `field_ownership` attribute will be **removed from the schema**.

**Rationale:**
1. Full visibility of ownership transitions preserved (via warnings during plan)
2. No Terraform consistency errors (private state not compared)
3. Architecturally sound (tracking external state we can observe but don't control)
4. `depends_on` capability deemed non-critical for this use case

**Implementation:**
- Remove `field_ownership` from object and patch resource schemas
- Track all ownership (all managers) in private state
- During plan: Compare previous ownership (private state) vs current ownership (from K8s)
- Emit warnings when ownership changes detected
- Update private state with current ownership only during Create/Update operations (NOT during Read)
  - This ensures we preserve "ownership at last apply" for transition detection
  - If we updated during Read, both current and previous would match, missing transitions

**Breaking Change:** Users referencing `field_ownership` in configs will need to remove those references. The ownership information will be visible only via plan warnings.

## Addendum: Clarification of Core Reasoning (2025-10-28)

The decision to use private state was correct, but the original rationale in this ADR was incomplete. After implementing the `map[string][]string` refactor (fixing the "last manager wins" bug) and achieving test stability, we revisited whether private state was actually necessary.

### The Architectural Principle

**Core principle for robust provider design:**
> Terraform state should only show drift that `terraform apply` can resolve.

Private state upholds this principle. Computed attributes violate it (even if rarely).

### The Design Requirement

**We need TWO different datasets with different purposes:**

1. **Comprehensive ownership tracking** - Track ownership of ALL fields (including fields we don't manage)
   - Required to show meaningful transitions: `"hpa-controller" → "k8sconnect"`
   - Must include external controllers, operator annotations, etc.
   - Used for detecting when we TAKE ownership from another controller

2. **Actionable presentation** - Show only transitions users can act on
   - Filter to fields we actually manage (in yaml_body or previously managed)
   - Suppress noise from external controller interactions

### Why Computed Attributes Violate the Principle

**In v0.1.7, we tracked ALL non-status fields as a computed attribute:**

```go
ownership := extractFieldOwnership(currentObj)  // ALL fields
for path, owner := range ownership {
    if strings.HasPrefix(path, "status.") || path == "status" {
        continue  // Only filtering: status fields
    }
    ownershipMap[path] = owner.Manager  // Includes fields we DON'T manage!
}
data.FieldOwnership = mapValue  // Shows in Terraform diffs
```

**The edge case that violates the principle:**

```diff
~ resource "k8sconnect_object" "deployment" {
    ~ field_ownership = {
        ~ "metadata.annotations.operator-a/state" = "operator-a" → "operator-b"
      }
  }
```

- We don't manage `operator-a/state` (not in our yaml_body)
- We can't resolve this drift (not our field)
- `terraform apply` doesn't clear it (we don't control the field)
- **Unresolvable drift**

**Note:** This is rare in practice (external controllers typically avoid conflicts via naming conventions), but it's architecturally possible. For a provider aiming for robustness, any possibility of unresolvable drift is unacceptable.

**With computed attributes, you CANNOT separate:**
- **Data collection** (comprehensive - need all ownership for transition context)
- **User presentation** (filtered - show only actionable changes)

Both are merged into public state, and all changes show as drift.

### Why Private State Is Correct

**Private state + warnings enables separation of concerns:**

1. **Private state stores comprehensive data:**
   ```go
   // Track ALL ownership (including fields we don't manage)
   allOwnership := extractAllFieldOwnership(obj)
   setFieldOwnershipInPrivateState(ctx, resp.Private, allOwnership)
   ```

2. **Warnings filter to actionable transitions:**
   ```go
   // Only warn about fields we manage or used to manage
   if weManageField(path) || weUsedToManageField(path) {
       emitOwnershipTransitionWarning(...)  // User sees this
   } else {
       // External controllers fighting - ignore silently
   }
   ```

**Result:**
- ✅ Full transition context preserved ("who we took it from")
- ✅ No unresolvable drift (filtered warnings only)
- ✅ Clean plan output (actionable signals only)

### Correction to Original Reasoning

**Original statement (line 52):**
> "Terraform's rule: State must only track what you control. Tracking external ownership violates this."

**This is incorrect.** Computed attributes regularly track external state:
- `aws_instance.arn` - AWS controls this
- `kubernetes_service.status.load_balancer.ingress` - K8s assigns this

**Correct reasoning:**
> "Computed attributes cannot separate comprehensive data collection from user-facing presentation. Private state enables tracking ALL ownership data (needed for transition detection) while emitting filtered, actionable warnings (needed for clean UX)."

### Post-Refactor Confirmation

After fixing the "last manager wins" bug with `map[string][]string` and achieving deterministic ordering, we confirmed that **private state was still the right choice** - not because of bugs (those are fixed), but because of the fundamental architectural principle.

The bugs (flaky tests, non-deterministic ordering) created urgency during initial decision-making, but analysis afterward confirmed the decision was architecturally sound. Private state is the only approach that guarantees **zero unresolvable drift** while preserving comprehensive ownership tracking for transition detection.

**Alternative considered:** Filter the computed attribute to only include fields we manage or used to manage. This would require storing historical ownership in private state anyway (to detect "used to manage"), resulting in maintaining both private state AND a computed attribute - more complexity than private state alone.

**Trade-offs accepted:** Loss of `terraform state show` observability and non-standard warning UX are acceptable costs for upholding the principle that state drift must always be resolvable.

## Related Documentation

- `docs/investigation/ssa-shared-ownership-evidence.md` - Complete evidence of shared ownership behavior
- ADR-005 - Field Ownership Strategy (to be updated)
- ADR-009 - User-Controlled Drift Exemption (uses field_ownership)
- ADR-011 - Concise Diff Format (shows field_ownership examples)
- ADR-021 - Ownership Transition Messaging (implements filtered warning system)
