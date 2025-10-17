# ADR-016: Separate Wait Resource to Avoid Tainting

## Status
Implemented

**Builds on:** ADR-008 (Selective Status Field Population Strategy)

This ADR changes WHO drives status population, not the principle itself. ADR-008's "You get ONLY what you wait for" principle remains correct - but now the WAIT resource determines what status gets populated on the MANIFEST resource, not the manifest's own `wait_for` attribute. The wait resource uses the exact same logic for status population and pruning as the current embedded wait_for feature.

## Context

### The Problem

When a `wait_for` condition times out on a `k8sconnect_object` resource, Terraform automatically taints the resource even though it was successfully created in Kubernetes. This causes the next `terraform apply` to **destroy and recreate** the resource instead of retrying the wait condition in-place.

**User Scenario:**
1. User creates a PVC with `wait_for.field_value = { "status.phase" = "Bound" }`
2. Apply runs - PVC is created successfully in Kubernetes via Server-Side Apply
3. State is saved with resource ID, projection, field ownership
4. Wait condition executes and times out after 30s (storage provisioner is slow)
5. Provider adds error diagnostic: `resp.Diagnostics.AddError("Wait condition failed", ...)`
6. Apply exits with code 1 (correct - user must be notified)
7. Terraform framework marks resource as `"status": "tainted"` in state file
8. User runs `terraform apply` again to retry
9. **Terraform destroys and recreates the PVC** instead of just waiting again
10. The new PVC has the same timeout issue (storage provisioner still slow)
11. User is stuck in an infinite loop, potentially losing data on each iteration

**Why this is unacceptable:**
- Destroying a resource doesn't fix wait conditions - the same underlying issue (slow controller, missing dependency, misconfiguration) will cause the new resource to also timeout
- Data loss risk - recreating PVCs destroys bound volumes, StatefulSets lose pod identity and volumes, Jobs lose completed work
- Wasted time - creating new resources is slower than waiting for existing ones, especially for resources with finalizers or webhooks
- User confusion - "Why is Terraform destroying my successfully created resource when the timeout is the issue, not the resource itself?"
- Infinite loop - if the wait condition requires external intervention (e.g., manual approval, external system), recreation won't help

### Hard Requirements (BOTH must be satisfied)

1. **Apply MUST fail (exit code 1) when wait times out**
   - Wait timeout is a FAILURE - user must know something is wrong
   - User must take action and retry
   - This is about USER EXPERIENCE and DEPENDENCY BLOCKING

2. **Resource MUST NOT be tainted/recreated on retry**
   - Recreating the resource doesn't fix wait conditions
   - Same conditions = same timeout = infinite loop
   - Data loss risk (PVCs, StatefulSets, etc.)
   - Retry must happen IN-PLACE on existing resource

**These are NOT trade-offs. We need BOTH or the feature is unusable.**

### Technical Root Cause

Terraform's plugin framework automatically taints resources when:
- Create/Update function completes (normally, without panic)
- `resp.Diagnostics.HasError()` returns true at the end of execution
- Regardless of whether state was saved or not
- Regardless of whether errors were added before or after state save

**Our Current Implementation (Correct State Save Pattern):**

```go
// internal/k8sconnect/resource/object/crud.go

// 9. SAVE STATE IMMEDIATELY after successful SSA apply
diags = resp.State.Set(ctx, rc.Data)
resp.Diagnostics.Append(diags...)

// 10. Execute wait conditions (AFTER initial state save)
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
// Function returns normally - NO early return after error
```

**What Gets Saved:**
- Resource ID (generated during SSA apply)
- K8s resource identity (apiVersion, kind, namespace, name)
- Managed state projection (from managedFields parsing)
- Field ownership tracking
- Status field (null with `pending_wait_status` flag in private state)

**What Happens Next:**

Terraform framework checks `resp.Diagnostics.HasError()` after Create returns. If true, it writes to the state file:

```json
{
  "instances": [{
    "status": "tainted",  // ← Terraform adds this automatically
    "attributes": { /* resource state */ },
    "private": "eyJwZW5kaW5nX3dhaXRfc3RhdHVzIjoidHJ1ZSJ9"
  }]
}
```

**We have NO control over the `"status": "tainted"` field - it's automatic framework behavior.**

**The fundamental conflict:**
- `AddError` → Apply fails ✅ + Blocks DAG ✅ + Resource tainted ❌
- `AddWarning` → No tainting ✅ + Apply succeeds ❌ + DAG not blocked ❌

**There is no way to fail the apply without tainting the resource within a single resource's lifecycle.**

