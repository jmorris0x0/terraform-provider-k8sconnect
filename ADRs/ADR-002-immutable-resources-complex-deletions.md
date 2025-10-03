# ADR-002: Handling Immutable Resources and Complex Deletions

## Status
Draft - Open Questions
Only enhanced error messages implemented. No automatic recreation or dry-run detection.

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
# Initial configuration
resource "k8sconnect_manifest" "pvc" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data-volume
spec:
  resources:
    requests:
      storage: 10Gi  # User changes to 20Gi
YAML
}
```

**Current behavior**: 
```
Error: Update: Invalid Resource

The PersistentVolumeClaim data-volume contains invalid fields or values. 
Review the YAML specification and ensure all required fields are present 
and correctly formatted. Details: PersistentVolumeClaim "data-volume" is 
invalid: spec: Forbidden: spec is immutable after creation
```

**Problems**:
- Generic error message doesn't explain immutability
- No guidance on how to resolve
- Users often don't understand they need to recreate

### Complex Deletions

When resources get stuck during deletion:
```hcl
# PVC with volume in use
# Namespace with resources still inside
# Custom resources with finalizers waiting for controller
```

**Current behavior**: We timeout and provide guidance, but could be more intelligent.

## Research: How Other Providers Handle This

### 1. HashiCorp Kubernetes Provider

**Immutable Fields**:
- Uses schema with `ForceNew: true` for known immutable fields
- Terraform automatically plans resource recreation
- Clear in plan output: `# must be replaced`

**Example**:
```go
"storage_class_name": {
    Type:     schema.TypeString,
    Optional: true,
    ForceNew: true,  // Forces recreation on change
    Computed: true,
}
```

**Complex Deletions**:
- Basic timeout support
- No built-in finalizer handling
- Users often resort to local-exec provisioners

### 2. Crossplane Provider

**Immutable Fields**:
- Plans to use CEL (Common Expression Language) rules for immutability validation
- Allows editing immutable fields but doesn't apply the changes
- Never automatically deletes resources based on forProvider changes (unlike Terraform)
- Treats managed resources as source of truth, expects all values under spec.forProvider

**Complex Deletions**:
- Implements cascading deletion policies
- Handles finalizers intelligently
- Provides deletion ordering

### 3. Kubectl Provider (Gavin Bunney)

**Immutable Fields**:
- No special handling - errors bubble up from Kubernetes
- Users must manually add lifecycle rules
- Community created helper modules for common cases

**Complex Deletions**:
- Simple timeout mechanism
- `force_delete` option that patches finalizers
- Limited intelligence about deletion order

### 4. ArgoCD

**Immutable Fields**:
- Detects immutable field changes via dry-run
- UI shows "OutOfSync - requires recreation"
- Offers "Replace" sync option

**Complex Deletions**:
- Sophisticated finalizer handling
- Cascading deletion with proper ordering
- Retry logic with exponential backoff

### 5. Flux

**Immutable Fields**:
- Uses server-side dry-run to detect issues
- Automatically recreates resources when needed
- Configurable recreation strategies

**Complex Deletions**:
- Prune policies with dependency ordering
- Finalizer removal after timeout
- Health checks during deletion

## Proposed Solutions

### Option 1: Dry-Run Detection with Automatic Recreation

**Approach**:
1. Before Update, perform dry-run apply
2. Detect immutable field errors (422 Invalid with specific patterns)
3. Automatically plan recreation if immutable fields changed
4. Show clear message in plan output

**Implementation**:
```go
func (r *manifestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
    // Perform dry-run first
    dryRunResult, err := client.DryRunApply(ctx, obj, k8sclient.ApplyOptions{
        FieldManager: DefaultFieldManager,
        DryRun:       []string{metav1.DryRunAll},
    })
    
    if err != nil && r.isImmutableFieldError(err) {
        // Force recreation instead of update
        resp.RequiresReplace = append(resp.RequiresReplace, path.Root("yaml_body"))
        return
    }
    
    // Proceed with normal update...
}

func (r *manifestResource) isImmutableFieldError(err error) bool {
    if statusErr, ok := err.(*errors.StatusError); ok {
        return statusErr.ErrStatus.Code == 422 && 
               strings.Contains(statusErr.ErrStatus.Message, "immutable")
    }
    return false
}
```

**Pros**:
- Automatic handling of immutable fields
- Clear plan output showing recreation
- No user intervention required

**Cons**:
- Extra API call for every update
- May force unwanted recreations
- Dry-run isn't perfect predictor

### Option 2: Enhanced Error Messages with Manual Recreation

**Approach**:
1. Detect immutable field errors during apply
2. Provide detailed error message with resolution steps
3. User must manually taint resource or add lifecycle rule

**Implementation**:
```go
func (r *manifestResource) classifyK8sError(err error, operation, resourceDesc string) (severity, title, detail string) {
    // ... existing cases ...
    
    case r.isImmutableFieldError(err):
        immutableFields := r.extractImmutableFields(err)
        return "error", fmt.Sprintf("%s: Immutable Field Changed", operation),
            fmt.Sprintf("Cannot update immutable fields on %s: %v\n\n"+
                "Immutable fields cannot be changed after resource creation.\n\n"+
                "Resolution options:\n"+
                "1. Revert the changes to immutable fields\n"+
                "2. Delete and recreate the resource:\n"+
                "   terraform destroy -target=%s\n"+
                "   terraform apply\n"+
                "3. Add lifecycle rule to force recreation:\n"+
                "   lifecycle {\n"+
                "     replace_triggered_by = [null_resource.trigger]\n"+
                "   }",
                resourceDesc, immutableFields, req.ID)
}
```

