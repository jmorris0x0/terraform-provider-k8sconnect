# ADR-008: Selective Status Field Population Strategy

## Status
Accepted

## Context

### The Problem

Kubernetes status fields contain volatile data that changes frequently without user intervention:
- `observedGeneration` increments on every update
- `resourceVersion` changes with any modification
- `conditions[].lastTransitionTime` updates constantly
- Controller-specific fields update unpredictably

When we store the entire status in Terraform state, these volatile fields cause constant false drift, with Terraform reporting "inconsistent result after apply" errors even when nothing meaningful has changed.

### The Specific Failure

Test `TestAccManifestResource_StatusStability` was failing because:
1. Resource created with `wait_for = { field = "status.readyReplicas" }`
2. Status stored includes all fields: `readyReplicas`, `observedGeneration`, etc.
3. Any update causes `observedGeneration` to change
4. Terraform sees drift in a field the user doesn't care about

### The Core Tension

Users need certain status fields (like LoadBalancer IPs) for:
- Outputs to reference in other resources
- Cross-resource dependencies
- External system integration

But storing status creates drift from fields users don't care about.

## Decision

**Implement selective status field population following the principle: "You get ONLY what you wait for"**

Specific rules:
1. **Only `wait_for.field` populates status** - Other wait types don't store status
2. **Prune status to requested path only** - Store only the specific field waited for
3. **All other wait types get null status** - Rollout, condition, and field_value waits don't populate status

## Rationale

### Why Only `field` Waits

The `wait_for` configuration serves different purposes:

```hcl
# User NEEDS the value (populate status)
wait_for = { field = "status.loadBalancer.ingress" }
# Use case: Output the load balancer URL

# User just wants to wait (don't populate status)  
wait_for = { rollout = true }                          # Just ensure deployment is ready
wait_for = { condition = "Ready" }                     # Just wait for condition
wait_for = { field_value = { "status.phase" = "Running" } }  # Just verify value
```

Only `field` waits indicate the user needs to reference the value later.

### Why Prune to Specific Path

When waiting for `status.loadBalancer.ingress`, we store ONLY:
```json
{
  "loadBalancer": {
    "ingress": [{"hostname": "abc.elb.amazonaws.com"}]
  }
}
```

NOT the full status with volatile fields:
```json
{
  "observedGeneration": 2,      // Changes every update
  "replicas": 3,                 // User didn't ask for this
  "readyReplicas": 3,           // User didn't ask for this
  "conditions": [...],          // Volatile timestamps
  "loadBalancer": {
    "ingress": [{"hostname": "abc.elb.amazonaws.com"}]
  }
}
```

## Considered Alternatives

### Option 1: Store Complete Status for All Waits
**Rejected**: Causes constant drift from volatile fields

### Option 2: Never Store Status
**Rejected**: Users can't access LoadBalancer IPs and other needed values

### Option 3: Store Status with Ignore Rules
Filter out known volatile fields like `observedGeneration`.

**Rejected**: 
- Requires maintaining list of volatile fields per resource type
- Doesn't work for CRDs with unknown volatile fields
- Still stores unnecessary data

### Option 4: Store Status for All Wait Types
Even `rollout` and `condition` waits would populate status.

**Rejected**:
- Reintroduces drift problem
- Most wait types don't need status data
- Violates principle of minimal state storage

### Option 5: User-Configured Status Fields
Let users specify which status fields to store:
```hcl
status_fields = ["loadBalancer.ingress", "readyReplicas"]
```

**Rejected**:
- Adds configuration complexity
- Users must predict what they'll need
- Can still include volatile fields by mistake

## Implementation

### Status Population Logic