**We tried removing the return statement after AddError** - doesn't work. Framework checks diagnostics at the end of the function, not at the return point. Resource still gets tainted.

**Private State Flag Pattern (ADR-006):**

Following the projection recovery pattern, we use private state to track pending waits:

```go
// When wait times out (crud_operations.go:258)
rc.Data.Status = types.DynamicNull()  // NOT unknown - Terraform contract
if privateSetter != nil {
    setPendingWaitStatusFlag(ctx, privateSetter)
}

// During next plan (plan_modifier.go:127)
hasPendingWait := checkPendingWaitStatusFlag(ctx, req.Private)
if hasPendingWait {
    plannedData.Status = types.DynamicUnknown()  // NOW set to unknown for planning
    // This blocks downstream DAG on next apply
    return
}
```

**Purpose:** Track pending waits across apply cycles and set status=unknown during planning to block downstream DAG.

**Problem:** This only affects PLANNING. It doesn't prevent tainting, so the retry still destroys/recreates.

### Why AddWarning is Also Unacceptable

AddWarning doesn't block the Terraform dependency graph:

```hcl
resource "k8sconnect_object" "pvc" {
  wait_for = { field_value = { "status.phase" = "Bound" } }
}

resource "k8sconnect_object" "pod" {
  depends_on = [k8sconnect_object.pvc]
  # Pod spec references the PVC
}
```

**With AddWarning:**
1. PVC created ✅
2. Wait for "Bound" times out ⚠️
3. AddWarning (not AddError)
4. Apply continues because no error
5. **Pod gets created anyway** ❌
6. Pod tries to mount unbound PVC
7. Pod fails to start (CrashLoopBackOff)

**The dependency is violated.** The pod should never have been created until the PVC was bound.

### Research: HashiCorp Has the Same Problem

The official `hashicorp/terraform-provider-kubernetes` has the EXACT same problem and considers resource tainting on wait timeout to be **EXPECTED BEHAVIOR**.

**Issue #1455**: [Timeouts Combined with wait_for Cause State Conflicts](https://github.com/hashicorp/terraform-provider-kubernetes/issues/1455)
- Problem: When wait timeout occurs, resource created in K8s but not tracked in Terraform state
- Fixed by PR #2163 - save state BEFORE adding error diagnostic
- Result: Resource saved to state BUT still tainted

**Issue #1790**: [Service Tainted on Every Run](https://github.com/hashicorp/terraform-provider-kubernetes/issues/1790)
- Problem: kubernetes_service marked as tainted after timeout, recreated on every apply
- Resolution: Closed as "NOT_PLANNED" after 30+ days inactive
- Status: **NEVER SOLVED**

**PR #2163**: [Save state before wait error diagnostic](https://github.com/hashicorp/terraform-provider-kubernetes/pull/2163)
- Their "solution" saves state before error diagnostic, but resource still gets tainted
- From the PR notes: "tainting the resource requiring the user to make the necessary changes"
- This is intentional, not a bug they're trying to fix

**terraform-plugin-sdk Issue #330**: [Ability to Prevent Taints on Create Errors](https://github.com/hashicorp/terraform-plugin-sdk/issues/330)
- Label: `upstream-protocol`
- Status: **OPEN - No solution**
- **Critical Quote**: "Providers cannot currently control tainting behavior on create errors"
- Confirms this is a Terraform CORE protocol limitation, cannot be fixed at provider level

**What this means:**
- This is a Terraform CORE protocol limitation
- Cannot be fixed in terraform-plugin-framework
- Cannot be fixed in terraform-plugin-sdk
- Requires changes to Terraform itself
- **Architectural limitation**

HashiCorp has had YEARS of user feedback about resources being tainted on timeout. They chose to:
- Keep the feature
- Accept the tainting behavior
- Promote it publicly
- Document it as working correctly

**Their implicit position:** Tainting on wait timeout is correct behavior, recreation on retry is acceptable, users should fix timeout issues between retries.

### Why HashiCorp Didn't Use This Approach

HashiCorp could have implemented separate wait resources, but chose not to. **This is a philosophical difference about Terraform's resource model.**

**HashiCorp's philosophy:** "One Terraform resource = One thing in the external system"
- `kubernetes_manifest` represents THE Kubernetes manifest
- All operations on that manifest (create, update, delete, wait) belong in that resource
- Separating wait into another resource violates this purity
- They accept the tainting trade-off to maintain this model

