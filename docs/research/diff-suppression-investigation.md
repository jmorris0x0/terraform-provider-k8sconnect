# Research: Diff Suppression for ignore_fields in terraform-plugin-framework

**Date:** 2025-10-06
**Researchers:** Engineering Team
**Objective:** Determine if terraform-plugin-framework supports conditional diff suppression based on `ignore_fields` attribute
**Outcome:** ❌ Not supported with current framework architecture

---

## Executive Summary

**Question:** Can we store complete K8s cluster state in Terraform state while suppressing diffs for paths matching `ignore_fields`?

**Answer:** No. terraform-plugin-framework does not provide mechanisms for conditional diff suppression within complex attributes.

**Key Findings:**
1. Framework has no equivalent to SDK v2's `DiffSuppressFunc`
2. Plan modifiers operate at attribute level, cannot suppress sub-paths within JSON
3. Semantic equality cannot access other attributes (like `ignore_fields`)
4. Terraform's consistency requirement prevents storing unfiltered state while hiding changes
5. **Our current approach (filter before storing) is the only viable solution**

---

## Background

### The Desired Pattern

```terraform
resource "k8sconnect_manifest" "example" {
  yaml_body = file("deployment.yaml")

  # Store complete cluster state
  cluster_state = computed  # Contains ALL fields from K8s

  # But only show diffs for non-ignored fields
  ignore_fields = [
    "spec.protocol",        # K8s default
    "status.*"              # Status fields
  ]
}
```

**Goal:** Honor Terraform's contract (state = complete reality) while providing clean UX (only show meaningful drift).

### Why This Matters

- **Terraform Contract (ADR-012):** State must show complete infrastructure reality
- **User Experience:** Don't overwhelm users with noise from fields they don't manage
- **Bootstrap UX:** Must not error on first apply when cluster doesn't exist
- **This is a make-or-break decision for the provider**

---

## Investigation Methods

1. **Code Analysis:** Examined our existing `ignore_fields` implementation
2. **Framework Research:** Studied terraform-plugin-framework APIs and documentation
3. **Community Research:** Reviewed GitHub issues and HashiCorp developer docs
4. **Pattern Analysis:** Evaluated alternative approaches and their feasibility

---

## Finding 1: No DiffSuppressFunc Equivalent

### What We Found

**From HashiCorp GitHub Issue #1030:**
> "DiffSuppressFunc use cases are replaced by custom type semantic equality. Terraform's data consistency rules prevent arbitrarily suppressing diffs."

**From Framework Documentation:**
> "The framework does not support arbitrary diff suppression. If plan differs from state, the difference must be shown to users."

### What This Means

- SDK v2's `DiffSuppressFunc` allowed providers to say "these values are different, but don't show it"
- Framework explicitly does NOT support this pattern
- Philosophy: Transparency over convenience

### Certainty Level

**95%+** - This is stated explicitly in official documentation and confirmed by maintainers.

---

## Finding 2: Plan Modifiers Operate at Attribute Level

### What We Found

Plan modifiers in terraform-plugin-framework work on entire attributes, not sub-paths within them.

**Example - What Works:**
```go
"my_attribute": schema.StringAttribute{
    Computed: true,
    PlanModifiers: []planmodifier.String{
        // Can suppress diff for entire attribute
        customPlanModifier(),
    },
}

func customPlanModifier() {
    // Can set: resp.PlanValue = req.StateValue
    // This suppresses diff for the ENTIRE attribute
}
```

**Example - What Does NOT Work:**
```go
"cluster_state": schema.StringAttribute{
    Computed: true,
    // Want: Suppress diffs for paths matching ignore_fields
    // Reality: Can only suppress diff for entire cluster_state
}
```

### Why This Matters

Our `cluster_state` would be a single JSON string containing the complete K8s object. We cannot selectively suppress parts of it - we can only suppress the entire thing or nothing.

### Certainty Level

**95%+** - Confirmed by framework API structure and our existing codebase patterns.

---

