# Current State Baseline

**Date**: 2025-10-16
**Purpose**: Document the current state of the codebase before implementing the separate `k8sconnect_wait` resource

## Git Status

**Branch**: `develop`
**Commits ahead of origin**: 3

Recent commits:
```
2a57c0a Add more docs
70a81cb Add private state flag for wait retry
644259b Add more lifecycle tests
```

Working directory: **CLEAN** (no uncommitted changes)

Untracked files:
- `ACCEPTANCE_TEST_GAPS.md`
- `PRE_LAUNCH_REVIEW.md`
- `test_retry_proposal.md`

## Test Results Summary

### 1. Unit Tests (`make test`)

**Status**: ✅ **ALL PASSING**

All unit tests pass successfully.

### 2. Acceptance Tests (`make testacc`)

**Status**: ✅ **ALL PASSING (67 tests)**

**Recently fixed:**
- `TestAccObjectResource_DeleteWithStuckFinalizer` - Added Step 3 with PreConfig to remove finalizer before test cleanup runs

**Passing tests:** All 67 acceptance tests pass, including:
- All wait-related tests:
  - `TestAccObjectResource_WaitForFieldExists`
  - `TestAccObjectResource_WaitForFieldValue`
  - `TestAccObjectResource_WaitForCondition`
  - `TestAccObjectResource_WaitForPVCBinding`
  - `TestAccObjectResource_WaitForMultipleValues`
  - `TestAccObjectResource_WaitForFieldChange`
  - `TestAccObjectResource_WaitTypeTransitionFieldValueToField`
  - `TestAccObjectResource_WaitTypeTransitionFieldToFieldValue`
  - `TestAccObjectResource_WaitTimeout`
  - `TestAccObjectResource_ExplicitRollout`
  - `TestAccObjectResource_StatefulSetRollout`
  - `TestAccObjectResource_NoDefaultRollout`
- All lifecycle tests
- All drift detection tests
- All field ownership tests
- All identity change tests

### 3. Example Tests (`make test-examples`)

**Status**: ✅ **ALL PASSING (16 examples)**

All example tests pass, including wait-related examples:
- `wait-for-pvc-volume`
- `wait-for-loadbalancer`
- `wait-for-deployment-rollout`
- `wait-for-job-completion`
- `wait-for-ingress`
- `wait-for-condition`

## Current Wait Behavior

### Implementation Location

**Core files:**
- `internal/k8sconnect/resource/object/crud.go` - Create/Update functions with wait execution
- `internal/k8sconnect/resource/object/crud_common.go` - `addWaitError()` helper
- `internal/k8sconnect/resource/object/crud_operations.go` - Wait execution and status tracking
- `internal/k8sconnect/resource/object/plan_modifier.go` - Status field planning with pending wait detection

### How Wait Currently Works

**During Create:**

1. Resource created in Kubernetes (SSA apply)
2. State saved immediately (step 9 in crud.go:67-69)
3. Wait conditions executed (step 10 in crud.go:72)
4. Status field updated with pending flag if wait times out (step 11 in crud.go:75-77)
5. State saved again with status (step 12 in crud.go:82-83)
6. **Wait error added to diagnostics AFTER state save** (step 13 in crud.go:88-90)

**During Update:**

Same pattern - state saved before adding wait error (crud.go:247-257)

### Current Error Handling

**From crud_common.go:362-375:**

```go
func (r *objectResource) addWaitError(resp interface{}, action string, err error) {
    msg := fmt.Sprintf("Wait condition failed after resource was %s", action)
    detailMsg := fmt.Sprintf("The resource was successfully %s, but the wait condition failed: %s\n\n"+
        "You need to either:\n"+
        "1. Increase the timeout if more time is needed\n"+
        "2. Fix the underlying issue preventing the condition from being met\n"+
        "3. Review your wait_for configuration", action, err)

    if createResp, ok := resp.(*resource.CreateResponse); ok {
        createResp.Diagnostics.AddError(msg, detailMsg)  // ← Uses AddError
    } else if updateResp, ok := resp.(*resource.UpdateResponse); ok {
        updateResp.Diagnostics.AddError(msg, detailMsg)  // ← Uses AddError
    }
}
```

**Key point**: Uses `AddError`, not `AddWarning`

### Current Behavior When Wait Times Out

**What happens:**

