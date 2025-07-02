# Architecture Decision Record: Kubernetes Terraform Provider with Managed Fields Projection

## Title
Implement Structured Diffs in Kubernetes Terraform Provider Using Managed Fields Projection

## Status
Proposed

## Context

### The Problem
When building a Terraform provider for Kubernetes that accepts raw YAML and uses server-side apply, there is a fundamental impedance mismatch between how Terraform and Kubernetes handle resource updates:

- **Terraform's model**: Compare two data structures (desired vs current) and compute a diff
- **Kubernetes' model**: Use server-side apply with strategic merge patches, webhooks, and defaulting

This creates a situation where the diff shown during `terraform plan` does not match what actually happens during `terraform apply`.

### Why This Matters
1. **User Trust**: Incorrect diffs erode confidence in infrastructure as code
2. **Operational Safety**: Teams need accurate plans to review changes before applying
3. **Automation**: CI/CD pipelines rely on accurate diffs for approval workflows

### Previous Attempts and Why They Failed

#### kubernetes-alpha Provider (HashiCorp, 2020)
- **Approach**: Use server-side apply directly
- **Failed because**: Field ownership conflicts, no meaningful diffs during plan phase
- **Result**: Deprecated

#### Schema Translation (Official Provider)  
- **Approach**: Translate every Kubernetes resource type into Terraform schema
- **Failed because**: Massive maintenance burden, can't support CRDs automatically, still shows incorrect diffs
- **Result**: Incomplete resource coverage

#### Text-Based Diffs (kubectl provider)
- **Approach**: Treat YAML as strings and show text diffs
- **Failed because**: Shows formatting changes that won't happen, misses actual changes
- **Result**: Not truly "structured" diffs

### The Gap in All Existing Providers

None of them:
- Show you who owns which fields
- Warn you when taking ownership from another controller
- Let you choose whether to force ownership
- Respect the collaborative nature of Kubernetes

### Technical Constraints

1. **Cannot Replace Terraform's Diff Engine**: The provider protocol assumes Terraform computes diffs
2. **Cannot Access Desired State During Read**: Read() only has current state
3. **CustomizeDiff Limitations**: Can only modify existing fields, works on structured data
4. **State Corruption Concerns**: Storing "synthetic" state breaks drift detection

## Decision

Implement a provider that uses **Managed Fields Projection** to achieve accurate structured diffs.

### Key Design Elements

1. **Store both YAML and managed field projection**
   ```hcl
   resource "kubernetes_manifest_v2" "example" {
     yaml_body = file("deployment.yaml")
   }
   ```

2. **Internal schema tracks only managed fields**
   ```go
   "yaml_body": {
       Type:     schema.TypeString,
       Required: true,
   },
   "managed_fields_data": {
       Type:     schema.TypeMap,
       Computed: true,
       Internal: true,
   }
   ```

3. **Use server-side apply with explicit field manager**
   - Field manager: `terraform-provider`
   - Only manage fields present in user's YAML
   - Respect other controllers' field ownership

4. **Leverage dry-run during diff phase**
   - CustomizeDiff performs dry-run to get actual changes
   - Modify Terraform's diff to match reality
   - Show accurate diffs for managed fields

### Implementation Strategy

#### Read Phase
```go
func (r *Resource) Read(ctx context.Context, d *schema.ResourceData, meta interface{}) error {
    // Get current resource from Kubernetes
    current, err := k8sClient.Get(ctx, resourceID)
    
    // Parse user's YAML to determine managed field paths
    userYAML := d.Get("yaml_body").(string)
    desiredObj := parseYAML(userYAML)
    managedPaths := extractFieldPaths(desiredObj)
    
    // Extract only managed fields from current state
    projection := make(map[string]interface{})
    for _, path := range managedPaths {
        if value, exists := getFieldByPath(current, path); exists {
            setFieldByPath(projection, path, value)
        }
    }
    
    // Store the projection
    d.Set("managed_fields_data", flattenForTerraform(projection))
    return nil
}
```

#### CustomizeDiff Phase
```go
func (r *Resource) CustomizeDiff(ctx context.Context, d *schema.ResourceDiff, meta interface{}) error {
    // Parse desired state
    desiredYAML := d.Get("yaml_body").(string)
    desired := parseYAML(desiredYAML)
    
    // Perform dry-run with field manager
    dryRunResult, err := k8sClient.DryRun(ctx, desired, 
        FieldManager: "terraform-provider",
        Force: true,
    )
    
    // Update Terraform's diff to match reality
    // ... (implementation details)
    
    return nil
}
```

## Consequences

### Positive
- **Accurate Diffs**: Shows exactly what SSA will change for managed fields
- **Bounded State**: Only stores fields present in user's YAML
- **Universal Resource Support**: Works with any Kubernetes resource, including CRDs
- **Respects Field Ownership**: Other controllers can modify other fields freely
- **Predictable Behavior**: Diff matches apply because both use SSA
- **Kubernetes-Native**: Embraces rather than fights Kubernetes patterns

### Negative
- **Implementation Complexity**: Requires sophisticated field extraction and projection logic
- **Performance Impact**: Dry-run API call during every plan
- **Import UX**: Requires configuration before import
- **Limited Computed Field Visibility**: Cannot show fields Kubernetes will add
- **Migration Path**: Existing users need state migration

### Neutral
- **Field Conflicts**: Must handle when taking ownership from other controllers
- **Debugging Complexity**: More complex internals than simple providers

## Implementation Notes

### Critical Components
1. **Field Path Extraction**: Parse YAML to identify all field paths
2. **Projection Logic**: Extract/inject values at arbitrary paths
3. **Type Preservation**: Handle lists, maps, primitives correctly
4. **Dry-Run Integration**: Proper error handling and conflict detection
5. **Field Manager Configuration**: Consistent naming and force behavior

### Edge Cases
1. **Import**: Require yaml_body in configuration before import
2. **Field Conflicts**: Default to force=true with clear documentation
3. **List Handling**: Strategic merge patch semantics
4. **Migration**: Tool to migrate from existing providers

## Alternatives Considered

1. **Store Full Structured State**: Rejected due to state bloat
2. **Synthetic State During Read**: Rejected as it corrupts drift detection
3. **Pure String Comparison**: Rejected as not truly structured
4. **Wait for Terraform Protocol Change**: Rejected as unlikely to happen

## References

- [Terraform Issue #8769: Custom Diff Logic in Providers](https://github.com/hashicorp/terraform/issues/8769)
- [kubernetes-alpha Provider Repository](https://github.com/hashicorp/terraform-provider-kubernetes-alpha)
- [Kubernetes Server-Side Apply Documentation](https://kubernetes.io/docs/reference/using-api/server-side-apply/)
- [Kubernetes Field Management](https://kubernetes.io/docs/reference/using-api/server-side-apply/#field-management)

## Appendix: Theoretical Foundation

For those interested in the mathematical underpinnings, this solution can be understood as a projection operator that maps the full Kubernetes state space to a managed field subspace. By working exclusively in this subspace, we preserve the diff structure while avoiding the impossibility of creating an isomorphism between Terraform's and Kubernetes' full state representations.

In essence, we compute diffs on π(S) rather than S, where π is the projection to managed fields. This ensures that our diff calculations remain accurate while operating in a bounded, well-defined space.