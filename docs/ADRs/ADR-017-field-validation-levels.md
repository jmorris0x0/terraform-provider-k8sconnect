# ADR-017: Server-Side Field Validation

## Status
Implemented

## Context

### The Strategic Insight: Typed Resources Are Obsolete

**Typed Terraform resources exist for ONE reason: plan-time validation.**

Before executing API calls, typed resources (`kubernetes_deployment`, `kubernetes_service`) validate field names, types, and required fields. This requires maintaining hundreds of Go structs mirroring Kubernetes API schemas.

**Field validation + dry-run projection eliminates this entire burden.**

### The Complete Validation Stack Without Schema Maintenance

| Validation Need | Typed Resources | k8sconnect + Field Validation |
|----------------|-----------------|-------------------------------|
| Field name validation | Terraform schema (maintained in Go) | K8s API server (source of truth) |
| Type validation | Terraform schema (maintained in Go) | K8s API server + dry-run |
| Required fields | Terraform schema (maintained in Go) | K8s API server |
| Default values | Hardcoded in provider | Dry-run projection (ADR-001) |
| CRD support | Requires provider update | Automatic (dynamic discovery) |
| Version lag | Weeks/months behind K8s | Zero (K8s is source of truth) |

**The only thing you lose:** IDE autocomplete in HCL.

**What you gain:**
- ✅ Universal CRD support (no provider updates needed)
- ✅ Zero version lag (K8s API is source of truth)
- ✅ No schema maintenance burden
- ✅ Accurate defaults from K8s (not hardcoded)
- ✅ Works for ANY resource (built-in or custom)

### Kubernetes Field Validation Feature

**GA since Kubernetes 1.27 (April 2023)**

Server-side field validation with three levels:
- **Strict**: Rejects unknown/duplicate fields with 400 error
- **Warn** (default): Succeeds but returns warnings in response headers
- **Ignore**: Silently drops unknown fields

API Usage: Query parameter `?fieldValidation=Strict|Warn|Ignore`

## Decision

Always use strict field validation (`FieldValidation="Strict"`) for all apply operations in both `k8sconnect_object` and `k8sconnect_patch` resources.

**No user configuration.** Validation just works.

### Implementation Approach

**Validation happens in TWO phases:**

#### 1. Plan Phase (plan_modifier.go)
- Dry-run includes `FieldValidation="Strict"`
- Catches typos before any cluster mutation
- Identical to typed resource behavior
- **Bootstrap caveat:** If connection is unknown, skip dry-run (graceful degradation per ADR-011)

#### 2. Apply Phase (crud_operations.go)
- Apply includes `FieldValidation="Strict"`
- Safety net for bootstrap scenarios where plan was skipped
- **Critical:** Field validation errors do NOT trigger CRD retry logic (they're permanent user errors)

### Error Handling

Field validation errors (status 400) are distinct from CEL validation (status 422):

```
IsFieldValidationError() → Check BEFORE IsCELValidationError()
```

Multiple field errors are parsed and displayed together (reuses CEL multi-error parsing).

### Integration Points

**With ignore_fields:**
- Full YAML is validated first
- Ignored fields removed after validation passes
- Ensures ignored fields are still valid K8s fields

**With CRD retry:**
- Field validation errors return immediately
- Don't retry permanent user errors
- CRD not found errors still retry as before

**With bootstrap (ADR-011):**
- Unknown connection → skip dry-run → no plan-time validation
- Validation runs during apply when cluster exists
- No breaking changes to bootstrap behavior

## Consequences

### Strategic Benefits

1. **Eliminates the primary advantage of typed resources** - Plan-time validation without schema maintenance
2. **Universal CRD support becomes viable** - No longer trade validation for flexibility
3. **Zero maintenance burden** - K8s API server validates against its own schema

### Technical Benefits

1. **Plan-time error detection** - Typos caught during plan phase via dry-run
2. **Works for CRDs automatically** - No provider updates needed
3. **Zero version lag** - New K8s fields immediately supported
4. **Clear error messages** - Points directly to problematic field

### Trade-offs

1. **Requires K8s 1.27+** - Matches our minimum supported version (1.28)
2. **No IDE autocomplete** - YAML blocks don't provide field completion
3. **Always strict** - No opt-out (intentional - matches typed resource behavior)

## References

- [Kubernetes 1.27 Field Validation GA](https://kubernetes.io/blog/2023/04/24/openapi-v3-field-validation-ga/)
- [KEP-2579: Server-Side Field Validation](https://github.com/kubernetes/enhancements/tree/master/keps/sig-api-machinery/2579-psp-replacement)