```go
func (r *manifestResource) updateStatus(rc *ResourceContext, waited bool) error {
    // No wait = no status
    if !waited {
        rc.Data.Status = types.DynamicNull()
        return nil
    }

    // Parse wait configuration
    var waitConfig waitForModel
    diags := rc.Data.WaitFor.As(rc.Ctx, &waitConfig, basetypes.ObjectAsOptions{})
    if diags.HasError() {
        rc.Data.Status = types.DynamicNull()
        return nil
    }

    // Only field waits populate status
    if waitConfig.Field.IsNull() || waitConfig.Field.ValueString() == "" {
        rc.Data.Status = types.DynamicNull()
        return nil
    }

    // Get current state from cluster
    currentObj, err := rc.Client.Get(rc.Ctx, rc.GVR, 
                                     rc.Object.GetNamespace(), 
                                     rc.Object.GetName())
    if err != nil {
        rc.Data.Status = types.DynamicNull()
        return nil
    }

    // Extract and prune status to only the waited-for field
    if statusRaw, found, _ := unstructured.NestedMap(currentObj.Object, "status"); found {
        fieldPath := waitConfig.Field.ValueString()
        prunedStatus := pruneStatusToPath(statusRaw, fieldPath)
        
        if prunedStatus != nil {
            statusValue, _ := ConvertToAttrValue(rc.Ctx, prunedStatus)
            rc.Data.Status = types.DynamicValue(statusValue)
        } else {
            rc.Data.Status = types.DynamicNull()
        }
    }
    
    return nil
}
```

### Pruning Implementation

```go
func pruneStatusToPath(fullStatus map[string]interface{}, fieldPath string) map[string]interface{} {
    // Remove "status." prefix if present
    path := strings.TrimPrefix(fieldPath, "status.")
    segments := strings.Split(path, ".")
    
    // Navigate to the requested value
    value := navigateToValue(fullStatus, segments)
    if value == nil {
        return nil
    }
    
    // Rebuild minimal map with just this path
    result := make(map[string]interface{})
    setNestedValue(result, segments, value)
    
    return result
}
```

## Consequences

### Positive
- **No drift** - Volatile fields never enter Terraform state
- **Predictable behavior** - Clear rule about when status is populated
- **Minimal state** - Only store what users explicitly need
- **Clean plans** - No constant "changes" to status fields
- **LoadBalancer IPs accessible** - Key use case still works

### Negative  
- **Breaking change** - Resources using non-field waits lose status
- **Less data available** - Users can't access status fields they didn't wait for
- **Multiple waits needed** - To get multiple status fields, need multiple resources

### Neutral
- **Documentation burden** - Must clearly explain when status is available
- **Learning curve** - Users must understand the wait type distinction

## Migration Impact

### Breaking Changes
Resources currently using these patterns will lose status after upgrade:
```hcl
wait_for = { rollout = true }           # Status becomes null
wait_for = { condition = "Ready" }       # Status becomes null  
wait_for = { field_value = {...} }       # Status becomes null
```

### Migration Guide
For users who need status with non-field waits:
```hcl
# Old (status was populated but caused drift)
resource "k8sconnect_manifest" "deployment" {
  yaml_body = "..."
  wait_for = { rollout = true }
}

# New (add explicit field wait for needed status)
resource "k8sconnect_manifest" "deployment" {
  yaml_body = "..."
  wait_for = { field = "status.readyReplicas" }  # Now explicitly requests status
}
```

## Success Metrics

- `TestAccManifestResource_StatusStability` passes
- No spurious drift on resources with `wait_for`
- LoadBalancer IP use case works perfectly
- Clear, predictable behavior documented
- Zero reports of "inconsistent result after apply" from status fields

## Future Considerations

### Possible Enhancement: Multiple Field Waits
Could later support waiting for and storing multiple specific fields:
```hcl
wait_for = {
  fields = [
    "status.loadBalancer.ingress",
    "status.readyReplicas"
  ]
}
```

This would maintain the principle while providing more flexibility.

## References

- Test case: `TestAccManifestResource_StatusStability`  
- Original issue: Status fields causing drift detection
- Kubernetes API conventions on status subresources
- Terraform provider best practices for computed fields

## Decision

We adopt the "You get ONLY what you wait for" principle for status field population. This solves the drift problem while maintaining access to critical status data like LoadBalancer IPs.

This establishes a broader architectural pattern: **Only store in Terraform state what users explicitly need to reference.** This principle should guide future decisions about what data to persist.
