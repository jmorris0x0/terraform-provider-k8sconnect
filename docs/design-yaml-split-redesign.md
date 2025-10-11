# Design Document: YAML Split Data Source Redesign

**Status**: Revised - Simplified Scope
**Date**: 2025-10-11
**Author**: Design Research
**Purpose**: Requirements analysis for yaml_split datasources

---

## Executive Summary

The `k8sconnect_yaml_split` datasource works well for its core purpose: splitting multi-document YAML files into individual manifests for Terraform for_each loops. This document clarifies the scope and purpose of two datasources:

1. **`k8sconnect_yaml_split`**: General-purpose YAML splitter (keep simple)
2. **`k8sconnect_yaml_scoped`**: Dependency ordering helper (CRDs → cluster-scoped → namespaced)

**Key Decision**: Filtering and structured metadata are **NOT goals** for the base datasource. Users who need filtering can write simple for_each expressions if needed. The scoped datasource solves the #1 real use case (dependency ordering).

---

## 1. Core Use Cases

### UC1: Split and Apply All (90% of users)
**Who**: Users with simple, homogeneous manifests
**What**: Split multi-doc YAML and apply everything
**Why**: Most basic workflow, must be dead simple

```hcl
data "k8sconnect_yaml_split" "app" {
  content = file("manifests.yaml")
}

resource "k8sconnect_manifest" "app" {
  for_each = data.k8sconnect_yaml_split.app.manifests
  yaml_body = each.value
  cluster_connection = var.connection
}
```

**Status**: ✅ Works perfectly, keep this pattern

### UC2: Dependency Ordering (10% of users)
**Who**: Users deploying CRDs + Custom Resources in one apply
**What**: Apply resources in correct order to avoid race conditions
**Why**: Kubernetes rejects resources if CRD/Namespace doesn't exist yet

**Current approach** (painful regex):
```hcl
resource "k8sconnect_manifest" "crds" {
  for_each = {
    for key, yaml in data.k8sconnect_yaml_split.all.manifests :
    key => yaml
    if can(regex("(?i)kind:\\s*CustomResourceDefinition", yaml))
  }
  yaml_body = each.value
}
```

**Solution**: Build `k8sconnect_yaml_scoped` datasource (Phase 2)
```hcl
data "k8sconnect_yaml_scoped" "all" {
  pattern = "./manifests/**/*.yaml"
}

resource "k8sconnect_manifest" "crds" {
  for_each = data.k8sconnect_yaml_scoped.all.crds
  yaml_body = each.value
}

resource "k8sconnect_manifest" "namespaced" {
  for_each = data.k8sconnect_yaml_scoped.all.namespaced
  yaml_body = each.value
  depends_on = [k8sconnect_manifest.crds]
}
```

**Status**: ❌ Core pain point, will be solved by yaml_scoped datasource

---

## 2. Non-Use Cases (Rejected)

### Multi-Cluster Routing by Labels
**Rejected because**: At scale, you organize by files/directories, not labels
**Alternative**: Separate `data "k8sconnect_yaml_split"` blocks per cluster

### Filtering by Scope/Kind/Labels
**Rejected because**: Real users at scale don't filter inline YAML by metadata
**Alternative**: If needed (rare), write simple for_each with regex (existing pattern)

### Structured Metadata Exposure
**Rejected because**:
- 100% of users pay the cost: `each.value.yaml_body` instead of `each.value`
- 2% of users get the benefit: type-safe filtering
- Dependency ordering users get `yaml_scoped` instead (better solution)

---

## 3. Design Decisions

### ✅ D1: Two Datasources Strategy

**Datasource 1: `k8sconnect_yaml_split`** (General purpose - KEEP SIMPLE)
- Input: `content` (string) or `pattern` (glob)
- Output: `manifests = map(string)` - **simple string map**
- Use case: Split and apply, custom filtering if needed (rare)
- Target: 90% of users

