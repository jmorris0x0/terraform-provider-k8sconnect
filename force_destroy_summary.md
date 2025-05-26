# Force Destroy Implementation for k8sinline Provider

## Problem Statement

Kubernetes resources with finalizers can become stuck in "Terminating" state indefinitely, causing **state drift** where Terraform thinks a resource is deleted but it still exists in the cluster.

Common scenarios:
- `PersistentVolumeClaim` with `kubernetes.io/pvc-protection` finalizer
- `Namespace` with content that prevents deletion
- Custom resources with operator-managed finalizers
- Jobs with `foregroundDeletion` finalizer

## Solution: Three-Attribute Approach

```hcl
resource "k8sinline_manifest" "example" {
  yaml_body = file("resource.yaml")
  
  delete_protection = false    # Safety: prevent accidental deletion
  delete_timeout = "10m"       # How long to wait for normal deletion
  force_destroy = false        # Remove finalizers if stuck
  
  cluster_connection {
    kubeconfig_raw = var.kubeconfig
  }
}
```

## Implementation Flow

### 1. Normal Deletion Process
```
1. Check delete_protection (error if enabled)
2. Initiate kubectl delete equivalent
3. Wait for deletion_timeout duration
4. If successful: ✅ Done
5. If timeout: Check force_destroy setting
```

### 2. Force Destroy Process (if enabled)
```
1. Get current object state
2. Check for finalizers
3. Log warning about bypass safety
4. Remove all finalizers via server-side apply
5. Wait additional 60s for deletion
6. ✅ Resource guaranteed deleted (no state drift)
```

### 3. Error Handling (if force_destroy disabled)
```
Provides actionable guidance:
- kubectl describe commands to investigate
- Suggestions for manual finalizer removal
- Option to enable force_destroy
- Timeout adjustment recommendations
```

## Configuration Examples

### Production Database (Maximum Safety)
```hcl
resource "k8sinline_manifest" "prod_db" {
  yaml_body = file("database-pvc.yaml")
  
  delete_protection = true     # Must explicitly disable
  delete_timeout = "20m"       # Storage takes time
  force_destroy = false        # Never bypass finalizers
  
  cluster_connection {
    kubeconfig_raw = var.kubeconfig
  }
}
```

### Development Resources (Convenience)
```hcl
resource "k8sinline_manifest" "dev_ns" {
  yaml_body = templatefile("namespace.yaml", { name = var.dev_name })
  
  delete_protection = false    # No protection needed
  delete_timeout = "2m"        # Don't wait long
  force_destroy = true         # Just delete if stuck
  
  cluster_connection {
    kubeconfig_raw = var.kubeconfig
  }
}
```

### Environment-Conditional Safety
```hcl
locals {
  is_production = var.environment == "production"
}

resource "k8sinline_manifest" "app_storage" {
  yaml_body = file("pvc.yaml")
  
  delete_protection = local.is_production
  delete_timeout = local.is_production ? "15m" : "5m"
  force_destroy = !local.is_production
  
  cluster_connection {
    kubeconfig_raw = var.kubeconfig
  }
}
```

## Error Messages & User Guidance

### Scenario 1: PVC with Protection Finalizer
```
Error: Deletion Blocked by Finalizers
Resource PersistentVolumeClaim test-pvc has been marked for deletion 
but is blocked by finalizers: [kubernetes.io/pvc-protection]

Options:
1. Wait longer - increase delete_timeout:
   delete_timeout = "20m"

2. Force deletion - bypass finalizers (⚠️ may cause data loss):
   force_destroy = true

3. Manual intervention:
   kubectl describe pvc test-pvc
   kubectl get events --field-selector involvedObject.name=test-pvc

4. Remove finalizers manually (⚠️ dangerous):
   kubectl patch pvc test-pvc --type='merge' -p '{"metadata":{"finalizers":null}}'
```

### Scenario 2: Force Destroy Warning
```
Warning: Force Destroying Resource with Finalizers
Removing finalizers from PersistentVolumeClaim test-pvc: [kubernetes.io/pvc-protection]

⚠️ WARNING: This bypasses Kubernetes safety mechanisms and may cause:
• Data loss or corruption
• Orphaned dependent resources  
• Incomplete cleanup operations

Only use force_destroy when you understand the implications.
```

## Benefits

### ✅ Eliminates State Drift
- Terraform state always matches cluster reality
- No "ghost" resources stuck in Terminating state
- Predictable `terraform apply` behavior

### ✅ User Control & Safety
- `delete_protection` prevents accidents
- `force_destroy` explicit opt-in to bypass safety
- Clear warnings about risks

### ✅ Follows Terraform Patterns
- Similar to AWS provider's `force_destroy` on S3 buckets
- Resource-level configuration (not provider-level)
- Environment-specific policies possible

### ✅ Excellent User Experience
- Smart defaults based on resource type
- Actionable error messages with kubectl commands
- Progressive escalation: normal → timeout → force

## Default Timeouts by Resource Type

| Resource Type | Default Timeout | Rationale |
|---------------|----------------|-----------|
| `Pod`, `ConfigMap`, `Secret` | 5m | Should delete quickly |
| `Namespace` | 10m | May contain resources |
| `PersistentVolume`, `PersistentVolumeClaim` | 10m | Often have protection finalizers |
| `CustomResourceDefinition` | 15m | Operators need cleanup time |
| `StatefulSet` | 8m | Ordered pod deletion |
| `Job`, `CronJob` | 8m | May have foregroundDeletion |

## Implementation Safety Features

1. **Two-phase deletion**: Try normal first, force only on timeout
2. **Explicit opt-in**: `force_destroy` must be set to `true`
3. **Detailed logging**: All finalizer removals logged
4. **Clear warnings**: User told exactly what finalizers were removed
5. **Conservative defaults**: Never force by default

## Testing Strategy

- Unit tests for timeout logic and force destroy flow
- Integration tests with real finalizers (PVC protection)
- Error message validation
- Performance tests (no excessive API calls)
- Acceptance tests across different resource types

This implementation completely eliminates state drift while providing users the control and safety they need for different environments and use cases.