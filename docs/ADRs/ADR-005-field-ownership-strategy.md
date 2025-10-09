# ADR-005: Field Ownership Strategy for Drift PreventionDR

## Status
Accepted

## Context

### The Problem

When building a Terraform provider for Kubernetes that uses Server-Side Apply (SSA), we discovered a fundamental incompatibility between three core requirements:

1. **Array-level tracking** - Track entire arrays like `spec.ports` as a unit for clean, readable diffs
2. **No special-casing** - Work correctly for any CRD without hardcoding field names
3. **Accurate dry-run diffs** - Show exactly what will change without false positives

This manifested as a critical bug in `TestAccManifestResource_StatusDriftDetection` where LoadBalancer Services showed constant drift:
```
Error: Provider produced inconsistent result after apply
.managed_state_projection: was cty.StringVal("...nodePort:32769..."), 
but now cty.StringVal("...nodePort:32156...")
```

### Why These Requirements Are Incompatible

When you create a Service with `type: LoadBalancer`, you specify:
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
- **Dry-run** predicts one nodePort (32769) but actual apply gets a different one (32156)
- **No special-casing** means we can't filter out `nodePort` specifically

This same problem exists for ANY field that ANY controller might add:
- Service controllers add `nodePort`, `clusterIP`
- Admission webhooks add defaults
- Operators add computed fields
- Controllers add status-like fields in spec

### Development History

The issue emerged after 40+ hours implementing status field tracking with 50+ passing tests. We had progressed through:

**Phase 1**: Initial managed state projection for drift detection (worked for simple resources)
**Phase 2**: Strategic merge handling for arrays with merge keys (worked for named containers)
**Phase 3**: Array-level tracking for ports (no unique name field)
**Phase 4**: LoadBalancer testing revealed the fundamental incompatibility

## Decision

**We implement Server-Side Apply (SSA) field ownership tracking to project only the fields we actually manage.**

## Considered Alternatives

### Option 1: Field-Level Tracking
Track individual fields instead of whole arrays:
```go
spec.ports[0].port = 80
spec.ports[0].targetPort = 8080
// nodePort not tracked (wasn't in YAML)
```

**Pros:**
- ✅ Accurate - only tracks user fields
- ✅ No special cases needed

**Cons:**
- ❌ Ugly diffs - shows individual field changes instead of array changes
- ❌ Poor UX for array modifications

**Verdict:** Rejected - significantly degrades user experience

### Option 2: Special-Case Volatile Fields
Filter out known volatile fields:
```go
if field == "nodePort" && inAutoRange(value) {
    // skip this field
}
```

**Pros:**
- ✅ Clean diffs
- ✅ Solves known cases

**Cons:**
- ❌ Requires maintaining list of volatile fields forever
- ❌ Doesn't work for unknown CRDs
- ❌ Breaks our "no special cases" principle

**Verdict:** Rejected - unmaintainable and incomplete

### Option 3: Accept the Drift
Store what dry-run predicted without updating after apply:

**Pros:**
- ✅ Clean diffs
- ✅ Simple implementation

**Cons:**
- ❌ Shows false drift when Kubernetes changes values
- ❌ Normalization ("1Gi" → "1073741824") not captured
- ❌ Users see "changes" that don't exist

**Verdict:** Rejected - fundamentally broken UX

### Option 4: SSA Field Ownership Tracking (Chosen)
Only track fields owned by our fieldManager:
```go
if fieldOwner == "k8sconnect" {
    // track this field
}
```

**Pros:**
- ✅ Theoretically correct - solves the problem completely
- ✅ No special cases - works for all resources
- ✅ Kubernetes-native solution - using SSA as intended
- ✅ Clean diffs maintained
- ✅ Accurate - no false positives

**Cons:**
- ❌ Complex implementation (2-4 week project)
- ❌ FieldsV1 format is poorly documented
- ❌ Major architectural change
- ❌ More API calls required

**Verdict:** Accepted - only solution that satisfies all requirements

## Implementation Details

### How Field Ownership Works

Server-Side Apply tracks which fieldManager owns each field. When we apply with `fieldManager: "k8sconnect"`, Kubernetes records:

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

### Architecture Changes

1. **New extraction function**: `extractOwnedPaths()` that parses managedFields
2. **Projection modification**: Only project fields we own according to managedFields  
3. **Two-phase CREATE**: Apply then read back to get actual ownership
4. **Parallel implementation**: Build alongside existing code for safe migration

