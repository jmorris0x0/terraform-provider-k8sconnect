---
page_title: "Migrating from hashicorp/kubernetes - k8sconnect Provider"
subcategory: "Guides"
description: |-
  Step-by-step guide to migrating from the hashicorp/kubernetes provider to k8sconnect, including resource mapping, state management, and feature comparisons.
---

# Migrating from hashicorp/kubernetes

## Why Migrate?

### Problems k8sconnect Solves

| Problem | hashicorp/kubernetes | k8sconnect |
|---------|---------------------|------------|
| **Bootstrap clusters + workloads** | ❌ Two-phase, providers at root only | ✅ Single apply, inline connections |
| **CRD + CR in one apply** | ❌ Requires two applies (Issue #1367) | ✅ Auto-retry, works in one apply |
| **HPA/operator coexistence** | ⚠️ SSA optional, fights with controllers | ✅ Always SSA + `ignore_fields` |
| **Accurate plan diffs** | ❌ Shows your YAML, not K8s result | ✅ Dry-run shows exact changes |
| **Typo detection** | ❌ Only at apply time | ✅ Field validation at plan time |
| **Patch managed resources** | ❌ Must import or take full ownership | ✅ `k8sconnect_patch` for surgical changes |
| **Wait timeout behavior** | ❌ Taints resource, forces recreate | ✅ Retriable, no taint |

### Migration Decision Tree

```
Do you need to bootstrap clusters + workloads in one apply?
├─ YES → Migrate to k8sconnect
│
└─ NO → Do you deploy CRDs + CRs together?
         ├─ YES → Migrate to k8sconnect
         │
         └─ NO → Are you fighting with HPA/operators?
                  ├─ YES → Migrate (ignore_fields FTW)
                  │
                  └─ NO → Consider migrating for better diffs/validation
```

## Resource Mapping

### kubernetes_manifest → k8sconnect_object

```terraform
# Before (HCL)
resource "kubernetes_manifest" "nginx" {
  manifest = {
    apiVersion = "apps/v1"
    kind       = "Deployment"
    # ...
  }
}

# After (YAML)
resource "k8sconnect_object" "nginx" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    # ...
  YAML

  cluster = local.cluster
}
```

**Changes**: HCL map → YAML, provider auth → inline `cluster`

### kubernetes_manifest (wait_for) → k8sconnect_wait

```terraform
# Before (inline wait)
resource "kubernetes_manifest" "service" {
  manifest = { /* ... */ }
  wait {
    fields = { "status.loadBalancer.ingress" = "^.+$" }
  }
}

# After (separate wait resource)
resource "k8sconnect_object" "service" {
  yaml_body = file("service.yaml")
  cluster = local.cluster
}

resource "k8sconnect_wait" "service" {
  object_ref = k8sconnect_object.service.object_ref
  wait_for   = { field = "status.loadBalancer.ingress", timeout = "10m" }
  cluster = local.cluster
}

# Access: k8sconnect_wait.service.result.status.loadBalancer.ingress[0].ip
```

**Benefit**: Timeout doesn't taint the object resource

### Typed Resources → k8sconnect_object

```terraform
# Before (typed resources with HCL schema)
resource "kubernetes_deployment" "nginx" {
  metadata { name = "nginx" }
  spec {
    replicas = 3
    # ...
  }
}

# After (universal YAML)
resource "k8sconnect_object" "nginx" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: nginx
    spec:
      replicas: 3
      # ...
  YAML
  cluster = local.cluster
}
```

**Why YAML**: No schema drift, copy-paste from kubectl, works with any K8s resource

## State Migration

### Import Strategy

k8sconnect supports three import workflows. Choose based on your Terraform version and preferences:

#### Option 1: CLI Import (All Terraform versions)

Traditional workflow - write config first, then import:

```bash
# 1. Get YAML from cluster
kubectl get deployment nginx -n default -o yaml > nginx.yaml

# 2. Create resource block
resource "k8sconnect_object" "nginx" {
  yaml_body          = file("nginx.yaml")
  cluster = local.cluster
}

# 3. Import existing resource
export KUBECONFIG=~/.kube/config
terraform import 'k8sconnect_object.nginx' 'prod:default:apps/v1/Deployment:nginx'

# 4. Remove old state
terraform state rm 'kubernetes_manifest.nginx'

# 5. Verify
terraform plan  # Should show no changes
```

#### Option 2: Import Blocks with Config Generation (Terraform 1.5+, Recommended)

Let Terraform generate the resource configuration for you:

```hcl
# Just write the import block
import {
  to = k8sconnect_object.nginx
  id = "prod:default:apps/v1/Deployment:nginx"
}
```

```bash
# Generate config automatically
export KUBECONFIG=~/.kube/config
terraform plan -generate-config-out=generated.tf

# Review generated.tf and move to your main config
# Note: Replace inlined kubeconfig with file() reference for cleaner code

# Remove old state
terraform state rm 'kubernetes_manifest.nginx'

# Verify
terraform plan  # Should show no changes
```

#### Option 3: Import Blocks (Terraform 1.5+)

Declarative import - write both import block and resource:

```hcl
import {
  to = k8sconnect_object.nginx
  id = "prod:default:apps/v1/Deployment:nginx"
}

resource "k8sconnect_object" "nginx" {
  yaml_body          = file("nginx.yaml")
  cluster = local.cluster
}
```

```bash
# Import and apply in one command
export KUBECONFIG=~/.kube/config
terraform apply

# Remove old state
terraform state rm 'kubernetes_manifest.nginx'
```

### Import ID Format

- **Namespaced**: `context:namespace:apiVersion/kind:name`
- **Cluster-scoped**: `context:apiVersion/kind:name`

**Examples:**
```
prod:default:v1/Pod:nginx
prod:default:apps/v1/Deployment:app
prod:networking.k8s.io/v1/Ingress:web
prod:v1/Namespace:my-namespace
```

### Automatic Ownership Takeover

When importing resources created by kubectl or other tools, k8sconnect automatically takes ownership using Server-Side Apply with `force=true`. You'll see a warning during import:

```
Warning: Managed Fields Override

Forcing ownership of fields managed by other controllers:
  - spec.replicas (managed by "kubectl-client-side-apply")
  - data.key1 (managed by "kubectl-client-side-apply")

These fields will be forcibly taken over.
```

This is expected and allows k8sconnect to manage the resource. Use `ignore_fields` to release specific fields back to other controllers (e.g., HPA managing `spec.replicas`).

## Migration Patterns

### Basic Resources

```terraform
# Before (provider-level auth)
provider "kubernetes" {
  config_path = "~/.kube/config"
}

resource "kubernetes_deployment" "app" {
  # ...
}

# After (inline auth)
provider "k8sconnect" {}

locals {
  cluster = { kubeconfig = file("~/.kube/config") }
}

resource "k8sconnect_object" "app" {
  yaml_body          = file("app.yaml")
  cluster = local.cluster
}
```

### Bootstrap (Single Apply)

```terraform
# Before: IMPOSSIBLE - can't reference cluster outputs in provider config
provider "kubernetes" {
  host = aws_eks_cluster.main.endpoint  # ERROR
}

# After: WORKS - inline connections
locals {
  cluster = {
    host                   = aws_eks_cluster.main.endpoint
    cluster_ca_certificate = aws_eks_cluster.main.certificate_authority[0].data
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", aws_eks_cluster.main.name]
    }
  }
}

resource "k8sconnect_object" "app" {
  yaml_body          = file("app.yaml")
  cluster = local.cluster
  depends_on         = [aws_eks_node_group.main]
}
```

**New capability**: Bootstrap cluster + workloads in one apply

### HPA Coexistence

```terraform
# Before (fights with HPA)
resource "kubernetes_manifest" "deployment" {
  manifest = {
    spec = { replicas = 3 }  # Terraform and HPA fight for control
  }
  field_manager { force_conflicts = true }  # Force ownership
}

# After (clean coexistence)
resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    spec:
      replicas: 3  # Initial value, HPA takes over
  YAML

  ignore_fields = ["spec.replicas"]  # Release to HPA
  cluster = local.cluster
}
```

### CRD + CR (Single Apply)

```terraform
# Before: Requires TWO applies
resource "kubernetes_manifest" "widget_crd" {
  manifest = yamldecode(file("widget-crd.yaml"))
}

resource "kubernetes_manifest" "widget" {
  manifest   = yamldecode(file("widget.yaml"))
  depends_on = [kubernetes_manifest.widget_crd]
}

# After: ONE apply (auto-retry)
resource "k8sconnect_object" "widget_crd" {
  yaml_body          = file("widget-crd.yaml")
  cluster = local.cluster
}

resource "k8sconnect_object" "widget" {
  yaml_body          = file("widget.yaml")
  cluster = local.cluster
  depends_on         = [k8sconnect_object.widget_crd]
}
```

### Multi-Cluster

```terraform
# Before: Separate providers
provider "kubernetes" {
  alias = "prod"
  # ...
}

# After: Inline connections
locals {
  prod_connection    = { kubeconfig = file("~/.kube/prod-config") }
  staging_connection = { kubeconfig = file("~/.kube/staging-config") }
}

resource "k8sconnect_object" "prod_app" {
  yaml_body          = file("app.yaml")
  cluster = local.prod_connection
}

resource "k8sconnect_object" "staging_app" {
  yaml_body          = file("app.yaml")
  cluster = local.staging_connection
}
```

## Feature Comparison

### Features k8sconnect Adds

| Feature | Description | Example |
|---------|-------------|---------|
| **Inline connections** | Per-resource cluster connection | `cluster = { kubeconfig = ... }` |
| **Auto CRD retry** | CRD + CR in one apply | Zero config, just works |
| **ignore_fields** | Release field ownership | `ignore_fields = ["spec.replicas"]` |
| **Dry-run projection** | Accurate plan diffs | Shows K8s defaults before apply |
| **Field validation** | Typo detection at plan time | Catches `replica` vs `replicas` |
| **k8sconnect_patch** | Surgical modifications | Patch EKS/Helm without full ownership |
| **k8sconnect_wait** | Retriable waits | Timeout doesn't taint resource |

### Features NOT in k8sconnect

| hashicorp/kubernetes Feature | k8sconnect Alternative |
|----------------------------|----------------------|
| Typed resources (`kubernetes_deployment`) | Use `k8sconnect_object` with YAML |
| `computed_fields` | Use `ignore_fields` instead |
| `wait.condition` (inline) | Use separate `k8sconnect_wait` resource |
| Provider-level auth | Use per-resource `cluster` |

## Common Challenges

### HCL to YAML Conversion

**Best approach**: Get YAML from running cluster
```bash
kubectl get deployment nginx -n default -o yaml > nginx.yaml
```

**Note**: HashiCorp recommends [tfk8s](https://github.com/jrhouston/tfk8s) for YAML → HCL conversion (opposite direction). No official HCL → YAML tool exists.

### Dynamic Manifests

```terraform
# Use templatefile for loops
resource "k8sconnect_object" "configs" {
  for_each = var.configs

  yaml_body = templatefile("${path.module}/configmap.yaml.tpl", {
    name = each.key
    data = each.value
  })

  cluster = local.cluster
}
```

## Validation

```bash
# After import, verify no changes
terraform plan  # Should show no changes

# Test modification
terraform apply

# Backup state first
cp terraform.tfstate terraform.tfstate.backup
```

## Related Resources

- [k8sconnect_object resource](../resources/object.md) - Primary resource reference
- [CRD + CR Management guide](crd-cr-management.md) - Auto-retry details
- [Managed Fields guide](field-ownership.md) - ignore_fields usage
- [Bootstrap Patterns guide](bootstrap-patterns.md) - Single-apply bootstrapping

## Summary

**Migration steps:**
1. Add k8sconnect resources to config
2. Import existing resources: `terraform import 'k8sconnect_object.x' 'apiVersion=...,kind=...,name=...'`
3. Remove old state: `terraform state rm 'kubernetes_manifest.x'`
4. Verify: `terraform plan` (should show no changes)

**Key benefits:**
- Bootstrap cluster + workloads in one apply
- CRD + CR in one apply (auto-retry)
- Clean HPA/operator coexistence (`ignore_fields`)
- Accurate diffs (dry-run projection)
- Multi-cluster in single config
- Retriable waits
