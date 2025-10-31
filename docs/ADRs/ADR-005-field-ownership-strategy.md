# ADR-005: Managed Fields Strategy for Drift Prevention

## Status
Accepted

**Scope:** This ADR covers how we use SSA field ownership to determine which fields to include in `managed_state_projection` (our filtered view of K8s state for drift detection). For how we display field ownership information to users, see ADR-020.

**Related ADRs:**
- ADR-020: Managed Fields Display Strategy (managed_fields computed attribute)
- ADR-021: Ownership Transition Messaging (centralized warning system)

## Context

When building a Terraform provider for Kubernetes that uses Server-Side Apply (SSA), we discovered a fundamental incompatibility between three core requirements:

1. **Array-level tracking** - Track entire arrays like `spec.ports` as a unit for clean, readable diffs
2. **No special-casing** - Work correctly for any CRD without hardcoding field names
3. **Accurate dry-run diffs** - Show exactly what will change without false positives

### The Problem: Server-Added Fields

When you create a LoadBalancer Service, you specify:
```yaml
spec:
  ports:
  - port: 80
    targetPort: 8080
```

But Kubernetes automatically adds:
```yaml
spec:
  ports:
  - port: 80
    targetPort: 8080
    nodePort: 32769  # Added by Kubernetes, randomly assigned
```

The conflict:
- **Array-level tracking** captures the entire `spec.ports` array, including the server-added `nodePort`
- **Dry-run** predicts one random nodePort but actual apply gets a different random nodePort
- **No special-casing** means we can't filter out `nodePort` specifically

This same problem exists for ANY field that ANY controller might add:
- Service controllers add `nodePort`, `clusterIP`
- Admission webhooks add defaults
- Operators add computed fields
- Controllers add status-like fields in spec

## Decision

**We implement Server-Side Apply (SSA) field ownership tracking to project only the fields we actually manage.**

## Considered Alternatives

**Option 1: Field-Level Tracking** - Track individual fields instead of whole arrays.
- Rejected: Ugly diffs (shows `spec.ports[0].port` changes instead of array-level changes), poor UX

**Option 2: Special-Case Volatile Fields** - Maintain hardcoded list of fields to ignore (nodePort, clusterIP, etc).
- Rejected: Requires maintaining list forever, doesn't work for unknown CRDs, breaks "no special cases" principle

**Option 3: Accept the Drift** - Store dry-run prediction without updating after apply.
- Rejected: Shows false drift, misses normalization ("1Gi" → "1073741824"), fundamentally broken UX

**Option 4: SSA Managed Fields Tracking (Chosen)** - Only track fields owned by our fieldManager.
- Accepted: Only solution that maintains clean diffs, works universally, and eliminates false positives

## How It Works

Server-Side Apply tracks which fieldManager owns each field. When we apply with `fieldManager: "k8sconnect"`, Kubernetes records ownership in `managedFields`:

```json
{
  "f:spec": {
    "f:ports": {
      "k:{\"port\":80,\"protocol\":\"TCP\"}": {
        "f:port": {},        // We own this
        "f:targetPort": {}   // We own this
        // Note: no f:nodePort - system owns it
      }
    }
  }
}
```

We parse this FieldsV1 structure via `extractOwnedPaths()` and only project fields we own. Server-added fields (nodePort, clusterIP, webhook defaults) are ignored in projection, eliminating false drift.

## Shared Ownership Discovery

Kubernetes `managedFields` is an **array** where each field manager has a separate entry with its own `fieldsV1` blob. Multiple managers can list the **same field** in their entries - this is "shared ownership".

**When it happens**: SSA creates shared ownership when multiple managers apply identical values to the same field. The `force=true` flag only takes exclusive ownership when values **differ**. When values match, all appliers become co-owners.

**Parsing implication**: Naively iterating the array and overwriting ownership creates a "last-one-wins" bug. Correct parsing must accumulate **all** managers for each field path. We track shared ownership using `map[string][]string` (field path → list of all co-owners), enabling accurate detection of whether we're an owner, exclusive owner, or one of multiple co-owners.

**Key lesson**: SSA ownership is more nuanced than "apply with force=true → you own it". Identical values create collaboration, different values create takeover.

## Critical Implementation Detail: Always Force Ownership

The provider **always uses `force=true`** during Server-Side Apply. This means we take ownership of conflicted fields from other controllers.

This design choice has a critical implication for projection: During plan phase, we must project ALL fields from user's YAML (not just unowned fields), because during apply we will force ownership of everything.

If we only projected unowned fields during plan but then forced ownership during apply, Terraform would error with "Provider produced inconsistent result after apply" - the plan wouldn't match what actually happened.

## Key Lessons

**Kubernetes is not deterministic** - Controllers modify resources after creation. We cannot predict what fields will be added without querying the server.

**SSA field ownership is the answer** - It's the only universal solution that works for all CRDs without special-casing field names.

**Partial merge key matching required** - User specifies `port: 80`, Kubernetes adds `protocol: TCP`. Our matching must handle partial keys when user's fields are a subset of the merge key.

**Plan must match apply** - Since we always use `force=true` during apply, we must project all user fields during plan. Otherwise Terraform errors with "inconsistent result after apply".
