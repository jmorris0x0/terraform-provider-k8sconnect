# ADR-002: Handling Immutable Resources and Complex Deletions

## Status
Accepted - Implemented (2025-10-06)

**Implementation:** Option A - ModifyPlan with RequiresReplace (Terraform Framework)

Immutable field changes are now detected during the plan phase via dry-run and automatically trigger resource replacement with clear user warnings.

## Context

Kubernetes resources present two significant operational challenges for Terraform providers:

1. **Immutable Fields**: Many Kubernetes resources have fields that cannot be changed after creation (e.g., PVC storage size, Service clusterIP, Pod containers)
2. **Complex Deletion**: Resources with finalizers, dependent resources, or admission webhooks can get stuck during deletion

Current k8sconnect behavior:
- **Immutable Updates**: Kubernetes API returns 422 Invalid errors, which we report as generic errors
- **Stuck Deletions**: We have `force_destroy` and `delete_timeout`, but the UX could be improved

## Problem Statement

### Immutable Field Updates

When users change immutable fields in Terraform configuration:
```hcl
resource "k8sconnect_object" "pvc" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
spec:
  resources:
    requests:
      storage: 10Gi  # User changes to 20Gi - immutable!
YAML
}
```

**Current behavior**:
```
Error: Update: Invalid Resource
Details: spec is immutable after creation
```

**Problems**:
- Generic error message doesn't explain immutability
- No guidance on how to resolve
- Users don't understand they need to recreate

### Complex Deletions

Resources with finalizers, volumes in use, or namespaces with resources inside get stuck during deletion. Current behavior times out and provides guidance, but could be more intelligent.

## Research: How Other Providers Handle This

### HashiCorp Kubernetes Provider

**Immutable Fields**:
- Uses schema with `ForceNew: true` for known immutable fields
- Terraform automatically plans resource recreation
- Clear in plan output: `# must be replaced`

**Limitation**: Requires maintaining per-resource schemas. Can't handle CRDs or unknown resources.

**Complex Deletions**:
- Basic timeout support
- No built-in finalizer handling
- Users often resort to local-exec provisioners

### Kubectl Provider (Gavin Bunney)

**Immutable Fields**:
- No special handling - errors bubble up from Kubernetes
- Users must manually add lifecycle rules
- Community created helper modules for common cases

**Complex Deletions**:
- Simple timeout mechanism
- Limited intelligence about deletion order

### ArgoCD

**Immutable Fields**:
- Detects immutable field changes via dry-run
- UI shows "OutOfSync - requires recreation"
- Offers "Replace" sync option

**Complex Deletions**:
- Sophisticated finalizer handling
- Cascading deletion with proper ordering
- Retry logic with exponential backoff

### Flux

**Immutable Fields**:
- Uses server-side dry-run to detect issues
- Automatically recreates resources when needed
- Configurable recreation strategies

**Complex Deletions**:
- Prune policies with dependency ordering
- Finalizer removal after timeout
- Health checks during deletion

## Proposed Solutions

### Option 1: Dry-Run Detection with Automatic Recreation (CHOSEN)

**Approach**: Perform dry-run during plan phase, detect immutable field errors (422 Invalid), automatically plan recreation.

**How it works**: ModifyPlan performs dry-run apply. If error contains "immutable" pattern, mark resource for replacement. User sees clear plan output showing recreation before apply.

**Pros**:
- Automatic handling - no user intervention
- Clear plan output showing recreation
- Works with any resource type including CRDs

**Cons**:
- Extra API call for every update (already doing dry-run for projection)
- Dry-run isn't perfect predictor (rare edge cases)

### Option 2: Enhanced Error Messages with Manual Recreation

**Approach**: Detect immutable errors at apply time, provide detailed resolution steps. User must manually taint or add lifecycle rule.

**Pros**:
- No performance overhead
- User has full control

**Cons**:
- Manual intervention required
- Poor user experience
- Error occurs at apply time (too late)

### Option 3: Configurable Recreation Strategy

**Approach**: Add `recreate_on_immutable_change` option, maintain list of known immutable fields per resource type.

**Pros**:
- User control over behavior
- Can be intelligent about known types

**Cons**:
- Maintaining immutable field list is a maintenance nightmare
- Complex implementation
- Will miss CRDs and custom resources

## Decision

Implement **Option 1: Dry-Run Detection with Automatic Recreation**.

We already perform dry-run during plan phase for accurate diff projection (ADR-001), so the performance overhead is already paid. Detection via dry-run is universal - works with any Kubernetes resource including CRDs without maintaining field lists.

The automatic recreation provides the best UX - users see the replacement in the plan output before apply, just like HashiCorp's provider, but without requiring schema definitions.

## Implementation Notes

### Detection Logic
During ModifyPlan, after dry-run:
1. Check if error is 422 Invalid
2. Check if error message contains "immutable" or "Forbidden: spec is immutable"
3. If both true, mark resource for replacement via RequiresReplace

### User Experience
Plan output shows:
```
# k8sconnect_object.pvc must be replaced
-/+ resource "k8sconnect_object" "pvc" {
    ~ yaml_body = <<YAML
        spec:
          resources:
            requests:
-             storage: 10Gi
+             storage: 20Gi
      YAML
}
```

## Key Considerations

### Data Loss Risk
Recreating resources like PVCs can cause data loss. We rely on Terraform's standard plan review workflow - users see the replacement before apply and can cancel if unintended.

### Partial Immutability
Some fields have directional constraints (e.g., PVC storage can grow but not shrink). Dry-run correctly detects these - the server returns the specific error.

### Server-Side Changes
If admission webhooks modify immutable fields, we respect the server's values via projection (ADR-001). We never show drift on fields we don't manage.

### Performance
Dry-run latency is already part of the plan phase for projection. Immutability detection adds no additional API calls.

### CRD Support
Works automatically with CRDs - we don't maintain field lists. The API server is the source of truth for what's immutable.

## Limitations

1. **Dry-run accuracy**: Rare edge cases where dry-run succeeds but actual apply fails
2. **Timing**: Detection happens at plan time, but some immutability might be conditional on resource state
3. **Warnings**: No special warnings for high-risk resources (PVCs with data) - users must review plan output

These limitations are acceptable because the alternative - cryptic errors at apply time - is worse.

## References

1. [Kubernetes API Conventions - Immutable Fields](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#primitive-types)
2. [HashiCorp Kubernetes Provider Source](https://github.com/hashicorp/terraform-provider-kubernetes)
3. [Server-Side Apply Field Management](https://kubernetes.io/docs/reference/using-api/server-side-apply/#field-management)
4. [Finalizers Documentation](https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/)
