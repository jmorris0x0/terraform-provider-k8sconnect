# UX Diff Analysis: managed_state_projection Display Format

## Current State (The Problem)

### What Users See Now - UPDATE Scenario

**Changing 2 fields in a Deployment (replicas: 3→2, cpu: 50m→100m):**

```terraform
~ field_ownership = jsonencode({...}) → (known after apply)  # 63 lines of noise
~ managed_state_projection = jsonencode({
    ~ spec = {
        ~ replicas = 3 → 2
        ~ template = {
            ~ spec = {
                ~ containers = [
                    ~ {
                        name = "nginx"
                        ~ resources = {
                            ~ requests = {
                                ~ cpu = "50m" → "100m"
                                # (1 unchanged attribute hidden)
                            }
                            # (1 unchanged attribute hidden)
                        }
                        # (4 unchanged attributes hidden)
                    }
                ]
            }
            # (1 unchanged attribute hidden)
        }
        # (1 unchanged attribute hidden)
    }
    # (3 unchanged attributes hidden)
  })  # 26 lines for 2 changes

~ yaml_body = <<-EOT
    ...
    - replicas: 3
    + replicas: 2
    ...
    - cpu: "50m"
    + cpu: "100m"
    ...
  EOT  # 47 lines total
```

**Total:** ~136 lines of diff output for 2 field changes!

**Problems:**
1. field_ownership is pure noise (63 lines)
2. managed_state_projection shows correct info but too verbose (26 lines for 2 changes)
3. yaml_body is redundant with managed_state_projection
4. User cognitive overload

## Competitor Comparison

### kubectl Provider
```terraform
~ yaml_body_parsed = <<-EOT
    - replicas: 3
    + replicas: 2
    ...
    - cpu: 50m
    + cpu: 100m
  EOT
```
**Clean, but LIES** - doesn't show what Kubernetes actually does (defaults, mutations, etc.)

### kubernetes (HashiCorp) Provider
```terraform
~ manifest = {...}  # Shows user's YAML
~ object = {...}    # Shows cluster state (but not accurate prediction)
```
**Two diffs, both verbose**, and `object` isn't a true dry-run prediction

## Research: Terraform Diff Rendering Capabilities

### What We CAN Control
1. **Attribute type** (String, Map, Dynamic, etc.)
2. **Data structure** (nested vs flat)
3. **Whether to include the attribute at all**

### What We CANNOT Control
1. **How Terraform formats the diff** (this is hardcoded in Terraform Core)
2. **Collapsing/expanding nested structures**
3. **Custom diff algorithms**

### Terraform's Diff Rendering By Type

| Type | Rendering | Example |
|------|-----------|---------|
| String (JSON) | Shows full JSON with nesting, adds `# (N unchanged)` | Current behavior |
| Map[String]String | Shows flat key-value pairs | What we want! |
| Dynamic | Variable, depends on content | Could work but unpredictable |

## Proposed Solutions

### Option A: Hide field_ownership, Keep Current Format
**Change:**
- Make `field_ownership` not visible in diffs (move to private state or internal-only)
- Keep `managed_state_projection` as JSON string

**Result:**
```terraform
~ managed_state_projection = jsonencode({...})  # Still 26 lines
~ yaml_body = <<-EOT...  # 47 lines
```
**Total: 73 lines** (down from 136)

**Pros:**
- Easy to implement (just hide one attribute)
- Keeps existing logic intact
- Projection is accurate

**Cons:**
- Still verbose for simple changes
- Two diffs for one logical change

### Option B: Flat Map Format ⭐ (RECOMMENDED)
**Change:**
- Hide `field_ownership`
- Change `managed_state_projection` to `Map[String]String`
- Build flat structure: `{"spec.replicas": "3 → 2", ...}`

**Result:**
```terraform
~ managed_state_projection = {
    "spec.replicas" = "3 → 2"
    "spec.template.spec.containers[0].resources.requests.cpu" = "50m → 100m"
  }
~ yaml_body = <<-EOT...  # 47 lines
```
**Total: ~51 lines** (down from 136)

**Pros:**
- Concise and scannable
- Shows exactly what changed
- Scales better with many changes
- Easy to grep/search