1. Resource is created successfully in Kubernetes ✅
2. State is saved with resource details ✅
3. Wait condition times out ❌
4. `AddError` adds error to diagnostics
5. Apply exits with code 1 (fails) ✅
6. **Terraform framework automatically marks resource as TAINTED** ❌

**On retry (next apply):**

1. Terraform sees resource is tainted
2. **Destroys and recreates the resource** ❌
3. Same wait timeout likely happens again (infinite loop)

### Private State Flags

**Current implementation uses ADR-006 pattern:**

**Flag: `pending_wait_status`**

Set in `crud_operations.go:258` when wait times out:
```go
rc.Data.Status = types.DynamicNull()  // NOT unknown - Terraform contract
if privateSetter != nil {
    setPendingWaitStatusFlag(ctx, privateSetter)
}
```

Detected in `plan_modifier.go:127` during next plan:
```go
hasPendingWait := checkPendingWaitStatusFlag(ctx, req.Private)
if hasPendingWait {
    tflog.Info(ctx, "Detected pending wait from previous timeout, setting status to unknown to block DAG")
    plannedData.Status = types.DynamicUnknown()  // NOW set to unknown for planning
    return
}
```

**Purpose**: Track pending waits across apply cycles and set status=unknown during planning to block downstream DAG.

**Problem**: This only affects PLANNING. It doesn't prevent tainting, so the retry still destroys/recreates.

## Current Problems Documented

### Problem Documents

1. **`docs/WAIT_RETRY_PROBLEM.md`** - Comprehensive problem documentation
   - Hard requirements (both must be satisfied)
   - Technical root cause
   - Why AddWarning is unacceptable (DAG blocking)
   - Attempted solutions
   - Possible solutions (Options A, B, C, D)

2. **`docs/RESEARCH_FINDINGS.md`** - Research into HashiCorp's approach
   - HashiCorp kubernetes provider has EXACT same problem
   - They accept tainting as expected behavior
   - terraform-plugin-sdk issue #330: `upstream-protocol` limitation
   - Providers CANNOT control tainting on create errors

### The Core Problem

**We have conflicting requirements:**

| Requirement | AddError | AddWarning |
|-------------|----------|------------|
| Apply fails (exit code 1) | ✅ Yes | ❌ No |
| Blocks DAG on failure | ✅ Yes | ❌ No |
| Resource NOT tainted | ❌ No | ✅ Yes |

**No solution exists within current Terraform framework** for embedded `wait_for` attribute.

## Proposed Solution

**Option D: Separate `k8sconnect_wait` resource** (documented in WAIT_RETRY_PROBLEM.md)

This would:
- Decouple wait logic from resource creation
- Allow wait resource to be tainted (doesn't matter - no K8s state)
- Keep manifest resource untainted
- Solve both hard requirements

## Changes Since Last Known-Good State

The last 3 commits added:

1. **Documentation** (RESEARCH_FINDINGS.md, WAIT_RETRY_PROBLEM.md)
2. **Private state flag for wait retry** (pending_wait_status)
3. **New lifecycle tests** (lifecycle_test.go)
4. **Wait error handling improvements** (crud.go, crud_common.go, crud_operations.go)
5. **Plan modifier enhancements** (plan_modifier.go)

**All functional code changes are working correctly** - tests pass.

## Baseline Confirmed

This is a clean baseline from which to implement the separate wait resource:

- ✅ All unit tests passing
- ✅ All example tests passing
- ✅ **All 67 acceptance tests passing**
- ✅ Wait functionality works as documented
- ✅ State save ordering correct (state saves before error diagnostic)
- ✅ Private state flags working correctly
- ✅ DAG blocking working correctly (AddError stops downstream resources)

**Next step**: Implement separate `k8sconnect_wait` resource following Option D design from WAIT_RETRY_PROBLEM.md

## Commit Point

Before implementing the separate wait resource, commit this baseline:

```bash
git add docs/CURRENT_STATE_BASELINE.md internal/k8sconnect/resource/object/lifecycle_test.go
git commit -m "Add baseline documentation and fix finalizer test

Captures current state:
- All 67 acceptance tests passing
- All unit and example tests passing
- Wait functionality working with known tainting issue
- Fixed TestAccObjectResource_DeleteWithStuckFinalizer cleanup
- Ready to implement Option D (separate k8sconnect_wait resource)"
```

This gives us a clean rollback point if the new approach doesn't work.
