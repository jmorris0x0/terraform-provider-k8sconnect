# Wait Condition Retry Problem

## CRITICAL SEVERITY

**If we cannot solve this problem, the wait_for feature must be removed entirely.**

Months of work will be lost if we cannot find a solution.

## Hard Requirements (BOTH must be satisfied)

1. **Apply MUST fail (exit code 1) when wait times out**
   - Wait timeout is a FAILURE
   - User must know something is wrong
   - User must take action and retry
   - This is not about CI/CD - this is about USER EXPERIENCE

2. **Resource MUST NOT be tainted/recreated on retry**
   - Recreating the resource doesn't fix wait conditions
   - Same conditions = same timeout = infinite loop
   - Data loss risk (PVCs, StatefulSets, etc.)
   - Retry must happen IN-PLACE on existing resource

**These are NOT trade-offs. We need BOTH or the feature is unusable.**

## Summary

When a wait condition times out, Terraform automatically marks the resource as "tainted" even though the resource was successfully created/updated. This causes the next `terraform apply` to **destroy and recreate** the resource instead of retrying the wait condition in-place. This creates an infinite loop where the same timeout condition keeps occurring.

## The Problem

### User Scenario

1. User creates a PVC with `wait_for.field_value = { "status.phase" = "Bound" }`
2. Apply runs - PVC is created successfully in Kubernetes
3. Wait condition times out after 30s (storage provisioner is slow)
4. Terraform marks resource as TAINTED even though state was saved
5. User runs `terraform apply` again to retry
6. **Terraform destroys and recreates the PVC** instead of just waiting again
7. The new PVC has the same timeout issue (storage provisioner still slow)
8. User is stuck in a loop

### Why This is Unacceptable

- **Destroying a resource doesn't fix wait conditions** - the same underlying issue (slow controller, missing dependency, etc.) will cause the new resource to also timeout
- **Data loss risk** - destroying and recreating PVCs, StatefulSets, etc. can cause data loss
- **Wasted time** - creating new resources is slower than just waiting for existing ones
- **User confusion** - "Why is Terraform destroying my successfully created resource?"

## Technical Root Cause

### Terraform Framework Behavior

From testing, we discovered:

```go
// In crud.go Create function:
resp.State.Set(ctx, rc.Data)  // State saved successfully
resp.Diagnostics.AddError(...)  // Error added AFTER state save
// Function returns normally (no early return)
```

Result in state file:
```json
{
  "instances": [{
    "status": "tainted",  // ← Terraform framework adds this automatically
    "attributes": { ... }
  }]
}
```

**The Terraform plugin framework automatically taints resources when:**
- Create/Update function completes
- `resp.Diagnostics` contains errors
- Regardless of whether state was saved or not

This is Terraform's safety mechanism: "If there were errors, this resource might be in a bad state, mark it for replacement."

### Why We Can't Avoid Tainting with AddError

We tried removing the `return` statement after `AddError`:

```go
// BEFORE (tainted)
if waitErr != nil {
    r.addWaitError(resp, "created", waitErr)
    return  // ← Early exit
}

// AFTER (still tainted!)
if waitErr != nil {
    r.addWaitError(resp, "created", waitErr)
    // No return - function completes normally
}
```

**Result:** Still tainted! The framework checks `resp.Diagnostics.HasError()` at the end of the function, not at the return point.

### AddWarning vs AddError Trade-off

| Approach | Apply Exit Code | Resource Tainted? | Retry Behavior | Acceptable? |
|----------|----------------|-------------------|----------------|-------------|
| `AddError` | ✅ Non-zero (fails) | ❌ Yes (WRONG) | ❌ Destroys + recreates | **NO** |
| `AddWarning` | ❌ Zero (succeeds - WRONG) | ✅ No | ✅ Retries in-place | **NO** |

**NEITHER OPTION IS ACCEPTABLE:**
- `AddError`: Satisfies requirement #1 (fails apply) but violates requirement #2 (taints resource)
- `AddWarning`: Satisfies requirement #2 (no taint) but violates requirement #1 (doesn't fail apply)

**WE NEED A THIRD OPTION** that fails the apply WITHOUT tainting the resource.

