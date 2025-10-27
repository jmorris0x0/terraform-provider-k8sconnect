# ADR-020: Field Ownership Display Strategy

**Status:** Accepted
**Date:** 2025-10-26
**Decision Date:** 2025-10-26
**Related ADRs:** ADR-005 (Field Ownership Strategy)

## Context

### The Field Ownership Feature

ADR-005 established that we track field-level ownership from Kubernetes Server-Side Apply (SSA) managedFields and expose it to users via a `field_ownership` state attribute. This feature took hundreds of hours to build and provides critical visibility into which controller manages which fields.

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

**Requirement 1:** Show ownership transitions (key feature, hundreds of hours invested)
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
- Update private state with current ownership after each plan/apply

**Breaking Change:** Users referencing `field_ownership` in configs will need to remove those references. The ownership information will be visible only via plan warnings.

## Related Documentation

- `docs/investigation/ssa-shared-ownership-evidence.md` - Complete evidence of shared ownership behavior
- ADR-005 - Field Ownership Strategy (to be updated)
- ADR-009 - User-Controlled Drift Exemption (uses field_ownership)
- ADR-011 - Concise Diff Format (shows field_ownership examples)
