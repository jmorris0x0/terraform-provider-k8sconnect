# ADR-012: Terraform's Fundamental Contract

## Status
Accepted - Foundational Principle

## Decision Made (2025-10-09)

**k8sconnect adopts the Pragmatic Interpretation** of Terraform's contract:

- **State shows managed fields only** (per SSA managedFields), not complete cluster object
- **Drift detection applies to managed fields** - fields the provider is responsible for
- **Framework limitations make strict interpretation technically infeasible** (see Framework Limitations section below)
- **This aligns with Kubernetes SSA semantics** and industry practice

See ADR-013 for details on why the yaml_body sensitivity approach (which would have required strict interpretation) was abandoned.

## Context

Terraform providers face constant trade-offs between UX, performance, accuracy, and maintainability. When making these trade-offs, we need a clear, immutable principle that guides all decisions.

**This ADR establishes the fundamental contract that MUST NOT be violated, regardless of other considerations.**

## The Contract

### Terraform's Promise to Users

```
HCL (desired state) + State (actual reality) = Truth

When they differ, the user MUST be informed.
```

**The contract has three parts:**

1. **HCL declares desired state**
   - What you write in your .tf files is what you want
   - This is the source of truth for intent

2. **State reflects actual reality**
   - What's actually running in the managed infrastructure
   - This is the source of truth for current state

3. **Drift detection is mandatory**
   - When HCL ≠ State, Terraform MUST show the user
   - The user makes the choice: update HCL, update infrastructure, or explicitly ignore
   - **The user MUST NOT be left blind to discrepancies**

### Provider Obligations

Given this contract, a Terraform provider MUST:

1. **Represent complete infrastructure state**
   - If a field/property exists in the managed resource, it must appear in Terraform state
   - Even if the provider didn't set it (system defaults, mutations, etc.)
   - Even if another controller/system owns it
   - **No invisible fields**

2. **Detect all drift**
   - If infrastructure state ≠ HCL, show it as drift
   - User can then choose to:
     - Update HCL to match reality (adopt the field)
     - Update infrastructure to match HCL (force the value)
     - Explicitly ignore (via provider-specific mechanisms)
   - **No silent discrepancies**

3. **Enable explicit choices**
   - User must be able to see what's different
   - User must be able to choose how to handle it
   - **No invisible behavior**

## Anti-Patterns Providers Must Avoid

### ❌ Anti-Pattern 1: "We don't manage it, so we don't show it"

**Wrong:**
```terraform
# User's HCL
resource "provider_thing" "example" {
  property_a = "value"
  # property_b not specified
}

# Infrastructure reality has property_b set by system defaults
# Provider state shows only property_a
# User has no idea property_b exists
```

**Why it's wrong:** User can't make informed decisions about fields they don't know exist.

**What if property_b changes?** User never knows, can't react.

### ❌ Anti-Pattern 2: "Ownership/management tracking means selective visibility"

**Wrong thinking:**
- "We only set property_a, so that's all we track in state"
- "Property_b is owned by the system, not our concern"
- "Internal management metadata determines what we show users"

**Why it's wrong:** Internal management/ownership is an **implementation detail** for the provider. It's not Terraform's contract. Terraform's contract is: **show what's actually running**.

### ❌ Anti-Pattern 3: "UX/convenience over correctness"

**Wrong:**
- "Let's hide system defaults to reduce noise"
- "Plan errors are bad UX, so skip validation steps"
- "Users won't notice if we don't show system mutations"

**Why it's wrong:** Short-term UX gain leads to long-term trust loss. Users discover the hidden behavior eventually, and then they **never trust the provider again**.

## The Correct Approach

### ✅ Pattern 1: Complete State Representation

**State must include ALL fields that exist in infrastructure**, regardless of who set them:

```terraform
# User's HCL
resource "provider_thing" "example" {
  property_a = "user-value"
  # property_b not specified
}

# Infrastructure reality
{
  property_a: "user-value"
  property_b: "system-default"    # System added this
  property_c: "auto-generated"    # System added this
}

# State MUST show:
{
  property_a = "user-value"
  property_b = "system-default"    # Even though user didn't set it
  property_c = "auto-generated"    # Even though user didn't set it
}
```

