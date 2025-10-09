# ADR-013: YAML Body Sensitivity Approach for Bootstrap UX

## Status
**Rejected** - Abandoned on 2025-10-09

See `docs/bootstrap-yaml-fallback-abandonment.md` for complete analysis of why this approach failed.

## Summary

This ADR proposed making `yaml_body` sensitive and using YAML fallback to populate `managed_state_projection` when dry-run cannot work (during bootstrap scenarios where the cluster doesn't exist yet).

**The approach was implemented, tested, and ultimately abandoned** due to fundamental incompatibility with Terraform's plan consistency requirements.

## Context

### The Original Problem

When creating Kubernetes clusters and resources in a single Terraform apply:
- During PLAN: Cluster doesn't exist yet, dry-run cannot work
- User has no way to review what will be created if we set projection to Unknown
- Desired solution: Make projection the single source of truth by hiding yaml_body

### Proposed Solution

1. **Make yaml_body sensitive** - hide it from plan output
2. **YAML fallback for projection** - when dry-run fails, set projection to parsed YAML
3. **User reviews projection** - single source of truth for plan review

### Expected Benefits

- Clean UX: Single field to review (projection)
- Bootstrap friendly: Users can see what will be created even when cluster doesn't exist
- Consistent experience: Same review workflow for all scenarios

## Why It Failed

### The Inconsistent Plan Error

When CRD and Custom Resource are created in the same apply:

**During PLAN:**
1. CRD doesn't exist yet
2. Dry-run fails (no API endpoint for custom resource)
3. YAML fallback sets projection to parsed YAML: `{"spec":{"setting":"value"}}`

**During APPLY:**
4. CRD gets created
5. Custom resource CREATE succeeds
6. Projection recalculated from cluster
7. K8s CRD schema strips `spec.setting` (not in schema)
8. Projection is now `{}` (no spec field)
9. **Terraform error: "inconsistent plan"** - projection changed from plan to apply

### Root Cause

**YAML fallback shows what you WROTE, not what Kubernetes will DO.**

Kubernetes applies:
- CRD schemas (strips fields not in schema)
- Admission controllers (modifies resources)
- Defaulting (adds missing fields)
- Validation (rejects invalid values)

YAML fallback cannot predict any of this. It's just parsed YAML.

### Why Private State Flags Didn't Help

We tried using private state to preserve the YAML fallback projection during apply (don't recalculate). But:

1. Private state only passes data within same operation
2. ModifyPlan is called multiple times (expand phase, plan phase)
3. Flag set in first call wasn't available in later calls
4. Even if it worked, we'd be saving inaccurate projection to state

## The Key Realization

**If `yaml_body` is visible, Unknown projection is perfectly acceptable.**

User can review `yaml_body` in plan output to see what will be created. They don't NEED projection during plan if the cluster doesn't exist yet.

## Decision

**Rejected this approach entirely.**

Instead:
1. Keep `yaml_body` visible (NOT sensitive)
2. Set projection to Unknown when dry-run can't work
3. Enhanced error detection to catch CRD-not-found errors
4. Removed all YAML fallback logic

## What We Learned

### 1. Terraform's UX Contract is Strict

Users must be able to review what will be created during plan. You can't hide the only source of truth and replace it with Unknown.

### 2. Predicting Kubernetes is Impossible Without Dry-Run

YAML fallback seemed reasonable but can't account for:
- CRD validation/defaulting
- Webhook mutations
- Field stripping
- API server transformations

### 3. "Known After Apply" is OK Sometimes

It's better to honestly say "we don't know yet" than to show a guess that might be wrong.

### 4. Dual Visibility Isn't That Bad

Showing both `yaml_body` and `managed_state_projection` provides value:
- `yaml_body` - what you configured
- `managed_state_projection` - what k8sconnect actually manages (for drift detection)

Users can use git diff for config changes and Terraform plan for cluster changes.

## Implementation History

### What Was Implemented

**Files Modified:**
- `plan_modifier.go`: Added `setProjectionToYamlFallback()` function
- `plan_modifier.go`: Logic to set projection from parsed YAML when dry-run failed
- `errors.go`: Enhanced CRD error detection with `isCRDNotFoundError()`
- Private state flags for preserving fallback projection

**Tests:**
- `TestAccManifestResource_CRDAndCRTogether` - Proved the approach caused inconsistent plan errors

### What Was Removed

All YAML fallback logic was completely removed:

```go
// REMOVED: setProjectionToYamlFallback function
// REMOVED: Logic to set projection from parsed YAML
// REMOVED: Private state flags for preserving fallback projection
```

**Replaced with:**
```go
// NEW: Set projection to Unknown when CRD not found
r.setProjectionUnknown(ctx, plannedData, resp,
    "CRD not found during plan: projection will be calculated during apply")
```

### Current Behavior (After Abandonment)

**Scenario 1: CREATE with existing cluster**
```hcl
# Plan output
+ yaml_body = <<-EOT
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-config
    data:
      key: value
  EOT

+ managed_state_projection = (known after apply)  # We don't do dry-run for CREATE yet
```

**Scenario 2: CREATE during bootstrap (cluster doesn't exist)**
```hcl
# Plan output
+ yaml_body = <<-EOT
    apiVersion: v1
    kind: Namespace
    metadata:
      name: demo
  EOT

+ managed_state_projection = (known after apply)  # Cluster doesn't exist, can't dry-run
```

**Scenario 3: UPDATE (cluster exists)**
```hcl
# Plan output
~ yaml_body = <<-EOT
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-config
    data:
      key: newvalue  # Changed
  EOT

~ managed_state_projection = {
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-config
    data:
      key: newvalue  # Shows exact diff
  }
```

Both show the change. Projection shows what will actually be in cluster (with K8s defaults).

## Related Work

### Enhanced CRD Error Detection

As part of this work, we enhanced CRD error detection in `errors.go`:

```go
func (r *manifestResource) isCRDNotFoundError(err error) bool {
    if statusErr, ok := err.(*errors.StatusError); ok {
        msg := strings.ToLower(statusErr.ErrStatus.Message)
        return strings.Contains(msg, "no matches for kind") ||
            strings.Contains(msg, "could not find the requested resource")
    }
    // Also check wrapped errors
    errMsg := strings.ToLower(err.Error())
    return strings.Contains(errMsg, "no matches for kind") ||
        strings.Contains(errMsg, "could not find the requested resource")
}
```

**Note:** We initially added `"failed to discover resources"` to this check, but removed it because:
- Too broad - caught both permanent (CRD doesn't exist) AND transient (API discovery delay) errors
- Caused empty GVR issues when prepareContext treated transient delays as "CRD doesn't exist"
- Let GetGVR's built-in retry logic handle transient delays instead

This enhancement remains and is useful for detecting genuine CRD-not-found scenarios.

## Alternative Approaches Considered

### Could We Make It Work?

Several attempts were made to salvage the approach:

1. **Private state to skip recalculation** - Failed due to ModifyPlan being called multiple times
2. **Smart detection of CRD scenarios** - Still couldn't predict what K8s would do
3. **Accepting inconsistent plans** - Violates Terraform's fundamental requirements

**Conclusion:** Not fixable. The fundamental issue is trying to predict Kubernetes behavior without dry-run.

### What About Smart CREATE Projection?

Future enhancement (not part of this ADR): We could add smart projection for CREATE operations when cluster EXISTS:

```go
if isCreate && clusterExists && allValuesKnown {
    // Do dry-run to show accurate projection
    // Helps users see K8s defaults before apply
}
```

This would be an enhancement, not related to the yaml_body sensitivity approach.

## Conclusion

Sometimes the right solution is to NOT solve the problem.

We don't need to hide `yaml_body`. We don't need YAML fallback. We just need:
- Accurate projection when we can get it (dry-run)
- Honest Unknown when we can't
- Visible `yaml_body` for user review

Simpler. More correct. Less brittle.

## References

- **Abandonment Analysis**: `docs/bootstrap-yaml-fallback-abandonment.md` (comprehensive technical details)
- **Related ADRs**:
  - ADR-001: Managed State Projection
  - ADR-011: Concise Diff Format (references old approach, needs updating)
  - ADR-012: Terraform Fundamental Contract
- **Original Planning Document**: This ADR replaces `docs/bootstrap-changes-implementation-plan.md`
- **Tests**: `TestAccManifestResource_CRDAndCRTogether` proved the inconsistency issue
