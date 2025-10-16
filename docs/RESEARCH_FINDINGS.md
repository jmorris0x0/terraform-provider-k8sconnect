# Research Findings: Wait Timeout and Resource Tainting

## Executive Summary

**The hashicorp/terraform-provider-kubernetes has the EXACT same problem we do, and they consider resource tainting on wait timeout to be EXPECTED BEHAVIOR.**

More critically: **Providers cannot control tainting behavior on create errors - it's a fundamental Terraform protocol limitation.**

## Key Finding #1: Kubernetes Provider Has Same Problem

### Issue #1455: Timeouts Combined with wait_for Cause State Conflicts
- **URL**: https://github.com/hashicorp/terraform-provider-kubernetes/issues/1455
- **Problem**: When wait timeout occurs, resource created in K8s but not tracked in Terraform state
- **Fix**: PR #2163 - Save state BEFORE adding error diagnostic
- **Result**: Resource saved to state BUT still tainted

### PR #2163: The "Solution"
- **URL**: https://github.com/hashicorp/terraform-provider-kubernetes/pull/2163
- **Changes**: Modified wait flow to save state even after timeout
- **Error Handling**: Uses `DiagnosticSeverityError` (not warning)
- **Outcome**:
  ```
  "the resource has been created in the state file while also
   tainting the resource requiring the user to make the necessary
   changes in order for their to not be another timeout error"
  ```
- **Interpretation**: HashiCorp considers tainting EXPECTED and CORRECT behavior

### Issue #1790: Service Tainted on Every Run
- **URL**: https://github.com/hashicorp/terraform-provider-kubernetes/issues/1790
- **Problem**: kubernetes_service marked as tainted after timeout, recreated on every apply
- **Resolution**: Closed as "NOT_PLANNED" after 30+ days inactive
- **Status**: **NEVER SOLVED**

## Key Finding #2: This is a Terraform Protocol Limitation

### Issue #330 (terraform-plugin-sdk): Ability to Prevent Taints on Create Errors
- **URL**: https://github.com/hashicorp/terraform-plugin-sdk/issues/330
- **Problem**: Multi-step resource creation - later step fails, entire resource tainted
- **Example**: "If the project gets created successfully but one of the other steps fails, the resource is tainted, which means retrying the apply after fixing whatever caused it to fail will destroy the project and recreate it"
- **Requested Feature**: Mechanism for providers to signal "don't taint this resource"
- **Label**: `upstream-protocol`
- **Status**: **OPEN - No solution**
- **Critical Quote**: "Providers cannot currently control tainting behavior on create errors"

### What This Means

**Tainting on create errors is AUTOMATIC Terraform behavior:**
1. Provider creates resource successfully
2. Provider saves state with `resp.State.Set()`
3. Provider adds error diagnostic with `resp.Diagnostics.AddError()`
4. Terraform's framework/protocol automatically marks resource as tainted
5. Provider has NO control over this

**The `upstream-protocol` label means:**
- This is a Terraform CORE protocol issue
- Cannot be fixed in terraform-plugin-framework
- Cannot be fixed in terraform-plugin-sdk
- Requires changes to Terraform itself
- Architectural limitation

## Comparison: Their Implementation vs Ours

### Kubernetes Provider (PR #2163)
```go
// 1. Execute wait (may timeout)
err := waitForCompletion(...)

// 2. Save resource to state (BEFORE error)
r, err := rs.Get(ctx, rname, metav1.GetOptions{})
// State saved here

// 3. Add error diagnostic
if reason, ok := err.(WaiterError); ok {
    resp.Diagnostics = append(resp.Diagnostics,
        &tfprotov5.Diagnostic{
            Severity: tfprotov5.DiagnosticSeverityError,
            Summary:  "Operation timed out",
            Detail:   reason.Error(),
        })
}

// Result: State saved, resource tainted
```

