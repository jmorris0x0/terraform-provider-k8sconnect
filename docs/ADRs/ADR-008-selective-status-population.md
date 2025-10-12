# ADR-008: Selective Status Field Population Strategy

## Status
Accepted

## Context

Kubernetes status fields contain volatile data (observedGeneration, resourceVersion, conditions[].lastTransitionTime) that changes without user intervention. Storing entire status in Terraform state causes constant false drift.

**Core Tension**: Users need certain status fields (LoadBalancer IPs) for outputs and cross-resource dependencies, but storing status creates drift from volatile fields users don't care about.

## Decision

**"You get ONLY what you wait for" - Only `wait_for.field` populates status, pruned to the specific path requested.**

Rules:
1. Only `wait_for = { field = "status.path" }` populates status
2. Prune status to requested path only (excludes volatile fields like observedGeneration)
3. All other wait types (rollout, condition, field_value) get null status

When waiting for `status.loadBalancer.ingress`, store ONLY `{"loadBalancer": {"ingress": [...]}}`, NOT the full status with observedGeneration, replicas, conditions, etc.

## Alternatives Considered

**Store Complete Status** - Rejected: Causes constant drift from volatile fields

**Never Store Status** - Rejected: Users can't access LoadBalancer IPs

**Store Status with Ignore Rules** - Rejected: Requires maintaining list of volatile fields per resource type, doesn't work for CRDs

**Store Status for All Wait Types** - Rejected: Reintroduces drift problem, most wait types don't need status data

**User-Configured Status Fields** - Rejected: Adds configuration complexity, users must predict needs

## Implementation

Implemented in `updateStatus()` (crud.go) and `pruneStatusToPath()` (crud_operations.go).

No wait → null status. Non-field wait types → null status. Field wait → get current state, prune to requested path only, store pruned result.

## Benefits

- **No drift** - Volatile fields never enter Terraform state
- **Predictable behavior** - Clear rule about when status is populated
- **Minimal state** - Only store what users explicitly need
- **LoadBalancer IPs accessible** - Key use case still works

## Drawbacks

- **Breaking change** - Resources using non-field waits lose status (must switch to `field` wait if status needed)
- **Less data available** - Users can't access status fields they didn't wait for

## Key Principle

**Only store in Terraform state what users explicitly need to reference.** This principle guides all decisions about what data to persist.
