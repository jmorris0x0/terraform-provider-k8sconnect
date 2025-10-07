# ADR-011: Concise Diff Format for Plan Output

## Status
PARTIALLY IMPLEMENTED - Option 4 Required (Contract Compliance)

## Implementation Updates (2025-10-06)

### CRITICAL: Current Implementation Violates Terraform Contract

**Problem discovered:** The current implementation (Option F variant) filters state by managedFields, hiding K8s defaults and other controller fields from users. This violates Terraform's fundamental contract (see ADR-012).

**Correct Solution:** Option 4 - Complete State + Bootstrap UX + Maintainability

### Current Implementation (Option F with String Format - INCOMPLETE)

Partially implemented Option F concept (yaml_body sensitive + managed_state_projection as single source of truth) but **MISSING critical requirement**: State must show ALL fields from cluster, not filtered by managedFields.

**Key Decision: Skip Dry-Run for CREATE Operations**

After discovering that bootstrap scenarios cause inconsistent plan errors, we made the critical decision to **never use dry-run for CREATE operations**. This preserves the first-use experience while maintaining all core value propositions.

**The Problem:**
1. During `terraform plan`: Cluster doesn't exist → uses yaml fallback → projection without K8s defaults (e.g., no `protocol: TCP`)
2. During `terraform apply`: Cluster now exists (created by dependency) → Terraform re-calls plan modifier → would use dry-run → projection WITH K8s defaults
3. Result: Projection changed between plan and apply → "inconsistent plan" error → **fatal UX failure on first use**

**The Solution:**
- CREATE operations: Always use parsed yaml as projection (never dry-run)
- UPDATE operations: Always use server-side dry-run (accurate predictions - core value prop!)
- This ensures consistent plan/apply behavior during bootstrap while preserving accuracy where it matters most

**Trade-offs Accepted:**

✅ **What we preserve:**
- Clean UX: Only managed_state_projection visible (no dual-diff confusion)
- First-use success: No errors during bootstrap scenarios
- UPDATE accuracy: Dry-run predictions for updates (core value prop)
- SSA semantics: Only track fields we own (managedFields)
- Terraform contract: HCL = desired state, drift detected for owned fields
- Maintenance-free: No hardcoded K8s defaults for CRDs

⚠️ **What we accept:**
- CREATE projections don't show K8s defaults during plan
- Admission controller mutations during CREATE discovered on first refresh (not in plan)
- User sees what they wrote, not what K8s will add as defaults

**The ACTUAL Impossible Triangle: Bootstrap UX vs Complete State vs Clean UX**

**CRITICAL DISCOVERY:** After deep research into terraform-plugin-framework (see `docs/research/diff-suppression-investigation.md`), we discovered that Option 4 is **NOT technically feasible** with current framework architecture.

**Initial Hope (Option 4 - PROVEN INFEASIBLE):**

We hoped to achieve:
- Bootstrap UX: No errors on first use ✅
- Complete State: Store ALL cluster fields ✅
- Clean UX: Suppress diffs for ignored fields ❌ **NOT SUPPORTED BY FRAMEWORK**

**Why Option 4 Cannot Work:**

1. **terraform-plugin-framework has no diff suppression API**
   - No equivalent to SDK v2's `DiffSuppressFunc`
   - Plan modifiers operate at attribute level, cannot suppress sub-paths within JSON
   - Semantic equality cannot access other attributes (like `ignore_fields`)

2. **Terraform's consistency requirement prevents it**
   - If we store complete state (all fields)
   - But suppress diffs during plan (hide ignored fields)
   - Apply returns complete state (including ignored fields)
   - Terraform detects: "Plan said X, Apply returned Y" → Error

3. **We already learned this lesson (ADR-009)**
   - 3-hour debugging session documented the issue
   - "Provider produced inconsistent result after apply"
   - Solution: Filter identically in Plan and Apply phases
   - **Cannot filter during plan but store complete during apply**

**Research Certainty: 99%+** - Based on:
- Official framework documentation
- GitHub issues from maintainers
- Our own experience (ADR-009)
- Comprehensive investigation of all alternatives

**See:** `docs/research/diff-suppression-investigation.md` for complete findings.

---

**The REAL Triangle (All Technically Feasible Options):**

```
        Bootstrap UX
       (No errors on first use)
              /\
             /  \
            /    \
           /      \
          /        \
         /          \
        /____________\
  Complete State    Clean UX
(Show all fields)  (Suppress ignored diffs)
```

**Framework Reality: Pick any TWO**

1. **Bootstrap UX + Clean UX** (CURRENT CHOICE)
   - ✅ No errors during first use
   - ✅ No noise from ignored fields
   - ❌ State filtered to managed fields (not complete cluster object)
   - **Pragmatic interpretation of Terraform's contract**

2. **Bootstrap UX + Complete State** (Rejected - Bad UX)
   - ✅ No errors during first use
   - ✅ State shows all fields
   - ❌ Users see drift for every K8s default and system field
   - ❌ ignore_fields cannot suppress diffs (framework limitation)
   - ❌ Plan output overwhelmed with noise

3. **Complete State + Clean UX** (Impossible with Current Framework)
   - ✅ State shows all fields
   - ✅ No noise from ignored fields
   - ❌ **Framework does not support this pattern**
   - ❌ Would require APIs that don't exist

**Key Insight:** The framework's architecture makes Option 3 impossible. We must choose between complete state (noisy UX) or filtered state (clean UX).