**Our philosophy:** "Correctness and user experience over architectural purity"
- A Kubernetes manifest is created via SSA apply (one operation)
- Waiting for readiness is a separate concern (monitoring operation)
- These should be separate resources because they have different lifecycles
- The manifest resource can succeed 100% of the time (respecting K8s self-healing)
- The wait resource is opt-in for users who need synchronous dependencies
- We reject data loss and infinite loops, even if it means breaking the "one resource" model

**Result:** We're willing to have two Terraform resources manage one K8s object if it produces better behavior. HashiCorp is not.

## Alternatives Considered

### Alternative 1: AddError (Current Implementation)
**Status:** Rejected - violates requirement #2

- Apply fails (exit code 1) ✅
- Blocks DAG correctly ✅
- Resource tainted on timeout ❌
- Retry destroys and recreates ❌

**Result:** Infinite loop with data loss risk

### Alternative 2: AddWarning
**Status:** Rejected - violates requirements #1 and blocks DAG

- No tainting ✅
- Apply succeeds (exit code 0) ❌
- DAG not blocked - downstream resources created when they shouldn't be ❌
- Dependency chain violated ❌

**Result:** Creates broken infrastructure

### Alternative 3: Add Computed `wait_status` Field
**Status:** Rejected - complex and unproven

Add a computed field that surfaces wait failure, then fail at plan level if `wait_status == "timeout"`. Plan-level errors might not taint resources.

**Cons:**
- More complex schema
- Mixing operational state with configuration
- Plan failures are unusual UX
- Unknown if plan-level errors actually avoid tainting
- Adds user-visible state for purely operational concerns

### Alternative 4: Accept HashiCorp's Pattern
**Status:** Rejected - unacceptable data loss risk

Follow the official kubernetes provider and accept tainting as expected behavior.

**What this means for users:**
- PVCs destroyed and recreated (data loss risk)
- StatefulSets destroyed and recreated (data loss risk)
- Jobs killed and restarted (work loss)
- LoadBalancer IPs change (integration breakage)

**Result:** HashiCorp accepts this. We won't.

## Decision

**Create a separate `k8sconnect_wait` resource type dedicated to waiting, decoupling wait logic from resource creation.**

### Usage Pattern

```hcl
# Create PVC (no wait_for attribute)
resource "k8sconnect_object" "pvc" {
  yaml_body = "..."  # PVC YAML
  cluster_connection = var.cluster_connection
}

# Separate wait resource blocks on PVC being bound
resource "k8sconnect_wait" "pvc_ready" {
  resource_ref = k8sconnect_object.pvc.resource_ref
  wait_for = {
    field_value = { "status.phase" = "Bound" }
    timeout = "5m"
  }
}

# Pod depends on wait resource, not manifest resource
resource "k8sconnect_object" "pod" {
  depends_on = [k8sconnect_wait.pvc_ready]
  yaml_body = "..."  # Pod YAML mounting the PVC
  cluster_connection = var.cluster_connection
}
```

**Key differences from current approach:**
- `wait_for` attribute removed from manifest resource
- Separate `k8sconnect_wait` resource performs the waiting
- Dependencies use `depends_on = [k8sconnect_wait.xxx]` instead of implicit wait behavior

### How It Works

**The `resource_ref` Output:**

The manifest resource has a computed output containing the minimum information needed to locate and connect to the resource:

```hcl
# Computed output from k8sconnect_object.pvc
resource_ref = {
  cluster_connection = { ... }  # Auth credentials and endpoint
  api_version        = "v1"     # Resource identity
  kind               = "PersistentVolumeClaim"
  namespace          = "default"
  name               = "my-pvc"
}
```

**Only includes:** cluster auth + resource identity (no projection, no status, no field ownership). Wait resource only needs to connect and read.

**The k8sconnect_wait Resource:**

- **Create**: Connects to cluster using `resource_ref`, executes wait conditions, populates status on manifest resource (following ADR-008 pruning logic), returns. AddError on timeout.
- **Read**: Connects to cluster, reads current status from K8s resource, updates manifest resource status if needed
- **Update**: Re-executes wait conditions with new configuration, updates manifest status
- **Delete**: No-op (nothing to delete in K8s)
- **Import**: Not supported (wait resources are ephemeral - nothing to import)

**Dependency Flow:**

```
k8sconnect_object.pvc (creates PVC)
         ↓ (outputs resource_ref)
k8sconnect_wait.pvc_ready (waits for PVC to be bound)
         ↓ (depends_on)
k8sconnect_object.pod (creates Pod that uses PVC)
```

**Edge Cases:**

