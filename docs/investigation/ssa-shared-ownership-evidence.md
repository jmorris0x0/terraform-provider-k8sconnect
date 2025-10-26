# SSA Shared Ownership Investigation - Evidence Document

**Date**: 2025-10-26
**Issue**: Flaky test `TestAccObjectResource_IgnoreFieldsJSONPathPredicate` fails ~16% of the time
**Error**: "Provider produced inconsistent result after apply" - predicted field ownership doesn't match actual

---

## Executive Summary

Investigation into flaky test revealed that Kubernetes Server-Side Apply (SSA) creates **shared ownership** when multiple field managers apply the same field with identical values. This behavior was previously unknown and contradicts our ADR-019 assumption that `force=true` guarantees exclusive ownership takeover.

---

## Test Scenario

The test exercises the following workflow:

1. **Initial state**: Terraform creates a Deployment with two env vars:
   - `MANAGED_VAR: "managed-value"` (NOT ignored)
   - `EXTERNAL_VAR: "external-value"` (`.value` ignored via JSONPath predicate)

2. **External modification** (15 iterations): kubectl-patch force-applies both env vars:
   ```bash
   ForceApplyDeploymentEnvVarSSA(..., "MANAGED_VAR", "kubectl-managed-N", "kubectl-patch")
   ForceApplyDeploymentEnvVarSSA(..., "EXTERNAL_VAR", "kubectl-external-N", "kubectl-patch")
   ```

3. **Terraform apply**: Should reclaim MANAGED_VAR, preserve EXTERNAL_VAR

4. **Expected behavior**:
   - MANAGED_VAR owned by k8sconnect (reclaimed with force=true)
   - EXTERNAL_VAR.value owned by kubectl-patch (ignored by k8sconnect)
   - EXTERNAL_VAR.name owned by k8sconnect (NOT ignored, reclaimed with force=true)

---

## Evidence: Test Failure Rates

### Overnight Test Results (50 runs)

```
Total runs: 50
Passed: 42
Failed: 8
Failure rate: 16%
```

**Failed runs**: 1, 4, 7, 8, 30, 31, 35, 43, 46

### Pattern Observation

Failures are non-deterministic. Same code, same test, sometimes passes, sometimes fails.

---

## Evidence: Shared Ownership in managedFields

### Raw managedFields Data

From `test-flaky-logs/run-1.log` (successful run), after kubectl-patch modifies env vars:

**kubectl-patch fieldsV1** (timestamp: 11:02:32.781):
```json
{
  "f:spec": {
    "f:template": {
      "f:spec": {
        "f:containers": {
          "k:{\"name\":\"app\"}": {
            ".": {},
            "f:env": {
              "k:{\"name\":\"EXTERNAL_VAR\"}": {
                ".": {},
                "f:name": {},
                "f:value": {}
              }
            },
            "f:name": {}
          }
        }
      }
    }
  }
}
```

**k8sconnect fieldsV1** (timestamp: 11:02:32.781):
```json
{
  "f:metadata": {
    "f:annotations": {
      "f:k8sconnect.terraform.io/created-at": {},
      "f:k8sconnect.terraform.io/terraform-id": {}
    }
  },
  "f:spec": {
    "f:replicas": {},
    "f:selector": {},
    "f:template": {
      "f:metadata": {
        "f:labels": {"f:app": {}}
      },
      "f:spec": {
        "f:containers": {
          "k:{\"name\":\"app\"}": {
            ".": {},
            "f:env": {
              "k:{\"name\":\"EXTERNAL_VAR\"}": {
                ".": {},
                "f:name": {}
              },
              "k:{\"name\":\"MANAGED_VAR\"}": {
                ".": {},
                "f:name": {}
              }
            },
            "f:image": {},
            "f:name": {}
          }
        }
      }
    }
  }
}
```

### Key Observation