---

**Previous Options (Based on False Dichotomy):**

1. **Bootstrap UX + Maintainability, SACRIFICE Contract** (PREVIOUS CHOICE - WRONG)
   - ✅ No errors during first use
   - ✅ No maintenance burden for CRDs
   - ❌ CREATE projections miss K8s defaults/mutations
   - ❌ **State filtered by managedFields (CONTRACT VIOLATION)**

2. **Bootstrap UX + CREATE Accuracy** (Rejected - Unmaintainable)
   - ✅ No errors during first use
   - ✅ Show accurate K8s defaults in CREATE plans
   - ❌ Requires hardcoding defaults for all K8s types and CRDs (unmaintainable)

3. **CREATE Accuracy + Maintainability** (Rejected - Bootstrap Errors)
   - ✅ Accurate CREATE projections via dry-run
   - ✅ No hardcoded defaults
   - ❌ Inconsistent plan errors during bootstrap (fatal for adoption)

**Why Current Implementation (Option 1) Is The Pragmatic Choice:**

**After discovering Option 4 is technically infeasible**, we must accept Option 1 with its trade-offs:

1. **Bootstrap UX Preserved**: ✅ No inconsistent plan errors during first apply. Critical for adoption.

2. **Clean UX**: ✅ No noise from K8s defaults and other controller fields. Users see only meaningful drift.

3. **Maintainability**: ✅ No hardcoded K8s defaults. Works for any K8s resource type or CRD.

4. **SSA Semantics**: ✅ Aligns with Kubernetes Server-Side Apply field ownership model. State shows what provider manages.

5. **User Control**: ✅ Users can take ownership (add to YAML) or release ownership (ignore_fields) of any field.

**Trade-off Accepted:**

❌ **State does NOT show complete cluster object** - Shows managed fields only (per SSA managedFields).

**Is this acceptable?**

- **Strict contract interpretation (ADR-012 original):** No - violates requirement to show all fields
- **Pragmatic interpretation (ADR-012 updated):** Yes - framework limitations make strict interpretation technically infeasible
- **See ADR-012 "Framework Limitations and Pragmatic Interpretation" section for full analysis**

**This is the only approach that:**
- Works within framework constraints (proven)
- Provides acceptable UX (clean diffs)
- Avoids bootstrap errors (critical)
- Respects SSA field ownership (K8s semantics)

**Current Implementation (Option 1 - Pragmatic Approach):**

1. **yaml_body marked sensitive** - ✅ Hidden from plan output, users review changes in git
2. **CREATE operations skip dry-run for projection** - ✅ Always use parsed yaml as projection (plan_modifier.go:51-53, 217-220)
3. **UPDATE operations use dry-run** - ✅ Server-side dry-run for accurate predictions (plan_modifier.go:297+)
4. **String format retained** - ✅ Hierarchical JSON more readable than flattened Map for CREATE/DESTROY operations
5. **Fixed field_ownership null error** - ✅ Added updateFieldOwnershipData call after CREATE (crud.go:72)
6. **State filtered to managedFields** - ✅ Only stores fields provider manages via SSA

**Key Implementation Detail:**

State is filtered to show only managed fields (per SSA managedFields). This is:
- ❌ A violation of strict contract interpretation (ADR-012 original)
- ✅ The only technically feasible approach given framework limitations
- ✅ A pragmatic interpretation aligned with SSA semantics (ADR-012 updated)

Files implementing this:
- `crud.go` - Reads cluster state
- `crud_common.go` - Filters to managedFields before storing
- `plan_modifier.go` - Uses managed projection for drift detection
- `projection.go` - Implements filtering logic based on managedFields and ignore_fields

**Why not store complete state?**

Research proved (99%+ certainty) that storing complete state while suppressing diffs for ignored fields is not supported by terraform-plugin-framework. See `docs/research/diff-suppression-investigation.md` for complete analysis.

**Implementation files:**
- `internal/k8sconnect/resource/manifest/manifest.go:110` - Added Sensitive: true to yaml_body
- `internal/k8sconnect/resource/manifest/plan_modifier.go:51-53` - Force connectionReady=false for CREATE operations
- `internal/k8sconnect/resource/manifest/plan_modifier.go:217-220` - Skip dry-run for CREATE in executeDryRunAndProjection
- `internal/k8sconnect/resource/manifest/plan_modifier.go:270-294` - calculateProjection uses parsed yaml for CREATE
- `internal/k8sconnect/resource/manifest/crud.go:72` - Call updateFieldOwnershipData after CREATE