### Key Technical Challenges Solved

1. **Array Key Mapping**: Convert `k:{"port":80,"protocol":"TCP"}` to array index [0]
2. **Strategic Merge**: Different resources use different merge strategies (name vs port+protocol)
3. **Nested Arrays**: Handle containers[].ports[] correctly
4. **Performance**: Cache managedFields parsing where possible

### Proof of Concept Results

Testing confirmed the approach works:
```
=== Managed Fields ===
Fields owned by k8sconnect:
  spec.ports[0].port ✅
  spec.ports[0].targetPort ✅
  
Fields owned by system:
  spec.ports[0].nodePort ❌ (ignored in projection)

Result: No drift detected!
```

## Consequences

### Positive
- **Eliminates false drift** - NodePort and similar fields no longer cause drift
- **Universal solution** - Works for any CRD without special cases
- **Kubernetes-native** - Aligns with SSA best practices
- **Maintains clean diffs** - Array-level display preserved
- **Future-proof** - Handles any controller-added fields

### Negative
- **Implementation complexity** - Significant engineering effort required
- **Learning curve** - Team needs to understand SSA field management
- **Debugging complexity** - More moving parts to troubleshoot

### Neutral
- **Performance impact** - Additional API call on CREATE (negligible in practice)
- **Migration required** - Existing resources need projection recalculation

## Lessons Learned

1. **Kubernetes is not deterministic** - Controllers modify resources after creation
2. **SSA is the answer** - Field ownership is the correct abstraction
3. **No shortcuts** - The "simple" solutions all have fatal flaws
4. **Testing reveals truth** - LoadBalancer Services exposed the fundamental issue

## Migration Plan

1. Implement `projection_v2.go` in parallel with existing code
2. Add feature flag for gradual rollout
3. Run both implementations in parallel for validation
4. Cut over once all tests pass
5. Remove old implementation

## Success Criteria

- ✅ LoadBalancer Services work without nodePort drift
- ✅ All existing tests continue to pass
- ✅ No special-casing of field names
- ✅ Works for arbitrary CRDs

## References

- [Kubernetes Server-Side Apply Documentation](https://kubernetes.io/docs/reference/using-api/server-side-apply/)
- [Field Management Design](https://github.com/kubernetes/enhancements/tree/master/keps/sig-api-machinery/0555-server-side-apply)
- Test case: `TestAccManifestResource_StatusDriftDetection`
- Original implementation: 40+ hours, 50+ tests before discovering the issue

## Decision

After extensive analysis and proof-of-concept validation, we commit to implementing SSA field ownership tracking. While complex, it is the only solution that maintains our quality bar of being the "best in the world" Kubernetes Terraform provider.

The 4-week implementation cost is justified by permanently solving a fundamental problem that affects all Kubernetes resources with server-managed fields.

## Implementation Challenges

During implementation of field ownership tracking, several non-obvious bugs emerged that required fixes:

### 1. Object Reference Bug
**Issue**: Initially passed `currentObj.Object` (actual K8s state) to `extractOwnedPaths` instead of `obj.Object` (desired config).
**Impact**: Completely broke drift detection as we were analyzing the wrong object.
**Lesson**: Field ownership must be calculated from desired state, not current state.

### 2. Partial Merge Key Matching
**Issue**: Required all fields in a merge key to be present in user's YAML, but Kubernetes adds defaults.
**Example**: User specifies `port: 80` but K8s adds `protocol: TCP`, causing match failure.
**Solution**: Accept partial matches when user's fields are a subset of the merge key.
**Lesson**: Kubernetes will always add defaults; our matching must be flexible.

### 3. Always Force Conflicts - Projection Strategy
**Issue**: Provider always uses SSA with `force=true` to take ownership of conflicted fields. Plan must anticipate this to avoid "inconsistent result after apply" errors.
**Solution**: Modified `ModifyPlan` to project ALL fields from YAML since we always take ownership during apply.
**Lesson**: Plan-time projection must match apply-time behavior exactly. Since we always force ownership via SSA, we must project all fields in the plan.

### 4. Import State Initialization
**Issue**: Field ownership flag not set during import operations.
**Impact**: Imported resources behaved differently than created ones.
**Lesson**: All resource paths (create, import, read) must initialize the same defaults.

### Critical Insight
The most important discovery: **Projection diffs ARE visible in Terraform output**. The core field ownership mechanism works correctly - the bugs were in the implementation details around edge cases, not the fundamental approach.
