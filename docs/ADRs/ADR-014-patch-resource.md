# ADR-014: Patch Resource for External Resource Modification

## Status
Proposed

**Target Release:** v1.1.0 (post v1.0 stabilization)

## Context

### The 6-Year Problem

Since January 2, 2020, the Terraform Kubernetes community has been requesting patch support. [HashiCorp Issue #723](https://github.com/hashicorp/terraform-provider-kubernetes/issues/723) has accumulated:
- **534 👍** upvotes
- **78 ❤️** hearts  
- **63 🚀** rockets
- **Total: 675+ reactions**

No Terraform provider has successfully implemented this feature. HashiCorp explicitly stated they won't build it due to philosophical conflicts with Terraform's ownership model.

### The Use Case

Cloud providers and operators create pre-configured Kubernetes resources that users need to modify but not fully manage:

**Real-world examples:**
- AWS EKS DaemonSets (aws-node, kube-proxy) - Add proxy settings
- GKE system pods - Inject environment variables
- AKS CNI plugins - Modify resource limits
- Operator-managed resources - Add annotations or labels
- Helm deployments - Surgical updates without re-templating

**Current workarounds (all bad):**
```hcl
# Workaround 1: null_resource with local-exec
resource "null_resource" "patch_aws_node" {
  provisioner "local-exec" {
    command = "kubectl patch daemonset aws-node -n kube-system --type=json -p='...'"
  }
}

# Problems:
# - Not idempotent
# - No drift detection
# - No plan preview
# - Credentials management nightmare
# - Race conditions with cluster creation
```

### Why HashiCorp Never Built This

From GitHub discussion and provider design patterns:

**1. Philosophical Conflicts**
- Terraform's model: Full resource ownership
- Patches: Partial field ownership
- Lifecycle ambiguity: What does `destroy` mean?

**2. Technical Challenges**
- Drift detection on partial resources
- Conflict resolution between controllers
- State representation complexity

**3. Business Priorities**
- Focus on `kubernetes_manifest` instead
- "Use kubectl" as official recommendation
- Enterprise users want full control

## Decision

**Implement `k8sconnect_patch` resource for surgical modifications to external resources.**

### Core Principles

**1. External Resources Only**

The patch resource is STRICTLY for resources NOT managed by this Terraform state:
- ✅ Cloud provider defaults (EKS, GKE, AKS)
- ✅ Operator-managed resources
- ✅ Helm deployments
- ✅ Resources from other tools
- ❌ Resources managed by `k8sconnect_manifest` in same state

**2. Explicit Ownership Acknowledgment**

Every patch MUST have `take_ownership = true`:
```hcl
resource "k8sconnect_patch" "example" {
  target = { ... }
  patch  = { ... }
  
  # Required - user must acknowledge forceful takeover
  take_ownership = true
}
```

**3. Destroy = Release Ownership**

When destroyed, the patch:
- ✅ Releases field ownership
- ✅ Leaves patched values in place
- ✅ Allows external controllers to reclaim fields
- ❌ Does NOT revert to original values (too risky)

This is the safest behavior and aligns with Kubernetes SSA semantics.

## Schema Design

```hcl
resource "k8sconnect_patch" "aws_node_proxy" {
  # Target identification (required)
  target = {
    api_version = "apps/v1"
    kind        = "DaemonSet"
    name        = "aws-node"
    namespace   = "kube-system"
  }

  # Patch content (choose one strategy)
  
  # Option 1: Strategic Merge Patch (recommended)
  patch = yamlencode({
    spec = {
      template = {
        spec = {
          containers = [{
            name = "aws-node"  # Merge key
            env = [
              { name = "HTTP_PROXY", value = "http://proxy:3128" },
              { name = "NO_PROXY", value = ".svc,.cluster.local" }
            ]
          }]
        }
      }
    }
  })
  
  # Option 2: JSON Patch (RFC 6902) - for precise operations
  json_patch = jsonencode([
    {
      op    = "add"
      path  = "/metadata/annotations/prometheus.io~1scrape"
      value = "true"
    }
  ])
  
  # Option 3: Merge Patch (simple key-value merges)
  merge_patch = yamlencode({
    metadata = {
      labels = {
        monitoring = "enabled"
      }
    }
  })
  
  # Required acknowledgment
  take_ownership = true
  
  # Standard connection
  cluster_connection = {
    host                   = var.cluster_endpoint
    cluster_ca_certificate = var.cluster_ca
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", var.cluster_name]
    }
  }
  
  # Optional: Wait for patch to take effect
  wait_for = {
    field   = "status.numberReady"
    timeout = "5m"
  }
  
  # Computed attributes
  managed_fields   = (computed)  # Only patched fields
  field_ownership  = (computed)  # Who owns what
  previous_owners  = (computed)  # Ownership before patch
}
```

## Implementation Architecture

### Resource Model

```go
type patchResourceModel struct {
    ID              types.String              `tfsdk:"id"`
    Target          patchTargetModel          `tfsdk:"target"`
    Patch           types.String              `tfsdk:"patch"`
    JSONPatch       types.String              `tfsdk:"json_patch"`
    MergePatch      types.String              `tfsdk:"merge_patch"`
    TakeOwnership   types.Bool                `tfsdk:"take_ownership"`
    WaitFor         *waitForModel             `tfsdk:"wait_for"`
    
    // Computed
    ManagedFields   types.String              `tfsdk:"managed_fields"`
    FieldOwnership  types.Map                 `tfsdk:"field_ownership"`
    PreviousOwners  types.Map                 `tfsdk:"previous_owners"`
    
    ClusterConnection clusterConnectionModel  `tfsdk:"cluster_connection"`
}

type patchTargetModel struct {
    APIVersion types.String `tfsdk:"api_version"`
    Kind       types.String `tfsdk:"kind"`
    Name       types.String `tfsdk:"name"`
    Namespace  types.String `tfsdk:"namespace"`
}
```

### CRUD Operations

#### Create

```go
func (r *patchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
    var data patchResourceModel
    req.Plan.Get(ctx, &data)
    
    // 1. Setup connection
    client, err := r.setupClient(ctx, data.ClusterConnection)
    if err != nil {
        resp.Diagnostics.AddError("Connection failed", err.Error())
        return
    }
    
    // 2. GET target resource (must exist)
    targetObj, err := client.Get(ctx, 
        data.Target.Kind.ValueString(),
        data.Target.Name.ValueString(),
        data.Target.Namespace.ValueString(),
    )
    if errors.IsNotFound(err) {
        resp.Diagnostics.AddError(
            "Target Resource Not Found",
            fmt.Sprintf("k8sconnect_patch can only modify existing resources. "+
                "The target resource %s/%s does not exist in namespace %s.\n\n"+
                "To create new resources, use k8sconnect_manifest instead.",
                data.Target.Kind.ValueString(),
                data.Target.Name.ValueString(),
                data.Target.Namespace.ValueString()),
        )
        return
    }
    
    // 3. CRITICAL VALIDATION: Prevent self-patching
    if r.isManagedByThisState(ctx, targetObj) {
        resp.Diagnostics.AddError(
            "Cannot Patch Own Resource",
            fmt.Sprintf("This resource is already managed by k8sconnect_manifest "+
                "in this Terraform state.\n\n"+
                "You cannot patch resources you already own. Instead:\n"+
                "1. Modify the k8sconnect_manifest directly, or\n"+
                "2. Use ignore_fields to allow external controllers to manage specific fields\n\n"+
                "Resource: %s/%s in namespace %s",
                data.Target.Kind.ValueString(),
                data.Target.Name.ValueString(),
                data.Target.Namespace.ValueString()),
        )
        return
    }
    
    // 4. Capture ownership BEFORE patching
    data.PreviousOwners = extractFieldOwnership(targetObj, getPatchedFieldPaths(data))
    
    // 5. Apply patch using Server-Side Apply with unique field manager
    fieldManager := fmt.Sprintf("k8sconnect-patch-%s", data.ID.ValueString())
    patchedObj, err := client.Patch(ctx, targetObj, 
        r.buildPatchPayload(data),
        k8sclient.PatchOptions{
            FieldManager: fieldManager,
            Force:        true,  // Required by take_ownership
            PatchType:    r.determinePatchType(data),
        },
    )
    if err != nil {
        resp.Diagnostics.AddError("Patch Failed", err.Error())
        return
    }
    
    // 6. Store ONLY patched fields (not full resource)
    data.ManagedFields = extractManagedFields(patchedObj, fieldManager)
    data.FieldOwnership = extractFieldOwnership(patchedObj, nil)
    
    // 7. Handle wait conditions if specified
    if data.WaitFor != nil {
        if err := r.waitForCondition(ctx, client, patchedObj, data.WaitFor); err != nil {
            // Patch succeeded but wait failed - save state anyway
            resp.Diagnostics.AddWarning("Wait Failed", err.Error())
        }
    }
    
    resp.State.Set(ctx, &data)
}
```

#### Read (Drift Detection)

```go
func (r *patchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
    var data patchResourceModel
    req.State.Get(ctx, &data)
    
    // 1. Setup connection
    client, _ := r.setupClient(ctx, data.ClusterConnection)
    
    // 2. GET current resource state
    currentObj, err := client.Get(ctx,
        data.Target.Kind.ValueString(),
        data.Target.Name.ValueString(),
        data.Target.Namespace.ValueString(),
    )
    if errors.IsNotFound(err) {
        // Target deleted externally - remove patch from state
        tflog.Info(ctx, "Target resource deleted, removing patch from state")
        resp.State.RemoveResource(ctx)
        return
    }
    
    // 3. Extract ONLY fields we patched (using managedFields)
    ourFieldManager := fmt.Sprintf("k8sconnect-patch-%s", data.ID.ValueString())
    currentManagedFields := extractManagedFields(currentObj, ourFieldManager)
    
    // 4. Detect drift in our patched fields
    if !reflect.DeepEqual(currentManagedFields, data.ManagedFields.ValueString()) {
        // External change to our patched fields
        tflog.Debug(ctx, "Drift detected in patched fields")
        data.ManagedFields = types.StringValue(currentManagedFields)
    }
    
    // 5. Update field ownership tracking
    data.FieldOwnership = extractFieldOwnership(currentObj, nil)
    
    resp.State.Set(ctx, &data)
}
```

#### Update

```go
func (r *patchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
    var state, plan patchResourceModel
    req.State.Get(ctx, &state)
    req.Plan.Get(ctx, &plan)
    
    // 1. If target changed, this requires replacement
    if !targetsEqual(state.Target, plan.Target) {
        resp.Diagnostics.AddError(
            "Target Changed",
            "Changing the patch target requires resource replacement. "+
            "Destroy the old patch and create a new one.")
        return
    }
    
    // 2. Setup connection
    client, _ := r.setupClient(ctx, plan.ClusterConnection)
    
    // 3. Re-apply updated patch
    currentObj, _ := client.Get(ctx,
        plan.Target.Kind.ValueString(),
        plan.Target.Name.ValueString(),
        plan.Target.Namespace.ValueString(),
    )
    
    fieldManager := fmt.Sprintf("k8sconnect-patch-%s", plan.ID.ValueString())
    patchedObj, err := client.Patch(ctx, currentObj,
        r.buildPatchPayload(plan),
        k8sclient.PatchOptions{
            FieldManager: fieldManager,
            Force:        true,
            PatchType:    r.determinePatchType(plan),
        },
    )
    if err != nil {
        resp.Diagnostics.AddError("Patch Update Failed", err.Error())
        return
    }
    
    // 4. Update state
    plan.ManagedFields = extractManagedFields(patchedObj, fieldManager)
    plan.FieldOwnership = extractFieldOwnership(patchedObj, nil)
    
    resp.State.Set(ctx, &plan)
}
```

#### Delete (Release Ownership)

```go
func (r *patchResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
    var data patchResourceModel
    req.State.Get(ctx, &data)
    
    // Setup connection
    client, err := r.setupClient(ctx, data.ClusterConnection)
    if err != nil {
        // Can't connect to release ownership - log and continue
        tflog.Warn(ctx, "Failed to connect for cleanup",
            map[string]interface{}{"error": err.Error()})
        return
    }
    
    // Check if target still exists
    targetObj, err := client.Get(ctx,
        data.Target.Kind.ValueString(),
        data.Target.Name.ValueString(),
        data.Target.Namespace.ValueString(),
    )
    if errors.IsNotFound(err) {
        // Target already deleted - nothing to do
        tflog.Info(ctx, "Target resource already deleted")
        return
    }
    
    // Create Kubernetes Event for observability
    r.createOwnershipReleaseEvent(ctx, client, targetObj, data)
    
    // Log what's happening
    tflog.Info(ctx, "Releasing patch ownership",
        map[string]interface{}{
            "target":        formatTarget(data.Target),
            "fields":        data.ManagedFields.ValueString(),
            "field_manager": fmt.Sprintf("k8sconnect-patch-%s", data.ID.ValueString()),
            "note":          "Patched values remain on resource, ownership released",
        })
    
    // That's it - state removed automatically by framework
    // The patched fields stay on the resource
    // External controllers can reclaim ownership if they want
    
    // NO REVERT - too risky with external controllers
    // NO DELETION - we don't own the resource
}
```

### Critical Safety Mechanisms

#### Prevent Self-Patching

```go
func (r *patchResource) isManagedByThisState(ctx context.Context, obj *unstructured.Unstructured) bool {
    // Check 1: Does it have our ownership annotation?
    annotations := obj.GetAnnotations()
    if ownedBy, exists := annotations["k8sconnect.io/owned-by"]; exists {
        // This is managed by k8sconnect_manifest
        tflog.Debug(ctx, "Resource has k8sconnect ownership annotation",
            map[string]interface{}{"owner": ownedBy})
        return true
    }
    
    // Check 2: Is field manager name a manifest manager (not patch manager)?
    for _, mf := range obj.GetManagedFields() {
        if strings.HasPrefix(mf.Manager, "k8sconnect-") && 
           !strings.Contains(mf.Manager, "patch") {
            tflog.Debug(ctx, "Resource managed by k8sconnect_manifest",
                map[string]interface{}{"manager": mf.Manager})
            return true
        }
    }
    
    return false
}
```

#### Validate take_ownership

```go
func (r *patchResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
    var data patchResourceModel
    req.Config.Get(ctx, &data)
    
    // Require explicit acknowledgment
    if !data.TakeOwnership.ValueBool() {
        resp.Diagnostics.AddError(
            "take_ownership Required",
            "Patches ALWAYS forcefully take field ownership from other controllers.\n\n"+
            "You must explicitly set take_ownership = true to acknowledge that:\n"+
            "1. This patch will force ownership transfer\n"+
            "2. External controllers may fight back for control\n"+
            "3. You understand the implications\n\n"+
            "This is not optional - it's a required safety acknowledgment.")
    }
    
    // Ensure only one patch type specified
    patchTypes := 0
    if !data.Patch.IsNull() { patchTypes++ }
    if !data.JSONPatch.IsNull() { patchTypes++ }
    if !data.MergePatch.IsNull() { patchTypes++ }
    
    if patchTypes != 1 {
        resp.Diagnostics.AddError(
            "Invalid Patch Configuration",
            "Specify exactly ONE of: patch, json_patch, or merge_patch")
    }
}
```

## State Storage

Unlike `k8sconnect_manifest` which stores the full resource, patches store minimal state:

```json
{
  "id": "abc-123-def",
  "target": {
    "api_version": "apps/v1",
    "kind": "DaemonSet",
    "name": "aws-node",
    "namespace": "kube-system"
  },
  "patch": "spec:\n  template:\n    spec:\n      containers:\n      - name: aws-node\n        env:\n        - name: HTTP_PROXY\n          value: http://proxy:3128",
  "take_ownership": true,
  "managed_fields": "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"aws-node\",\"env\":[{\"name\":\"HTTP_PROXY\",\"value\":\"http://proxy:3128\"}]}]}}}}",
  "field_ownership": {
    "spec.template.spec.containers[name=aws-node].env[name=HTTP_PROXY]": "k8sconnect-patch-abc-123-def"
  },
  "previous_owners": {
    "spec.template.spec.containers[name=aws-node].env": "eks.amazonaws.com"
  }
}
```

## User Experience

### Plan Output

```terraform
Terraform will perform the following actions:

  # k8sconnect_patch.aws_node_proxy will be created
  + resource "k8sconnect_patch" "aws_node_proxy" {
      + id               = (known after apply)
      + take_ownership   = true
      + managed_fields   = (known after apply)
      + field_ownership  = (known after apply)
      + previous_owners  = (known after apply)
      
      + target {
          + api_version = "apps/v1"
          + kind        = "DaemonSet"
          + name        = "aws-node"
          + namespace   = "kube-system"
        }
        
      + patch = <<-EOT
            spec:
              template:
                spec:
                  containers:
                  - name: aws-node
                    env:
                    - name: HTTP_PROXY
                      value: http://proxy:3128
                    - name: NO_PROXY
                      value: .svc,.cluster.local
        EOT
    }

Warning: Field Ownership Override

This patch will forcefully take ownership of fields currently managed by: eks.amazonaws.com

The external controller (eks.amazonaws.com) may attempt to reclaim these fields.
This is expected behavior when patching cloud provider defaults.

Patched fields: spec.template.spec.containers[name=aws-node].env
```

### Drift Detection

```terraform
# k8sconnect_patch.aws_node_proxy will be updated in-place
~ resource "k8sconnect_patch" "aws_node_proxy" {
    ~ managed_fields = jsonencode({
        ~ spec = {
            ~ template = {
                ~ spec = {
                    ~ containers = [
                        ~ {
                            ~ env = [
                              - { name = "HTTP_PROXY", value = "http://proxy:3128" },
                              + { name = "HTTP_PROXY", value = "http://new-proxy:3128" },
                            ]
                          }
                      ]
                  }
              }
          }
      })
  }

Plan: 0 to add, 1 to change, 0 to destroy.
```

## Documentation Requirements

### Critical Warnings

Every piece of documentation must include:

```markdown
⚠️ **CRITICAL: This resource forcefully takes ownership of fields from other controllers**

**ONLY use k8sconnect_patch for:**
- ✅ Cloud provider defaults (AWS EKS, GCP GKE, Azure AKS system resources)
- ✅ Operator-managed resources (cert-manager, nginx-ingress, etc.)
- ✅ Helm chart deployments
- ✅ Resources created by other tools

**NEVER use k8sconnect_patch for:**
- ❌ Resources managed by k8sconnect_manifest in the same state
- ❌ Resources you want full lifecycle control over
- ❌ Resources where you could use k8sconnect_manifest instead

**Destroy behavior:**
When you `terraform destroy` a patch:
- ✅ Ownership is released
- ✅ Patched values REMAIN on the resource
- ❌ Values are NOT reverted to original state

This is intentional - patches are designed to "hand off" configuration
to external controllers, not to revert changes.
```

### Migration Guide

```markdown
## Migrating from null_resource kubectl patches

**Before:**
```hcl
resource "null_resource" "patch_aws_node" {
  provisioner "local-exec" {
    command = <<-EOT
      kubectl patch daemonset aws-node -n kube-system \
        --type=json \
        -p='[{"op":"add","path":"/spec/template/spec/containers/0/env/-","value":{"name":"HTTP_PROXY","value":"http://proxy:3128"}}]'
    EOT
  }
}
```

**After:**
```hcl
resource "k8sconnect_patch" "aws_node_proxy" {
  target = {
    api_version = "apps/v1"
    kind        = "DaemonSet"
    name        = "aws-node"
    namespace   = "kube-system"
  }
  
  patch = yamlencode({
    spec = {
      template = {
        spec = {
          containers = [{
            name = "aws-node"
            env = [
              { name = "HTTP_PROXY", value = "http://proxy:3128" }
            ]
          }]
        }
      }
    }
  })
  
  take_ownership = true
  cluster_connection = var.eks_connection
}
```

**Benefits:**
- ✅ Drift detection
- ✅ Plan preview
- ✅ Idempotent
- ✅ No credential management complexity
- ✅ Works with dynamic cluster creation
```

## Implementation Roadmap

### Phase 1: MVP (Weeks 1-2)
- [ ] Basic resource schema
- [ ] Strategic merge patch support
- [ ] Self-patching validation
- [ ] Create/Read/Update/Delete operations
- [ ] Field ownership tracking
- [ ] Unit tests for safety mechanisms

**Deliverable:** Working patch resource with strategic merge

### Phase 2: Drift Detection (Weeks 2-3)
- [ ] Accurate drift detection for patched fields only
- [ ] Re-apply patch on external changes
- [ ] Ownership conflict detection
- [ ] Previous owners tracking
- [ ] Enhanced plan output
- [ ] Acceptance tests for drift scenarios

**Deliverable:** Production-quality drift detection

### Phase 3: Patch Types (Weeks 3-4)
- [ ] JSON Patch (RFC 6902) support
- [ ] Merge Patch support
- [ ] Patch type validation during plan
- [ ] Array manipulation testing
- [ ] Edge case handling
- [ ] Documentation for each patch type

**Deliverable:** All three patch strategies working

### Phase 4: Production Hardening (Weeks 4-5)
- [ ] Comprehensive error handling
- [ ] Performance optimization
- [ ] Large patch testing
- [ ] Multi-patch coordination testing
- [ ] Complete documentation
- [ ] Migration guides
- [ ] Example gallery

**Deliverable:** Production-ready v1.1.0 release

## Testing Strategy

### Unit Tests
- Self-patching detection (positive and negative cases)
- Field ownership extraction
- Patch payload building
- Target equality checking
- Previous owners capture

### Acceptance Tests
1. **Basic Operations**
   - Create patch on existing resource
   - Update patch content
   - Destroy patch (verify values remain)

2. **Drift Detection**
   - External controller overwrites patch
   - Terraform detects and corrects
   - Multiple patches on same resource

3. **Safety Mechanisms**
   - Attempt to patch k8sconnect_manifest resource (should fail)
   - Attempt without take_ownership (should fail)
   - Multiple patch types specified (should fail)

4. **Real-World Scenarios**
   - Patch AWS EKS aws-node DaemonSet
   - Patch GKE kube-proxy ConfigMap
   - Patch operator-managed CRD instance
   - Patch Helm-deployed service

### Integration Tests
- Patch applied before external controller starts
- External controller fights back for ownership
- Terraform re-applies patch (with force)
- Clean ownership handoff on destroy

## Success Metrics

**Technical:**
1. Zero self-patching incidents in testing
2. 100% drift detection accuracy for patched fields
3. Clean ownership transitions (no orphaned managers)
4. Passes all acceptance tests

**User Experience:**
1. Clear error messages for misuse
2. Intuitive plan output
3. Predictable destroy behavior
4. Comprehensive documentation

**Market:**
1. Captures demand from 534+ waiting users
2. First-to-market advantage maintained
3. Clear differentiation from other providers
4. Positive community feedback

## Risks and Mitigation

| Risk | Impact | Probability | Mitigation |
|------|--------|-------------|------------|
| Users patch own resources | HIGH | HIGH | Hard validation prevents self-patching |
| Confusion about when to use | MEDIUM | HIGH | Clear docs, examples, validation errors |
| External controllers fighting back | MEDIUM | MEDIUM | Document as expected, provide force option |
| Complex drift scenarios | MEDIUM | LOW | Extensive testing, clear field tracking |
| Destroy behavior surprises | LOW | MEDIUM | Prominent warnings, clear documentation |

## Alternatives Considered

### Alternative 1: Store Original Values and Revert on Destroy

**Rejected because:**
- Race conditions with external controllers
- Original values may be stale after days/weeks
- External controllers may have added their own changes
- Risk of breaking other controllers' work

### Alternative 2: Delete Target Resource on Patch Destroy

**Rejected because:**
- Violates user expectations (patch ≠ ownership)
- Dangerous for cloud provider defaults
- Would delete resources users don't "own"

### Alternative 3: Make Destroy Behavior Configurable

**Deferred because:**
- Adds complexity for marginal benefit
- Users don't need revert in practice
- Can add later if proven necessary
- Default behavior is safest

## Relationship to Other ADRs

### ADR-005: Field Ownership Strategy
- **Foundation:** Patch resource builds on field ownership tracking
- **Reuse:** Same field manager concepts and ownership detection

### ADR-009: User-Controlled Drift Exemption
- **Distinction:** `ignore_fields` = allow drift, `patch` = force ownership
- **Complementary:** Use ignore_fields when you own the resource, patch when you don't

### ADR-001: Managed State Projection
- **Difference:** Manifests track full resource, patches track only patched fields
- **Similar Pattern:** Both use dry-run and projection for drift detection

## References

**External:**
1. [HashiCorp Issue #723](https://github.com/hashicorp/terraform-provider-kubernetes/issues/723) - 534+ users requesting patch support
2. [Gavinbunney Issue #64](https://github.com/gavinbunney/terraform-provider-kubectl/issues/64) - kubectl provider patch request
3. [Kubernetes Server-Side Apply](https://kubernetes.io/docs/reference/using-api/server-side-apply/) - Field management specification
4. [RFC 6902: JSON Patch](https://datatracker.ietf.org/doc/html/rfc6902) - JSON Patch specification

**Internal:**
- ADR-005: Field Ownership Strategy
- ADR-009: User-Controlled Drift Exemption  
- ADR-001: Managed State Projection
- `k8sconnect_patch Resource Design Summary.md` - Original design discussion

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2025-10-11 | Create patch resource | 534+ user demand, clear market gap |
| 2025-10-11 | Destroy = release ownership (not revert) | Safest behavior, aligns with K8s SSA |
| 2025-10-11 | External resources only | Prevents confusion with manifest resource |
| 2025-10-11 | Require take_ownership = true | Explicit acknowledgment of forceful takeover |
| 2025-10-11 | Target v1.1.0 (post v1.0) | Stabilize manifest resource first |

## Conclusion

The `k8sconnect_patch` resource fills a 6-year gap in the Terraform Kubernetes ecosystem. By building on our existing Server-Side Apply foundation and field ownership tracking, we can deliver this feature safely and correctly where others have failed.

**Key differentiators:**
1. ✅ First provider to implement patch support
2. ✅ Built on solid SSA foundation from day one
3. ✅ Clear boundaries (external resources only)
4. ✅ Safe destroy semantics (release, not revert)
5. ✅ Answers 534+ users who have been waiting since 2020

This feature positions k8sconnect as the most flexible and powerful Kubernetes provider for Terraform, solving real problems that force users to ugly workarounds today.

