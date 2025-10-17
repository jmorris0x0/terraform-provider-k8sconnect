# Wait Resource Drift Analysis

## The Problem

The wait resource has two distinct use cases with conflicting requirements for how drift should be handled:

### Use Case 1: Value Extraction (Dynamic Values)
```hcl
resource "k8sconnect_wait" "ingress" {
  object_ref = k8sconnect_object.ingress.object_ref
  wait_for = {
    field = "status.loadBalancer.ingress[0].hostname"
  }
}

resource "cloudflare_record" "firewall" {
  value = k8sconnect_wait.ingress.status.loadBalancer.ingress[0].hostname
}
```

**Expected behavior:**
- If LoadBalancer IP/hostname changes → Detect drift → Propagate to firewall
- Status should be refreshed on every `terraform plan/refresh`

### Use Case 2: Dependency Ordering (Synchronization Gates)
```hcl
resource "k8sconnect_wait" "migration" {
  object_ref = k8sconnect_object.migration_job.object_ref
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
- If Job is recreated (new instance, same name) → Status becomes unknown temporarily
- Aurora DB should **NOT** be destroyed
- Once Job completes again → Wait succeeds → Continue

## Current Implementation Gaps

### Gap #1: Read Operation Doesn't Refresh Status

**Current code** (`internal/k8sconnect/resource/wait/crud.go:78-107`):

```go
func (r *waitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// ... get state ...

	// Build wait context
	wc, diags := r.buildWaitContext(ctx, &data)

	// Verify the resource still exists
	_, err := wc.Client.Get(ctx, wc.GVR, ...)
	if err != nil {
		resp.State.RemoveResource(ctx)
		return
	}

	// Resource still exists, keep state as-is  ← PROBLEM
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
```

**Problems:**
1. **Doesn't re-check wait conditions** - Only verifies resource exists
2. **Doesn't update status** - Keeps stale status in state
3. **Doesn't detect value changes** - LoadBalancer IP change never detected

**Impact:**
- Use Case 1 (Value Extraction): **BROKEN** - Stale values never updated
- Use Case 2 (Dependency Ordering): Works by accident (keeps last known state)

### Gap #2: No Drift Detection Strategy

The wait resource has no documented strategy for:
- When to refresh status
- What to do when waited-for condition is no longer met
- How to handle unknown vs changed vs missing values

## Research Findings: Terraform's Behavior with Unknown Values

### Key Finding 1: `depends_on` vs Reference-Based Dependencies

From [Terraform docs](https://developer.hashicorp.com/terraform/language/meta-arguments/depends_on):

> "Using `depends_on` can cause Terraform to create more conservative plans that replace more resources than necessary, with more values showing as unknown '(known after apply)'"

**Implications:**

**Using `depends_on` (less specific):**
```hcl
resource "aws_db_instance" "aurora" {
  depends_on = [k8sconnect_wait.migration]
}
```
- If wait resource changes AT ALL → Aurora DB may be recreated
- Terraform doesn't know WHAT changed, so it's conservative

**Using references (more specific):**
```hcl
resource "aws_db_instance" "aurora" {
  tags = {
    migration_status = k8sconnect_wait.migration.status.phase
  }
}
```
- Only Aurora DB's tags are affected if status.phase changes
- Aurora DB itself won't be replaced (unless tags marked as RequiresReplace)

### Key Finding 2: Unknown Computed Attributes Can Trigger Replacement

From [terraform-plugin-framework#189](https://github.com/hashicorp/terraform-plugin-framework/issues/189):

> "When a computed attribute's planned value is set to Unknown, and that attribute is marked as RequiresReplace, Terraform Core marks the resource as requiring replacement, even if the value wouldn't actually change after apply."

**Implications:**
- If wait status goes from "Complete" → Unknown → Aurora DB's tags become unknown
- If tags are marked RequiresReplace → Aurora DB gets replaced
- Even if the eventual value will still be "Complete"

### Key Finding 3: Downstream Resources May Be Replaced

From web search results:

> "If a resource attribute is unknown during the plan phase because an upstream dependency is recreated, the resource may be destroyed first then recreated, even if the actual value is unchanged."

**This is the core problem the user identified.**

## The Unknowns Problem: A Critical Design Question

### Scenario: Job Recreated, Aurora DB Already Exists

```
Step 1 (Initial):
  k8sconnect_object.job → Job created
  k8sconnect_wait.migration → Wait succeeds, status.phase = "Complete"
  aws_db_instance.aurora → Created (depends on wait)

Step 2 (Job Recreated):
  k8sconnect_object.job → Job recreated (new instance, same name)
  Job status.phase = "Running" (not yet "Complete")

  terraform plan is run...

  What should k8sconnect_wait.migration.status.phase be?
```

### Option A: Set status to Unknown

```
status.phase = Unknown ("known after apply")
```

**Pros:**
- Accurate representation - we don't know the phase yet
- Blocks downstream resources from being created prematurely
- Terraform's intended behavior for computed values

**Cons:**
- May trigger destruction of existing Aurora DB
- Any resource referencing status.phase becomes unknown
- If those unknowns affect RequiresReplace fields → replacement cascade

### Option B: Keep Last Known Value

```
status.phase = "Complete" (stale, from old Job)
```

**Pros:**
- Prevents destruction of Aurora DB
- Stable state, no cascading unknowns

**Cons:**
- **Incorrect** - Represents old Job's state, not current Job
- Violates Terraform contract (state should reflect reality)
- May cause errors if downstream resources try to use the value
- Hides the fact that migration hasn't re-run yet

### Option C: Set status to Null

```
status.phase = null
```

**Pros:**
- Clearly indicates "not tracking this anymore"
- Different from Unknown (which blocks)

**Cons:**
- Loss of information
- Downstream resources see null instead of value
- May still trigger changes/replacements

### Option D: Don't Populate Status for Ordering Waits

Following ADR-008, only field waits populate status:

```hcl
# Use Case 1: Value extraction → Has status
wait_for = { field = "status.loadBalancer.ingress" }
# status = { loadBalancer: { ingress: [...] } }

# Use Case 2: Ordering → No status
wait_for = { condition = "Complete" }
# status = null
```

**Pros:**
- Separates value extraction from ordering concerns
- Ordering waits never cause downstream unknowns (no status to reference)
- Already implemented per ADR-008!

**Cons:**
- Can't use `depends_on` with status references for ordering waits
- Must use `depends_on` instead of attribute references

## Critical Questions That Need Empirical Testing

### Question 1: How Does `depends_on` Actually Work?

**Hypothesis:** `depends_on` establishes ordering but doesn't propagate unknowns to dependent resources.

```hcl
resource "k8sconnect_wait" "migration" {
  wait_for = { condition = "Complete" }
  # No status output per ADR-008
}

resource "aws_db_instance" "aurora" {
  depends_on = [k8sconnect_wait.migration]
  # Not referencing any attributes from wait resource
}
```

**If wait resource is tainted and recreated:**
1. Wait resource destroyed (no-op)
2. Wait resource recreated (re-runs wait)
3. **Does Aurora DB get destroyed?**

**Need to verify:** Whether `depends_on` alone (without attribute references) can cause downstream replacement.

### Question 2: Does `ignore_changes` Work with Computed Null/Unknown Values?

**Proposed pattern for safety:**

```hcl
resource "k8sconnect_wait" "migration" {
  wait_for = { field = "status.phase" }  # Populates status
}

resource "aws_db_instance" "aurora" {
  tags = {
    migration_status = k8sconnect_wait.migration.status.phase
  }

  lifecycle {
    ignore_changes = [tags]  # Ignore changes to tags after creation
  }
}
```

**Critical unknowns:**

1. **Can you reference a null/unknown value?**
   - If `status.phase` becomes null, does `k8sconnect_wait.migration.status.phase` fail during planning?
   - Or does it evaluate to null and propagate?

2. **Does `ignore_changes` prevent changes from computed values?**
   - `ignore_changes` is typically used for user-configured values
   - Does it work when the value is computed from another resource's output?

3. **Does `ignore_changes` handle null vs unknown differently?**
   - If value goes from "Complete" → null: Does `ignore_changes` keep old value?
   - If value goes from "Complete" → unknown: Does `ignore_changes` prevent unknown propagation?

4. **Does this prevent downstream replacement?**
   - Even with `ignore_changes = [tags]`, if tags become unknown, does Aurora get replaced?
   - Or does `ignore_changes` stabilize the value and prevent replacement?

**Possible outcomes:**

**Best case:**
- `ignore_changes` works with computed values
- Keeps old value when new value is null/unknown
- Prevents Aurora from being replaced
- **This would be the ideal solution**

**Worst case:**
- Reference to null/unknown fails during plan
- Or `ignore_changes` doesn't apply to computed values
- Aurora gets replaced anyway
- **Pattern doesn't work, need different solution**

**Middle case:**
- Works for null but not unknown
- Or works but with warnings/degraded behavior
- **Partial solution, needs documentation of limitations**

**This MUST be tested empirically before recommending the pattern.**

## HashiCorp's Kubernetes Provider Behavior

**Research needed:** How does `hashicorp/terraform-provider-kubernetes` handle:
- Wait conditions timing out
- Resources being recreated
- Status field drift

Do they have similar issues? How do users work around them?

## Proposed Solutions

### Solution 1: Implement Proper Read() with Status Refresh

**For field waits only** (Use Case 1: Value Extraction):

```go
func (r *waitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data waitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	wc, diags := r.buildWaitContext(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get current resource state
	currentObj, err := wc.Client.Get(ctx, wc.GVR, ...)
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		// Resource exists but couldn't read - keep last known state
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}

	// For field waits, update status from current state
	if !wc.WaitConfig.Field.IsNull() {
		if err := r.updateStatus(ctx, wc); err != nil {
			tflog.Warn(ctx, "Failed to update status", map[string]interface{}{"error": err.Error()})
		}
	}

	// Save potentially updated state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
```

**Behavior:**
- Field waits: Status refreshed on every plan/refresh → Detects changes
- Non-field waits: No status, so no drift to detect
- If field missing: Status becomes null (or unknown? - needs decision)
- If resource missing: Wait resource removed from state

### Solution 2: Document The Pattern

**For ordering waits (Use Case 2):**

Make it clear that:
1. Ordering waits use `condition`/`rollout`/`field_value` (not `field`)
2. These don't populate status (per ADR-008)
3. Downstream resources use `depends_on` (not attribute references)
4. This prevents cascading unknowns

**For value extraction (Use Case 1):**

Make it clear that:
1. Value extraction uses `field`
2. Status is populated and refreshed
3. Downstream resources reference the attribute directly
4. Changes propagate as expected

### Solution 3: Stabilize Status During Transient Unknowns

**Smart status handling:**

```go
// If we previously had a value, and resource is being recreated
if previousStatus.IsKnown() && resourceIsNew() {
	// Keep last known value during transition
	// Set private state flag: "status_stale"
	data.Status = previousStatus
	setStaleStatusFlag(ctx, resp.Private)
} else if staleStatusFlag && conditionNowMet() {
	// Clear flag, update with fresh value
	clearStaleStatusFlag(ctx, resp.Private)
	updateStatus(ctx, wc)
}
```

**Pros:**
- Prevents cascading unknowns
- Doesn't destroy downstream resources
- Eventually updates to correct value

**Cons:**
- Complex state management
- Temporarily incorrect state
- When is it safe to transition back to unknown?

## Recommendations

### Immediate Actions

1. **Create empirical tests for Terraform behavior** (MUST DO FIRST)
   - Test 1: `depends_on` alone - does it cause downstream replacement when wait is tainted?
   - Test 2: `ignore_changes` with computed values - does it work when value becomes null?
   - Test 3: `ignore_changes` with computed values - does it work when value becomes unknown?
   - Test 4: Can you reference null/unknown values, or does planning fail?

   **Why critical:** We're making recommendations based on assumptions. Need facts.

2. **Based on test results, implement Read() properly**
   - If `ignore_changes` works: Implement status refresh, users can protect themselves
   - If it doesn't work: Need different strategy (maybe keep last known value with flags?)

3. **Document the patterns with empirically-verified behavior**
   - Don't guess at what works - show what actually works
   - Include workarounds for any limitations discovered

### Research Needed

1. **Terraform Core behavior with `depends_on`**
   - Does it cause downstream replacement when dependency is tainted?
   - Or just ensures ordering?

2. **HashiCorp kubernetes provider**
   - How do they handle similar scenarios?
   - What do users complain about?

3. **Unknown value lifecycle**
   - When does a value transition from known → unknown → known?
   - Can we control this transition?

### Decisions for ADR-016

1. **What should Read() do?**
   - Refresh status for field waits? (Recommended: Yes)
   - Re-check conditions? (Recommended: No - too expensive)
   - Handle missing resources? (Recommended: Remove from state)

2. **What happens when field becomes unknown?**
   - Option A: Set to unknown (accurate, may cause replacements)
   - Option B: Keep last value (stable, inaccurate)
   - Option C: Set to null (clear signal, loses info)
   - **Recommendation: Test empirically before deciding**

3. **Should we prevent cascading unknowns?**
   - Use private state flags to stabilize during transitions?
   - Or trust Terraform's unknown value handling?
   - **Recommendation: Start simple (trust Terraform), add complexity only if needed**

## Next Steps

1. Create test case for `depends_on` behavior with tainted wait resources
2. Run test against real Terraform to observe behavior
3. Update ADR-016 based on empirical findings
4. Implement Read() status refresh for field waits
5. Add drift detection test coverage
