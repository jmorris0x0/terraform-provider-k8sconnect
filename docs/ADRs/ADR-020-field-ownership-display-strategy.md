# ADR-020: Managed Fields Display Strategy

**Status:** Implemented
**Date:** 2025-10-26
**Last Updated:** 2025-01-29
**Related ADRs:** ADR-005 (Managed Fields Strategy), ADR-021 (Ownership Transition Messaging)

## Context

ADR-005 established that we track field-level ownership from Kubernetes managedFields and expose it via a `managed_fields` computed attribute. This provides critical visibility:

- See ownership transitions in Terraform plan diffs (e.g., "kubectl" → "k8sconnect")
- Understand controller conflicts (HPA managing replicas, etc.)
- Use `ignore_fields` to avoid ownership fights
- Reference in `depends_on` for orchestration

### The Critical Question

When displaying managed_fields to users, which managers should we track?

**Option A:** Track ALL field managers (k8sconnect, kubectl, HPA, operators, etc.)
**Option B:** Track ONLY fields where k8sconnect is an owner

## Decision

**Track ownership for fields in `managed_state_projection` only.**

### Critical Architectural Constraint

**managed_fields is bounded by managed_state_projection**. Since managed_state_projection contains fields based on yaml_body (what we want to manage), managed_fields shows managers for those fields.

**This enables ownership transition visibility:**
```hcl
# Before kubectl scales deployment
managed_fields = {
  "spec.replicas" = "k8sconnect"
}

# After kubectl scale (drift)
managed_fields = {
  "spec.replicas" = "kube-controller-manager" -> "k8sconnect"  # Transition visible!
}
```

**What you WILL see:**
- ✅ Ownership transitions for fields in yaml_body (drift detection)
- ✅ Who currently owns each managed field (external controllers can take ownership)
- ✅ Who will own after apply (typically k8sconnect with force=true)

**What you will NOT see:**
- ❌ Fields added by external controllers that aren't in yaml_body
- ❌ Status fields (filtered regardless)

### Implementation Approach

**Internal Tracking (Phase 0 from ADR-021):**
```go
// Track ALL co-owners for each field
ownership := map[string][]string{
    "spec.replicas": []string{"k8sconnect", "horizontal-pod-autoscaler"},
}
```

**User Display (Deterministic Flattening):**
```go
// Flatten to single manager per field for schema
func FlattenManagedFields(ownership map[string][]string) map[string]string {
    result := make(map[string]string)
    for path, managers := range ownership {
        if len(managers) == 0 {
            continue
        }
        // Deterministic: show first manager (ordered by timestamp in managedFields)
        result[path] = managers[0]
    }
    return result
}
```

## The Rollback Story: Why ALL Managers Matter

### v0.2.0: Failed k8sconnect-Only Attempt

We attempted to implement "track only k8sconnect ownership" for better stability.

**Implementation:** Used `parseFieldsV1ToPathMap()` to extract only k8sconnect's managedFields entry, ignoring all other managers.

**Result:** 17 test failures across import and external controller scenarios.

**Root Cause Analysis:**

1. **Import scenarios broken:**
   ```
   1. Import kubectl-created resource
   2. k8sconnect not yet in managedFields (import hasn't written)
   3. managed_fields = {} (empty)
   4. After apply: managed_fields populated
   5. Test fails: "Provider produced inconsistent result after apply"
   ```

2. **External controller scenarios broken:**
   ```
   1. HPA manages spec.replicas exclusively
   2. k8sconnect not an owner yet
   3. managed_fields missing spec.replicas entry
   4. Cannot detect transition or apply force=true correctly
   ```

**Rollback:** Reverted to v0.1.7's `extractAllManagedFields()` + `FlattenManagedFields()` approach.

### v0.1.7: ALL-Managers Approach (Current Implementation)

**Implementation:** Parse ALL managedFields entries, track all co-owners internally, flatten for display.

**Why this is correct:**

1. **Import detection:** See existing ownership before we claim it
2. **Conflict detection:** Know which controller we're fighting (HPA, Flux, operator, etc.)
3. **Force=true logic:** Need to see external ownership to know when to override (ADR-019)
4. **Transition visibility:** Show "kubectl" → "k8sconnect" in diffs

**Stability achieved through:**
- Parse ALL managedFields entries consistently
- Track all co-owners in `map[string][]string`
- Flatten using first manager (timestamp-ordered, deterministic)
- Always use force=true during apply (ADR-019)

## Key Insights

### Why k8sconnect-Only Extraction Fails

The k8sconnect-only approach seems cleaner but breaks a fundamental requirement:

**You cannot correctly apply force=true if you don't know what you're forcing.**

With ALL-managers tracking:
```
1. Read current state from K8s
2. Parse managedFields: detect "HPA owns spec.replicas"
3. User config includes spec.replicas (not in ignore_fields)
4. Apply with force=true, knowing we're overriding HPA
5. Show transition: "horizontal-pod-autoscaler" → "k8sconnect"
```

With k8sconnect-only tracking:
```
1. Read current state from K8s
2. Parse only k8sconnect's entry: empty (we don't own it yet)
3. No knowledge of HPA ownership
4. Lose visibility and context
5. Cannot show meaningful transition
```

### Shared Ownership in SSA

When multiple managers apply the same value with force=true, Kubernetes creates **shared ownership** - both managers listed in managedFields. This is intentional SSA design for collaboration.

Our flattening handles this deterministically by showing the first co-owner (timestamp-ordered), while internally we track all co-owners for comprehensive conflict detection.

### Status Field Filtering

We filter status fields from display because:
- Status is server-managed, never user-controlled
- Clutters output with non-actionable information
- Users cannot meaningfully act on status field ownership

## Trade-offs

**Benefits:**
- ✅ Import scenarios work correctly
- ✅ External controller detection works
- ✅ Force=true logic has full context
- ✅ Transition visibility preserved ("kubectl" → "k8sconnect")
- ✅ Deterministic (all 17 test failures fixed)

**Limitations:**
- ❌ Can show ownership changes for fields we don't manage (rare edge case)
- ❌ External controller fights might appear as drift

**Mitigation:**
- ADR-021 implements filtered warning system to show only actionable ownership transitions
- `ignore_fields` allows users to explicitly defer ownership to external controllers

## Implementation: Computed Attribute

managed_fields is tracked as a computed attribute (visible in `terraform state show`). Internally, we track all co-owners per field using `map[string][]string` to properly handle SSA shared ownership scenarios where multiple managers co-own the same field.

**Scope:** Tracking is limited to fields in `managed_state_projection` - we do not track ownership for fields we don't manage.

**Note:** v0.2.0-v0.2.2 briefly moved managed_fields to private state. This was rolled back in v0.2.3 due to test failures.

## Related Documentation

- ADR-005: Managed Fields Strategy (force=true usage)
- ADR-021: Ownership Transition Messaging (centralized warning system)
