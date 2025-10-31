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

### How It Works

**Read Phase**: Get current resource from Kubernetes, parse user's YAML to determine which field paths to track, extract only those fields from the live state, store as JSON projection.

**Plan Phase**: Parse desired YAML, perform dry-run apply with field manager, extract managed fields from dry-run result, update plan with accurate projection.

This ensures the diff compares the same fields that will actually be managed.

## Consequences

### Positive
- **Accurate Diffs**: Shows exactly what SSA will change for managed fields
- **Bounded State**: Only stores fields present in user's YAML
- **Universal Resource Support**: Works with any Kubernetes resource, including CRDs
- **Respects Managed Fields**: Other controllers can modify other fields freely
- **Predictable Behavior**: Diff matches apply because both use SSA
- **Kubernetes-Native**: Embraces rather than fights Kubernetes patterns

### Negative
- **Implementation Complexity**: Requires handling multiple Kubernetes-specific edge cases

### Neutral
- **Field Conflicts**: Must handle when taking ownership from other controllers
- **Limited Computed Field Visibility**: This is the reality of field management - Terraform only owns what it manages

## Implementation Notes

### Why This Complexity Is Necessary
Handling Kubernetes correctly requires addressing multiple edge cases. This is not over-engineering - each handled case prevents a class of user-facing bugs:

- **Without quantity normalization handling**: Users see false diffs on every plan
- **Without strategic merge handling**: List updates show incorrect changes
- **Without unknown value handling**: Plans fail when using computed values
- **Without type preservation**: Boolean/string confusion causes apply failures

The alternative is to continue showing incorrect diffs, which makes Terraform unreliable for Kubernetes.

### Critical Edge Cases That Must Be Handled

#### Kubernetes Quantity Normalization
Kubernetes normalizes resource quantities into canonical forms:
```yaml
# User writes:        # Server stores as:
memory: "1Gi"         # "1073741824"
cpu: "100m"           # "0.1"
storage: "5000Mi"     # "5Gi"
```

**Impact**: Without handling this, every plan shows false changes.

**Solution**: Store the dry-run's normalized values in projection, not user's input. This ensures like-for-like comparison.

#### Unknown Values During Planning
When connection details depend on other resources:
```hcl
cluster = {
  host = aws_eks_cluster.main.endpoint  # Unknown during plan
}
```

**Solution**: Detect unknown values, skip dry-run, set projection to unknown. Perform actual projection during apply when values are known.

#### Strategic Merge Patch Semantics
Kubernetes uses different merge strategies for different list types:
```yaml
args: ["--flag1"]          # Replace entire list
containers:
- name: app                # Merge by 'name' field
tolerations:
- key: "key1"              # Uses deletionStrategy
```

**Solution**: Parse merge keys from `managedFields.fieldsV1` to understand which array items we own. The actual strategic merge is performed server-side by Kubernetes during SSA - we just need to correctly extract our owned fields from the result.

### Import Strategies
Import supports multiple strategies via import ID format:
- `namespace/kind/name` - Import all fields (take full ownership)
- `namespace/kind/name?unowned` - Import only unowned fields
- `namespace/kind/name?manager=kubectl` - Import fields owned by specific manager

### Edge Cases
1. **Field Conflicts**: Default to force=true with clear documentation
2. **List Handling**: Strategic merge patch semantics (use Kubernetes libraries)
3. **Migration**: Not needed - pre-alpha release

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