**Both managers list identical field paths**:
- `spec.template.spec.containers[name=app].name`
- `spec.template.spec.containers[name=app].env[name=EXTERNAL_VAR].name`

This is **shared ownership** - both managers have the exact same fields in their fieldsV1 entries.

---

## Evidence: What We Apply vs What kubectl-patch Applies

### kubectl-patch SSA Apply

From `internal/k8sconnect/common/test/ssa_client.go:333-358`:

```go
patch := map[string]interface{}{
    "apiVersion": "apps/v1",
    "kind":       "Deployment",
    "metadata": map[string]interface{}{
        "name":      name,
        "namespace": namespace,
    },
    "spec": map[string]interface{}{
        "template": map[string]interface{}{
            "spec": map[string]interface{}{
                "containers": []map[string]interface{}{
                    {
                        "name": containerName,  // ← Sends container name
                        "env": []map[string]interface{}{
                            {
                                "name":  envVarName,   // ← Sends env var name
                                "value": value,        // ← Sends env var value
                            },
                        },
                    },
                },
            },
        },
    },
}
```

**Fields sent**: container name, env[].name, env[].value

### k8sconnect SSA Apply

From `test-flaky-logs/run-8.log` (03:20:14.016):

```
paths=["spec.template.metadata.labels.app",
       "spec.template.spec.containers[name=app].env[name=MANAGED_VAR].name",
       "spec.template.spec.containers[name=app].env[name=MANAGED_VAR].value",
       "spec.template.spec.containers[name=app].env[name=EXTERNAL_VAR].name",  ← We send this
       "spec.template.spec.containers[name=app].image",
       "spec.template.spec.containers[name=app].name",  ← We send this
       "spec.replicas",
       "spec.selector.matchLabels.app",
       "apiVersion", "kind", "metadata.name", "metadata.namespace",
       "metadata.annotations.k8sconnect.terraform.io/created-at",
       "metadata.annotations.k8sconnect.terraform.io/terraform-id"]

ignore_fields=["spec.template.spec.containers[?(@.name=='app')].env[?(@.name=='EXTERNAL_VAR')].value"]
```

**Fields sent**: container name, env[EXTERNAL_VAR].name (but NOT .value), env[MANAGED_VAR].name + .value

### Overlap Analysis

Both managers send:
- `containers[name=app].name` with value `"app"`
- `env[name=EXTERNAL_VAR].name` with value `"EXTERNAL_VAR"`

**These values are identical** between kubectl-patch and k8sconnect.

---

## Evidence: Ownership Parsing Bug

### Current Implementation

From `internal/k8sconnect/common/fieldmanagement/ownership.go:23-49`:

```go
func ParseFieldsV1ToPathMap(managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) map[string]FieldOwnership {
    result := make(map[string]FieldOwnership)

    // Process each field manager's fields
    for _, mf := range managedFields {
        if mf.FieldsV1 == nil {
            continue
        }

        var fields map[string]interface{}
        if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
            continue
        }

        // Extract paths owned by this manager
        paths := extractPathsFromFieldsV1(fields, "", userJSON)

        // Record ownership for each path
        for _, path := range paths {
            // Skip internal k8sconnect annotations
            if strings.HasPrefix(path, "metadata.annotations.k8sconnect.terraform.io/") {
                continue
            }
            result[path] = FieldOwnership{  // ← OVERWRITES PREVIOUS OWNER
                Manager: mf.Manager,
                Version: mf.APIVersion,
            }
        }
    }

    return result
}
```

### Behavior

When processing managedFields array:
1. First manager's fields are added to `result` map
2. Second manager's fields **overwrite** entries in `result` for shared fields
3. Only the **last manager** in the array is reported for shared fields

### Order Dependency

Kubernetes sorts managedFields by `(timestamp ASC, manager name ASC)`.

From `test-flaky-logs/run-8.log`:

