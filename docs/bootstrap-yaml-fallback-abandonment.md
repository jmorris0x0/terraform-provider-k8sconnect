# Bootstrap YAML Fallback: Abandonment and Lessons Learned

## TL;DR

**We tried to hide `yaml_body` and use YAML fallback for projection. It caused "inconsistent plan" errors. We abandoned it.**

**Current behavior (correct):**
- Both `yaml_body` and `managed_state_projection` are visible
- When dry-run can't work, projection is Unknown
- Users review `yaml_body` in plan output (it's not sensitive)

## The Original Goal

Make `managed_state_projection` the single source of truth by hiding `yaml_body` (marking it sensitive).

**Problem:** When dry-run can't work (CRD doesn't exist, cluster being created, etc.), projection would be Unknown, leaving users with nothing to review.

**Attempted Solution:** YAML fallback - set projection to parsed YAML when dry-run fails.

## Why YAML Fallback Failed

### The Inconsistent Plan Error

When CRD and Custom Resource are created in same apply:

**During PLAN:**
1. CRD doesn't exist yet
2. Dry-run fails (no API endpoint for custom resource)
3. YAML fallback sets projection to parsed YAML: `{"spec":{"setting":"value"}}`

**During APPLY:**
4. CRD gets created
5. Custom resource CREATE succeeds
6. Projection recalculated from cluster
7. K8s CRD schema strips `spec.setting` (not in schema)
8. Projection is now `{}`  (no spec field)
9. **Terraform error: "inconsistent plan"** - projection changed from plan to apply

### The Root Problem

**YAML fallback shows what you WROTE, not what Kubernetes will DO.**

Kubernetes applies:
- CRD schemas (strips fields not in schema)
- Admission controllers (modifies resources)
- Defaulting (adds missing fields)
- Validation (rejects invalid values)

YAML fallback can't predict any of this. It's just parsed YAML.

### Why Private State Flags Didn't Help

We tried using private state to preserve the YAML fallback projection during apply (don't recalculate). But:

1. Private state only passes data within same operation
2. ModifyPlan is called multiple times (expand phase, plan phase)
3. Flag set in first call wasn't available in later calls
4. Even if it worked, we'd be saving inaccurate projection to state

## The Key Realization

**If `yaml_body` is visible, Unknown projection is perfectly acceptable.**

User can review `yaml_body` in plan output to see what will be created. They don't NEED projection during plan if the cluster doesn't exist yet.

## The Decision: Abandon yaml_body Sensitivity

**What we did:**
1. Removed YAML fallback logic entirely
2. Kept `yaml_body` visible (NOT sensitive)
3. Set projection to Unknown when dry-run can't work
4. Enhanced error detection to catch CRD-not-found errors

**Why this is better:**
- No inconsistent plan errors
- Simpler code (less state management)
- Users see yaml_body for review during plan
- Projection shows accurate diff after cluster exists (on next plan/apply cycle)

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

## Current Behavior (After Abandonment)

### Scenario 1: CREATE with existing cluster

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

User reviews yaml_body to see what will be created.

### Scenario 2: CREATE during bootstrap (cluster doesn't exist)

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

User reviews yaml_body. After apply, projection is calculated from actual cluster state.

### Scenario 3: UPDATE (cluster exists)

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

## What Changed in Code

### Removed from plan_modifier.go

```go
// REMOVED: setProjectionToYamlFallback function
// REMOVED: Logic to set projection from parsed YAML
// REMOVED: Private state flags for preserving fallback projection
```

### Changed in plan_modifier.go

```go
// OLD: Set projection to yaml fallback when CRD not found
r.setProjectionToYamlFallback(ctx, plannedData, desiredObj, resp,
    "CRD not found during plan: using YAML fallback for projection")

// NEW: Set projection to Unknown when CRD not found
r.setProjectionUnknown(ctx, plannedData, resp,
    "CRD not found during plan: projection will be calculated during apply")
```

### Enhanced in errors.go

```go
// Added more robust CRD error detection
// (but removed "failed to discover resources" - too broad, caught transient delays)
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

## Future Consideration: Smart Projection for CREATE

We could add smart projection for CREATE operations when cluster EXISTS:

```go
if isCreate && clusterExists && allValuesKnown {
    // Do dry-run to show accurate projection
    // Helps users see K8s defaults before apply
}
```

But this is:
1. An enhancement, not a fix
2. Requires careful handling of "cluster exists" detection
3. Not needed for correctness (Unknown is fine)

See `bootstrap-changes-implementation-plan.md` for the original detailed plan (now obsolete).

## Related Issues

- CRD race condition (CRD + CR in same apply) - Solved by letting GetGVR retry logic handle API discovery delays
- Field ownership after CREATE - Separate issue, needs addressing
- Inconsistent plan errors - Root cause was YAML fallback

## Conclusion

**Sometimes the right solution is to NOT solve the problem.**

We don't need to hide `yaml_body`. We don't need YAML fallback. We just need:
- Accurate projection when we can get it (dry-run)
- Honest Unknown when we can't
- Visible `yaml_body` for user review

Simpler. More correct. Less brittle.