### ✅ Pattern 2: Explicit Drift Detection

When infrastructure ≠ HCL, **always show the difference**:

```terraform
# Next plan shows:
~ resource "provider_thing" "example" {
    property_a = "user-value"
  + property_b = "system-default"    # Exists in infrastructure but not in your HCL
  + property_c = "auto-generated"    # Exists in infrastructure but not in your HCL
  }
```

User now has information to make a choice:
1. Add to HCL: `property_b = "system-default"` (explicitly manage it)
2. Use provider-specific ignore mechanism (if available)
3. Accept seeing it in every plan (acknowledge it exists)

### ✅ Pattern 3: Management Metadata as Supplemental Info, Not a Gate

If a provider tracks management/ownership metadata internally:

**Use it to:**
- Provide additional context to users (who owns what)
- Warn about conflicts before they happen
- Guide users toward proper resolution strategies

**Never use it to:**
- Hide fields from state
- Skip drift detection
- Determine visibility to users

**Always show the complete infrastructure state**, regardless of internal management tracking.

### ✅ Pattern 4: Explicit Ignore After Drift Detection

**The "explicit ignore" option respects the contract** when used correctly:

```terraform
# Step 1: User creates resource
resource "provider_thing" "example" {
  property_a = "user-value"
  # property_b not specified
}

# Step 2: Infrastructure runs, another controller sets property_b
# State now shows:
{
  property_a = "user-value"
  property_b = "controller-value"  # Added by another controller
}

# Step 3: Next plan shows DRIFT
~ resource "provider_thing" "example" {
    property_a = "user-value"
  + property_b = "controller-value"  # Another controller set this!
  }

# Step 4: User makes EXPLICIT choice
resource "provider_thing" "example" {
  property_a = "user-value"

  # Option A: Explicitly ignore (let other controller own it)
  ignore_fields = ["property_b"]

  # Option B: Explicitly take ownership (ownership forced automatically)
  property_b = "my-value"
  # Note: Provider uses SSA force=true, shows warning about ownership override

  # Option C: Adopt current value (accept what's there)
  property_b = "controller-value"
}
```

**Why this respects the contract:**

1. ✅ **User sees the drift first** - they're informed of the discrepancy
2. ✅ **User makes explicit choice** - it's not hidden or automatic
3. ✅ **No surprises** - after the choice, behavior is documented and expected
4. ✅ **User can change their mind** - remove `ignore_fields` to see drift again

**The critical difference:**

| Approach | Contract Status |
|----------|----------------|
| **Never show fields another controller owns** | ❌ Violates - user never informed |
| **Show drift, let user explicitly ignore** | ✅ Respects - user informed, then chooses |

**Ownership conflict workflow:**

```
Infrastructure reality changes
         ↓
Provider detects drift
         ↓
User sees the drift in plan
         ↓
User evaluates options:
  • Is this expected? (another controller managing it)
  • Is this a problem? (conflict with my intent)
  • What do I want? (ignore, take ownership, or adopt)
         ↓
User makes explicit choice in HCL
         ↓
No more surprises - choice is documented
```

This workflow **honors the "no surprises" principle** because:
- Drift detection triggered the user awareness
- User made an informed, explicit decision
- Decision is visible in HCL (documented intent)
- User can revisit the decision anytime

## The Hard Truth About Honoring the Contract

### What Providers Must Accept

**If we honor the contract:**

1. **State will show fields user didn't specify** ✅ This is correct
   - System defaults
   - Auto-generated values
   - Fields managed by other systems
   - **User NEEDS to see these to make informed decisions**

2. **Plans may show "drift" for fields user doesn't explicitly manage** ✅ This is correct
   - User can then explicitly handle them (adopt or ignore)
   - **Explicit is better than implicit**

3. **First plan/refresh after CREATE may show unexpected fields** ✅ This is correct
   - State reflects complete infrastructure reality
   - Plan shows: "infrastructure has fields your HCL doesn't"
   - User decides: add to HCL, ignore, or accept the plan noise
   - **Education moment, not a bug**

