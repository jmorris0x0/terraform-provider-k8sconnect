# ADR-011: Concise Diff Format for Plan Output

## Status
Partially Implemented

## Implementation Updates (2025-10-06)

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

## Decision

**Recommendation: Option B - Flat Map Format for both attributes**

Implement `Map[String]String` for both `field_ownership` and `managed_state_projection`.

### Rationale

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

### Real-World Scenarios

| Scenario | Current | Option A | Option B | Option C |
|----------|---------|----------|----------|----------|
| Small (2-3 fields) | 136 lines | 73 lines | **53 lines** ✅ | 50 lines (no detail) |
| Medium (10-15 fields) | ~200 lines | ~130 lines | **~70 lines** ✅ | 50 lines (no detail) |
| Large (50+ fields) | ~400 lines | ~300 lines | **~150 lines** ✅ | 50 lines (no detail) |

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
