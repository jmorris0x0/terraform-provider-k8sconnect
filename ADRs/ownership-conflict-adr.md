# ADR-001: Cross-State Resource Ownership Conflicts in Kubernetes Terraform Providers

## Status
Proposed

## Context

### The Problem

When multiple Terraform states (configurations) attempt to manage the same Kubernetes resource, existing Terraform Kubernetes providers fail to detect or prevent conflicts, leading to silent corruption and unpredictable behavior. This is a fundamental issue affecting all major Kubernetes Terraform providers.

**Scenario**: Two separate Terraform projects manage the same Kubernetes namespace:
```hcl
# State A (team-a.tfstate)
resource "k8sinline_manifest" "prod_ns" {
  yaml_body = "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: production"
  # ... connection config
}

# State B (team-b.tfstate) 
resource "k8sinline_manifest" "prod_namespace" {
  yaml_body = "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: production"  
  # ... connection config
}
```

Both states generate identical ownership annotations and field manager names, resulting in:
1. **Silent conflicts** - both states believe they own the resource
2. **Unpredictable behavior** - last-applied-wins with no warning
3. **State corruption** - Terraform state becomes inconsistent with reality
4. **Difficult debugging** - no indication of the ownership conflict

### Current Provider Analysis

#### 1. Official Kubernetes Provider

**Ownership Mechanism:**
- Uses Server-Side Apply (SSA) with fixed `fieldManager` name ("Terraform" or "hashicorp")
- Relies on Kubernetes native field management
- Provides `force_conflicts = true` to override conflicts

**Cross-State Conflict Behavior:**
- Multiple Terraform states use **identical field manager names**
- No distinction between different Terraform configurations
- Conflicts only detected when external tools (kubectl, operators) modify resources