## Finding 3: Semantic Equality Cannot Access Other Attributes

### What We Found

**From GitHub Issue #887:**
> "Custom types with semantic equality cannot access other resource attributes during comparison."

**The Problem:**
```go
type ClusterStateValue struct {
    basetypes.StringValue
}

func (v ClusterStateValue) StringSemanticEquals(ctx context.Context, new basetypes.StringValuable) (bool, diag.Diagnostics) {
    // ❌ PROBLEM: Cannot access ignore_fields attribute here
    // No access to req.Plan, req.State, or any other attributes
    // Only have access to old and new values of THIS attribute

    // Cannot implement: "compare but ignore paths in ignore_fields"
    return reflect.DeepEqual(v, new), nil
}
```

### Why This Matters

Even if we use custom types with semantic equality (the framework's replacement for DiffSuppressFunc), we cannot access `ignore_fields` to know what to filter.

### Certainty Level

**95%+** - GitHub issue explicitly documents this limitation, and it's a fundamental constraint of the type system.

---

## Finding 4: Terraform's Consistency Requirement

### What We Found

**From Terraform Documentation:**
> "After apply, the provider must return state that matches what the plan indicated. If state differs from plan, Terraform assumes a provider bug and errors."

### The Impossible Sequence

If we try to store complete state while suppressing diffs:

1. **Plan Phase:**
   ```go
   // User has ignore_fields = ["spec.protocol"]
   // We suppress diff for protocol field
   plan.ClusterState = state.ClusterState  // Keep old value in plan
   ```

2. **Apply Phase:**
   ```go
   // K8s returns complete state including protocol
   actualState := k8sClient.Get(ctx, ...)
   data.ClusterState = toJSON(actualState)  // Store complete state
   ```

3. **Terraform's Validation:**
   ```
   Plan said: ClusterState = {...no protocol...}
   Apply returned: ClusterState = {...protocol: TCP...}
   Error: "Provider produced inconsistent result after apply"
   ```

### Why This Matters

We cannot "lie" to Terraform during plan by hiding fields, then return complete state during apply. Terraform will catch the inconsistency and error.

### Our Team Already Discovered This

**From ADR-009 (lines 84-120):**
> "Bug Investigation: 3-Hour Debug Session - inconsistent plan modifier behavior...The issue was that projection computed during Plan was different from projection computed during Apply...Solution: Filter ignore_fields identically in both phases."

**We already learned this lesson the hard way.**

### Certainty Level

**99%+** - This is a fundamental Terraform constraint, and we have direct experience hitting this error.

---

## Finding 5: Current Implementation Is The Only Viable Approach

### Our Current Pattern

```go
// During Plan
ignoreFields := getIgnoreFields(ctx, data)
filteredPaths := filterIgnoredPaths(allPaths, ignoreFields)
projection := projectFields(clusterState, filteredPaths)
data.ManagedStateProjection = toJSON(projection)

// During Apply
actualState := k8sClient.Get(ctx, ...)
ignoreFields := getIgnoreFields(ctx, data)  // Same filtering!
filteredPaths := filterIgnoredPaths(allPaths, ignoreFields)
projection := projectFields(actualState, filteredPaths)
data.ManagedStateProjection = toJSON(projection)
```

**Key Insight:** Filter BEFORE storing, not after. Plan and Apply use identical filtering logic.

### Why This Works

1. ✅ Plan expects filtered state
2. ✅ Apply returns filtered state
3. ✅ Plan ≡ Apply → No consistency errors
4. ✅ Ignored fields never enter state → No diffs for them
5. ✅ Works within framework constraints

### Why We Cannot Do Better

The framework simply does not provide APIs to:
- Store complete state in an attribute
- Selectively suppress diffs for sub-paths based on another attribute
- Access other attributes during semantic equality checks

### Certainty Level

**99%+** - Based on comprehensive research, framework limitations, and our existing working implementation.

---

## Alternative Approaches Evaluated

### Alternative 1: Separate Attributes

```go
"managed_state_projection": // Filtered, used for drift
"cluster_state_complete":   // Complete, no plan modifier
```

**Evaluation:**
- ✅ Can store complete state
- ❌ `cluster_state_complete` will STILL show changes in plan output
- ❌ Terraform shows changes for any computed attribute that changes
- ❌ No way to completely hide an attribute's changes

**Verdict:** Doesn't solve the problem.

### Alternative 2: Data Source for Complete State

```hcl
resource "k8sconnect_manifest" "example" {
  managed_state_projection = computed  # Filtered
}

data "k8sconnect_resource" "example_complete" {
  namespace = "default"
  name      = "example"

  full_state = computed  # Complete, read-only
}
```

**Evaluation:**
- ✅ Resource handles management (filtered)
- ✅ Data source provides complete state (no diffing)
- ✅ Clean separation of concerns
- ✅ Works within framework constraints
- ⚠️ Requires separate data source declaration

**Verdict:** This IS viable, but doesn't provide complete state in the resource itself.

### Alternative 3: Dynamic Type with Custom Comparison

**Evaluation:**
- ❌ Dynamic types don't support custom comparison logic
- ❌ Comparison is done by Terraform core, not provider

**Verdict:** Not supported by framework.

### Alternative 4: Resource-Level ModifyPlan with JSON Manipulation

```go
func (r *manifestResource) ModifyPlan(...) {
    // Filter cluster_state JSON, compare, conditionally preserve
    if filteredStatesEqual(plan, state, ignoreFields) {
        plan.ClusterState = state.ClusterState
    }
}
```

**Evaluation:**
- ✅ Technically possible
- ❌ Hits Terraform consistency requirement
- ❌ If we preserve state during plan, but apply returns updated state, Terraform errors
- ❌ **This is exactly what ADR-009 documented as the bug**

**Verdict:** Violates Terraform's consistency model.

---

## Implications for Terraform Contract Compliance

### The Conflict

**ADR-012 states:**
> "State must show complete infrastructure reality. All fields, even ones not managed by provider."

**Framework reality:**
> "Cannot store complete state while suppressing diffs for subsets of fields."

### The Impossible Requirements

1. **Terraform Contract:** Show all fields in state
2. **Framework Limitation:** Cannot suppress diffs for fields in state
3. **User Experience:** Must not overwhelm with noise
4. **Bootstrap UX:** Must not error on first apply

**You cannot satisfy all four with current framework architecture.**

### What Terraform's Contract Actually Means

After extensive analysis, the contract has a nuance:

**Strict Interpretation (ADR-012 initial version):**
> "State must contain ALL fields that exist in infrastructure, regardless of who manages them."

**Practical Interpretation (Considering Framework Limits):**
> "State must accurately represent what the provider manages. If provider uses field ownership (SSA), state represents owned fields. Drift detection applies to managed fields only."

**Key Question:** Does Terraform's contract mean:
- A) Show literally every field in the cluster (impossible with clean UX)
- B) Show fields the provider is responsible for managing (current approach)

