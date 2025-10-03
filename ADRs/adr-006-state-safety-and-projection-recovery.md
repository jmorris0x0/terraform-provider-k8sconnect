# ADR-006: State Safety and Projection Recovery

## Status
Accepted

## Date
2025-10-02

## Summary

The provider implements graceful recovery from projection calculation failures using framework Private State to track incomplete operations. When projection calculation fails (commonly due to network issues), the resource is saved to Terraform state with an internal recovery flag, allowing subsequent applies to automatically complete the operation without manual cleanup.

## Context

### The Problem

Terraform providers must handle partial failures gracefully. When creating a Kubernetes resource:
1. Resource is successfully applied to K8s cluster (exists, has ownership annotation)
2. Projection calculation fails (network timeout, API unavailable)
3. Provider must decide: save state or fail?

**Critical constraint**: Network issues during `terraform apply` are **common**, not edge cases:
- WiFi disconnections
- Laptop closures mid-apply
- API server restarts
- Network partitions in CI/CD

### What Users Expect

```bash
$ terraform apply
Error: Projection calculation failed (network timeout)

$ terraform apply  # Just retry
✅ Apply complete! Resources created.
```

**NOT:**
```bash
$ terraform apply
Error: Projection calculation failed

$ terraform apply
Error: Resource exists with different ID, manual cleanup required

$ kubectl delete deployment my-app  # Manual surgery
$ terraform apply
✅ Works
```

### Failed Approaches Considered

#### Approach 1: Make Projection Failures Fatal (No State Save)

```go
if err := updateProjection(); err != nil {
    return Error  // Don't save state
}
```

**Problems:**
- ✅ Stops CI/CD pipeline (good for preventing cascading failures)
- ❌ Second apply generates new random ID
- ❌ Ownership check fails: "resource managed by different k8sconnect resource"
- ❌ Requires manual cleanup every time network blips

**Verdict**: Unacceptable UX for common network issues.

#### Approach 2: Save with Warning (Broken Drift Detection)

```go
if err := updateProjection(); err != nil {
    Warning("Projection failed")
    projection = "{}"  // Empty
    SaveState()  // Continue
}
```

**Problems:**
- ❌ Exit code 0 (CI/CD continues to dependent resources)
- ❌ Drift detection broken until next successful refresh
- ❌ Silent corruption spreads through dependency graph
- ❌ Violates enterprise CI/CD requirement: fail fast on infrastructure issues

**Verdict**: Dangerous for enterprise deployments.

#### Approach 3: Schema Field for Partial State

```go
type Model struct {
    PartiallyCreated types.Bool `tfsdk:"partially_created"`
}
```

**Problems:**
- ❌ Clutters user-visible schema
- ❌ Shows in `terraform show` output
- ❌ Requires documentation
- ❌ Breaking change to remove later
- ✅ Would work functionally

**Verdict**: Functional but pollutes API surface.

## Decision

**Use Terraform Plugin Framework Private State to track incomplete projections.**

### Implementation

```go
// When projection fails during Create()
if err := r.updateProjection(rc); err != nil {
    // Save state with recovery flag in Private
    resp.Private.SetKey(ctx, "pending_projection", []byte("true"))
    rc.Data.ManagedStateProjection = types.StringValue("{}")
    resp.State.Set(ctx, rc.Data)

    // Return error (stops CI/CD)
    resp.Diagnostics.AddError("Projection Failed", ...)
    return
}

// Update() checks for pending projection
data, _ := req.Private.GetKey(ctx, "pending_projection")
if data != nil && string(data) == "true" {
    tflog.Info(ctx, "Completing pending projection from previous apply")

    if err := r.updateProjection(rc); err != nil {
        // Still failing - keep flag
        resp.Private.SetKey(ctx, "pending_projection", []byte("true"))
        return Error
    }

    // Success - clear flag
    resp.Private.SetKey(ctx, "pending_projection", nil)
    tflog.Info(ctx, "Successfully completed projection")
}
```

### Recovery Paths

**Path 1: Automatic (Network Recovers)**
```
Apply 1: Create → Projection fails (network) → State saved + Error
Apply 2: Update → Retries projection → Success → Flag cleared
Apply 3+: Normal operation
```

**Path 2: Persistent Failure**
```
Apply 1: Create → Projection fails → State saved + Error
Apply 2: Update → Still fails → Error
Apply N: Manual intervention (delete resource, fix network, etc.)
```

**Path 3: Refresh Recovery**
```
Apply 1: Create → Projection fails → State saved + Error
Refresh: Opportunistically retries projection → May succeed
```

## Rationale

### Why Private State?

| Criterion | Private State | Schema Field | No State (Fatal) |
|-----------|---------------|--------------|------------------|
| **Hidden from users** | ✅ | ❌ | N/A |
| **No schema pollution** | ✅ | ❌ | ✅ |
| **Persisted across applies** | ✅ | ✅ | ❌ |
| **Auto-recovery** | ✅ | ✅ | ❌ |
| **Stops CI/CD** | ✅ | ✅ | ✅ |
| **No manual cleanup** | ✅ | ✅ | ❌ |
| **Clean user experience** | ✅ | ⚠️ | ❌ |

