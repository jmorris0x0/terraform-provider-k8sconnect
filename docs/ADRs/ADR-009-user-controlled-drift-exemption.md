# ADR-009: User-Controlled Drift Exemption (ignore_fields)

## Status
Accepted

## Context

Kubernetes resources are often modified by multiple controllers simultaneously (HPA modifies `spec.replicas`, cert-manager modifies secrets, service meshes inject sidecars). When Terraform manages a resource and an external controller modifies a field, SSA detects field ownership conflicts. Users need a way to **intentionally allow** these modifications without Terraform interference.

**Use Case**: Deployment with HPA. Set `ignore_fields = ["spec.replicas"]` to allow HPA to manage replicas while Terraform manages the rest.

## Decision

**Add `ignore_fields` attribute to specify field paths exempt from drift detection and conflict resolution.**

**Critical Semantics**: `ignore_fields` is **intentional drift exemption**, NOT "unmanaged fields."

Terraform still:
- ✅ Creates these fields with initial values from YAML
- ✅ Updates them if user changes the Terraform config
- ✅ Applies them via Server-Side Apply

But:
- ❌ Excludes from drift detection (not in `managed_state_projection`)
- ❌ Excludes from field ownership tracking (not tracked in private state)
- ❌ Allows external controllers to take ownership without conflicts

## Critical Implementation Detail: Consistency bug (Historical)

**Note**: As of ADR-020, `managed_fields` was moved to private state, making this bug obsolete. This section is preserved for historical context.

**Bug discovered**: `managed_fields` filtering MUST happen in **both** `ModifyPlan()` and `ModifyApply()`.

**Initial broken implementation**: Only filtered in Apply, not Plan.

**Error**: "Provider produced inconsistent result after apply" - Plan had different managed_fields than State.

**Root cause**: Terraform compares Plan projection to Apply projection. If they differ, it assumes the provider is buggy.

**Fix**: Any computed attribute depending on `ignore_fields` must apply identical filtering logic in both Plan and Apply phases. Implemented in plan_modifier.go and crud_operations.go.

**Lesson**: This bug was difficult to find. Plan/Apply consistency is critical.

## Test Coverage

6 acceptance tests cover both SUCCESS and ERROR cases (basic happy path, add ignore_fields to resolve conflicts, remove when external owns [ERROR], modify list when we own [SUCCESS], remove from list when external owns [ERROR], remove when we still own [SUCCESS]).

## Alternatives Considered

**`unmanaged_fields`** - Rejected: Wrong semantics (users want to set initial values, just not manage drift), more complex

**`lifecycle { ignore_changes }`** - Rejected: Operates on Terraform attribute level not Kubernetes field level, would require flattening YAML into schema

**Automatic drift exemption** - Rejected: Surprising behavior, could hide legitimate conflicts, no way to override

## Benefits

- **Enables multi-controller scenarios** - HPA, cert-manager, service meshes all work
- **Explicit user control** - clear declaration of intent
- **Builds on field ownership** - leverages existing SSA mechanism
- **Future-proof** - works for any CRD or controller

## Drawbacks

- **Learning curve** - users must understand which fields to ignore
- **Verbose for common cases** - HPA scenario requires explicit config
- **Potential for misuse** - ignoring too many fields reduces Terraform value
