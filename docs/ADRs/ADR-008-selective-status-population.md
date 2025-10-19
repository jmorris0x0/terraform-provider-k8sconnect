# ADR-008: Selective Status Field Population Strategy

## Status
Accepted

## Context

Kubernetes status fields contain volatile data (observedGeneration, resourceVersion, conditions[].lastTransitionTime) that changes without user intervention. Providers typically handle this by making status a computed field (preventing drift detection), but this limits reliable use in cross-resource dependencies.

**Core Tension**: Users need certain status fields (LoadBalancer IPs) for outputs and cross-resource dependencies, but traditional approaches either cause drift or make status unreliable for dependencies.

## Decision

**"You get ONLY what you wait for" - Only `wait_for.field` populates status, pruned to the specific path requested.**

Rules:
1. Only `wait_for = { field = "status.path" }` populates status
2. Prune status to requested path only (excludes volatile fields like observedGeneration)
3. All other wait types (rollout, condition, field_value) get null status

When waiting for `status.loadBalancer.ingress`, store ONLY `{"loadBalancer": {"ingress": [...]}}`, NOT the full status with observedGeneration, replicas, conditions, etc.

## Null vs Unknown: Dependency Graph Correctness

**Critical distinction for downstream resource blocking:**

### Status = Null
**Meaning**: "We are NOT tracking this field"

**When used**:
- No `wait_for` configured
- `wait_for` is set but NOT using `field` (e.g., using `rollout`, `condition`, or `field_value`)

**Behavior**: Downstream resources can proceed with null value - no blocking

**Example**:
```hcl
resource "k8sconnect_object" "service" {
  yaml_body = "..."
  # No wait_for - status will be null
}

resource "aws_route53_record" "lb" {
  # This proceeds immediately with null - may fail or create incorrect record
  records = [k8sconnect_object.service.status.loadBalancer.ingress[0].ip]
}
```

### Status = Unknown
**Meaning**: "We ARE tracking this field, but the value isn't ready yet"

**When used** (when `wait_for.field` is configured):
1. Field not found in status yet (waiting for controller to populate it)
2. Failed to read from cluster (transient network error)
3. Failed to convert status to Terraform type (temporary issue)
4. No status object exists yet on the resource
5. Wait timed out before field appeared

**Behavior**: **Downstream resources are BLOCKED** until next apply when field becomes available

**Example**:
```hcl
resource "k8sconnect_object" "service" {
  yaml_body = "..."
  wait_for = {
    field = "status.loadBalancer.ingress"
    timeout = "5m"
  }
  # If timeout expires, status = unknown (not null!)
}

resource "aws_route53_record" "lb" {
  # BLOCKED - won't try to create until service status is known
  # On next "terraform apply", wait_for runs again and may succeed
  records = [k8sconnect_object.service.status.loadBalancer.ingress[0].ip]
}
```

### Why This Matters

**If we used null when field isn't ready yet:**
- Downstream resources proceed with null values
- Creates incorrect resources (Route53 record with null IP)
- Breaks dependency graph semantics
- User must manually fix downstream resources

**Using unknown when field isn't ready:**
- Terraform blocks downstream resources
- Next `terraform apply` retries the wait
- When field appears, status updates and downstream proceeds
- Dependency graph works correctly

### Implementation

In `updateStatus()` (crud_operations.go:177-240):
- Returns `types.DynamicNull()` when NO `wait_for.field` configured
- Returns `types.DynamicUnknown()` when `wait_for.field` IS configured but:
  - Field path not found in status (line 232)
  - No status object exists (line 236)
  - Failed to read from cluster (line 205)
  - Failed to convert status (line 223)

The Read() operation (crud.go:155-164) also calls `updateStatus()` to populate status on refreshes, ensuring that status gets set even after timeouts once the field becomes available.

## Alternatives Considered

**Store Complete Status** - Rejected: Causes constant drift from volatile fields

**Never Store Status** - Rejected: Users can't access LoadBalancer IPs

**Store Status with Ignore Rules** - Rejected: Requires maintaining list of volatile fields per resource type, doesn't work for CRDs

**Store Status for All Wait Types** - Rejected: Reintroduces drift problem, most wait types don't need status data

**User-Configured Status Fields** - Rejected: Adds configuration complexity, users must predict needs

## Implementation

Implemented in `updateStatus()` (crud_operations.go) and `pruneStatusToField()` (status.go).

**Status value rules:**
- No `wait_for` → **null** status
- Non-field wait types (`rollout`, `condition`, `field_value`) → **null** status
- `wait_for.field` configured:
  - Field exists and ready → **populated** with pruned status
  - Field not ready yet (timeout, not found, error) → **unknown** status (blocks downstream)

See "Null vs Unknown: Dependency Graph Correctness" section above for complete behavior specification.

The Read() operation also calls `updateStatus()` to ensure status is populated on refreshes after timeouts.

## Benefits

- **No drift** - Volatile fields never enter Terraform state
- **Predictable behavior** - Clear rule about when status is populated
- **Minimal state** - Only store what users explicitly need
- **LoadBalancer IPs accessible** - Key use case still works
- **Correct dependency blocking** - Using unknown (not null) when field isn't ready ensures downstream resources wait for values instead of proceeding with null

## Drawbacks

- **Breaking change** - Resources using non-field waits lose status (must switch to `field` wait if status needed)
- **Less data available** - Users can't access status fields they didn't wait for

## Key Principle

**Only store in Terraform state what users explicitly need to reference.** This principle guides all decisions about what data to persist.