**Results:**
- **Bootstrap scenario**: Works perfectly, no inconsistent plan errors ✅
- **CREATE projection**: Shows parsed yaml (cluster doesn't exist, best available) ✅
- **CREATE state**: Shows COMPLETE cluster reality after apply (contract compliance) ✅
- **UPDATE**: Shows server-side dry-run projection (accurate, includes K8s mutations) ✅
- **Single diff**: Only managed_state_projection shown, no dual-diff confusion ✅
- **Readable format**: Hierarchical JSON, not flattened paths ✅
- **Drift detection**: Shows ALL fields including K8s defaults - user uses ignore_fields to handle ⚠️

### User Experience: What Friction Will Users Encounter?

**Scenario 1: K8s Defaults Shown After CREATE (Contract-Compliant Behavior)**

```terraform
# User's YAML - doesn't specify protocol
resource "k8sconnect_manifest" "service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    spec:
      ports:
      - port: 80
        name: http
  YAML
}
```

**What happens:**
1. **Plan phase (CREATE)**: Shows projection WITHOUT `protocol` field (cluster doesn't exist, using yaml fallback)
2. **Apply phase**: Resource created, K8s adds `protocol: TCP` as default
3. **State after apply**: Shows COMPLETE cluster state including `protocol: TCP` (CONTRACT COMPLIANCE)
4. **First plan/refresh**: Shows DRIFT - state has protocol, HCL doesn't

```terraform
~ resource "k8sconnect_manifest" "service" {
    ~ managed_state_projection = jsonencode({
        ~ spec = {
            ~ ports = [
                ~ {
                    + protocol = "TCP"  # K8s added this as default
                    # ... other fields
                  }
              ]
          }
      })
  }
```

**User reaction:** ❓ "Why is terraform showing drift? I didn't change anything!"

**Answer (from documentation):**
- This is correct behavior (ADR-012)
- State shows complete infrastructure reality, including K8s defaults
- You have three choices:
  1. Add `protocol: TCP` to your YAML (explicitly manage it)
  2. Add to `ignore_fields: ["spec.ports[*].protocol"]` (explicitly ignore it)
  3. Accept seeing it in every plan (acknowledge it exists)

**If user ignores it:**
```terraform
resource "k8sconnect_manifest" "service" {
  yaml_body = <<-YAML
    # ... same YAML
  YAML
  ignore_fields = ["spec.ports[*].protocol"]
}
```

**What if protocol changes to UDP after ignoring?**
- We DON'T detect this as drift (user explicitly ignored it)
- **This is correct** - user made explicit choice to not manage this field
- User can remove from ignore_fields anytime to start tracking it again

**Scenario 2: User Explicitly Specifies Defaults**

```terraform
resource "k8sconnect_manifest" "service" {
  yaml_body = <<-YAML
    spec:
      ports:
      - port: 80
        protocol: TCP  # Explicitly specified
  YAML
}
```

**What happens:**
1. **Plan phase**: Shows projection WITH `protocol: TCP` (user specified it)
2. **Apply phase**: We send it, K8s accepts it, we own it (in managedFields)
3. **Something changes it to UDP**: Next plan shows drift!

**User reaction:** ✅ "Perfect, terraform detected the change to my field."

**Scenario 3: Admission Controller Mutations During CREATE**

```terraform
resource "k8sconnect_manifest" "pod" {
  yaml_body = <<-YAML
    spec:
      containers:
      - name: app
        image: nginx:1.21
        # User specifies no securityContext
  YAML
}
```

**What happens:**
1. **Plan phase**: Shows projection without securityContext (user didn't specify it)
2. **Apply phase**: Admission controller adds `securityContext: {runAsNonRoot: true}`
3. **First refresh**: Projection updates to show securityContext (we now own it - it's in managedFields)

**User reaction:** ❓ "Wait, why does the projection show securityContext now? I didn't add it to my YAML!"

**Explanation needed:**
- Admission controller mutated your resource during CREATE
- The mutated fields became part of your managedFields
- Next apply will send them (and might conflict with admission controller)
- **Recommended action:** Add the fields to your YAML to match reality, or add to `ignore_fields` to release ownership

**Scenario 4: Projection Changes After First Apply (Bootstrap)**

```terraform
# During terraform plan (cluster doesn't exist yet)
+ managed_state_projection = jsonencode({
    apiVersion = "apps/v1"
    kind = "Deployment"
    spec = {
      replicas = 3
      # ... user's fields only
    }
  })

# After terraform apply (cluster now exists)
# First refresh shows exact same projection
~ managed_state_projection = jsonencode({
    apiVersion = "apps/v1"
    kind = "Deployment"
    spec = {
      replicas = 3
      # ... still just user's fields
    }
  })
```

**User reaction:** ✅ "Projection stayed consistent - good!"

**BUT if admission controller mutated:**
```terraform
# After apply, first refresh
~ managed_state_projection = jsonencode({
    spec = {
      replicas = 3
      template = {
        spec = {
+         securityContext = { runAsNonRoot = true }
        }
      }
    }
  })
```

**User reaction:** ❓ "Why did the projection change? I didn't modify my YAML!"

**Explanation needed:** Admission controller added this during CREATE. You now own it. Either:
1. Add it to your YAML (recommended - makes desired state explicit)
2. Add to `ignore_fields` (if you want admission controller to manage it)

### Documentation Requirements for Users

To minimize confusion, we must document:

1. **State shows complete infrastructure reality (Terraform Contract)**:
   - "State reflects ALL fields in the cluster, even ones you didn't specify"
   - "This includes K8s defaults, admission controller mutations, and other controller changes"
   - "This is Terraform's contract: state = infrastructure reality, always"

2. **CREATE behavior**:
   - "During CREATE plan: Shows what you wrote (cluster doesn't exist yet)"
   - "After CREATE apply: State populated with complete cluster reality"
   - "First plan after CREATE: May show drift for K8s defaults you didn't specify"
   - "This is expected - use ignore_fields to handle fields you don't want to manage"

3. **UPDATE behavior**:
   - "During UPDATE plan: Shows accurate server-side dry-run predictions"
   - "Includes all K8s behavior, defaults, and mutations"

4. **How to handle drift for K8s defaults**:
   - "Option 1: Add field to your YAML (explicitly manage it)"
   - "Option 2: Add to ignore_fields (explicitly don't manage it)"
   - "Option 3: Accept seeing it in every plan (acknowledge it exists)"
   - "All three are valid choices - pick what makes sense for your use case"

5. **ignore_fields is explicit choice, not automatic**:
   - "You WILL see drift first, then you decide"
   - "This ensures no surprises - you're always informed"
   - "ignore_fields documents your choice: 'I see this field, I choose not to manage it'"

6. **When another controller takes a field**:
   - "If field ownership changes, you'll see it in plan (field_ownership attribute)"
   - "You can take it back (force_conflicts) or release it (ignore_fields)"
   - "Both choices are explicit and documented in your HCL"

### Completed: field_ownership Map Format

Implemented Map format for `field_ownership` with additional UX enhancements beyond the original ADR:

1. **Map format (core ADR goal)** - Field ownership now shows as flat map instead of verbose JSON. Unchanged keys are automatically hidden by Terraform's Map diff behavior.

2. **Preservation during UPDATEs** - When `ignore_fields` and `force_conflicts` haven't changed, we preserve `field_ownership` from state during plan to prevent showing all old values disappearing with `-> (known after apply)`. This eliminates 16+ lines of noise on every UPDATE.

3. **Status field filtering** - Automatically filter out `status.*` paths from field_ownership tracking. Status fields are always owned by Kubernetes controllers (never by k8sconnect), so tracking them provides no actionable information and adds clutter during destroy operations.

**Implementation files:**
- `internal/k8sconnect/resource/manifest/manifest.go` - Schema changed to MapAttribute
- `internal/k8sconnect/resource/manifest/crud_common.go` - Convert to Map and filter status
- `internal/k8sconnect/resource/manifest/crud_operations.go` - Convert to Map and filter status
- `internal/k8sconnect/resource/manifest/plan_modifier.go` - Preservation logic
- All acceptance tests updated

### Not Yet Implemented: managed_state_projection Map Format

Still using JSON String format. The `field_ownership` improvements proved sufficient for reducing noise, so converting `managed_state_projection` is deferred until proven necessary.

**Update (2025-10-06):** Options E and F have been added to address both the verbosity issue AND the bootstrap "(known after apply)" problem. Option E converts `managed_state_projection` to Map format while intelligently populating it from yaml_body when cluster doesn't exist yet. Option F extends this by making `yaml_body` sensitive, eliminating dual-diff confusion and providing a single source of truth (managed_state_projection) in plan output. Both options under consideration.

### Results

Field ownership diffs went from **63 lines of verbose JSON** on every update to:
- **0 lines** when ownership unchanged (most common case)
- **1-5 lines** when ownership actually changes (e.g., HPA takes over replicas)
- **No status field noise** in any operation

Combined with Terraform's Map diff behavior (hiding unchanged keys), this achieves the ADR's primary goal of reducing noise while preserving critical SSA information. Users now only see field_ownership changes when they're meaningful.

## Context

Terraform plan output for UPDATE operations is extremely verbose, overwhelming users with excessive detail. For a simple change of 2 fields (replicas: 3→2, cpu: 50m→100m), users currently see **136 lines of diff**:

```terraform
~ field_ownership = jsonencode({
    - "metadata.annotations.deployment.kubernetes.io/revision" = {
        - manager = "kube-controller-manager"
        - version = "apps/v1"
      }
    # ... 63 lines total
  }) → (known after apply)

~ managed_state_projection = jsonencode({
    ~ spec = {
        ~ replicas = 3 → 2
        ~ template = {
            ~ spec = {
                ~ containers = [
                    ~ {
                        ~ resources = {
                            ~ requests = {
                                ~ cpu = "50m" → "100m"
                                # (1 unchanged attribute hidden)
                            }
                        }
                    }
                ]
            }
        }
    }
  })  # 26 lines total

~ yaml_body = <<-EOT
    apiVersion: apps/v1
    kind: Deployment
    ...
    - replicas: 3
    + replicas: 2
    ...
    - cpu: "50m"
    + cpu: "100m"
    ...
  EOT  # 47 lines total
```

### Why Each Attribute Matters

**Initial instinct**: Hide `field_ownership` as "noise"

**Realization**: Field ownership changes are **critical information** for SSA-aware infrastructure management. If the HPA takes over `spec.replicas`, or a mutating webhook starts managing annotations, users need to see that. This is the whole point of being an SSA-based provider.

The problem isn't *what* we're showing - it's *how verbose the format is*.

### The Core Value Proposition

Users chose k8sconnect specifically because:
1. **managed_state_projection** - Shows what Kubernetes will actually do (accurate predictions via dry-run)
2. **field_ownership** - Shows who manages what (SSA awareness and conflict detection)
3. **yaml_body** - Shows their original configuration

All three have value. The challenge is making them **scannable and concise**.

## Terraform Diff Rendering Constraints

After research, we found:

**What we CAN control:**
- Attribute type (String, Map, Dynamic, etc.)
- Data structure (nested vs flat)
- Whether to include the attribute at all

**What we CANNOT control:**
- How Terraform formats the diff (hardcoded in Terraform Core)
- Collapsing/expanding nested structures
- Custom diff algorithms

**Terraform's rendering by type:**
| Type | Rendering | Verbosity |
|------|-----------|-----------|
| String (JSON) | Shows full nested structure with `# (N unchanged)` | Very verbose |
| Map[String]String | Shows flat key-value pairs | Concise |
| Dynamic | Variable, depends on content | Unpredictable |

## Options Considered

### Option A: Hide field_ownership Only

**Change:** Remove `field_ownership` from schema or move to private state

**Result:**
```terraform
~ managed_state_projection = jsonencode({...})  # Still 26 lines
~ yaml_body = <<-EOT...  # 47 lines
```
**Total: 73 lines** (down from 136)

**Pros:**
- Easy to implement (1 hour)
- Low risk
- 46% reduction in verbosity

**Cons:**
- **Loses critical SSA information** - users can't see field ownership changes
- Still verbose for simple changes
- Two diffs for one logical change (managed_state_projection + yaml_body)

**Verdict:** ❌ Unacceptable - defeats the SSA-aware value proposition

### Option B: Flat Map Format (RECOMMENDED)

**Change:** Convert both `field_ownership` and `managed_state_projection` to `Map[String]String`

**Schema:**
```go
"field_ownership": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Field ownership tracking - shows which controller manages each field",
}

"managed_state_projection": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Accurate field-by-field diff of what Kubernetes will change",
}
```

**Result:**
```terraform
~ field_ownership = {
    ~ "spec.replicas" = "k8sconnect → kube-controller-manager"  # HPA took over!
  }
~ managed_state_projection = {
    ~ "spec.replicas" = "3 → 2"
    ~ "spec.template.spec.containers[0].resources.requests.cpu" = "50m → 100m"
  }
~ yaml_body = <<-EOT...  # 47 lines
```
**Total: ~53 lines** (down from 136)

**Pros:**
- **61% reduction** in verbosity
- Preserves all critical information (SSA ownership + accurate values + user config)
- Concise and scannable format
- Shows exact before→after transitions
- Easy to grep/search with dot-notation paths
- Scales reasonably well (50 changes = ~50 lines vs ~400 currently)

**Cons:**
- Loses hierarchical structure (but paths are self-documenting)
- Medium refactoring effort (4-6 hours)
- **State migration risk** - existing users have JSON strings in state

**Mitigation for state migration:**
- Implement custom state upgrader
- Document breaking change in upgrade guide
- Consider phasing: v0.x can have breaking changes before v1.0 GA

### Option C: Summary Strings Only

**Change:** Convert to simple summary strings

**Result:**
```terraform
~ field_ownership = "1 field ownership changed: spec.replicas"
~ managed_state_projection = "2 fields changed: spec.replicas, spec.template.spec.containers[0].resources.requests.cpu"
~ yaml_body = <<-EOT...
```
**Total: ~50 lines**

**Pros:**
- Most concise (always 1 line per attribute)
- Scales perfectly regardless of change count

**Cons:**
- **Doesn't show values** - users can't see what changed without looking at yaml_body
- Doesn't show ownership transitions
- **Defeats the entire purpose** of accurate dry-run predictions

**Verdict:** ❌ Unacceptable - loses the core value proposition

### Option D: Hide yaml_body on Updates

**Change:** Conditionally suppress yaml_body diff during UPDATE operations

**Result:**
```terraform
~ field_ownership = jsonencode({...})  # 63 lines
~ managed_state_projection = jsonencode({...})  # 26 lines
```
**Total: 89 lines**

**Pros:**
- Single source of truth (managed_state_projection)
- Shows what Kubernetes will actually do

**Cons:**
- **May not be possible** - Terraform requires diffs for required attributes
- Users lose familiar YAML view of their config changes
- Harder to distinguish "what I changed" vs "what Kubernetes added"
- Still verbose (89 lines)

**Verdict:** ❌ Technical feasibility unknown, still verbose

### Option E: Populate managed_state_projection from yaml_body During Bootstrap

**Change:** Convert `managed_state_projection` to Map format (like Option B), but populate it intelligently based on cluster availability:
- When cluster is accessible (normal updates): Use server-side dry-run (accurate)
- When cluster doesn't exist (bootstrap): Parse yaml_body and convert to Map format

**Schema:**
```go
"managed_state_projection": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Field-by-field projection of managed state. Shows server-side dry-run results when cluster is accessible, or yaml_body fields when cluster doesn't exist yet.",
}
```

**Result during bootstrap (cluster doesn't exist):**
```terraform
+ resource "k8sconnect_manifest" "nginx" {
    + yaml_body = <<-EOT
        apiVersion: apps/v1
        kind: Deployment
        metadata:
          name: nginx
        spec:
          replicas: 2
      EOT
    + managed_state_projection = {
        + "apiVersion" = "apps/v1"
        + "kind" = "Deployment"
        + "metadata.name" = "nginx"
        + "spec.replicas" = "2"
      }
  }
```
**Total: ~15 lines** (shows both YAML and flat map view of same content)

**Result during update (cluster exists):**
```terraform
~ resource "k8sconnect_manifest" "nginx" {
    ~ yaml_body = <<-EOT
        - replicas: 2
        + replicas: 3
      EOT
    ~ managed_state_projection = {
        ~ "spec.replicas" = "2 → 3"
        # ... accurate dry-run results from server
      }
  }
```
**Total: ~53 lines** (same as Option B)

**Pros:**
- ✅ **Solves the bootstrap problem** - No more "(known after apply)" showing nothing useful
- ✅ **Always shows meaningful information** - Either dry-run results or yaml content
- ✅ **Correct semantics** - "projection" means subset of fields; yaml fields ARE the managed fields during create
- ✅ **Graceful degradation** - Best available data in all scenarios
- ✅ **Two views of same data** - YAML (familiar) + Map (scannable) complement each other
- ✅ **No user education needed** - Naturally makes sense (shows what you're creating)
- ✅ **Maintains all value props** - SSA awareness, accurate predictions when possible, user intent
- ✅ **Clean implementation** - Parse yaml_body to Map when dry-run unavailable

**Cons:**
- Different data sources (yaml vs dry-run) for same attribute depending on context
- During bootstrap, managed_state_projection shows "intended state" not "actual cluster response"
- May surprise users if they expect dry-run accuracy in all cases
- Slight implementation complexity handling two code paths

**Mitigation:**
- Clear documentation explaining the fallback behavior
- During apply, always use actual dry-run once cluster is accessible (update state with accurate values)
- Add note in plan output or description when showing yaml-sourced projection vs dry-run projection

**Implementation approach:**
```go
func (r *manifestResource) planManagedStateProjection(ctx context.Context, yaml string, conn types.Object) (types.Map, error) {
    // Try to get cluster client
    client, err := r.getClient(conn)

    if err == nil && client != nil {
        // Cluster accessible - use server-side dry-run (accurate)
        dryRunResult := doServerSideDryRun(ctx, client, yaml)
        return extractManagedFieldsAsMap(dryRunResult), nil
    }

    // Cluster not accessible (bootstrap scenario)
    // Parse yaml and return all fields as map - these ARE the managed fields we'll create
    parsed := parseYaml(yaml)
    return convertToFlatMap(parsed), nil
}
```

**Verdict:** ⚠️ **Promising - solves bootstrap UX problem while maintaining value props**

Combines the verbosity wins from Option B with a solution to the "(known after apply)" problem. Semantic correctness is maintained since yaml fields ARE the projection of managed fields during create. Requires careful documentation but provides superior UX in both bootstrap and update scenarios.

### Option F: Hide yaml_body, Show Only managed_state_projection (Option E + Sensitive)

**Change:** Combine Option E (Map format with bootstrap support) and make `yaml_body` sensitive so users only see `managed_state_projection` in diffs.

**Schema:**
```go
"yaml_body": schema.StringAttribute{
    Required:  true,
    Sensitive: true,  // Hidden from plan output
    Description: "Your manifest input. Changes tracked internally via managed_state_projection. Use git diff to review your HCL changes.",
}

"managed_state_projection": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Field-by-field projection of managed state showing exactly what Kubernetes will apply. Single source of truth in plan diffs.",
}
```

**Result during bootstrap (cluster doesn't exist):**
```terraform
+ resource "k8sconnect_manifest" "nginx" {
    + yaml_body = (sensitive value)
    + managed_state_projection = {
        + "apiVersion" = "apps/v1"
        + "kind" = "Deployment"
        + "metadata.name" = "nginx"
        + "spec.replicas" = "2"
      }
  }
```
**Total: ~8 lines** (only projection shown)

**Result during update (cluster exists):**
```terraform
~ resource "k8sconnect_manifest" "nginx" {
    ~ yaml_body = (sensitive value)
    ~ managed_state_projection = {
        ~ "spec.replicas" = "2 → 3"
        ~ "spec.template.spec.containers[0].resources.requests.cpu" = "50m → 100m"
      }
  }
```
**Total: ~5 lines** (only projection shown)

**Pros:**
- ✅ **Single source of truth** - Only one diff to review (managed_state_projection)
- ✅ **Maximum clarity** - No dual-diff confusion
- ✅ **Minimal verbosity** - ~5-8 lines for typical changes
- ✅ **Separation of concerns** - Git shows config changes, Terraform shows cluster changes
- ✅ **No whitespace noise** - Formatting changes in YAML don't show (already suppressed internally)
- ✅ **Scannable Map format** - Field-by-field changes easy to review
- ✅ **Bootstrap works** - Shows parsed yaml as projection when cluster doesn't exist
- ✅ **Aligns with gavinbunney pattern** - Similar to yaml_body (sensitive) + yaml_body_parsed approach
- ✅ **Users already primed** - Familiar pattern from other providers

**Cons:**
- Users can't see their original YAML in plan output (must check git/HCL)
- May feel "hidden" to users unfamiliar with the pattern
- Debug complexity: must expose sensitive values to see actual yaml_body
- Breaking change: existing users see yaml_body diffs currently

**Rationale:**
Users already have git for tracking "what did I change in my config". Terraform should show "what will Kubernetes actually do". The managed_state_projection is computed via server-side dry-run (when available) or parsed yaml (during bootstrap), making it the most accurate view of what will happen. Hiding yaml_body eliminates duplicate information and focuses users on the actual cluster changes.

**Implementation notes:**
- Already have projection comparison logic that suppresses yaml_body diffs when projection unchanged (plan_modifier.go:177)
- Just need to add `Sensitive: true` to yaml_body schema
- Complete Option E implementation (Map format + bootstrap population)
- Document: "Use git diff to see config changes, terraform plan to see cluster changes"

**Verdict:** ⭐ **Strongest option - clean separation of concerns with minimal verbosity**

This approach provides the clearest UX by showing exactly one thing: what Kubernetes will change. It leverages existing version control (git) for tracking config changes and positions Terraform as the infrastructure control plane showing actual cluster state changes. Combined with Map format and bootstrap support, it solves all identified problems while maintaining semantic correctness.

### Option Progression Summary

The options build on each other progressively:

- **Option A**: Remove field_ownership → ❌ Loses SSA value prop
- **Option B**: Convert to Map format → ✅ 61% verbosity reduction, but still dual-diff
- **Option C**: Summary strings → ❌ Loses detail
- **Option D**: Hide yaml_body conditionally → ❌ Technically complex, still verbose
- **Option E**: Option B + bootstrap support → ✅ Solves "(known after apply)", maintains dual-diff
- **Option F**: Option E + sensitive yaml_body → ⭐ **Single source of truth, maximum clarity**

Each option addresses different aspects of the UX problem, with Option F providing the most comprehensive solution by combining all improvements while maintaining semantic correctness and aligning with user workflows (git for config, terraform for cluster state).

## Decision

**Final Decision: Option F with String Format**

Implemented Option F concept (hiding yaml_body, showing only managed_state_projection) but retained String format for managed_state_projection instead of converting to Map. This decision was made after recognizing that:

1. **Map format is excellent for UPDATEs** (shows only changed fields) but **poor for CREATE/DESTROY** (shows all fields flattened)
2. **String format with hierarchical JSON** is more readable for showing complete resource structure during CREATE
3. **Updates are already optimized** - The existing projection comparison logic suppresses yaml_body changes when projection is unchanged
4. **Single source of truth achieved** - Making yaml_body sensitive eliminates dual-diff confusion

The implementation provides:
- ✅ Single diff in plan output (managed_state_projection only)
- ✅ Intelligent fallback (dry-run when possible, parsed yaml when not)
- ✅ Readable hierarchical format (not flattened)
- ✅ Clean separation of concerns (git for config, terraform for cluster state)

### Rationale for Option B

1. **Preserves all value propositions:**
   - Users see accurate dry-run predictions (managed_state_projection)
   - Users see SSA field ownership changes (field_ownership)
   - Users see their YAML config (yaml_body)

2. **Dramatic UX improvement:**
   - 61% reduction in verbosity (136 → 53 lines)
   - Each change = 1 scannable line
   - Scales to large changes (50 fields = ~50 lines vs ~400 currently)

3. **Maintains trust:**
   - Users can verify exactly what will change and who owns what
   - No hidden "magic" - everything is transparent
   - Aligns with "show exactly what Kubernetes will do" philosophy

4. **Implementation risk acceptable for pre-GA:**
   - Medium effort (4-6 hours)
   - State migration can be handled with upgraders
   - Breaking changes acceptable before v1.0

### Rationale for Option E (If Adopted)

All benefits of Option B, plus:
- **Solves bootstrap UX** - Shows parsed yaml content instead of "(known after apply)"
- **Semantically correct** - yaml fields ARE the managed field projection during create
- **Always informative** - Users see useful content in both bootstrap and update scenarios
- **Minimal complexity** - Fallback to yaml parsing when dry-run unavailable

### Rationale for Option F (If Adopted)

All benefits of Option E, plus:
- **Eliminates dual-diff confusion** - Only one thing to review (managed_state_projection)
- **Cleanest separation of concerns** - Git tracks config, Terraform tracks cluster changes
- **Maximum verbosity reduction** - 60-96% reduction from current (depends on change size)
- **Aligns with user workflows** - Users already use git diff for config review
- **Familiar pattern** - Similar to gavinbunney's yaml_body (sensitive) approach

### Real-World Scenarios

| Scenario | Current | Option A | Option B | Option C | Option E | Option F |
|----------|---------|----------|----------|----------|----------|----------|
| Small (2-3 fields) | 136 lines | 73 lines | 53 lines | 50 lines (no detail) | 53 lines | **~5 lines** ✅⭐ |
| Medium (10-15 fields) | ~200 lines | ~130 lines | ~70 lines | 50 lines (no detail) | ~70 lines | **~12 lines** ✅⭐ |
| Large (50+ fields) | ~400 lines | ~300 lines | ~150 lines | 50 lines (no detail) | ~150 lines | **~52 lines** ✅⭐ |
| Bootstrap (CREATE) | "(known after apply)" | "(known after apply)" | "(known after apply)" | "(known after apply)" | ~15 lines | **~8 lines** ✅⭐ |
| Dual-diff confusion | High | Medium | Medium | Low | Medium | **None** ✅⭐ |

## Implementation

### 1. Schema Changes

**File:** `internal/k8sconnect/resource/manifest/manifest.go`

```go
type manifestResourceModel struct {
    ID                     types.String  `tfsdk:"id"`
    YAMLBody               types.String  `tfsdk:"yaml_body"`
    ClusterConnection      types.Object  `tfsdk:"cluster_connection"`
    DeleteProtection       types.Bool    `tfsdk:"delete_protection"`
    DeleteTimeout          types.String  `tfsdk:"delete_timeout"`
    FieldOwnership         types.Map     `tfsdk:"field_ownership"`           // ← CHANGED
    ForceDestroy           types.Bool    `tfsdk:"force_destroy"`
    ForceConflicts         types.Bool    `tfsdk:"force_conflicts"`
    IgnoreFields           types.List    `tfsdk:"ignore_fields"`
    ManagedStateProjection types.Map     `tfsdk:"managed_state_projection"`  // ← CHANGED
    WaitFor                types.Object  `tfsdk:"wait_for"`
    Status                 types.Dynamic `tfsdk:"status"`
}

// Schema attributes
"field_ownership": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Field ownership tracking - shows which controller manages each field. Format: 'path': 'manager' or 'old_manager → new_manager' when ownership changes.",
},
"managed_state_projection": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Accurate field-by-field diff of what Kubernetes will change. Shows dry-run predictions in 'before → after' format.",
},
```

### 2. Projection Building Logic

**File:** `internal/k8sconnect/resource/manifest/projection.go`

```go
// buildFlatProjection creates a flat map showing field changes
func buildFlatProjection(before, after *unstructured.Unstructured, paths []string) map[string]string {
    result := make(map[string]string)

    for _, path := range paths {
        beforeVal := getValueAtPath(before, path)
        afterVal := getValueAtPath(after, path)

        if !reflect.DeepEqual(beforeVal, afterVal) {
            result[path] = fmt.Sprintf("%v → %v", formatValue(beforeVal), formatValue(afterVal))
        } else {
            // Unchanged fields - only include if newly managed
            result[path] = fmt.Sprintf("%v", formatValue(afterVal))
        }
    }

    return result
}

// formatValue handles various types for clean display
func formatValue(v interface{}) string {
    switch val := v.(type) {
    case string:
        return fmt.Sprintf("%q", val)
    case nil:
        return "<unset>"
    case []interface{}, map[string]interface{}:
        // For complex types, show count/type
        return fmt.Sprintf("<%T>", val)
    default:
        return fmt.Sprintf("%v", val)
    }
}

// getValueAtPath extracts value at dot-notation path
func getValueAtPath(obj *unstructured.Unstructured, path string) interface{} {
    // Split path and traverse object
    // Handle array indices [0] and strategic merge keys [name=foo]
    // Return value or nil if not found
}
```

### 3. Field Ownership Building Logic

**File:** `internal/k8sconnect/resource/manifest/field_ownership.go`

```go
// buildFlatOwnership creates a flat map showing field ownership
func buildFlatOwnership(currentOwnership, previousOwnership map[string]FieldOwnership) map[string]string {
    result := make(map[string]string)

    // Track all paths from both current and previous
    allPaths := make(map[string]bool)
    for path := range currentOwnership {
        allPaths[path] = true
    }
    for path := range previousOwnership {
        allPaths[path] = true
    }

    for path := range allPaths {
        current := currentOwnership[path]
        previous := previousOwnership[path]

        if previous.Manager == "" {
            // Newly managed field
            result[path] = current.Manager
        } else if current.Manager != previous.Manager {
            // Ownership changed
            result[path] = fmt.Sprintf("%s → %s", previous.Manager, current.Manager)
        } else {
            // Unchanged ownership - only show current manager
            result[path] = current.Manager
        }
    }

    return result
}
```

### 4. Update All Setters

Update all code that sets `FieldOwnership` and `ManagedStateProjection`:
- `crud_common.go` - Update after create/read/update
- `crud_operations.go` - Update during operations
- `plan_modifier.go` - Update during plan modifications

Convert from:
```go
data.FieldOwnership = types.StringValue(string(ownershipJSON))
```

To:
```go
ownershipMap := buildFlatOwnership(currentOwnership, previousOwnership)
mapValue, _ := types.MapValueFrom(ctx, types.StringType, ownershipMap)
data.FieldOwnership = mapValue
```

### 5. State Migration

**File:** `internal/k8sconnect/resource/manifest/state_upgrade.go` (new)

```go
func (r *manifestResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
    return map[int64]resource.StateUpgrader{
        0: {
            PriorSchema: &schema.Schema{
                // Old schema with StringAttribute
            },
            StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
                // Convert JSON string to Map
                // Handle both field_ownership and managed_state_projection
            },
        },
    }
}
```

### 6. Test Updates

Update all tests that check these attributes:
- `drift_test.go` - Update assertions
- `field_ownership_test.go` - Update assertions
- `ignore_fields_test.go` - Update assertions
- All acceptance tests with state checks

### 7. Documentation Updates

- `docs/resources/manifest.md` - Update attribute descriptions
- Add migration guide for existing users
- Update examples to show new format

## Consequences

### Positive

1. **Massive UX improvement** - 61% reduction in diff verbosity
2. **Preserves all information** - SSA awareness, accurate predictions, user config
3. **Better scannability** - One line per field with clear before→after
4. **Maintains trust** - Users see exactly what changes and who owns what
5. **Better grep-ability** - Dot-notation paths easy to search
6. **Reasonable scaling** - Handles large changes better than current format

### Negative

1. **Breaking change** - Requires state migration
2. **Medium implementation effort** - 4-6 hours of refactoring
3. **Loses hierarchical nesting** - Paths are flat (but self-documenting)
4. **Complex values abbreviated** - Objects/arrays shown as `<type>` not full content

### Mitigation

- Implement state upgrader for seamless migration
- Document breaking change in upgrade guide
- Version as v0.x to set expectations (breaking changes OK before v1.0)
- Add verbose logging for debugging if users need full object detail

## Timeline

**For v1.0 GA:** This should be implemented before GA release to avoid breaking changes post-v1.0.

**Estimated effort:** 4-6 hours
1. Schema changes (30 min)
2. Projection builder refactor (2 hours)
3. Field ownership builder refactor (1 hour)
4. Update all setters (1 hour)
5. Test updates (2 hours)
6. Documentation (30 min)

## References

- Research document: `docs/research/ux-diff-analysis.md`
- Current implementation: `internal/k8sconnect/resource/manifest/projection.go`
- Field ownership: `internal/k8sconnect/resource/manifest/field_ownership.go`
- Real-world examples: `terraform/ux_comparison/apply.out`