### What Providers Cannot Do

**These violate the contract and must not be done:**

1. ❌ **Hiding fields from state** because "we don't manage them"
2. ❌ **Skipping drift detection** for fields "owned by other systems"
3. ❌ **Incomplete state representation** to "reduce noise" or "improve UX"
4. ❌ **Different visibility** between CREATE and UPDATE operations
5. ❌ **UX improvements** that sacrifice visibility of truth

## Decision

**The fundamental contract is sacred and inviolable.**

All provider design decisions must be evaluated against this contract:
- Does it show complete infrastructure state?
- Does it detect all drift?
- Does it enable explicit user choices?

If the answer to any is "no," the design is wrong, regardless of how good the UX seems.

### When Trade-offs Are Necessary

If a provider faces impossible trade-offs:

1. **Contract compliance is non-negotiable** - must show complete state
2. **Evaluate other corners** - can we sacrifice UX? Performance? Features?
3. **If no solution exists** - the feature may not be implementable
4. **Document clearly** - explain what users will experience

**Never sacrifice the contract for convenience.**

---

## Framework Limitations and Pragmatic Interpretation

### When Framework Cannot Support Strict Contract (Critical Update)

**Discovery Date:** 2025-10-06
**Research:** See `docs/research/diff-suppression-investigation.md`

After comprehensive investigation, we discovered that **terraform-plugin-framework does not provide APIs to fully satisfy the strict contract interpretation while maintaining clean UX**.

### The Specific Limitation

**What the contract requires (strict interpretation):**
- Store complete infrastructure state (ALL fields)
- Detect drift on all fields
- Allow user to explicitly ignore specific fields
- Only show drift for non-ignored fields (clean UX)

**What terraform-plugin-framework supports:**
- Store any state you want ✅
- Detect drift on stored state ✅
- Allow user configuration ✅
- **❌ Suppress diffs for subsets of stored state - NOT SUPPORTED**

**The gap:** Framework has no API for "store field X, but don't show diffs for it if ignore_fields says so."

### Why This Matters

**The impossible sequence:**
1. Store complete state including `spec.protocol`
2. User sets `ignore_fields = ["spec.protocol"]`
3. Field changes in cluster: `protocol: TCP → UDP`
4. **Cannot suppress this diff** - framework will show it
5. User sees noise they explicitly asked to ignore

**Framework facts (95%+ certainty):**
- No equivalent to SDK v2's `DiffSuppressFunc`
- Plan modifiers work at attribute level, not sub-paths
- Semantic equality cannot access other attributes
- Custom types cannot read `ignore_fields` during comparison

### Pragmatic Interpretation for SSA-Based Providers

**Given framework limitations, for providers using Server-Side Apply (SSA):**

**Strict interpretation (ideal, but framework-blocked):**
> "State must contain ALL fields that exist in infrastructure"

**Pragmatic interpretation (framework-compatible):**
> "State must contain all fields the provider is responsible for managing, as determined by the infrastructure's field ownership mechanism (managedFields in K8s)"

### Rationale for Pragmatic Interpretation

1. **Technical feasibility**: Can be implemented with existing framework APIs
2. **SSA semantics**: Kubernetes already has a field ownership model (managedFields)
3. **User control**: `ignore_fields` lets users explicitly release ownership
4. **Drift detection**: Still works for fields provider actually manages
5. **Industry practice**: No major K8s provider achieves strict interpretation with clean UX

### What This Means for k8sconnect Provider

**Current implementation:**
- State shows fields provider manages via SSA (managedFields)
- Does NOT show fields owned by other controllers
- Does NOT show K8s defaults user didn't specify
- User can control this via `ignore_fields`

**Is this a contract violation?**

**Strict interpretation:** Yes - not showing all fields
**Pragmatic interpretation:** No - showing all managed fields

### The Modified Contract for SSA-Based Providers

**Adapted contract:**

```
HCL (desired state) + State (managed fields) = Truth

When managed fields differ, user MUST be informed.
```

**The three parts become:**

1. **HCL declares desired state for managed fields**
   - What you write is what you want to manage
   - Fields you don't write may be managed by other controllers