## CRITICAL: The Dependency Graph Problem

### Why AddWarning is ALSO Unacceptable

**Original concern:** AddWarning doesn't fail the apply (exit code 0), so users might not notice.

**ACTUAL CRITICAL PROBLEM:** AddWarning doesn't block the dependency graph.

```hcl
resource "k8sconnect_manifest" "pvc" {
  wait_for = {
    field_value = { "status.phase" = "Bound" }
  }
}

resource "k8sconnect_manifest" "pod" {
  depends_on = [k8sconnect_manifest.pvc]
  # Pod spec references the PVC
}
```

**First Apply with AddWarning:**
1. PVC created ✅
2. Wait for "Bound" times out ⚠️
3. AddWarning (not AddError)
4. Apply continues because no error
5. **Pod gets created anyway** ❌
6. Pod tries to mount unbound PVC
7. Pod fails to start (CrashLoopBackOff)

**The dependency is violated.** Pod should never have been created until PVC was bound.

**First Apply with AddError:**
1. PVC created ✅
2. Wait for "Bound" times out ❌
3. AddError → apply STOPS
4. Pod never attempted (correct!)
5. Terraform halts DAG execution (correct!)

### The Real Requirements Matrix

| Requirement | AddError | AddWarning | No Diagnostic |
|-------------|----------|------------|---------------|
| Apply fails (exit code 1) | ✅ Yes | ❌ No | ❌ No |
| Blocks DAG on failure | ✅ Yes | ❌ No | ❌ No |
| Resource NOT tainted | ❌ No | ✅ Yes | ✅ Yes |
| User sees failure | ✅ Yes | ⚠️ Maybe | ❌ No |

**All three options are fatally flawed:**
- **AddError**: Blocks DAG correctly ✅, but taints resource ❌
- **AddWarning**: No tainting ✅, but doesn't block DAG ❌
- **No diagnostic**: No tainting ✅, but doesn't block DAG ❌ AND user doesn't know ❌

### Why "Fail at Plan Level" Doesn't Help

**Proposed idea:** Don't add diagnostic during Create/Update, add error during ModifyPlan on next run.

**Why this doesn't work:**

**First Apply:**
1. PVC created
2. Wait times out
3. No diagnostic added
4. **Downstream resources created anyway** ❌
5. Dependency chain violated on FIRST apply

**Second Apply:**
- ModifyPlan detects pending flag, adds error
- Too late - downstream resources already created

**The DAG violation happens on the FIRST apply.** Plan-level errors only help on subsequent runs.

### The Fundamental Conflict

**What we need:**
1. Error diagnostic during Create/Update → blocks DAG ✅
2. Error diagnostic during Create/Update → doesn't taint resource ❌

**These are mutually exclusive in Terraform's architecture.**