**Schema** (unchanged):
```hcl
data "k8sconnect_yaml_split" "example" {
  content = string  # Optional: inline YAML
  pattern = string  # Optional: glob pattern (supports **)

  # Outputs
  id        = string          # content-<hash> or pattern-<hash>
  manifests = map(string)     # map[id]yaml_string  ← KEEP THIS
}
```

**Datasource 2: `k8sconnect_yaml_scoped`** (Dependency ordering - TO BE BUILT)
- Input: `content` or `pattern`
- Output: Three categorized maps - `crds`, `cluster_scoped`, `namespaced`
- Use case: CRDs + Custom Resources in single apply
- Target: 10% of users (dependency ordering)

**Rationale**:
- Base datasource stays simple (no structured output, no filter block)
- Scoped datasource solves the #1 real pain point (dependency ordering)
- No over-engineering for rare use cases

### ✅ D2: Keep Output Simple
**Decision**: Base datasource outputs `map(string)`, not `map(object)`.

**Rationale**:
- Clean UX for 100% of users: `yaml_body = each.value`
- No breaking change from structured output
- Advanced filtering (if needed) can use regex on strings (existing pattern)
- Dependency ordering gets dedicated datasource instead

### ✅ D3: No Filter Block
**Decision**: Do not add declarative filter block to base datasource.

**Rationale**:
- Couldn't identify real use cases beyond dependency ordering
- Dependency ordering gets `yaml_scoped` datasource
- Over-engineering for rare edge cases
- Keeps base datasource focused and simple

### ✅ D4: Cross-Datasource Collision Strategy
**Decision**: Document best practices, don't engineer a solution.

**Recommended pattern**: Separate resource blocks (no merging)
```hcl
resource "k8sconnect_manifest" "apps" {
  for_each = data.k8sconnect_yaml_split.apps.manifests
  yaml_body = each.value
}

resource "k8sconnect_manifest" "infra" {
  for_each = data.k8sconnect_yaml_split.infra.manifests
  yaml_body = each.value
}
```

**Rationale**:
- Merging multiple datasources is rare in practice
- Users understand Terraform's `merge()` behavior
- Separate resource blocks avoid collision entirely
- Don't over-engineer for 1% edge case

---

## 4. What We Keep (Base Datasource)

### ✅ Current Implementation is Excellent

**ID Format** (unchanged):
- Cluster-scoped: `{kind}.{name}` → `namespace.production`
- Namespaced: `{kind}.{namespace}.{name}` → `deployment.prod.nginx`