### Other Providers' Approaches

**kubernetes (hashicorp/terraform-provider-kubernetes):**
- Uses complete objects in state
- Does NOT filter by field ownership
- Result: Users see drift for system defaults
- Community complaints about noise
- Many resources have `ignore_changes` meta-argument in examples

**kubectl (gavinbunney/terraform-provider-kubectl):**
- Uses `yaml_body` (sensitive) + `yaml_body_parsed`
- Shows diffs on parsed YAML
- Does NOT track individual field ownership
- Result: Coarse-grained drift detection

**Our Provider (k8sconnect):**
- Uses SSA field ownership
- Filters state to managed fields only
- Fine-grained drift detection
- Clean UX for multi-controller scenarios

**Industry Pattern:** No major provider successfully implements "complete state + clean diffs for subsets."

---

## Recommendations

### Recommendation 1: Keep Current Implementation (95% Confidence)

**Rationale:**
1. Works within framework constraints
2. Provides clean UX
3. Honors SSA field ownership semantics
4. ADR-009 documents why this is correct
5. No alternative approach satisfies all requirements

**What This Means:**
- State shows **managed fields only** (not complete cluster object)
- Drift detection applies to **managed fields only**
- Ignored fields never enter state (no diffs for them)
- **This is a pragmatic interpretation of Terraform's contract**

