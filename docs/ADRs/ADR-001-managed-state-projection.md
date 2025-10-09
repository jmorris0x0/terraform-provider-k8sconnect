# ADR-001: Kubernetes Terraform Provider with Managed State Projection

## Status
Accepted

## Summary

Use Kubernetes dry-run to show accurate diffs. Store a JSON projection of managed fields. Field path extraction and projection operations track only the fields defined in the user's YAML. This is the only approach that can provide accurate diffs while respecting Kubernetes' collaborative model.

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

### Why This Is The Only Viable Solution

After extensive analysis, this approach is the **only** one that can provide accurate diffs while respecting Kubernetes' model because:

1. **Client-side prediction is impossible** - You cannot predict webhook mutations, admission controller changes, or server-side defaults without reimplementing the entire Kubernetes API server
2. **Server-side dry-run is the source of truth** - Only the actual API server knows what will happen when you apply a resource
3. **Field ownership must be respected** - Kubernetes is designed for multiple controllers to collaborate on resources

### Technical Constraints

1. **Cannot Replace Terraform's Diff Engine**: The provider protocol assumes Terraform computes diffs
2. **Cannot Access Desired State During Read**: Read() only has current state
3. **Plan Modification Limitations**: Can only modify planned values within constraints
4. **State Corruption Concerns**: Storing "synthetic" state breaks drift detection

Note: The performance impact of an additional dry-run API call during planning is negligible - Terraform already makes multiple API calls during any plan operation to refresh state.

## Decision

Implement a provider that uses **Managed Fields Projection** to achieve accurate structured diffs.

### Key Design Elements

1. **Store both YAML and managed field projection**
   ```hcl
   resource "kubernetes_manifest_v2" "example" {
     yaml_body = file("deployment.yaml")
   }
   ```

2. **Internal schema tracks managed fields as JSON**
   ```go
   "yaml_body": schema.StringAttribute{
       Required: true,
   },
   "managed_fields_projection": schema.StringAttribute{
       Computed: true,
       Description: "JSON projection of fields managed by this resource",
       PlanModifiers: []planmodifier.String{
           &managedFieldsProjectionModifier{},
       },
   }
   ```

3. **Use server-side apply with explicit field manager**
   - Field manager: `terraform-provider`
   - Only manage fields present in user's YAML
   - Respect other controllers' field ownership

4. **Leverage dry-run during plan phase**
   - ModifyPlan performs dry-run to get actual changes
   - Update planned values to match reality
   - Show accurate diffs for managed fields

### Implementation Strategy

Two key operations handle the projection:

#### Read Phase
```go
func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
    // Get current resource from Kubernetes
    current, err := k8sClient.Get(ctx, resourceID)
    
    // Get yaml_body from state to determine managed field paths
    var data manifestResourceModel
    req.State.Get(ctx, &data)
    
    desiredObj := parseYAML(data.YAMLBody.ValueString())
    managedPaths := extractFieldPaths(desiredObj)
    
    // Extract only managed fields from current state
    projection := make(map[string]interface{})
    for _, path := range managedPaths {
        if value, exists := getFieldByPath(current, path); exists {
            setFieldByPath(projection, path, value)
        }
    }
    
    // Store the projection
    data.ManagedFieldsProjection = types.StringValue(toJSON(projection))
    resp.State.Set(ctx, &data)
}
```

#### Plan Modification Phase (Using Plugin Framework)
```go
func (r *Resource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
    // Skip during destroy
    if req.Plan.Raw.IsNull() {
        return
    }
    
    // Get desired state from plan
    var plannedData manifestResourceModel
    req.Plan.Get(ctx, &plannedData)
    
    // Parse desired state
    desiredObj := parseYAML(plannedData.YAMLBody.ValueString())
    
    // Perform dry-run with field manager
    dryRunResult, err := k8sClient.DryRunApply(ctx, desiredObj, 
        FieldManager: "terraform-provider",
        Force: true,
    )
    
    // Extract managed fields from dry-run result
    managedPaths := extractFieldPaths(desiredObj)
    projectedState := extractFieldsAtPaths(dryRunResult, managedPaths)
    
    // Update the plan with accurate projection
    plannedData.ManagedFieldsProjection = types.StringValue(toJSON(projectedState))
    resp.Plan.Set(ctx, &plannedData)
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
- **Simple Import**: Multiple import strategies (all fields, unowned only, or from specific manager)
- **Minimal Performance Impact**: One additional API call is negligible compared to existing operations

### Negative
- **Implementation Complexity**: Requires handling multiple Kubernetes-specific edge cases

### Neutral
- **Field Conflicts**: Must handle when taking ownership from other controllers
- **Limited Computed Field Visibility**: This is the reality of field management - Terraform only owns what it manages
- **Debugging Complexity**: More complex internals than simple providers, but well worth the accuracy

## Implementation Notes

### Why This Complexity Is Necessary
Handling Kubernetes correctly requires addressing multiple edge cases. This is not over-engineering - each handled case prevents a class of user-facing bugs:

- **Without quantity normalization handling**: Users see false diffs on every plan
- **Without strategic merge handling**: List updates show incorrect changes
- **Without unknown value handling**: Plans fail when using computed values
- **Without type preservation**: Boolean/string confusion causes apply failures

The alternative is to continue showing incorrect diffs, which makes Terraform unreliable for Kubernetes.

### Critical Components
1. **Field Path Extraction**: Tree traversal to identify all field paths
2. **Projection Logic**: Get/set operations on paths
3. **Type Preservation**: Handle lists, maps, primitives correctly
4. **Dry-Run Integration**: API call with proper error handling
5. **Field Manager Configuration**: Consistent naming and force behavior

### Critical Edge Cases That Must Be Handled

#### Kubernetes Quantity Normalization
Kubernetes normalizes resource quantities into canonical forms that differ from user input:
```yaml
# User writes:
resources:
  limits:
    memory: "1Gi"      # → Server stores as: "1073741824"
    cpu: "100m"        # → Server stores as: "0.1"
    storage: "5000Mi"  # → Server might store as: "5Gi"