**Features** (keep all):
- ✅ Robust YAML splitting (handles comments, line endings, quoted strings)
- ✅ Supports inline content and file patterns
- ✅ Recursive glob with `**` support
- ✅ Stable, human-readable IDs
- ✅ Fail-fast on parsing errors
- ✅ Detailed error messages (file, doc#, line#)
- ✅ Excellent unit test coverage

**Output** (unchanged):
```hcl
manifests = map(string)  # Each value is raw YAML string
```

**Usage** (unchanged):
```hcl
resource "k8sconnect_manifest" "all" {
  for_each = data.k8sconnect_yaml_split.all.manifests
  yaml_body = each.value  # ← Simple, clean
}
```

---

## 5. Implementation Roadmap

### Phase 1: Base Datasource (COMPLETE - No Changes Needed)
**Status**: ✅ Current implementation is correct

The base datasource (`k8sconnect_yaml_split`) already does exactly what it should:
- Split YAML documents
- Generate stable IDs
- Output simple string map
- Excellent error handling

**No code changes needed.**

### Phase 2: Create Scoped Datasource (k8sconnect_yaml_scoped)
**Goal**: Pre-categorized outputs for dependency ordering

**Tasks**:
1. Create new datasource file: `yaml_scoped.go`
2. Reuse parsing logic from base datasource
3. Implement categorization logic:
   - CRD detection: `kind == "CustomResourceDefinition"`
   - Scope detection: cluster-scoped vs namespaced (use hardcoded map)
4. Three output maps: `crds`, `cluster_scoped`, `namespaced`
5. Add comprehensive tests
6. Create runnable example: CRD + CR in single apply
7. Update documentation

**Schema**:
```hcl
data "k8sconnect_yaml_scoped" "all" {
  content = string  # Optional: inline YAML
  pattern = string  # Optional: glob pattern

  # Outputs
  id             = string
  crds           = map(string)  # CustomResourceDefinitions only
  cluster_scoped = map(string)  # Namespaces, ClusterRoles, etc (NOT CRDs)
  namespaced     = map(string)  # Deployments, Services, etc
}
```

**Usage example**:
```hcl
data "k8sconnect_yaml_scoped" "all" {
  pattern = "./manifests/**/*.yaml"
}

# Apply CRDs first
resource "k8sconnect_manifest" "crds" {
  for_each = data.k8sconnect_yaml_scoped.all.crds
  yaml_body = each.value
}

# Apply cluster-scoped (Namespaces, etc)
resource "k8sconnect_manifest" "cluster_scoped" {
  for_each = data.k8sconnect_yaml_scoped.all.cluster_scoped
  yaml_body = each.value
  depends_on = [k8sconnect_manifest.crds]
}

# Apply namespaced resources
resource "k8sconnect_manifest" "namespaced" {
  for_each = data.k8sconnect_yaml_scoped.all.namespaced
  yaml_body = each.value
  depends_on = [k8sconnect_manifest.cluster_scoped]
}
```

**Estimated effort**: 1-2 days

### Phase 3: Update Examples
**Goal**: Replace dependency-ordering example with yaml_scoped

**Tasks**:
1. Update `yaml-split-dependency-ordering` example to use `yaml_scoped`
2. Verify other examples still work (they should - no changes to base datasource)
3. Add best practices doc (when to use which datasource)

**Estimated effort**: 0.5 days

**Total estimated effort**: 2-3 days

---

## 6. Success Criteria

A successful implementation must:

1. ✅ **Keep base datasource unchanged**: No breaking changes, simple stays simple
2. ✅ **Solve dependency ordering**: yaml_scoped eliminates regex filtering
3. ✅ **No over-engineering**: Two focused datasources, not five like kubectl provider
4. ✅ **Clean UX**: `each.value` stays simple (not `each.value.yaml_body`)
5. ✅ **Copy-paste examples**: Obvious patterns for common cases
6. ✅ **Maintainable**: Reuse parsing logic, minimal code duplication

---

## 7. Comparison with kubectl Provider

| Feature | kubectl | k8sconnect |
|---------|---------|-----------|
| Number of datasources | 5 | 2 |
| Basic split | ✅ kubectl_file_documents | ✅ k8sconnect_yaml_split |
| Pattern matching | ✅ kubectl_path_documents | ✅ k8sconnect_yaml_split |
| Dependency ordering | ❌ Manual regex | ✅ k8sconnect_yaml_scoped |
| Learning curve | 🟡 Medium (5 options!) | 🟢 Low (2 clear options) |
| Regex required for filtering | ❌ Yes | ✅ No (yaml_scoped handles it) |

**Key insight**: kubectl provider has 5 datasources but still requires regex for dependency ordering. We have 2 datasources with cleaner solution.

---

## Conclusion

**Base datasource (`k8sconnect_yaml_split`)**: Already perfect, no changes needed
**Scoped datasource (`k8sconnect_yaml_scoped`)**: Build this to solve dependency ordering

**Design philosophy**:
- Simple case stays simple (90% of users)
- Common pain point gets dedicated solution (10% of users)
- No over-engineering for rare edge cases

The implementation strategy is clear: **Keep Phase 1 complete as-is, build Phase 2 (yaml_scoped datasource).**