### Our Implementation
```go
// 9. SAVE STATE IMMEDIATELY after successful creation
diags = resp.State.Set(ctx, rc.Data)
resp.Diagnostics.Append(diags...)

// 10. Execute wait conditions (now AFTER initial state save)
waited, waitErr := r.handleWaitExecution(ctx, rc, "created")

// 12. Save state again with status update
diags = resp.State.Set(ctx, rc.Data)
resp.Diagnostics.Append(diags...)

// 13. Add wait error to diagnostics (AFTER state is saved)
if waitErr != nil {
    r.addWaitError(resp, "created", waitErr)
}

// Result: State saved, resource tainted
```

**They are IDENTICAL approaches with IDENTICAL outcomes.**

## What HashiCorp's Approach Accepts

The official Kubernetes provider's "solution" to this problem is:

1. ✅ Save state so resource is tracked
2. ✅ Add ERROR diagnostic so apply fails
3. ✅ Resource gets tainted (accepted as expected)
4. ✅ User must "make necessary changes" to fix timeout issue
5. ✅ Next apply = destroy + recreate (accepted as expected)

**They explicitly designed it this way.** From the PR notes:
- "tainting the resource requiring the user to make the necessary changes"
- This is intentional, not a bug they're trying to fix

## HashiCorp's Current Position (2024)

**The feature is STILL ACTIVE and ACTIVELY PROMOTED:**

1. **Official Blog Post**: HashiCorp published "Wait Conditions in the Kubernetes Provider for HashiCorp Terraform"
   - Promotes wait_for as a feature
   - No mention of tainting issues
   - Encourages usage

2. **Actively Documented (2024)**:
   - `wait_for` exists on `kubernetes_manifest` resource
   - Generic wait conditions supported
   - `wait_for_rollout` on Deployments and StatefulSets (default: true)
   - `wait_for_load_balancer` on Ingress resources

3. **No Plans to Remove**:
   - Despite Issue #1790 closed as "NOT_PLANNED"
   - Despite Issue #330 (prevent tainting) still open with `upstream-protocol` label
   - Feature continues to be developed and promoted

**What This Means:**

HashiCorp has had YEARS of user feedback about resources being tainted on timeout. They know about the recreation issue. They chose to:
- Keep the feature
- Accept the tainting behavior
- Promote it publicly
- Document it as working correctly

**Their implicit position:**
- Tainting on wait timeout is CORRECT behavior
- Recreation on retry is ACCEPTABLE
- Users should fix timeout issues between retries
- If resource recreates, that's okay

## Does HashiCorp Restrict wait_for to "Safe" Resources?

**NO. They allow wait_for on ALL resources without restriction.**

### What wait_for Supports in kubernetes_manifest

The `kubernetes_manifest` resource has a **generic `wait_for` attribute** that works on:
- ✅ PersistentVolumeClaims (data loss risk on recreation)
- ✅ StatefulSets (data loss risk on recreation)
- ✅ Jobs (work loss risk on recreation)
- ✅ Services with LoadBalancer (IP change risk on recreation)
- ✅ Any Custom Resource
- ✅ **Literally ANY Kubernetes object**

### Documentation and Warnings

**No warnings found** in HashiCorp documentation about:
- Data loss when PVCs are recreated after wait timeout
- Work loss when Jobs are recreated after wait timeout
- IP/identity changes when Services are recreated after wait timeout

**No restrictions** on which resource types can use `wait_for`.

**No guidance** on avoiding wait_for for stateful resources.

### What This Means

HashiCorp is **fully aware** that:
1. `wait_for` can timeout on ANY resource type
2. Timeouts cause tainting
3. Tainting causes recreation
4. Recreation of PVCs/StatefulSets/etc. causes data loss

**And they shipped it anyway** without warnings or restrictions.

Either:
- They don't consider data loss a serious concern (unlikely)
- They expect users to understand Terraform tainting behavior (possible)
- They believe users will fix timeout issues before retry (optimistic)
- They haven't thought through the implications (concerning)