**Success case** (03:20:15.194):
```
index=0: kubectl-patch (time="2025-10-26 03:20:13")
index=1: k8sconnect   (time="2025-10-26 03:20:14") ← Last, reported as owner
index=2: k3s          (time="2025-10-26 03:20:14")

actual_ownership_map="...env[1].name:k8sconnect..."  ✅ Matches prediction
```

**Failure case** (03:20:15.994):
```
index=0: k8sconnect   (time="2025-10-26 03:20:14")
index=1: kubectl-patch (time="2025-10-26 03:20:15") ← Last, reported as owner
index=2: k3s          (time="2025-10-26 03:20:15")

actual_ownership_map="...env[1].name:kubectl-patch..."  ❌ Doesn't match prediction
```

---

## Evidence: ADR-019 Assumption

### What ADR-019 Claims

From ADR-019 (documented in plan_modifier.go:291-294):

> "Kubernetes dry-run doesn't predict force=true ownership takeover, so we must explicitly recognize that fields we apply with force=true WILL be owned by k8sconnect after the actual apply"

Implementation (plan_modifier.go:315-327):

```go
for path, currentOwner := range ownershipMap {
    // Only override if we're actually sending this field
    if fieldsWeAreSending[path] && currentOwner != "k8sconnect" {
        tflog.Debug(ctx, "ADR-019: Overriding ownership prediction", map[string]interface{}{
            "path":            path,
            "dry_run_owner":   currentOwner,
            "predicted_owner": "k8sconnect",
            "reason":          "force=true guarantees ownership takeover",
        })
        ownershipMap[path] = "k8sconnect"  // Override to k8sconnect
        overrideCount++
        overriddenPaths = append(overriddenPaths, path)
    }
}
```

**Assumption**: `force=true` guarantees **exclusive** ownership - we will be the ONLY owner.

### What Actually Happens

From Kubernetes SSA documentation (WebSearch results):

> "When two or more appliers set a field to the same value, they **share ownership** of that field. **No conflict occurs when the values are identical.**"

> "The force parameter is **not required** when field values are identical. Force is only needed when there's an actual conflict - when you're trying to change a field to a **different** value."

**Reality**: When applying with `force=true` but with identical values, we become a **co-owner**, not the exclusive owner.

---

## Evidence: SSA Apply Behavior

### Test Case: Identical Values

From logs (03:20:14.016):
- kubectl-patch sends: `containers[name=app].name = "app"`
- k8sconnect sends: `containers[name=app].name = "app"`
- Result: **Both managers listed in managedFields**

### Test Case: Different Values

Expected behavior (not yet tested):
- kubectl-patch sends: `env[MANAGED_VAR].value = "kubectl-managed-4"`
- k8sconnect sends: `env[MANAGED_VAR].value = "managed-value"`
- Result: **k8sconnect should take exclusive ownership** (force=true with different value)

**Evidence gap**: We don't have logs showing exclusive ownership takeover when values differ.

---

## Hypotheses Supported by Evidence

### Hypothesis A: Shared Ownership is Standard SSA Behavior

**Supporting evidence**:
- Both managers have identical field paths in their fieldsV1 (see Raw managedFields Data)
- This matches Kubernetes documentation description of shared ownership
- Test sometimes passes (when k8sconnect appears last) and sometimes fails (when kubectl-patch appears last)

**Contradicting evidence**: None

**Confidence**: Very High

### Hypothesis B: Our Ownership Parsing Has a Bug

**Supporting evidence**:
- Code explicitly overwrites previous owner (ownership.go:44)
- Test failure correlates with managedFields order
- When kubectl-patch appears last in array → test fails
- When k8sconnect appears last in array → test passes

**Contradicting evidence**: None

**Confidence**: Very High

### Hypothesis C: ADR-019's Assumption is Incorrect

**Supporting evidence**:
- We apply with force=true and identical values → become co-owner, not exclusive owner
- Kubernetes docs say force is unnecessary when values are identical
- Dry-run shows kubectl-patch ownership → ADR-019 overrides to k8sconnect → but reality is both managers co-own