### Recommendation 2: Document Contract Interpretation (95% Confidence)

**Update ADR-012 to clarify:**
- Terraform's contract in theory: "Show complete infrastructure"
- Framework reality: "Cannot suppress diffs for stored fields"
- Pragmatic interpretation: "Show what provider is responsible for managing"
- For SSA-based providers: "State represents owned fields per managedFields"

**This reconciles the contract with technical reality.**

### Recommendation 3: Provide Complete State via Data Source (Optional)

**If users need access to complete cluster state:**
```hcl
data "k8sconnect_resource" "complete" {
  # Reference managed resource
  # Returns complete unfiltered state
}
```

**Benefits:**
- Users can access full state when needed
- Doesn't interfere with drift detection
- Clean separation: resource = management, data = observability

---

## Certainty Levels Summary

| Finding | Certainty | Basis |
|---------|-----------|-------|
| No DiffSuppressFunc equivalent | 95%+ | Official docs, maintainer statements |
| Plan modifiers are attribute-level | 95%+ | Framework API structure |
| Semantic equality can't access other attrs | 95%+ | GitHub issue #887 |
| Terraform consistency requirement | 99%+ | Official docs, our ADR-009 experience |
| Current implementation is only viable approach | 99%+ | Comprehensive research, framework limits |
| Contract must be interpreted pragmatically | 90%+ | No provider successfully implements strict interpretation |

---

## Open Questions

1. **Contract Philosophy:** Does "state = infrastructure" mean "literally all fields" or "fields provider is responsible for"?
   - Impact: Determines if current implementation violates contract
   - Decision: Needs provider author's philosophical stance

2. **Industry Best Practice:** Is there ANY Terraform provider that stores complete state while cleanly filtering diffs?
   - Research: None found in K8s ecosystem
   - Impact: If nobody does it, maybe it's not expected

3. **Future Framework Enhancement:** Will terraform-plugin-framework add diff suppression APIs?
   - Status: No indication from maintainers
   - Timeline: Unknown
   - Impact: Can't design around potential future features

---

## Conclusion

**The hard truth:** terraform-plugin-framework does not support the pattern of storing complete state while conditionally suppressing diffs for subsets of fields.

**Our current implementation** (filtering before storage) is the only approach that:
- Works within framework constraints
- Provides clean UX
- Avoids Terraform consistency errors
- Respects SSA field ownership

**The remaining decision:** How to interpret Terraform's contract given these technical constraints.

**Options:**
1. **Strict interpretation:** Must show all fields → Cannot satisfy with clean UX → Provider may not be viable
2. **Pragmatic interpretation:** Show managed fields → Current implementation is correct → Provider is viable

**Recommendation:** Adopt pragmatic interpretation, document rationale, and proceed with current approach.

---

## References

**Internal:**
- ADR-009: User Controlled Drift Exemption (ignore_fields)
- ADR-011: Concise Diff Format for Plan Output
- ADR-012: Terraform's Fundamental Contract
- ADR-001: Managed State Projection
- ADR-005: Field Ownership Strategy

**External:**
- terraform-plugin-framework GitHub Issue #1030: DiffSuppressFunc alternative
- terraform-plugin-framework GitHub Issue #887: Semantic equality limitations
- HashiCorp Developer Docs: Plan Modification
- terraform-plugin-framework-jsontypes: Semantic equality examples

**Code:**
- `internal/k8sconnect/resource/manifest/manifest.go`
- `internal/k8sconnect/resource/manifest/plan_modifier.go`
- `internal/k8sconnect/resource/manifest/projection.go`
- `internal/k8sconnect/resource/manifest/crud_common.go`
