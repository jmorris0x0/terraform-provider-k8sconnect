# ADR-013: YAML Body Sensitivity Approach

## Status
**Rejected** - Abandoned on 2025-10-09

## Context

**Goal**: Make `managed_state_projection` the single source of truth by hiding `yaml_body` (marking it sensitive).

**Problem**: When dry-run can't work, projection would be Unknown, leaving users with nothing to review.

**Four scenarios where dry-run fails**:
1. **Cluster bootstrap** - cluster being created, doesn't exist yet
2. **CRD not found** - CRD doesn't exist yet for a custom resource
3. **Namespace not found** - namespace doesn't exist yet for a namespaced resource
4. **Unknown values** - yaml_body contains computed values from other resources (unknown during plan)

**Proposed solution**: YAML fallback - set projection to parsed YAML when dry-run fails.

## Why It Failed

**The Inconsistent Plan Error**: When CRD + Custom Resource created in same apply:

1. **PLAN**: CRD doesn't exist → dry-run fails → YAML fallback sets projection to parsed YAML: `{"spec":{"setting":"value"}}`
2. **APPLY**: CRD created → CR CREATE succeeds → K8s CRD schema strips `spec.setting` (not in schema) → Projection now `{}`
3. **Terraform error**: "inconsistent plan" - projection changed from plan to apply

**Root cause**: **YAML fallback shows what you WROTE, not what Kubernetes will DO.** K8s applies CRD schemas (strips fields), admission controllers (modifies), defaulting (adds fields), validation (rejects). YAML fallback cannot predict any of this.

**Private state flags didn't help**: Even if we preserved fallback projection during apply, we'd be saving inaccurate projection to state.

## Key Insight

**If `yaml_body` is visible, Unknown projection is perfectly acceptable.**

Users can review `yaml_body` in plan output to see what will be created. They don't NEED projection during plan if the cluster doesn't exist yet.

## Decision

**Rejected this approach entirely.**

Instead:
1. Keep `yaml_body` visible (NOT sensitive)
2. Set projection to Unknown when dry-run can't work
3. Removed all YAML fallback logic

## Lessons Learned

1. **Predicting Kubernetes is impossible without dry-run** - YAML fallback can't account for CRD validation/defaulting, webhook mutations, field stripping, API server transformations
2. **"Known After Apply" is OK sometimes** - Better to honestly say "we don't know yet" than show a guess that might be wrong
3. **Dual visibility isn't bad** - Showing both `yaml_body` (what you configured) and `managed_state_projection` (what k8sconnect manages) provides value