```

**Impact**: Without handling this, every plan will show false changes.

**Solution**: The dry-run response contains the server's normalized values. We must store these normalized forms in our projection, not the user's original input. This ensures the diff compares like-for-like.

#### Unknown Values During Planning
When connection details depend on other resources, they may be unknown during planning:
```hcl
cluster_connection = {
  host = aws_eks_cluster.main.endpoint  # Unknown during plan
}
```

**Solution**: 
- Detect unknown values in ModifyPlan
- Skip dry-run when connection is unknown
- Set managed_fields_projection to unknown
- Let Terraform show generic "will be modified" 
- Perform actual projection during apply when values are known

#### Strategic Merge Patch Semantics
Kubernetes uses different merge strategies for different list types:
```yaml
# Replace entire list (default)
args: ["--flag1", "--flag2"]

# Strategic merge by name
containers:
- name: app    # Merged by 'name' field
  image: v2

# Strategic merge with patch strategy
tolerations:   # Uses deletionStrategy
- key: "key1"
  operator: "Equal"
```

**Solution**: Use `k8s.io/apimachinery/pkg/util/strategicpatch` to handle merging correctly. Without this, list updates will show incorrect diffs.

### Import Strategies
Import supports multiple strategies via import ID format:
- `namespace/kind/name` - Import all fields (take full ownership)
- `namespace/kind/name?unowned` - Import only unowned fields
- `namespace/kind/name?manager=kubectl` - Import fields owned by specific manager

Example implementation:
```go
func extractFieldsBasedOnStrategy(liveObj *unstructured.Unstructured, strategy string) []string {
    switch strategy {
    case "unowned":
        return extractUnownedFields(liveObj.GetManagedFields())
    case "manager=kubectl":
        return extractFieldsOwnedBy(liveObj.GetManagedFields(), "kubectl")
    default:
        return extractAllFields(liveObj)
    }
}
```

### Core Operations
```go
// Extract paths using recursion
func extractFieldPaths(obj map[string]interface{}, prefix string) []string {
    var paths []string
    for key, value := range obj {
        path := prefix + "." + key
        paths = append(paths, path)
        if nested, ok := value.(map[string]interface{}); ok {
            paths = append(paths, extractFieldPaths(nested, path)...)
        }
    }
    return paths
}
```

### Edge Cases
1. **Field Conflicts**: Default to force=true with clear documentation
2. **List Handling**: Strategic merge patch semantics (use Kubernetes libraries)
3. **Migration**: Not needed - pre-alpha release

## Plugin Framework Specifics

The Terraform Plugin Framework provides the necessary capabilities through:

1. **ResourceWithModifyPlan Interface**: Allows modification of the entire resource plan
2. **Attribute Plan Modifiers**: Can modify individual attribute values
3. **Computed Attributes**: Can be updated during planning based on dry-run results

This is superior to SDK v2's CustomizeDiff because:
- More explicit API with clear request/response types
- Better separation of concerns (plan vs state vs config)
- Native support for complex types without string manipulation

## Alternatives Considered

1. **Store Full Structured State**: Rejected due to state bloat
2. **Synthetic State During Read**: Rejected as it corrupts drift detection
3. **Pure String Comparison**: Rejected as not truly structured
4. **Wait for Terraform Protocol Change**: Rejected as unlikely to happen

## Limitations

This approach has known limitations that users must understand:

1. **Normalized Values**: The stored state will contain server-normalized values (e.g., "1Gi" → "1073741824"), not user input
2. **Computed Fields**: Only shows changes to fields present in user's YAML - computed fields from other controllers are invisible
3. **Dry-Run Accuracy**: Depends on dry-run accurately predicting mutations (true for well-behaved webhooks)
4. **Additional API Call**: Adds latency during planning phase (typically 50-200ms per resource)

These limitations are acceptable because the alternative - showing wrong diffs - is worse.

## References

- [Terraform Plugin Framework Plan Modification](https://developer.hashicorp.com/terraform/plugin/framework/resources/plan-modification)
- [kubernetes-alpha Provider Repository](https://github.com/hashicorp/terraform-provider-kubernetes-alpha)
- [Kubernetes Server-Side Apply Documentation](https://kubernetes.io/docs/reference/using-api/server-side-apply/)
- [Kubernetes Field Management](https://kubernetes.io/docs/reference/using-api/server-side-apply/#field-management)

## Appendix: Why This Works

Instead of trying to predict what Kubernetes will do (impossible), we ask Kubernetes what it will do (dry-run), then show only the parts we care about (projection).

It's like asking "what will change if I apply this?" instead of guessing. The field projection ensures we only track fields we actually manage, preventing state bloat and respecting Kubernetes' collaborative model.