- **Manifest destroyed**: Wait resource depends on manifest's `resource_ref` output, so Terraform automatically destroys wait resource first (normal dependency behavior)
- **resource_ref auth changes**: Wait resource detects change in `resource_ref`, triggers Update which re-executes wait with new auth
- **resource_ref identity changes** (apiVersion/kind/namespace/name): Triggers replacement of wait resource (Update wouldn't make sense - different resource identity = different resource)

### Why This Solves BOTH Requirements

**Requirement #1: Apply MUST fail when wait times out**

✅ **SATISFIED**

When wait times out:
1. `k8sconnect_object.pvc` succeeds (PVC created)
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

### Requirements Matrix

| Requirement | Manifest+wait_for | Separate wait resource |
|-------------|-------------------|------------------------|
| Apply fails on timeout | ✅ Yes | ✅ Yes |
| Blocks DAG on timeout | ✅ Yes | ✅ Yes |
| Manifest NOT tainted | ❌ No | ✅ Yes |
| Manifest NOT recreated | ❌ No | ✅ Yes |
| Wait retries in-place | ❌ No | ✅ Yes |
| User sees failure | ✅ Yes | ✅ Yes |

## Benefits

1. **Solves both hard requirements** - no compromises needed
2. **Uses normal Terraform patterns** - output→input dependency flow
3. **Clean separation of concerns** - create vs wait are separate operations
4. **No framework hacks** - works within Terraform's architecture
5. **Explicit dependencies** - `depends_on = [k8sconnect_wait.xxx]` is clearer than implicit
6. **Granular control** - can wait for multiple resources independently
7. **Not verbose** - minimal additional code in user configurations
8. **Prevents data loss** - manifest resources never recreated due to wait timeouts
9. **Natural retry flow** - re-running apply just re-executes the wait, not the creation
10. **Respects Kubernetes self-healing nature** - resource creation succeeds immediately (100% success rate for SSA apply), wait is opt-in for users who need it
11. **Most users don't need wait** - they just want `terraform apply` to succeed and then diagnose with `kubectl`. Wait is an advanced feature for complex dependencies.

## Drawbacks

1. **Breaking change** - existing `wait_for` attribute becomes deprecated
2. **Extra resource block** - requires separate resource declaration for waits
3. **Unfamiliar pattern** - most providers embed waiting in the resource
4. **Migration required** - existing users need to update configurations
5. **Two resources in state** - instead of one (but both are lightweight)

## Implementation

### Manifest Resource Changes

1. Add `resource_ref` computed output to schema
2. Populate `resource_ref` during Create/Update (after K8s object known)
3. Deprecate `wait_for` attribute (keep for backward compatibility initially)
4. Continue populating `status` field when using embedded `wait_for` (backward compat)

### New k8sconnect_wait Resource

**Schema:**
- `resource_ref` (required input) - object containing cluster_connection + resource identity (apiVersion, kind, namespace, name)
- `wait_for` (required) - same structure as current wait_for attribute (field, field_value, condition, rollout, timeout)

**CRUD Operations:**
- **Create**: Execute wait using resource_ref info, populate manifest status following ADR-008 logic, AddError on timeout
- **Read**: Read current status from K8s, update manifest status if needed
- **Update**: Re-execute wait with new conditions or new resource_ref auth
- **Delete**: No-op
- **Import**: Not supported

**State:** resource_ref and wait_for config (for diff detection), plus status field if using Option 1 below.

**Status Population (Open Design Question):**

Following ADR-008 "You only get what you wait for", status should only be populated for fields that are waited on. Implementation options:

1. **Wait resource has its own status field:** Wait resource stores the pruned status for the field it's waiting on. Users access status via `k8sconnect_wait.pvc_ready.status` instead of `k8sconnect_object.pvc.status`. Manifest resource never populates status when using separate wait.

2. **Manifest Read operation checks for wait resources:** Manifest detects if any wait resources reference it, reads their wait_for configs, populates its own status field accordingly. Complex cross-resource dependency.

3. **Status only works with embedded wait_for:** Separate wait resource doesn't populate status anywhere - it just blocks. Users who need status must use the deprecated embedded `wait_for` attribute. Simpler but less capable.

**Recommendation:** Option 1 - wait resource has its own status field. Cleanest separation, follows ADR-008 principle, no cross-resource state updates needed.

### Drift Detection and Status Refresh

The wait resource has two distinct use cases with different drift handling requirements:

**Use Case 1: Value Extraction (Dynamic Values)**

Wait for a field to exist and extract its value for use in other resources:

```hcl
resource "k8sconnect_wait" "ingress" {
  resource_ref = k8sconnect_object.ingress.resource_ref
  wait_for = {
    field = "status.loadBalancer.ingress[0].hostname"
  }
}

resource "cloudflare_record" "firewall" {
  value = k8sconnect_wait.ingress.status.loadBalancer.ingress[0].hostname
  # If hostname changes → firewall updates (DESIRED)
}
```

**Expected behavior:**
- Status refreshed on every `terraform plan/refresh`
- Detects when LoadBalancer IP/hostname changes
- Drift propagates to dependent resources (firewall record updates)

**Implementation:** Read() operation refreshes status from K8s for field waits.

**Use Case 2: Dependency Ordering (Synchronization Gates)**

Wait for a condition to block dependent resource creation:

```hcl
resource "k8sconnect_wait" "migration" {
  resource_ref = k8sconnect_object.migration_job.resource_ref
  wait_for = {
    condition = "Complete"
  }
}

resource "aws_db_instance" "aurora" {
  depends_on = [k8sconnect_wait.migration]
  # Only create after migration completes
}
```

**Expected behavior:**
- If Job is recreated → Wait resource re-executes → Aurora DB **NOT destroyed**
- Using `depends_on` alone (without attribute references) creates ordering dependency without lifecycle coupling
- Per [Terraform GitHub Issue #2895](https://github.com/hashicorp/terraform/issues/2895): "If resource A depends_on resource B, A will be created after B, but if B gets tainted then nothing will happen to A (assuming no other reference to B)"

**Implementation:** Condition/rollout waits don't populate status per ADR-008. Downstream resources use `depends_on` only (no attribute references).

**Critical Design Decision:**

The two use cases require different patterns:

| Pattern | Use Case | Status Populated | Dependency Type | Drift Propagation |
|---------|----------|------------------|-----------------|-------------------|
| Field wait | Value extraction | Yes (pruned to field) | Attribute reference | Yes |
| Condition wait | Ordering gate | No (null) | depends_on only | No |

**Recommended Patterns:**

```hcl
# Pattern 1: Value Extraction (status refresh enabled)
resource "k8sconnect_wait" "ingress" {
  wait_for = { field = "status.loadBalancer.ingress[0].hostname" }
  # Populates status per ADR-008
}

resource "cloudflare_record" "firewall" {
  value = k8sconnect_wait.ingress.status.loadBalancer.ingress[0].hostname
  # If hostname changes → firewall updates (DESIRED)
}

# Pattern 2: Dependency Ordering (no drift propagation)
resource "k8sconnect_wait" "migration" {
  wait_for = { condition = "Complete" }
  # Status = null per ADR-008
}

resource "aws_db_instance" "aurora" {
  depends_on = [k8sconnect_wait.migration]
  # NO attribute references → Aurora NOT destroyed if wait is tainted
}
```

**Read() Implementation:** Refreshes status from K8s for field waits only. Does NOT re-execute wait conditions (too expensive).

### Migration Path

1. **Phase 1**: Ship both patterns (wait_for attribute + separate wait resource)
2. **Phase 2**: Mark wait_for as deprecated in docs, provide migration guide
3. **Phase 3**: Remove wait_for attribute in next major version

### Example: Multi-Resource Dependency Chain

The pattern scales naturally to complex dependency chains:
- PVC → wait for bound → Deployment
- Deployment → wait for rollout → Service
- Service → wait for LoadBalancer IP → DNS record creation

Each wait resource blocks its dependent resources, creating explicit dependency graphs without embedding wait logic in the manifest resource lifecycle.

## Related

### HashiCorp GitHub Issues
- [terraform-provider-kubernetes#1455](https://github.com/hashicorp/terraform-provider-kubernetes/issues/1455) - Timeouts Combined with wait_for Cause State Conflicts
- [terraform-provider-kubernetes#1790](https://github.com/hashicorp/terraform-provider-kubernetes/issues/1790) - Service Tainted on Every Run (NOT_PLANNED)
- [terraform-provider-kubernetes#2163](https://github.com/hashicorp/terraform-provider-kubernetes/pull/2163) - PR that saves state before error but accepts tainting
- [terraform-plugin-sdk#330](https://github.com/hashicorp/terraform-plugin-sdk/issues/330) - Request to prevent taints on create errors (upstream-protocol)

### Internal ADRs
- ADR-006: State Safety and Projection Recovery - similar use of private state flags for recovery
- ADR-008: Selective Status Field Population Strategy - "You only get what you wait for" principle

### Research Documents
- `docs/wait-drift-analysis.md` - Analysis of drift detection requirements for wait resources
- `docs/wait-drift-research-findings.md` - Confirmed Terraform behavior with depends_on and ignore_changes
