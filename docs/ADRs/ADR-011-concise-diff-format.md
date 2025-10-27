# ADR-011: Concise Diff Format for Plan Output

## Status
Superseded by ADR-020 (2025-10-26)

**Note**: This ADR documents the evolution of field ownership display strategy. The final solution was to move `field_ownership` to private state with warnings (ADR-020), rather than improving the diff format. This ADR is preserved for historical context.

## Context

**Problem**: Terraform plan output for simple 2-field change showed **136 lines of diff** (field_ownership: 63 lines, managed_state_projection: 26 lines, yaml_body: 47 lines).

**Initial instinct**: Hide `field_ownership` as "noise".

**Realization**: Field ownership changes are **critical information** for SSA-aware infrastructure. If HPA takes over `spec.replicas`, users need to see that. Problem isn't *what* we show - it's *how verbose the format is*.

## Attempted Solution: field_ownership Map Format

Converted `field_ownership` from verbose JSON string to concise Map format. Terraform automatically hides unchanged keys.

**Before**: 63 lines showing all fields on every update
**After**: 0 lines when ownership unchanged, 1-5 lines when ownership changes (e.g., HPA takes over replicas)

**Enhancements**:
- Preserve field_ownership from state during plan when `ignore_fields` unchanged (prevents "(known after apply)" noise)
- Filter out `status.*` paths (always owned by K8s controllers, never by k8sconnect)

**Ultimate outcome**: While this reduced verbosity, tracking external field ownership in public state created stability issues (flaky tests, inconsistent plan errors). The final solution was ADR-020: Move field_ownership to private state and emit warnings during plan instead.

## Rejected: yaml_body Sensitivity with YAML Fallback

**Proposal**: Make `yaml_body` sensitive, use YAML fallback for `managed_state_projection` when dry-run unavailable (bootstrap). Goal: single source of truth (only projection visible).

**Why it failed**: When CRD + Custom Resource created in same apply:
1. **PLAN**: CRD doesn't exist → dry-run fails → YAML fallback sets projection to parsed YAML: `{"spec":{"setting":"value"}}`
2. **APPLY**: CRD created → CR CREATE succeeds → K8s CRD schema strips `spec.setting` → Projection now `{}`
3. **Terraform error**: "inconsistent plan" - projection changed

**Root cause**: **YAML fallback shows what you WROTE, not what Kubernetes will DO.** K8s applies CRD schemas, admission controllers, defaulting, validation - YAML fallback cannot predict any of this.

**Key insight**: If `yaml_body` is visible, Unknown projection is acceptable. Users can review `yaml_body` to see what will be created. They don't need projection during CREATE when cluster doesn't exist yet.

**See ADR-013** for complete analysis.

## Not Implemented: managed_state_projection Map Format

**Deferred** until proven necessary by user feedback. After field_ownership improvements, remaining verbosity is acceptable:
- Most changes are small (2-10 fields), JSON hierarchical structure is readable
- CREATE operations: projection is Unknown (no verbosity)
- Map format would help for very large changes (50+ fields) but these are rare