**Contradicting evidence**: None

**Additional consideration**: ADR-019 might be correct for the case where values DIFFER. We don't have evidence of that yet.

**Confidence**: High (for identical values), Unknown (for different values)

### Hypothesis D: We Shouldn't Send Fields Adjacent to Ignored Fields

**Supporting evidence**:
- We ignore `env[EXTERNAL_VAR].value` but send `env[EXTERNAL_VAR].name`
- kubectl-patch sends the entire env entry (both .name and .value)
- Result: Shared ownership of .name even though .value is ignored

**Contradicting evidence**:
- `.name` is NOT in the ignore_fields JSONPath - we legitimately need to send it to identify the array element
- Kubernetes uses `.name` as the merge key for env arrays
- Not sending `.name` might break SSA array element matching

**Confidence**: Low - needs more investigation

### Hypothesis E: managedFields Timestamp Race Condition

**Supporting evidence**:
- kubectl-patch calls SSA twice (MANAGED_VAR, then EXTERNAL_VAR)
- Sometimes kubectl-patch's second call gets a timestamp AFTER k8sconnect's apply
- This changes the order in managedFields array
- Order affects which owner we report (due to parsing bug)

**Contradicting evidence**: None

**Confidence**: High

---

## Data Gaps and Questions

### Gap 1: Do We See Exclusive Ownership Takeover When Values Differ?

**What we know**: Shared ownership happens when values are identical

**What we don't know**: Does force=true take exclusive ownership when values differ?

**How to test**:
- Find a log sequence where kubectl-patch sets `MANAGED_VAR="kubectl-managed-4"`
- Then k8sconnect applies `MANAGED_VAR="managed-value"` with force=true
- Check if k8sconnect becomes exclusive owner or co-owner

**Current logging**: Should be sufficient - we log fieldsV1 content for all managers

### Gap 2: What Should field_ownership Report for Co-Owners?

**What we know**: Current implementation reports last manager in array (buggy)

**What we don't know**:
- What is the correct behavior from a Terraform perspective?
- Should we report all co-owners? Just k8sconnect if we're one of them?
- How does this affect drift detection?

**How to answer**: Design decision, not data collection

### Gap 3: How Common is Shared Ownership in Real-World Use?

**What we know**: Happens in our test with ignore_fields

**What we don't know**:
- Does this happen in other scenarios?
- Is this a common pattern with K8s controllers?
- What fields are typically shared?

**How to test**: Run additional scenarios, examine real-world deployments

### Gap 4: Timestamp Ordering Precision

**What we know**: managedFields are sorted by timestamp

**What we don't know**:
- What's the timestamp precision? Milliseconds? Microseconds?
- Are there guarantees about ordering when timestamps are identical?
- Could we see non-determinism even with same timestamps?

**How to test**: Examine more managedFields entries, look for timestamp collisions

### Gap 5: Does ignore_fields Work Correctly with Shared Ownership?

**What we know**:
- We ignore `.value`, send `.name`
- Both become co-owned
- Test expects k8sconnect to exclusively own `.name`

**What we don't know**:
- Is this the desired behavior?
- Should ignore_fields affect what we send?
- How should projection work with co-owned fields?

**How to answer**: Design decision based on user expectations

---

## Logging Assessment

### Current Logging Coverage

✅ **We have**:
- Raw managedFields entries (manager, operation, timestamp)
- FieldsV1 content for each manager (complete JSON)
- Fields sent in SSA Apply
- ignore_fields configuration
- Predicted vs actual ownership maps
- ADR-019 override decisions

❌ **We don't have**:
- Actual field VALUES sent in SSA apply (only paths)
- Field values from kubectl-patch SSA apply
- Explicit marker for which managedFields entry the parsing loop is processing
- Indication of whether ownership is exclusive vs shared

### Sufficiency Assessment

