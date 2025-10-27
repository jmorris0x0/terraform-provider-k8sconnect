# ADR-014: Patch Resource for External Resource Modification

## Status
Accepted - Implemented (2025-10-11)

## Context

**The 6-year problem**: [HashiCorp Issue #723](https://github.com/hashicorp/terraform-provider-kubernetes/issues/723) requesting patch support has 675+ reactions since 2020. No Terraform provider has successfully implemented this feature due to philosophical conflicts with Terraform's ownership model (what does `destroy` mean for a patch?) and technical challenges (drift detection on partial resources, conflict resolution).

**Real use cases**: Users need to surgically modify resources they don't fully manage:
- AWS EKS DaemonSets (aws-node, kube-proxy) - add proxy settings
- GKE/AKS system pods - inject environment variables
- Operator-managed resources - add annotations/labels
- Helm deployments - surgical updates without re-templating

**Current workarounds**: `null_resource` with `local-exec` kubectl commands - not idempotent, no drift detection, no plan preview, credential management nightmare.

**Why we can build this**: Our Server-Side Apply (SSA) foundation (ADR-005) provides field-level ownership tracking that solves the partial resource ownership problem. We can track exactly which fields we patch and detect drift on only those fields.

## Decision

**Implement `k8sconnect_patch` resource with per-field ownership transfer on destroy.**

### Core Design Principles

**1. External Resources Only** - Patches are STRICTLY for resources NOT managed by `k8sconnect_object` in this state. Critical safety mechanism: `isManagedByThisState()` checks for k8sconnect annotations and field managers to prevent self-patching.

**2. Per-Field Ownership Transfer on Destroy** - When destroyed:
- Parse `previous_owners` map to group fields by their original controller
- Transfer each field group back to its specific previous owner using SSA
- Patched values remain in place
- External controllers can reclaim ownership

This is NOT "most common owner" - each field returns to its specific original controller (e.g., if patching HPA-managed `spec.replicas` and EKS-managed `spec.proxy`, each field goes back to its respective owner).

**3. Target Changes Require Replacement** - All target fields (apiVersion, kind, name, namespace) have `RequiresReplace()` plan modifiers. Changing what you're patching creates a new patch resource.

### Schema

```hcl
resource "k8sconnect_patch" "aws_node_proxy" {
  target = {
    api_version = "apps/v1"
    kind        = "DaemonSet"
    name        = "aws-node"
    namespace   = "kube-system"
  }

  # Choose one patch type: patch (strategic merge), json_patch (RFC 6902), merge_patch (RFC 7386)
  patch = jsonencode({
    spec = {
      template = {
        spec = {
          containers = [{
            name = "aws-node"
            env  = [{ name = "HTTP_PROXY", value = "http://proxy:3128" }]
          }]
        }
      }
    }
  })

  cluster = var.eks_connection

  # Computed
  managed_fields   = (computed)  # Only patched fields
  # Note: field_ownership tracked in private state (ADR-020)
  previous_owners  = (computed)  # Pre-patch ownership (for destroy)
}
```

## Critical Implementation Challenges

### Challenge 1: What Does Destroy Mean?

**Problem**: When you destroy a patch, should it revert to original values?

**Options considered:**
1. **Revert to original values** - Rejected: Original values may be stale after days/weeks, race conditions with external controllers, risk of breaking other controllers' work
2. **Delete target resource** - Rejected: Violates expectations (patch ≠ ownership), dangerous for cloud provider defaults
3. **Just remove from state** - Rejected: Leaves orphaned `k8sconnect-patch-*` field manager entries that block future patches
4. **Transfer ownership back to original controllers** - Accepted: Clean handoff, aligns with SSA semantics

**Decision**: Per-field ownership transfer. Parse `previous_owners` map, group fields by original controller, transfer each group back using SSA. Patched values remain in place.

### Challenge 2: Per-Field vs "Most Common Owner"

**Initial assumption**: When multiple fields have different previous owners, pick "most common owner" and transfer all fields to it.

**Critical correction**: Each field has exactly ONE previous owner. If patching HPA-managed `spec.replicas` and EKS-managed `spec.proxy`, using "most common owner" would incorrectly transfer both fields to one controller.

**Solution**: Group fields by their specific previous owner, apply separate SSA patches for each owner group.

### Challenge 3: Idempotent Destroy

**Problem**: Destroy has multiple sequential SSA operations (transfer to HPA, transfer to EKS, etc.). If network fails mid-destroy, some fields transferred but not others.

**Terraform limitation**: Cannot update state during Delete operation to track progress.

**Solution**: Make each transfer idempotent by refetching resource before transferring each owner group and filtering to only transfer fields currently owned by our patch manager. If destroy is retried, already-transferred fields are skipped.

### Challenge 4: Testing Custom Field Managers

**Problem**: Need to test ownership transfer, but `k8sconnect_object` hardcodes `field_manager = "k8sconnect"`.

**Options considered:**
1. Add `field_manager` attribute to manifest resource - Rejected: Big change, out of scope
2. Use external kubernetes/kubectl provider - Considered but felt awkward
3. Use k8s client directly in test setup - Accepted: Clean, no external dependencies

**Solution**: Created test helper that uses k8s client's `Patch` API with `ApplyPatchType` and custom `FieldManager` to create resources with multiple field managers for testing.

## Key Lessons

**SSA field ownership solves the partial resource problem** - Unlike full-resource Terraform providers, we can track exactly which fields we patch and detect drift on only those fields.

**Destroy is the hardest problem** - "What does destroy mean for a patch?" is why HashiCorp never built this. Our answer: Clean ownership handoff, not reversion or deletion.

**Per-field ownership matters** - Each field may come from a different controller. Grouping by previous owner ensures correct handoff.

**Terraform's Delete can't update state** - Made destroy idempotency harder. Solution: Refetch and filter before each transfer group.

**Safety is paramount** - Self-patching prevention (`isManagedByThisState()`) is critical. Patching your own manifest resources would create chaos.

## Relationship to Other ADRs

**ADR-005: Field Ownership Strategy** - Foundation for patch resource. Reuses field manager concepts, `managedFields` parsing, and ownership detection logic.

**ADR-009: User-Controlled Drift Exemption** - Complementary: Use `ignore_fields` when you own the resource but want to allow external changes. Use `patch` when you don't own the resource but need to modify it.

**ADR-010: Identity Changes** - Similar pattern: Target changes require replacement (implemented via `RequiresReplace()` plan modifiers on all target fields).