2. **State reflects actual managed fields**
   - Shows fields provider actually manages (per SSA managedFields)
   - Doesn't show fields other controllers own
   - User controls management via explicit field specifications

3. **Drift detection is mandatory for managed fields**
   - When HCL ≠ State for managed fields, show user
   - When another controller takes a field, show ownership change
   - User makes choice: ignore field, take back ownership, or fix HCL

### User Experience with Pragmatic Interpretation

**Scenario: K8s default not shown**

```terraform
resource "k8sconnect_manifest" "service" {
  yaml_body = <<-YAML
    spec:
      ports:
      - port: 80
        # protocol not specified
  YAML
}
```

**Strict contract:** State should show `protocol: TCP`, user sees drift, must ignore it
**Pragmatic contract:** State doesn't show `protocol` (we don't manage it per SSA)

**If protocol changes TCP → UDP:**
- Strict: Would show drift (if framework allowed suppression)
- Pragmatic: Doesn't show drift (not our field)

**Is user blind to this change?**
- Yes, for that specific field
- But: They never asked to manage it
- And: They can manage it by adding to YAML

**Trade-off:** User might miss changes to fields they didn't know existed, but UX is clean and they can opt-in to managing any field.

### When Pragmatic Interpretation Is Acceptable

**Acceptable when:**
1. ✅ Infrastructure has explicit field ownership mechanism (K8s SSA)
2. ✅ User can see ownership information (`field_ownership` attribute)
3. ✅ User can take ownership by explicitly specifying fields
4. ✅ User can release ownership via `ignore_fields`
5. ✅ Framework technically cannot support strict interpretation with clean UX
6. ✅ Documentation clearly explains the behavior
7. ✅ Other mature providers use similar approach

**Not acceptable when:**
1. ❌ Hiding drift for convenience (not framework limitation)
2. ❌ No way for user to take ownership of hidden fields
3. ❌ No visibility into who owns what
4. ❌ Undocumented or surprising behavior

### Documentation Requirements

**If using pragmatic interpretation, MUST document:**

1. "This provider uses Kubernetes Server-Side Apply (SSA) for field ownership"
2. "State shows fields you manage (via your YAML) and fields you haven't released"
3. "To manage a K8s default, add it to your YAML explicitly"
4. "To release ownership of a field, add to ignore_fields"
5. "Field ownership changes are shown via field_ownership attribute"
6. "This is a framework limitation, not a design choice"

### Open Question for Provider Authors

**The philosophical question remains:**

Is pragmatic interpretation acceptable, or does failure to achieve strict interpretation mean the provider should not be built?

**Arguments for pragmatic:**
- Framework limitations are real
- SSA provides legitimate field ownership model
- Users maintain control via explicit specifications
- Industry practice supports this approach
- Provides value despite limitation

**Arguments for strict or nothing:**
- Contract is fundamental to Terraform
- Users should never be blind to infrastructure changes
- Even framework limitations don't justify violation
- Better to wait for framework enhancement
- Trust erosion if contract not honored

**This decision is philosophical, not technical. Research proves pragmatic is technically necessary given framework. Whether it's philosophically acceptable is a judgment call.**

## Implementation Guidance

### ❌ Wrong: Selective State Based on Internal Metadata

```go
// DON'T DO THIS - hiding fields based on ownership/management
for field in resource.fields {
    if field.managedBy != "our-provider" {
        continue // Skip fields we don't "own"
    }
    state[field.name] = field.value
}
```

**Violates contract** - hides infrastructure reality from user.

### ✅ Right: Complete State, Supplemental Metadata

```go
// DO THIS - show everything, provide context
for field in resource.fields {
    state[field.name] = field.value  // Always show the field

    // Optionally provide management context
    if field.hasMetadata() {
        metadata[field.name] = field.managedBy  // Who owns it
    }
}

// User can now decide:
// - Add to HCL to manage it explicitly
// - Use provider ignore mechanism (if available)
// - Accept seeing it in plans
```

### ❌ Wrong: Incomplete State for "Better UX"