**Pros**:
- No performance overhead
- User has full control
- Works with any resource type

**Cons**:
- Manual intervention required
- Poor user experience
- Error occurs at apply time

### Option 3: Configurable Recreation Strategy

**Approach**:
1. Add `recreate_on_immutable_change` option to resource
2. Maintain list of known immutable fields per resource type
3. Detect changes to immutable fields during plan
4. Force recreation based on configuration

**Implementation**:
```go
type manifestResourceModel struct {
    // ... existing fields ...
    RecreateOnImmutableChange types.Bool `tfsdk:"recreate_on_immutable_change"`
}

var knownImmutableFields = map[string][]string{
    "PersistentVolumeClaim": {"spec.storageClassName", "spec.accessModes", "spec.resources.requests.storage"},
    "Service": {"spec.clusterIP", "spec.type", "spec.ports[*].protocol"},
    "Pod": {"spec.containers[*].image", "spec.containers[*].command"},
}
```

**Pros**:
- User control over behavior
- Can be intelligent about known types
- Good balance of automation and control

**Cons**:
- Maintaining immutable field list
- Complex implementation
- May miss custom resources

## Open Questions

### 1. Immutable Field Detection Strategy

**Q1.1**: Should we use dry-run to detect immutability proactively or handle errors reactively?
- **Consideration**: Dry-run adds latency but provides better UX
- **Trade-off**: Performance vs user experience

**Q1.2**: How do we handle partial immutability (e.g., can grow PVC but not shrink)?
- **Example**: PVC storage can increase but not decrease
- **Challenge**: Simple immutable/mutable classification insufficient

**Q1.3**: Should we maintain a registry of known immutable fields?
- **Pros**: Faster detection, better error messages
- **Cons**: Maintenance burden, doesn't handle CRDs

### 2. Recreation Behavior

**Q2.1**: Should recreation be automatic or require user confirmation?
- **Option A**: Automatic with clear plan output
- **Option B**: Error with manual intervention required
- **Option C**: Configurable behavior

**Q2.2**: How do we handle resources with persistent data (PVCs, StatefulSets)?
- **Risk**: Data loss on recreation
- **Mitigation**: Special warnings? Block recreation?

**Q2.3**: What about resources with external dependencies?
- **Example**: Service with external DNS/load balancer
- **Challenge**: Recreation might break external integrations

### 3. Deletion Improvements

**Q3.1**: Should we implement intelligent finalizer handling beyond force_destroy?
- **Option A**: Remove specific finalizers after timeout
- **Option B**: Cascading deletion strategies
- **Option C**: Exponential backoff with health checks

**Q3.2**: How do we handle deletion ordering for dependent resources?
- **Example**: Namespace with resources inside
- **Challenge**: Need dependency graph analysis

**Q3.3**: Should we provide deletion progress feedback?
- **Current**: Silent until timeout
- **Proposed**: Periodic status checks with progress

### 4. API and UX Design

**Q4.1**: Where should recreation configuration live?
```hcl
# Option A: Resource-level attribute
resource "k8sconnect_manifest" "example" {
  recreate_on_immutable_change = true
  # ...
}

# Option B: Lifecycle-style block
resource "k8sconnect_manifest" "example" {
  immutable_fields {
    behavior = "recreate" # or "error"
    known_fields = ["spec.storageClassName"]
  }
  # ...
}

# Option C: Provider-level default with overrides
provider "k8sconnect" {
  immutable_field_behavior = "recreate"
}
```

**Q4.2**: How should we surface immutability information in plans?
- Show which fields are immutable?
- Warn about potential recreation before apply?
- Custom diff formatting?

### 5. Performance Considerations

**Q5.1**: Is the overhead of dry-run acceptable for all updates?
- **Measurement needed**: Latency impact
- **Alternative**: Only dry-run for known problematic types

**Q5.2**: Should we cache immutability information?
- **Example**: Remember that a field is immutable after first error
- **Challenge**: Cache invalidation, schema changes

### 6. Edge Cases

**Q6.1**: How do we handle server-side changes to immutable fields?
- **Scenario**: Admission webhook modifies immutable field
- **Current**: Would cause perpetual diff
- **Solution**: Ignore? Warn? Error?

**Q6.2**: What about resources that become immutable conditionally?
- **Example**: Some fields immutable only after resource is "Ready"
- **Challenge**: Need state awareness

**Q6.3**: How do we handle schema evolution?
- **Scenario**: Field becomes immutable in new K8s version
- **Challenge**: Behavior changes without Terraform changes

### 7. Compatibility and Migration

**Q7.1**: How do we ensure backward compatibility?
- Default behavior must not break existing configs
- Migration path for new features

**Q7.2**: Should this be opt-in or opt-out?
- **Consideration**: Safety vs convenience
- **Precedent**: What do other providers do?

## Next Steps

1. **Spike**: Implement dry-run detection prototype to measure performance impact
2. **Survey**: Poll community on preferred behavior (automatic vs manual recreation)
3. **Research**: Deep dive into specific resource types (PVC, StatefulSet, Service)
4. **Design**: Create detailed proposal based on findings
5. **POC**: Build proof-of-concept for most promising approach

## References

1. [Kubernetes API Conventions - Immutable Fields](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#primitive-types)
2. [HashiCorp Kubernetes Provider Source](https://github.com/hashicorp/terraform-provider-kubernetes)
3. [Server-Side Apply Field Management](https://kubernetes.io/docs/reference/using-api/server-side-apply/#field-management)
4. [Finalizers Documentation](https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/)
5. [Crossplane Composition Functions](https://docs.crossplane.io/latest/concepts/composition-functions/)
