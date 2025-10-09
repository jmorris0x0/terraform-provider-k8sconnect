# ADR-011: Concise Diff Format for Plan Output

## Status
Partially Implemented (2025-10-09)

## Summary

This ADR addresses the verbosity of Terraform plan output. For a simple 2-field change, users previously saw **136 lines of diff**. We implemented field_ownership as a Map to reduce this noise, and documented why further improvements (like making yaml_body sensitive) were abandoned.

## Context

### The Problem

Terraform plan output for UPDATE operations is extremely verbose. For a simple change (replicas: 3→2, cpu: 50m→100m), users see:

```terraform
~ field_ownership = jsonencode({...}) → (known after apply)  # 63 lines
~ managed_state_projection = jsonencode({...})                # 26 lines
~ yaml_body = <<-EOT...                                       # 47 lines
```

**Total: 136 lines of diff** for 2 field changes!

### Why Each Attribute Matters

Initial instinct: Hide `field_ownership` as "noise".

Realization: Field ownership changes are **critical information** for SSA-aware infrastructure management. If HPA takes over `spec.replicas`, or a mutating webhook starts managing annotations, users need to see that. This is the whole point of being an SSA-based provider.

The problem isn't *what* we're showing - it's *how verbose the format is*.

### The Core Value Proposition

Users choose k8sconnect specifically because:
1. **managed_state_projection** - Shows what Kubernetes will actually do (accurate predictions via dry-run)
2. **field_ownership** - Shows who manages what (SSA awareness and conflict detection)
3. **yaml_body** - Shows their original configuration

All three have value. The challenge is making them **scannable and concise**.

## Implemented Solution: field_ownership Map Format

### What We Did

Converted `field_ownership` from verbose JSON string to concise Map format:

**Before:**
```terraform
~ field_ownership = jsonencode({
    - "metadata.annotations.deployment.kubernetes.io/revision" = {
        - manager = "kube-controller-manager"
        - version = "apps/v1"
      }
    - "spec.replicas" = {
        - manager = "k8sconnect"
        - version = "apps/v1"
      }
    # ... 63 lines total
  }) → (known after apply)
```

**After:**
```terraform
~ field_ownership = {
    ~ "spec.replicas" = "k8sconnect → kube-controller-manager"  # HPA took over!
  }
# Unchanged keys automatically hidden by Terraform
```

### Additional UX Enhancements

1. **Preservation during UPDATEs** - When `ignore_fields` hasn't changed, we preserve `field_ownership` from state during plan to prevent showing "(known after apply)" noise.

2. **Status field filtering** - Automatically filter out `status.*` paths from field_ownership tracking. Status fields are always owned by Kubernetes controllers (never by k8sconnect), so tracking them provides no value.

### Results

Field ownership diffs went from **63 lines** on every update to:
- **0 lines** when ownership unchanged (most common case)
- **1-5 lines** when ownership actually changes (e.g., HPA takes over replicas)
- **No status field noise** in any operation

### Implementation

**Files Modified:**
- `internal/k8sconnect/resource/manifest/manifest.go` - Schema changed to MapAttribute
- `internal/k8sconnect/resource/manifest/crud_common.go` - Convert to Map and filter status
- `internal/k8sconnect/resource/manifest/crud_operations.go` - Convert to Map and filter status
- `internal/k8sconnect/resource/manifest/plan_modifier.go` - Preservation logic
- All acceptance tests updated

## Rejected Approach: yaml_body Sensitivity

### What Was Proposed