```go
// DON'T DO THIS - showing only provider inputs
func buildState(userInput, infrastructureActual) {
    return userInput  // "Cleaner" diff, but incomplete
}
```

**Violates contract** - state doesn't reflect actual infrastructure.

### ✅ Right: Complete State, Always

```go
// DO THIS - state reflects reality
func buildState(userInput, infrastructureActual) {
    // Read actual infrastructure state after CREATE/UPDATE
    actual := fetchCompleteState()
    return actual  // Show complete infrastructure reality
}
```

## Conclusion

**The contract is simple:** Show users the truth about their infrastructure.

- **HCL** = what you want
- **State** = what you have
- **Drift** = the difference

Everything else - UX, performance, implementation details - is secondary to this truth.

When in doubt, ask: "Does this help or hide the user from reality?"

**If it hides, it's wrong.**
**If it shows, evaluate the trade-offs.**
**Never hide.**

---

## The Framework Limitation Exception (2025-10-06 Update)

**The Hard Reality:**

After comprehensive research (see `docs/research/diff-suppression-investigation.md`), we discovered:

1. **terraform-plugin-framework does not support** storing complete state while selectively suppressing diffs
2. **The only technically feasible options** given framework constraints:
   - Store complete state → Users see ALL diffs (overwhelming noise)
   - Store filtered state → Clean UX, but not "complete" state

**The Decision Point:**

Provider authors must choose:

### Option A: Strict Interpretation (Principle Over Pragmatism)

**Choice:** "If I cannot honor the strict contract, I should not build the provider."

**Rationale:**
- Terraform's contract is fundamental
- Users trust state to represent complete reality
- Framework limitations don't justify contract violation
- Better to wait for framework enhancement
- Provider that hides reality erodes user trust

**Outcome:** Don't build provider until framework supports it, or accept overwhelming diff noise.

### Option B: Pragmatic Interpretation (Value Within Constraints)

**Choice:** "I will interpret 'complete state' as 'complete managed state' for SSA-based providers."

**Rationale:**
- Framework limitations are real and documented
- Kubernetes SSA provides legitimate field ownership model
- Users maintain control via explicit field specifications
- Industry practice: No K8s provider achieves strict interpretation with clean UX
- Provides significant value despite limitation
- Fully documented and transparent about behavior

**Outcome:** Build provider with pragmatic interpretation, document thoroughly, accept philosophical debate.

### The Philosophical Question

**Is pragmatic interpretation acceptable?**

**Technical answer:** Pragmatic is the ONLY option (strict is technically impossible)

**Philosophical answer:** This is a judgment call for each provider author

**For k8sconnect specifically:**

**If you choose strict interpretation:**
- Provider should not be released in current form
- Wait for framework to add diff suppression APIs
- Or accept overwhelming diff noise (bad UX, but contract-compliant)

**If you choose pragmatic interpretation:**
- Current implementation is correct given constraints
- Document behavior clearly in user-facing docs
- Update ADR-012 to reflect this is a conscious choice
- Accept that some will view this as a contract violation
- Provide `field_ownership` attribute for transparency

**The maintainability option referenced in conversation:**

This refers to Option 2 in ADR-011's triangle:
- **Bootstrap UX + Complete State** (no clean UX)
- Users would see drift for every K8s default and system field
- `ignore_fields` cannot suppress these diffs (framework limitation)
- Plan output would be overwhelmed with noise
- Technically honors strict contract, but UX may be unusable

**Certainty levels:**
- Technical impossibility of Option 3 (Complete + Clean): **99%+**
- Framework limitation is real: **99%+**
- Maintainability option provides bad UX: **95%+**
- Whether pragmatic interpretation is philosophically acceptable: **Subjective - cannot be quantified**

**Next Steps:**

1. Decide philosophical stance (strict vs pragmatic)
2. If strict: Evaluate maintainability option (Option 2) despite bad UX
3. If pragmatic: Accept current implementation, document thoroughly
4. Update user-facing documentation with chosen interpretation
5. Be transparent about limitations and trade-offs

**The ball is in the provider author's court.** Technology has done its part; research is complete. The remaining choice is philosophical: principle or pragmatism?