**Cons:**
- Loses hierarchical structure
- Medium refactoring effort (change projection building logic)
- With 50+ changes, still shows 50+ lines

### Option C: Summary String
**Change:**
- Hide `field_ownership`
- Change `managed_state_projection` to simple summary string

**Result:**
```terraform
~ managed_state_projection = "2 fields changed: spec.replicas, spec.template.spec.containers[0].resources.requests.cpu"
~ yaml_body = <<-EOT...  # 47 lines
```
**Total: ~49 lines** (down from 136)

**Pros:**
- Most concise
- Scales perfectly (always 1 line)

**Cons:**
- Doesn't show values (loses accuracy value prop)
- Users can't see what changed without looking at yaml_body
- **Defeats the entire purpose of the provider**

### Option D: Hide yaml_body on Updates

**UPDATE (2025-10-09): This option was fully investigated and ABANDONED. See ADR-013 for complete details.**

**Change:**
- Hide `field_ownership`
- Keep `managed_state_projection` as is
- Find way to suppress yaml_body diff (research needed - may not be possible)

**Result:**
```terraform
~ managed_state_projection = jsonencode({...})  # 26 lines
```
**Total: 26 lines** (down from 136)

**Pros:**
- Single source of truth
- Accurate and complete
- Shows what Kubernetes will actually do

**Cons:**
- **May not be possible** - Terraform shows diffs for required attributes
- Users lose their familiar YAML view
- Harder to see what THEY changed vs what Kubernetes added

## Real-World Scenario Testing

### Scenario 1: Small Change (2-3 fields)
**Current:** 136 lines
**Option A:** 73 lines
**Option B:** ~51 lines ⭐ Winner
**Option C:** 49 lines (but loses info)

### Scenario 2: Medium Change (10-15 fields)
**Current:** ~200 lines
**Option A:** ~130 lines
**Option B:** ~70 lines ⭐ Winner
**Option C:** 49 lines (but loses info)

### Scenario 3: Large Change (50+ fields - full deployment rewrite)
**Current:** ~400 lines
**Option A:** ~300 lines
**Option B:** ~150 lines ⭐ Still best
**Option C:** 49 lines (but useless - no detail)

### Scenario 4: CREATE
**All options:** yaml_body + "(known after apply)" for projection
**No diff issue** - only showing yaml_body which is correct

## Recommendation: Option B with Enhancement

### Primary Change: Flat Map Format
```go
// Change schema
"managed_state_projection": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Accurate field-by-field diff of what Kubernetes will change",
}

// Change projection building
func buildFlatProjection(before, after *unstructured.Unstructured, paths []string) map[string]string {
    result := make(map[string]string)
    for _, path := range paths {
        beforeVal := getValueAtPath(before, path)
        afterVal := getValueAtPath(after, path)
        if beforeVal != afterVal {
            result[path] = fmt.Sprintf("%v → %v", beforeVal, afterVal)
        }
    }
    return result
}
```

### Enhancement: Smart Grouping for Many Changes
If >20 fields changed, add summary header:
```terraform
~ managed_state_projection = {
    "_summary" = "23 fields changed"
    "spec.replicas" = "3 → 2"
    ... (22 more)
  }
```

### Hide field_ownership
Move to private state or make internal-only (not shown in plan/state).

## Implementation Effort

| Option | Effort | Risk |
|--------|--------|------|
| A | 1 hour | Low |
| B | 4-6 hours | Medium (refactoring projection logic) |
| C | 2 hours | Low (but defeats purpose) |
| D | Unknown | High (may not be possible) |

## Conclusion

**Recommendation: Option B (Flat Map Format)**

**Why:**
- 60% reduction in diff noise
- Preserves accuracy (the core value prop)
- Scales reasonably well to many changes
- Still shows exact before→after values
- Clean, scannable format

**Implementation:**
1. Hide `field_ownership` (move to private state)
2. Change `managed_state_projection` schema to MapAttribute
3. Refactor projection building to create flat map with "before → after" strings
4. Update all tests
5. Update documentation

**Key Metric:** **"This is the actual diff and that's why I trust k8sconnect"**

This is still achievable with flat map format - users see concise, accurate field-level diffs showing exactly what Kubernetes will do.