**User Complaints:**
> "Apply failed with 1 conflict: conflict with 'kubectl' using apps/v1: .spec.replicas" [[1]](https://github.com/hashicorp/terraform-provider-kubernetes-alpha/issues/134)

> "The suggested fix by setting force_conflicts = true is not a good solution. It will allow us to apply the plan, but always show the same changes on every plan output." [[2]](https://www.arthurkoziel.com/managing-kubernetes-resources-in-terraform-kubernetes-provider/)

**Limitations:**
- No protection against cross-state conflicts
- Binary conflict resolution (force all or fail)
- Field manager conflicts with external tools require `force_conflicts`

#### 2. Gavin Bunney's kubectl Provider

**Ownership Mechanism:**
- Client-side apply by default (no field management)
- Optional server-side apply with "kubectl" field manager
- No custom ownership tracking

**Cross-State Conflict Behavior:**
- Client-side apply: No conflict detection at all
- Server-side apply: Same issues as official provider
- Field manager name changes when switching apply modes

**User Complaints:**
> "It is currently impossible to switch to using server_side_apply = true for resources created without it due to the change in field manager name (Hashicorp to kubectl)" [[3]](https://github.com/gavinbunney/terraform-provider-kubectl/issues/139)

**Limitations:**
- No cross-state conflict detection
- Inconsistent field manager naming
- Mode switching breaks ownership tracking

#### 3. Kubernetes Alpha Provider (Archived)

**Approach:**
- Server-Side Apply focused from inception
- Enhanced field management support planned but never delivered
- Abandoned without solving ownership conflicts

**User Complaints:**
> "FieldManagerConflict on second 'tf apply'" [[4]](https://github.com/hashicorp/terraform-provider-kubernetes-alpha/issues/96)

> "Apply failed with 1 conflict: conflict with 'pilot-discovery' using admissionregistration.k8s.io/v1: .webhooks[name='rev.validation.istio.io'].failurePolicy" [[5]](https://www.arthurkoziel.com/managing-kubernetes-resources-in-terraform-kubernetes-provider/)

**Outcome:**
- Project archived without solving fundamental field management issues
- Demonstrates the complexity of the problem

#### 4. Helm Provider

**Ownership Mechanism:**
- Helm release tracking via Kubernetes secrets
- Unique release names within namespaces
- Built-in ownership model through Helm's design

**Cross-State Conflict Behavior:**
- **Natural isolation** - different release names prevent conflicts
- Clear error messages: "a release named X already exists"
- Graceful handling of release state corruption

**User Experience:**
> "Error: rpc error: code = Unknown desc = a release named my-chart already exists. Run: helm ls --all my-chart; to check the status of the release" [[6]](https://github.com/terraform-providers/terraform-provider-helm/issues/318)

**Success Factors:**
- Release names provide unique identity per deployment
- Helm's metadata tracks ownership explicitly
- Predictable conflict resolution

### Industry Best Practices

#### Server-Side Apply Field Management

Kubernetes documentation recommends:
> "Controllers should ensure that they don't cause unnecessary conflicts when modifying objects... controllers should really make sure that only fields related to the controller are included in the applied object." [[7]](https://kubernetes.io/blog/2022/11/04/live-and-let-live-with-kluctl-and-ssa/)

Advanced tooling like Kluctl implements:
- Field-level conflict resolution
- Custom field manager strategies  
- Selective force-apply based on conflict analysis

#### GitOps and Multi-Tool Environments

The Kubernetes ecosystem expects:
- Multiple tools managing different aspects of resources
- Clear ownership boundaries between tools
- Conflict detection and resolution mechanisms

## Decision Drivers

1. **User Safety**: Prevent silent conflicts that corrupt Terraform state
2. **Multi-State Support**: Enable safe operation across multiple Terraform configurations
3. **Backward Compatibility**: Don't break existing deployments
4. **Clear Error Messages**: Provide actionable guidance when conflicts occur
5. **Operational Simplicity**: Avoid complex configuration requirements

## Considered Options

### Option 1: Status Quo (No Solution)
**Description:** Continue with current Kubernetes field manager approach

**Pros:**
- No development effort required
- Maintains current behavior
- Compatible with all existing resources

**Cons:**
- Silent conflicts continue to occur
- No protection against cross-state resource corruption
- Poor user experience with unpredictable behavior
- Debugging ownership issues remains difficult

**Verdict:** Unacceptable - fails to solve the core problem

### Option 2: Enhanced Field Manager Names
**Description:** Include Terraform context in field manager names

```go
fieldManager := fmt.Sprintf("k8sinline-%s-%s", 
    terraform.WorkspaceName(), 
    hash(configPath)[:8])
```

**Pros:**
- Leverages existing Kubernetes field management
- Provides some conflict detection
- Minimal changes to current architecture

**Cons:**
- Field manager names become non-deterministic
- Terraform workspace/config info not reliably available to providers
- Still binary conflict resolution (force/fail)
- Breaks when configurations change

**Verdict:** Insufficient - addresses symptoms but not root cause

### Option 3: Distributed Lock System
**Description:** Use Kubernetes resources (ConfigMaps/Secrets) as distributed locks

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: k8sinline-lock-namespace-production
  namespace: k8sinline-system
data:
  owner: "team-a-state-abc123"
  created: "2024-01-01T00:00:00Z"
```

**Pros:**
- Strong consistency guarantees
- Clear ownership model
- Supports lease renewal and expiration

**Cons:**
- Requires additional cluster permissions
- Complex implementation and error handling
- Creates additional Kubernetes resources
- Difficult to clean up orphaned locks

**Verdict:** Over-engineered - adds significant complexity

### Option 4: Annotation-Based Ownership Tracking
**Description:** Store ownership metadata in resource annotations

```yaml
metadata:
  annotations:
    k8sinline.terraform.io/id: "abc123..."           # Deterministic resource ID
    k8sinline.terraform.io/context: "team-a-cfg"     # Terraform context identifier  
    k8sinline.terraform.io/created: "2024-01-01T00:00:00Z"
```

**Pros:**
- Leverages existing Kubernetes metadata
- No additional resources required
- Clear ownership tracking
- Supports conflict detection and resolution
- Maintains backward compatibility

**Cons:**
- Requires annotation management
- Potential annotation conflicts (low probability)
- Context detection may be imperfect

**Verdict:** Promising - balances functionality with simplicity

### Option 5: Hybrid Approach (Annotations + Enhanced Field Managers)
**Description:** Combine annotation-based ownership with improved field management

**Primary Ownership (Annotations):**
```yaml
metadata:
  annotations:
    k8sinline.terraform.io/id: "deterministic-hash"
    k8sinline.terraform.io/context: "context-specific-hash"  
    k8sinline.terraform.io/created: "timestamp"
```

**Secondary Ownership (Field Managers):**
```go
fieldManager := "k8sinline" // Consistent across all instances
// Use force_conflicts judiciously based on annotation analysis
```

**Pros:**
- Robust conflict detection through annotations
- Leverages Kubernetes field management for external tool conflicts
- Provides clear error messages with resolution guidance
- Maintains backward compatibility with legacy resources
- Enables sophisticated conflict resolution strategies

**Cons:**
- More complex implementation
- Requires careful annotation and field manager coordination

**Verdict:** Optimal - provides comprehensive solution

## Recommended Solution

**Option 5: Hybrid Approach (Annotations + Enhanced Field Managers)**

### Implementation Strategy

#### 1. Ownership Annotation Schema
```go
const (
    OwnershipIDAnnotation      = "k8sinline.terraform.io/id"
    OwnershipContextAnnotation = "k8sinline.terraform.io/context"  
    OwnershipTimestampAnnotation = "k8sinline.terraform.io/created-at"
)

type OwnershipContext struct {
    ConfigHash string `json:"config_hash"`  // Hash of YAML + available context
    Workspace  string `json:"workspace,omitempty"`
    CreatedAt  string `json:"created_at"`
}
```

#### 2. Conflict Detection Logic
```go
func (r *manifestResource) validateOwnership(liveObj *unstructured.Unstructured, expectedID string) error {
    annotations := liveObj.GetAnnotations()
    
    // Check for k8sinline ownership
    actualID := annotations[OwnershipIDAnnotation]
    if actualID == "" {
        return fmt.Errorf("resource exists but not managed by k8sinline - use terraform import")
    }
    
    if actualID != expectedID {
        return fmt.Errorf("resource managed by different k8sinline configuration")
    }
    
    // Check context for potential conflicts
    contextJSON := annotations[OwnershipContextAnnotation]
    if contextJSON != "" {
        var existingContext OwnershipContext
        json.Unmarshal([]byte(contextJSON), &existingContext)
        
        currentContext := generateOwnershipContext(data)
        if existingContext.ConfigHash != currentContext.ConfigHash {
            return fmt.Errorf("CONFLICT: Resource managed by different Terraform configuration\n" +
                "Existing: %s (created: %s)\n" +  
                "Current:  %s\n\n" +
                "Resolution options:\n" +
                "1. Use 'terraform import' to adopt the resource\n" +
                "2. Ensure only one Terraform configuration manages this resource\n" +
                "3. Remove k8sinline annotations to release ownership",
                existingContext.ConfigHash, existingContext.CreatedAt,
                currentContext.ConfigHash)
        }
    }
    
    return nil
}
```

#### 3. Context Generation Strategy
```go
func generateOwnershipContext(data manifestResourceModel) OwnershipContext {
    // Create context hash from available information
    contextData := fmt.Sprintf("%s|%s|%s",
        data.YAMLBody.ValueString(),          // Configuration content
        getTerraformWorkspace(),              // Workspace if available
        getConfigContext())                   // Additional context
    
    hash := sha256.Sum256([]byte(contextData))
    
    return OwnershipContext{
        ConfigHash: hex.EncodeToString(hash[:8]),
        Workspace:  getTerraformWorkspace(),
        CreatedAt:  time.Now().UTC().Format(time.RFC3339),
    }
}
```

#### 4. Error Messages and User Experience
- **Clear conflict descriptions** with specific resource information
- **Actionable resolution steps** (import, manual cleanup, etc.)
- **Backward compatibility** with legacy resources (no annotations)
- **Graceful degradation** when context detection fails

### Benefits

1. **Prevents Silent Conflicts**: Cross-state conflicts are detected and reported clearly
2. **Maintains Compatibility**: Existing resources continue to work
3. **Clear Error Messages**: Users understand what went wrong and how to fix it
4. **Flexible Resolution**: Multiple strategies for handling conflicts
5. **External Tool Safety**: Field manager still provides protection against kubectl/operator conflicts

### Risks and Mitigations

**Risk**: Annotation conflicts between different k8sinline versions
- **Mitigation**: Use versioned annotation keys and migration logic

**Risk**: Context detection failures
- **Mitigation**: Graceful fallback to legacy behavior with warnings

**Risk**: Performance impact from annotation management  
- **Mitigation**: Minimal overhead - annotations are small and cached

## References

1. [Terraform kubernetes-alpha provider Issue #134](https://github.com/hashicorp/terraform-provider-kubernetes-alpha/issues/134) - kubectl field manager conflicts
2. [Managing Kubernetes resources in Terraform](https://www.arthurkoziel.com/managing-kubernetes-resources-in-terraform-kubernetes-provider/) - Force conflicts analysis
3. [kubectl provider Issue #139](https://github.com/gavinbunney/terraform-provider-kubectl/issues/139) - Server-side apply field manager issues
4. [Kubernetes Alpha Provider Issue #96](https://github.com/hashicorp/terraform-provider-kubernetes-alpha/issues/96) - FieldManager conflicts on second apply
5. [RabbitMQ Cluster Operator Issue #999](https://github.com/rabbitmq/cluster-operator/issues/999) - Operator field ownership conflicts
6. [Helm Provider Issue #318](https://github.com/terraform-providers/terraform-provider-helm/issues/318) - Release already exists conflicts
7. [Kubernetes Blog: Live and let live with Kluctl and SSA](https://kubernetes.io/blog/2022/11/04/live-and-let-live-with-kluctl-and-ssa/) - Server-side apply best practices
8. [Kubernetes Blog: Advanced Server-Side Apply](https://kubernetes.io/blog/2022/10/20/advanced-server-side-apply/) - SSA conflict resolution strategies

## Conclusion

The cross-state ownership conflict problem is a fundamental issue affecting all major Kubernetes Terraform providers. Current solutions rely on Kubernetes' field management system, which cannot distinguish between different Terraform configurations. 

The recommended hybrid approach using ownership annotations combined with enhanced field management provides a comprehensive solution that:
- Prevents silent conflicts between Terraform states
- Maintains backward compatibility with existing resources  
- Provides clear error messages and resolution guidance
- Leverages both custom ownership tracking and Kubernetes native field management

This approach positions k8sinline as the first Terraform Kubernetes provider to properly address cross-state ownership conflicts, providing a significant user experience improvement over existing alternatives.