## Why This Matters for Our Requirements

### Our Hard Requirements
1. ✅ Apply MUST fail (exit code 1) when wait times out
2. ❌ Resource MUST NOT be tainted/recreated on retry

### HashiCorp's Kubernetes Provider
1. ✅ Apply DOES fail (exit code 1) when wait times out
2. ❌ Resource DOES get tainted/recreated on retry

**Their requirement #2 doesn't exist. They accept tainting.**

## Possible Interpretations

### Option 1: HashiCorp is Wrong
- They don't understand the problem
- They haven't thought through the retry UX
- They're accepting bad behavior

**Likelihood**: Low - they've had multiple issues filed about this

### Option 2: HashiCorp Has Different Use Cases
- Their users fix the timeout issue (adjust timeout, fix K8s issue) before retry
- Retry after fixing = resource probably works anyway
- They don't care about PVCs/StatefulSets data loss

**Likelihood**: Possible - different priorities

### Option 3: This is Fundamentally Impossible
- Terraform's architecture ties "error in diagnostics" to "resource tainted"
- There is NO mechanism to fail apply without tainting
- AddWarning is the only option that doesn't taint
- **Our requirement #1 and #2 cannot both be satisfied**

**Likelihood**: High - supported by issue #330 being labeled `upstream-protocol`

## What Needs to Happen

If option #3 is correct (fundamentally impossible), we have two paths:

### Path A: Accept AddWarning
**REJECTED - Violates DAG blocking requirement**

- Apply succeeds with warning (exit code 0)
- Resource not tainted, retry works ✅
- **Downstream resources created when they shouldn't be** ❌
- **Dependency chain violated** ❌

**Example failure:**
```hcl
resource "k8sconnect_manifest" "pvc" {
  wait_for = { field_value = { "status.phase" = "Bound" } }
}

resource "k8sconnect_manifest" "pod" {
  depends_on = [k8sconnect_manifest.pvc]
  # Pod tries to mount PVC
}
```

**What happens:**
1. PVC created but not bound (wait timeout)
2. AddWarning → apply continues
3. Pod created anyway
4. Pod crashes because PVC not bound
5. **Infrastructure is broken** ❌

**This is unacceptable.** Wait conditions exist to enforce dependencies. If we don't block the DAG, the feature is worse than useless - it creates broken infrastructure.

### Path B: Accept Tainting (follow HashiCorp's pattern)
**The only viable option if we keep the feature**

- Apply fails with error (exit code 1) ✅
- Blocks DAG correctly ✅
- Resource tainted, retry recreates ❌
- Follow HashiCorp's pattern ✅
- Accept that retry = recreation ❌

**What this means for users:**
- PVCs destroyed and recreated (data loss risk)
- StatefulSets destroyed and recreated (data loss risk)
- Jobs killed and restarted (work loss)
- LoadBalancer IPs change (integration breakage)

**HashiCorp accepts this.** We would too.

### Path C: Remove wait_for Feature
**Only option if Path B is unacceptable**

- Cannot satisfy both requirements
- Feature creates either broken infrastructure (Path A) or bad retry UX (Path B)
- Months of work lost

## Next Steps

1. **Verify this is truly impossible** by:
   - Checking terraform-plugin-framework source code for tainting logic
   - Searching for ANY provider that solves this
   - Checking if newer framework versions have changed this

2. **Make decision** based on findings:
   - If impossible: Choose Path A, B, or C
   - If possible: Implement the solution

3. **If choosing Path A or B**, document tradeoffs clearly for users

## Related Issues

- hashicorp/terraform-provider-kubernetes#1455 - Timeouts cause state conflicts
- hashicorp/terraform-provider-kubernetes#1790 - Service tainted on every run (NOT_PLANNED)
- hashicorp/terraform-provider-kubernetes/pull/2163 - The "fix" that accepts tainting
- hashicorp/terraform-plugin-sdk#330 - Request to prevent taints on create errors (upstream-protocol)