Make `yaml_body` sensitive and use YAML fallback to populate `managed_state_projection` when dry-run cannot work (during bootstrap scenarios where the cluster doesn't exist yet).

**Goal:** Single source of truth in plan output (only managed_state_projection visible).

### Why It Failed

**The Inconsistent Plan Error:**

When CRD and Custom Resource are created in the same apply:

1. **During PLAN:** CRD doesn't exist → dry-run fails → YAML fallback sets projection to parsed YAML: `{"spec":{"setting":"value"}}`
2. **During APPLY:** CRD gets created → Custom resource CREATE succeeds → Kubernetes CRD schema strips `spec.setting` (not in schema) → Projection is now `{}`
3. **Terraform error:** "inconsistent plan" - projection changed from plan to apply

### Root Cause

**YAML fallback shows what you WROTE, not what Kubernetes will DO.**

Kubernetes applies CRD schemas (strips fields), admission controllers (modifies resources), defaulting (adds fields), and validation (rejects values). YAML fallback cannot predict any of this.

### The Key Realization

**If `yaml_body` is visible, Unknown projection is perfectly acceptable.**

User can review `yaml_body` in plan output to see what will be created. They don't NEED projection during plan if the cluster doesn't exist yet.

### Current Behavior

**Scenario 1: CREATE with existing cluster**
```hcl
+ yaml_body = <<-EOT
    apiVersion: v1
    kind: ConfigMap
    ...
  EOT
+ managed_state_projection = (known after apply)
```

**Scenario 2: UPDATE (cluster exists)**
```hcl
~ yaml_body = <<-EOT
    - key: oldvalue
    + key: newvalue
  EOT
~ managed_state_projection = {
    "data.key": "oldvalue → newvalue"  # Shows exact diff
  }
```

Both fields visible. Projection shows Unknown during CREATE (acceptable), accurate diff during UPDATE.

### See Also

**ADR-013: YAML Body Sensitivity Approach** - Complete analysis of why this approach was rejected, including implementation history and lessons learned.

## Not Implemented: managed_state_projection Map Format

### Proposal

Convert `managed_state_projection` from JSON string to Map format for better readability:

```terraform
~ managed_state_projection = {
    ~ "spec.replicas" = "3 → 2"
    ~ "spec.template.spec.containers[0].resources.requests.cpu" = "50m → 100m"
  }
```

### Why Not Implemented

After implementing field_ownership improvements, the remaining verbosity is acceptable:
- **CREATE operations:** Projection is Unknown (no verbosity issue)
- **UPDATE operations:** JSON format is actually readable for small-medium changes
- **yaml_body:** Users have this for context

Map format would be better for very large changes (50+ fields), but:
- Most changes are small (2-10 fields)
- JSON hierarchical structure is familiar
- Implementation effort not justified for marginal improvement

**Decision:** Defer until proven necessary by user feedback.

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
| Type | Rendering | Example |
|------|-----------|---------|
| String (JSON) | Full nested structure with `# (N unchanged)` | Current managed_state_projection |
| Map[String]String | Flat key-value pairs, unchanged keys hidden | Current field_ownership |
| Dynamic | Variable, depends on content | Not used |

## Current State Summary

### What We Have

1. ✅ **field_ownership** - Concise Map format, 0 lines when unchanged
2. ✅ **managed_state_projection** - Accurate dry-run predictions (JSON string)
3. ✅ **yaml_body** - Visible user configuration
4. ✅ **Bootstrap support** - Projection Unknown when cluster doesn't exist (no errors)

### Typical Diff Sizes

| Scenario | Lines |
|----------|-------|
| Small change (2-3 fields) | ~75 lines (down from 136) |
| Medium change (10-15 fields) | ~100 lines (down from 200) |
| Large change (50+ fields) | ~200 lines (down from 400) |
| Ownership change (e.g., HPA) | +3 lines (field_ownership shows change) |
| No ownership change | +0 lines (field_ownership hidden) |

### Remaining Verbosity Sources

- **yaml_body** (~40-50 lines) - Shows user's YAML configuration changes
- **managed_state_projection** (~20-30 lines) - Shows accurate K8s predictions in JSON

Both provide value:
- yaml_body: What user changed in their config
- managed_state_projection: What Kubernetes will actually do (with defaults, mutations, etc.)

## Future Enhancements (If Needed)

### Option: managed_state_projection Map Format

**When to implement:** If users complain about verbosity for large changes (50+ fields).

**Expected benefit:** ~40% additional reduction in verbosity.

**Effort:** 4-6 hours (schema change, projection builder refactor, tests).

### Option: Smart Projection for CREATE

**Goal:** Show accurate projection during CREATE when cluster exists (not just Unknown).

**Approach:** Do dry-run during CREATE plan when cluster is accessible.

**Benefit:** Users see K8s defaults before apply.

**Note:** This is unrelated to diff format - it's about when we can calculate projection.

## Lessons Learned

### 1. YAML Fallback Cannot Work

Attempting to predict Kubernetes behavior without dry-run is fundamentally flawed. CRD schemas, admission controllers, defaulting, and validation all happen server-side. YAML fallback is just parsed YAML - it's a lie.

### 2. "Known After Apply" is OK Sometimes

It's better to honestly say "we don't know yet" than to show a guess that might be wrong (and cause inconsistent plan errors).

### 3. Dual Visibility Isn't Bad

Showing both `yaml_body` and `managed_state_projection` provides value:
- `yaml_body` - what you configured
- `managed_state_projection` - what k8sconnect actually manages (for drift detection)

Users can use git diff for config changes and Terraform plan for cluster changes.

### 4. Incremental Improvements Work

We don't need to solve all verbosity problems at once. field_ownership Map format alone eliminated 63 lines of noise. That's a massive win.

## References

- **ADR-013:** YAML Body Sensitivity Approach (rejected)
- **ADR-012:** Terraform Fundamental Contract (why state shows both fields)
- **Research:** `docs/research/ux-diff-analysis.md` - Detailed options analysis
- **Research:** `docs/research/DIFF_RENDERING_RESEARCH.md` - Terraform Core rendering internals
- **Abandonment Doc:** `docs/bootstrap-yaml-fallback-abandonment.md` - Technical details of why YAML fallback failed