When Create/Update completes with `resp.Diagnostics.HasError() == true`:
- Terraform halts DAG execution (requirement #1) ✅
- Terraform taints the resource (violates requirement #2) ❌

**There is no way to separate these behaviors.**

## Current Implementation Details

### Terraform Plugin Framework Version

We are using `terraform-plugin-framework` with the following dependencies:

```go
// From go.mod
github.com/hashicorp/terraform-plugin-framework v1.13.0
github.com/hashicorp/terraform-plugin-testing v1.13.3
```

### Expected Retry Flow (What SHOULD Happen)

**First Apply:**
1. User runs `terraform apply`
2. Provider creates resource in Kubernetes successfully
3. Provider saves state (resource ID, projection, etc.)
4. Provider executes wait condition
5. Wait times out after configured duration
6. Apply FAILS with exit code 1
7. State is saved with resource marked as created but with `pending_wait_status` flag

**Second Apply (Retry):**
1. User fixes underlying issue (or just waits longer)
2. User runs `terraform apply` again
3. Provider detects existing resource with `pending_wait_status` flag
4. Provider RETRIES wait condition on existing resource (no recreation)
5. Wait succeeds (or times out again)
6. Apply completes

**What ACTUALLY Happens:**
- Steps 1-7 work correctly for first apply
- On second apply, Terraform sees "tainted" status and schedules REPLACEMENT
- Resource is destroyed and recreated
- Same timeout conditions = infinite loop

### State Save Order and Private State Flags

We implemented careful state save ordering to ensure state persists even on wait failure:

```go
// 9. SAVE STATE IMMEDIATELY after successful creation
diags = resp.State.Set(ctx, rc.Data)
resp.Diagnostics.Append(diags...)

// 10. Execute wait conditions (now AFTER initial state save)
waited, waitErr := r.handleWaitExecution(ctx, rc, "created")

// 11. Update status field with private state for pending wait tracking
if err := r.updateStatus(rc, waited, resp.Private, resp.Private); err != nil {
    tflog.Warn(ctx, "Failed to update status", map[string]interface{}{"error": err.Error()})
}

// 12. Save state again with status update
diags = resp.State.Set(ctx, rc.Data)
resp.Diagnostics.Append(diags...)

// 13. Add wait error to diagnostics (AFTER state is saved)
if waitErr != nil {
    r.addWaitError(resp, "created", waitErr)
}
```

This correctly saves:
- Resource ID
- K8s resource name/namespace
- Managed state projection
- Field ownership
- Status field (null with pending flag)
- Private state flag (`pending_wait_status`)

**BUT:** Terraform still taints the resource because `resp.Diagnostics.HasError() == true`.

### Private State Flag: `pending_wait_status`

Following the ADR-006 pattern for projection recovery, we use a private state flag to track pending waits:

**When wait times out:**
```go
// In updateStatus() - crud_operations.go:258
rc.Data.Status = types.DynamicNull()  // NOT unknown - Terraform contract
if privateSetter != nil {
    setPendingWaitStatusFlag(ctx, privateSetter)
}
```

**During next plan:**
```go
// In plan_modifier.go:127
hasPendingWait := checkPendingWaitStatusFlag(ctx, req.Private)
if hasPendingWait {
    tflog.Info(ctx, "Detected pending wait from previous timeout, setting status to unknown to block DAG")
    plannedData.Status = types.DynamicUnknown()  // NOW set to unknown for planning
    return
}
```

This pattern:
- Sets status=null after timeout (satisfies Terraform's "no unknown after apply" contract)
- Marks flag in private state to remember the pending wait
- Sets status=unknown during NEXT plan to block downstream DAG
- Triggers retry logic when user runs apply again

### Status Field Behavior

The `status` computed field has complex behavior based on `wait_for` configuration:

**During Apply (after wait timeout):**
- Set to `null` (NOT unknown - violates Terraform contract)
- Private flag `pending_wait_status` is set

**During Next Plan (with pending flag):**
- Set to `unknown` to block downstream resources
- Forces retry on apply

**Why null then unknown?**
- Terraform forbids setting computed fields to unknown during Apply
- Setting to unknown after apply causes "invalid result object" error
- Solution: null during apply + flag, then unknown during plan
- This is the ADR-006 pattern used for projection recovery

### State File Structure

The tainting happens in the Terraform state file structure:

```json
{
  "version": 4,
  "terraform_version": "1.13.0",
  "resources": [
    {
      "mode": "managed",
      "type": "k8sconnect_manifest",
      "name": "test_cm",
      "instances": [
        {
          "status": "tainted",          ← THIS IS THE PROBLEM
          "schema_version": 0,
          "attributes": {
            "id": "91064618d11c",
            "yaml_body": "...",
            "managed_state_projection": { ... },
            "status": null
          },
          "private": "eyJwZW5kaW5nX3dhaXRfc3RhdHVzIjoidHJ1ZSJ9"  ← Our flag is here
        }
      ]
    }
  ]
}
```

The `"status": "tainted"` field is added by Terraform's framework when:
- The Create/Update function completes
- `resp.Diagnostics.HasError()` returns true

We have NO control over this from the provider code - it's automatic framework behavior.

## Attempted Solutions

### 1. Remove `return` after AddError ❌
**Status:** Tried - doesn't work
**Why:** Framework checks diagnostics at end, not at return point

### 2. Use AddWarning instead of AddError ❌
**Status:** UNACCEPTABLE - violates requirement #1
**Why:** Apply must FAIL when wait times out. AddWarning = exit code 0 = apply succeeds = user doesn't know there's a problem

### 3. Don't add error to Create/Update diagnostics at all ❌
**Status:** Would work but misleading
**Why:** Users wouldn't know the wait failed until they check state/status

## Possible Solutions

**Goal:** Find a way to fail the apply (exit code 1) WITHOUT Terraform tainting the resource.

### Option A: Add Computed `wait_status` Field

Add a computed field that surfaces the wait failure:

```hcl
resource "k8sconnect_manifest" "pvc" {
  yaml_body = "..."
  wait_for = { ... }

  # Computed - populated by provider
  wait_status = "timeout"  # or "success", "pending"
}
```

Then fail at plan level if `wait_status == "timeout"`:

```go
func (r *manifestResource) ModifyPlan(...) {
    if stateData.WaitStatus == "timeout" {
        resp.Diagnostics.AddError("Wait Retry Required",
            "Run terraform apply again to retry the wait condition")
        return
    }
}
```

**Pros:**
- Errors at plan level don't taint resources
- Clear user feedback
- Explicit retry mechanism

**Cons:**
- More complex schema
- Mixing operational state with configuration
- Plan failures are unusual
- Unknown if plan-level errors avoid tainting

### Option B: Investigate Terraform's Internal Tainting Logic

Research if there's a way to explicitly clear taint or prevent tainting via plugin framework APIs.

**Status:** Unknown - needs investigation

**Potential areas:**
- `resp.State.RemoveTaint()` or similar
- Private state flags that influence tainting
- Framework version differences
- Newer framework versions with better control

### Option C: Research Other Providers (COMPLETED)

**Research completed - see RESEARCH_FINDINGS.md for full details.**

Check how other providers handle similar scenarios:
- `hashicorp/kubernetes` - do they have wait logic? How do they handle timeouts?
- `hashicorp/aws` - how do they handle waits (e.g., RDS creation, EC2 instance ready)?
- `kubectl` provider (gavinbunney) - wait behavior?
- `hashicorp/helm` - charts often wait for resources to be ready

**What to look for:**
- Do they use errors or warnings for wait failures?
- Do resources get tainted on wait timeout?
- How do they communicate wait failures to users?
- Is there a pattern or framework API we're missing?
- Do they have any special handling in ModifyPlan vs Create/Update?

**Result:** HashiCorp's kubernetes provider has the EXACT same problem and accepts tainting as expected behavior. Terraform plugin SDK issue #330 confirms this is an `upstream-protocol` limitation - providers CANNOT control tainting on create errors.

### Option D: Separate k8sconnect_wait Resource (NEW)

**Create a separate resource type dedicated to waiting, decoupling wait logic from resource creation.**

#### Usage Pattern

```hcl
resource "k8sconnect_manifest" "pvc" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-pvc
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 1Gi
YAML
  cluster_connection = var.cluster_connection

  # NO wait_for attribute on manifest resource
  # Outputs: resource_ref (computed)
}

resource "k8sconnect_wait" "pvc_ready" {
  resource_ref = k8sconnect_manifest.pvc.resource_ref

  wait_for = {
    field_value = { "status.phase" = "Bound" }
    timeout = "5m"
  }
}

resource "k8sconnect_manifest" "pod" {
  depends_on = [k8sconnect_wait.pvc_ready]  # Wait on the WAIT resource, not the manifest

  yaml_body = <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
spec:
  volumes:
    - name: storage
      persistentVolumeClaim:
        claimName: my-pvc
  # ...
YAML
  cluster_connection = var.cluster_connection
}
```

#### How It Works

**The `resource_ref` Output:**

The manifest resource would have a new computed output containing all information needed to identify and connect to the resource:

```hcl
# Computed output from k8sconnect_manifest.pvc
resource_ref = {
  cluster_connection = { ... }  # Full connection details
  api_version        = "v1"
  kind               = "PersistentVolumeClaim"
  namespace          = "default"
  name               = "my-pvc"
}
```

**The k8sconnect_wait Resource:**

- **Create**: Connects to cluster using `resource_ref`, executes wait conditions, returns
- **Read**: Always succeeds (no state in K8s to read)
- **Update**: Re-executes wait conditions with new configuration
- **Delete**: No-op (nothing to delete in K8s)

**Dependency Flow:**

```
k8sconnect_manifest.pvc (creates PVC)
         ↓ (outputs resource_ref)
k8sconnect_wait.pvc_ready (waits for PVC to be bound)
         ↓ (depends_on)
k8sconnect_manifest.pod (creates Pod that uses PVC)
```

#### Why This Solves BOTH Requirements

**Requirement #1: Apply MUST fail when wait times out**

✅ **SATISFIED**

When wait times out:
1. `k8sconnect_manifest.pvc` succeeds (PVC created)
2. `k8sconnect_wait.pvc_ready` fails with AddError
3. Apply stops with exit code 1
4. User sees error and knows something is wrong
5. DAG execution halted - Pod never created

**Requirement #2: Resource MUST NOT be recreated on retry**

✅ **SATISFIED**

When user retries after fixing the issue:
1. Terraform sees `k8sconnect_wait.pvc_ready` is tainted (this is FINE)
2. **Manifest resource is NOT tainted** - no recreation
3. Wait resource "destroyed" (no-op - nothing in K8s)
4. Wait resource "recreated" = just re-executes wait on existing PVC
5. If PVC now bound, wait succeeds
6. Pod gets created

**The key insight:** Tainting the wait resource doesn't matter because destroying/recreating it has NO side effects - it's just re-executing the wait logic. The actual K8s resource (PVC) is never touched.

#### Requirements Matrix

| Requirement | Manifest+wait_for | Separate wait resource |
|-------------|-------------------|------------------------|
| Apply fails on timeout | ✅ Yes | ✅ Yes |
| Blocks DAG on timeout | ✅ Yes | ✅ Yes |
| Manifest NOT tainted | ❌ No | ✅ Yes |
| Manifest NOT recreated | ❌ No | ✅ Yes |
| Wait retries in-place | ❌ No | ✅ Yes |
| User sees failure | ✅ Yes | ✅ Yes |

#### Pros

1. **Solves both hard requirements** - no compromises needed
2. **Uses normal Terraform patterns** - output→input dependency flow
3. **Clean separation of concerns** - create vs wait are separate operations
4. **No framework hacks** - works within Terraform's architecture
5. **Explicit dependencies** - `depends_on = [k8sconnect_wait.xxx]` is clearer than implicit
6. **Granular control** - can wait for multiple resources independently
7. **Not verbose** - minimal additional code in user configurations

#### Cons

1. **Breaking change** - existing `wait_for` attribute becomes deprecated
2. **Extra resource block** - requires separate resource declaration for waits
3. **Unfamiliar pattern** - most providers embed waiting in the resource
4. **Migration required** - existing users need to update configurations
5. **Two resources in state** - instead of one (but both are lightweight)

#### Implementation Considerations

**Manifest Resource Changes:**

1. Add `resource_ref` computed output to schema
2. Populate `resource_ref` during Create/Update (after K8s object known)
3. Deprecate `wait_for` attribute (but keep for backward compatibility initially)

**New k8sconnect_wait Resource:**

1. Schema:
   - `resource_ref` (required input) - takes output from manifest
   - `wait_for` (required) - same structure as current wait_for attribute

2. CRUD operations:
   - Create: Execute wait using resource_ref info, AddError on timeout
   - Read: No-op (or verify resource still exists for safety?)
   - Update: Re-execute wait with new conditions
   - Delete: No-op

3. State: Minimal - just resource_ref and wait_for config (for diff detection)

**Migration Path:**

1. Phase 1: Ship both patterns (wait_for attribute + separate wait resource)
2. Phase 2: Mark wait_for as deprecated, document migration guide
3. Phase 3: Remove wait_for attribute in next major version

#### Example: Complex Multi-Resource Wait

```hcl
# Create namespace
resource "k8sconnect_manifest" "namespace" {
  yaml_body = "..."
  cluster_connection = var.cluster_connection
}

# Create PVC
resource "k8sconnect_manifest" "pvc" {
  yaml_body = "..."
  cluster_connection = var.cluster_connection
  depends_on = [k8sconnect_manifest.namespace]
}

# Wait for PVC to be bound
resource "k8sconnect_wait" "pvc_bound" {
  resource_ref = k8sconnect_manifest.pvc.resource_ref
  wait_for = {
    field_value = { "status.phase" = "Bound" }
    timeout = "5m"
  }
}

# Create deployment
resource "k8sconnect_manifest" "deployment" {
  yaml_body = "..."
  cluster_connection = var.cluster_connection
  depends_on = [k8sconnect_manifest.namespace]
}

# Wait for deployment rollout
resource "k8sconnect_wait" "deployment_ready" {
  resource_ref = k8sconnect_manifest.deployment.resource_ref
  wait_for = {
    rollout = true
    timeout = "10m"
  }
}

# Create service
resource "k8sconnect_manifest" "service" {
  yaml_body = "..."
  cluster_connection = var.cluster_connection
  depends_on = [
    k8sconnect_wait.deployment_ready,
    k8sconnect_manifest.namespace
  ]
}

# Wait for LoadBalancer IP
resource "k8sconnect_wait" "service_ready" {
  resource_ref = k8sconnect_manifest.service.resource_ref
  wait_for = {
    field = "status.loadBalancer.ingress"
    timeout = "5m"
  }
}

# Use the LoadBalancer IP in another resource
resource "some_provider_dns" "endpoint" {
  # Access status through manifest resource, wait through wait resource
  ip = k8sconnect_manifest.service.status.loadBalancer.ingress[0].ip
  depends_on = [k8sconnect_wait.service_ready]
}
```

This example shows:
- Multiple waits on different resources
- Clear dependency ordering
- Granular control over what to wait for
- Status still accessible through manifest resource

## Testing Evidence

From `test/examples/wait_retry_test.go`:

```
Step 1: Running apply expecting wait timeout...
✓ Apply failed as expected with wait timeout

Step 2: Verifying state was saved despite wait failure...
Instance status: tainted          ← The smoking gun
✓ State was saved with resource

Step 5: Retrying apply (should succeed now)...
# k8sconnect_manifest.test_cm is tainted, so must be replaced
-/+ resource "k8sconnect_manifest" "test_cm" {
```

The resource instance has `"status": "tainted"` in the state file, causing Terraform to schedule replacement.

## Next Steps (CRITICAL PATH)

**PRIORITY 1: Research Other Providers (Option C)**

This is the most likely path to finding a solution. We MUST investigate:

1. **Clone and examine these provider repositories:**
   - `hashicorp/terraform-provider-kubernetes`
   - `gavinbunney/terraform-provider-kubectl`
   - `hashicorp/terraform-provider-aws`
   - `hashicorp/terraform-provider-helm`

2. **Search their code for wait/polling patterns:**
   - How do they handle resource readiness checks?
   - What happens when waits timeout?
   - Do they use AddError or AddWarning?
   - Look at their test cases for wait timeout scenarios

3. **Test their providers with intentional timeout scenarios:**
   - Create resources with waits that will timeout
   - Check if resources get tainted
   - See how retry behaves

**PRIORITY 2: Framework Investigation (Option B)**

If provider research doesn't reveal a solution:

1. Deep dive into terraform-plugin-framework source code
2. Search for tainting logic - where does it happen?
3. Check framework GitHub issues for similar problems
4. Look for any APIs to control tainting behavior

**PRIORITY 3: Prototype wait_status Field (Option A)**

If Options C and B don't work:

1. Add computed `wait_status` field to schema
2. Move error to ModifyPlan instead of Create/Update
3. Test if plan-level errors avoid tainting
4. Verify retry behavior works correctly

**IF ALL OPTIONS FAIL:** Remove wait_for feature entirely to avoid shipping broken functionality.

## Related Code

- `/internal/k8sconnect/resource/manifest/crud.go` - Create/Update with wait logic
- `/internal/k8sconnect/resource/manifest/crud_operations.go` - updateStatus with pending flags
- `/internal/k8sconnect/resource/manifest/plan_modifier.go` - Status field plan modifier
- `/test/examples/wait_retry_test.go` - Test demonstrating taint issue