### Why Save State + Error?

**Enterprise CI/CD Requirements:**
1. ✅ Infrastructure failures must stop the pipeline
2. ✅ Dependent resources must not be created
3. ✅ Operators must be alerted to problems
4. ✅ Recovery should be automatic when possible

**Combining state save with error achieves:**
- **First apply fails** → Exit code 1 → CI/CD stops
- **Second apply retries** → Same resource ID → Auto-recovery
- **No orphaned resources** → Tracked in state from first apply
- **No manual cleanup** → Just retry

### When Projection Actually Fails

**Projection calculation can fail in three ways:**

1. **Network timeout reading for projection** (line 219 in updateProjection)
   - Already retried 5 times with exponential backoff (2 min total)
   - If still fails → severe network issue
   - **Recovery strategy**: Save + error, retry later

2. **Logic error in projectFields()** (line 240)
   - Shouldn't happen if extractOwnedPaths produces valid paths
   - Would indicate provider bug
   - **Recovery strategy**: Error immediately (shouldn't retry)

3. **JSON marshaling failure** (line 246)
   - Only fails on unmarshalable types (channels, functions)
   - Impossible with K8s objects (all JSON-compatible)
   - **Recovery strategy**: Error immediately (shouldn't happen)

**In practice**: 99% of failures are network timeouts.

### Wait Timeout vs. Projection Timeout

**Different failure modes, different handling:**

| Failure | Resource Exists? | State Saved? | Recovery |
|---------|-----------------|--------------|----------|
| **Apply timeout** | Maybe | ❌ | SSA idempotent retry |
| **Projection timeout** | ✅ | ✅ (with flag) | Auto-retry projection |
| **Wait timeout** | ✅ | ✅ | Next update continues |

**Wait timeout is already handled** by saving state before wait execution (Issue #1 fix).

**Projection timeout needs special handling** because it affects drift detection.

## Consequences

### Positive

1. **Graceful network failure recovery**
   - Common laptop-closure scenario: apply → error → apply → success
   - No manual cleanup required

2. **Enterprise CI/CD compatible**
   - Failures stop pipeline (exit code 1)
   - Prevent cascading failures to dependent resources
   - Clear error messages for operators

3. **Clean API surface**
   - No user-visible schema fields
   - No documentation burden
   - Can remove flag in future without breaking changes

4. **Automatic recovery**
   - Update operation checks flag
   - Refresh operation can opportunistically fix
   - Self-healing on next interaction

5. **Same resource ID**
   - No "different ID" errors
   - No ownership conflicts
   - Natural retry flow

### Negative

1. **Increased complexity**
   - ~50-75 additional lines of code
   - Three new code paths (Create/Update/Read handling flag)
   - More test scenarios

2. **Hidden state**
   - Users can't inspect flag directly
   - Debugging requires TF_LOG=INFO
   - Must trust framework Private state

3. **Drift detection gap**
   - Between failure and recovery, drift detection unavailable
   - Projection is empty "{}" during this window
   - Acceptable because state shows error occurred

### Testing Strategy

**Unit tests with mock client:**
```go
TestCreate_ProjectionFails_SavesPrivateFlag()
TestUpdate_CompletesProjection_ClearsFlag()
TestUpdate_ProjectionStillFails_KeepsFlag()
TestRead_OpportunisticallyCompletesProjection()
TestNormalUpdate_IgnoresFlagWhenProjectionSucceeds()
```

**Mock client scenarios:**
- Apply succeeds, Get fails (network drop after create)
- Get succeeds on retry (network recovery)
- Get consistently fails (persistent issue)

**Acceptance tests:**
- Real cluster with network proxy dropping packets (optional)
- Manual verification of retry flow

## Implementation Checklist

- [ ] Add Private state handling to Create()
- [ ] Add Private state check to Update()
- [ ] Add opportunistic recovery to Read()
- [ ] Write unit tests with mock client
- [ ] Update error messages with recovery guidance
- [ ] Add TF_LOG examples to documentation
- [ ] Test manual cleanup path (persistent failure)

## References

- [Terraform Plugin Framework - Private State](https://developer.hashicorp.com/terraform/plugin/framework/resources/private-state)
- ADR-001: Managed State Projection (foundational architecture)
- Issue #1: Wait timeout state saving
- Issue #2: Projection update failures

## Related Issues

### State Safety Audit Findings

**Critical Issues Addressed:**
1. ✅ Wait operation state not saved on timeout (separate fix)
2. ✅ Projection update failures (this ADR)
3. ✅ Status field volatility (already implemented - pruning exists)

**Key Insight from Audit:**
- Framework only persists state when `resp.State.Set()` is called
- No automatic rollback - providers must handle explicitly
- Combining state save with error return is valid pattern

### Enterprise Requirements

From real-world usage patterns:
- Network disconnections during apply: **Common** (daily occurrence in large teams)
- Laptop closures mid-apply: **Common** (remote work, battery issues)
- API server maintenance: **Common** (cluster upgrades, scaling events)

**Requirement**: Provider must gracefully handle network issues without requiring state surgery.

**This ADR fulfills that requirement.**