**For understanding current bug**: ✅ Sufficient
- We can see shared ownership in fieldsV1
- We can see parsing bug (last-one-wins)
- We can correlate failure with managedFields order

**For validating hypothesis about different values**: ⚠️ Partially sufficient
- We can identify cases where values differ (MANAGED_VAR)
- We can see if exclusive ownership happens
- But we can't see the actual values in logs (only know they differ from test setup)

**For fixing the bug**: ✅ Sufficient
- We understand the root cause
- We know what needs to change (ownership parsing)
- We have enough data to verify a fix

### Recommended Additional Logging

**If we want to be extra thorough**:

1. **Add value logging to SSA apply** (low priority):
   ```go
   tflog.Debug(ctx, "Field value being sent", map[string]interface{}{
       "path": path,
       "value": actualValue,
   })
   ```

2. **Add shared ownership detection** (medium priority):
   ```go
   if len(managersForField) > 1 {
       tflog.Warn(ctx, "Shared ownership detected", map[string]interface{}{
           "path": path,
           "managers": managersForField,
       })
   }
   ```

3. **Add iteration marker in parsing loop** (low priority):
   ```go
   tflog.Trace(ctx, "Processing manager's fields", map[string]interface{}{
       "manager": mf.Manager,
       "field_count": len(paths),
   })
   ```

---

## Test Duration Assessment

### Current Testing

- Overnight run: 50 iterations, 8 failures (16%)
- Recent run: 3 iterations, 0 failures (0%)

### Statistical Confidence

**16% failure rate** means:
- In 3 runs: ~39% chance of seeing at least 1 failure
- In 10 runs: ~83% chance of seeing at least 1 failure
- In 50 runs: ~99.97% chance of seeing at least 1 failure

We saw 0/3 failures in recent run, which is consistent with ~39% probability.

### Sufficiency

**For understanding the bug**: ✅ Sufficient
- We already understand the root cause from overnight data
- More runs won't change our understanding

**For validating a fix**: ⚠️ Will need more testing
- After implementing a fix, we should run 50+ iterations
- Need to confirm 0% failure rate (not just lucky with 3 runs)

**Current recommendation**: No need for more data collection NOW, but will need extensive testing AFTER implementing fix.

---

## Next Steps Recommendation

### Immediate Actions (Ready to Proceed)

1. **Fix the ownership parsing bug**
   - Change `ParseFieldsV1ToPathMap` to handle shared ownership
   - Decide on reporting strategy (all co-owners? just k8sconnect?)

2. **Decide on ADR-019 behavior**
   - Update assumption to account for shared ownership
   - Clarify when we expect exclusive vs shared ownership

3. **Update tests**
   - Adjust expectations to account for shared ownership
   - Or adjust what we send to avoid shared ownership

### Deferred Actions (Need Design Decisions First)

4. **Evaluate ignore_fields semantics**
   - Should we send `.name` when `.value` is ignored?
   - How does this interact with SSA merge keys?

5. **Document shared ownership behavior**
   - Update ADRs with new understanding
   - Add examples to documentation

### Testing Actions (After Fix)

6. **Validate fix with extended testing**
   - Run 50-100 iterations of flaky test
   - Confirm 0% failure rate
   - Test with different scenarios (not just ignore_fields)

---

## Conclusion

**We have sufficient evidence to proceed with fixing the identified bugs.** The data conclusively shows:

1. ✅ Shared ownership exists and is standard SSA behavior
2. ✅ Our ownership parsing has a last-one-wins bug
3. ✅ ADR-019's assumption is incomplete (doesn't account for shared ownership)

**We do NOT need more logging or longer test runs** to understand the current issue.

**We WILL need design decisions** on:
- How to report co-ownership in `field_ownership` state attribute
- Whether ADR-019 prediction should distinguish shared vs exclusive
- Whether ignore_fields should affect what fields we send

**We WILL need extensive testing** after implementing fixes to validate they work correctly.

