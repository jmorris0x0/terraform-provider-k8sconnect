# K8sconnect Kubernetes Terraform Provider

![Tests](https://github.com/jmorris0x0/terraform-provider-k8sconnect/actions/workflows/test.yml/badge.svg)
![Security](https://github.com/jmorris0x0/terraform-provider-k8sconnect/actions/workflows/security.yml/badge.svg)
![Release](https://github.com/jmorris0x0/terraform-provider-k8sconnect/actions/workflows/release.yml/badge.svg)

Bootstrap Kubernetes clusters and workloads in a **single `terraform apply`**. No two-phase deployments.

**k8sconnect** uses inline, per-resource connections to break Terraform's provider dependency hell. Create a cluster and deploy to it immediately, target multiple clusters from one module, or use dynamic outputs for authentication.

---

## Why `k8sconnect`

| Pain point                            | Conventional providers                                                      | **`k8sconnect`**                                                            |
| ------------------------------------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Cluster‑first dependency hell         | ❌ Two-phase workflow: deploy cluster, configure provider, then deploy apps | ✅ Single apply handles cluster creation and workloads together             |
| Module & multi-cluster limits         | ❌ Providers at root only, requires aliases for multiple clusters           | ✅ Self-contained resources work in any module, any cluster                 |
| Static provider configuration         | ❌ Provider config must be hardcoded at plan time                           | ✅ Use outputs, computed values, and loops dynamically                      |
| CRD + CR in single apply              | ❌ Manual workaround or requires config                                     | ✅ Auto-retry, zero configuration                                           |
| Controller coexistence                | ⚠️ SSA optional or no ignore_fields                                         | ✅ Always-on SSA + ignore_fields for HPA, webhooks, operators               |
| Unpredictable plan diffs              | ❌ Plan shows what you send, not what K8s will do                           | ✅ Dry-run projections show exact changes before apply                      |
| Surgical patches on managed resources | ❌ Import or take full ownership                                            | ✅ Patch EKS/GKE/Helm/operator resources                                    |


**Stop fighting [Terraform's provider model](https://news.ycombinator.com/item?id=27434363). Create clusters and bootstrap workloads in one apply.**

---

## Getting Started

```hcl
terraform {
  required_providers {
    k8sconnect = {
      source  = "jmorris0x0/k8sconnect"
      version = ">= 0.1.0"
    }
  }
}

provider "k8sconnect" {}

# Create a new cluster
resource "aws_eks_cluster" "main" {
  name     = "my-cluster"
  role_arn = aws_iam_role.cluster.arn
  # ... (cluster configuration)
}

# Deploy workloads immediately - no waiting for provider configuration!
resource "k8sconnect_manifest" "cert_manager" {
  yaml_body = file("cert-manager.yaml")

  cluster_connection = {
    host                   = aws_eks_cluster.main.endpoint
    cluster_ca_certificate = aws_eks_cluster.main.certificate_authority[0].data
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", aws_eks_cluster.main.name]
    }
  }
}
```

That's it. The connection is inline—Terraform can resolve the outputs, and everything applies in one run.

---

## Connection Methods

The provider supports three ways to connect to clusters:

**Inline with token auth**
```hcl
cluster_connection = {
  host                   = "https://k8s.example.com"
  cluster_ca_certificate = file("ca.pem")
  token                  = var.cluster_token
}
```

**Inline with exec auth** (AWS EKS, GKE, etc.)
```hcl
cluster_connection = {
  host                   = "https://k8s.example.com"
  cluster_ca_certificate = file("ca.pem")
  exec = {
    api_version = "client.authentication.k8s.io/v1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", "my-cluster"]
  }
}
```

**kubeconfig**
```hcl
# From file
cluster_connection = {
  kubeconfig = file("~/.kube/config")
  context    = "production"  # optional
}

# From variable (CI-friendly)
cluster_connection = {
  kubeconfig = var.kubeconfig_content
}
```

**Multi-cluster deployments**

Since connections are per-resource, you can target multiple clusters from a single module:

```hcl
locals {
  prod_connection = {
    host                   = aws_eks_cluster.prod.endpoint
    cluster_ca_certificate = aws_eks_cluster.prod.certificate_authority[0].data
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", "prod"]
    }
  }

  staging_connection = {
    kubeconfig = file("~/.kube/staging-config")
    context    = "staging"
  }
}

# Deploy to different clusters in one apply
resource "k8sconnect_manifest" "prod_app" {
  yaml_body          = file("app.yaml")
  cluster_connection = local.prod_connection
}

resource "k8sconnect_manifest" "staging_app" {
  yaml_body          = file("app.yaml")
  cluster_connection = local.staging_connection
}
```

---

## Multi-Document YAML

Split YAML files containing multiple Kubernetes manifests:

```hcl
# Split multi-document YAML
data "k8sconnect_yaml_split" "app" {
  content = file("${path.module}/app-manifests.yaml")
}

# Apply each manifest individually  
resource "k8sconnect_manifest" "app" {
  for_each = data.k8sconnect_yaml_split.app.manifests
  
  yaml_body = each.value
  
  cluster_connection = {
    kubeconfig = var.kubeconfig
  }
}
```

The `yaml_split` data source creates stable IDs like `deployment.my-app.nginx` and `service.my-app.nginx`, preventing unnecessary resource recreation when manifests are reordered.

**→ [Browse 12 runnable examples](examples/)** - EKS bootstrap, HPA coexistence, patch patterns, and more

---

## Surgical Patching

Modify resources managed by others without taking full ownership. Supports Strategic Merge Patch (shown), JSON Patch, and Merge Patch.

```hcl
# Patch AWS EKS system resources (using Strategic Merge Patch)
resource "k8sconnect_patch" "aws_node_config" {
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
            env  = [{ name = "ENABLE_PREFIX_DELEGATION", value = "true" }]
          }]
        }
      }
    }
  })

  take_ownership     = true
  cluster_connection = var.cluster_connection
}
```

Perfect for EKS/GKE defaults, Helm deployments, and operator-managed resources. On destroy, ownership transfers back cleanly.

**→ [Patch examples](examples/patch-strategic-merge/)** | **[Documentation](docs/resources/patch.md)**

---

## How It Works: SSA + Dry-Run = Predictable Infrastructure

k8sconnect uses **Server-Side Apply with Dry-Run** for every operation, giving you:

1. **Accurate plan diffs** - The `managed_state_projection` attribute shows exactly what Kubernetes will change, computed via dry-run. No surprises between plan and apply.

2. **SSA-aware field ownership** - The `field_ownership` attribute tracks which controller owns each field. See when HPA takes over replicas, when webhooks modify annotations, or when another Terraform state conflicts with yours.

3. **True drift detection** - Only diffs fields you actually manage. If a controller updates status or another field manager changes something, you'll see it clearly separated:
   - `yaml_body` diffs = Changes you made to your config
   - `managed_state_projection` diffs = External changes that will be corrected
   - `field_ownership` diffs = Ownership changes between controllers

This provider combines Server-Side Apply field ownership tracking with dry-run projections during plan, enabling accurate diffs and multi-controller coexistence patterns via ignore_fields.

---

## Importing Resources

Import existing Kubernetes resources into Terraform management:

```bash
# Set your kubeconfig
export KUBECONFIG=~/.kube/config

# Namespaced resources: context/namespace/Kind/name
terraform import k8sconnect_manifest.nginx "prod/default/Pod/nginx-abc123"

# Cluster-scoped resources: context/Kind/name
terraform import k8sconnect_manifest.namespace "prod/Namespace/my-namespace"
```

After import, add the `cluster_connection` block to your configuration to match how you want to connect during normal operations.

> **Note:** Import is only available for `k8sconnect_manifest`. Patches are non-destructive modifications and don't support import.

---

## Security Considerations 🔐

Connection credentials are stored in Terraform state. Mitigate by:
- Using dynamic credentials (exec auth) instead of static tokens
- Encrypting remote state (S3 + KMS, Terraform Cloud, etc.) 

*You should probably be doing these things regardless.*


All `cluster_connection` fields are marked sensitive and won't appear in logs or plan output.

---

## Resources & Data Sources

**Resources:**
- `k8sconnect_manifest` - Full lifecycle management for any Kubernetes resource ([docs](docs/resources/manifest.md))
- `k8sconnect_patch` - Surgical modifications to existing resources ([docs](docs/resources/patch.md))

**Data Sources:**
- `k8sconnect_yaml_split` - Parse multi-document YAML files ([docs](docs/data-sources/yaml_split.md))
- `k8sconnect_resource` - Read existing cluster resources ([docs](docs/data-sources/resource.md))
- `k8sconnect_yaml_scoped` - Filter resources by category ([docs](docs/data-sources/yaml_scoped.md))

---

## Requirements
- Terraform >= 1.0.11
- Kubernetes >= 1.28

> **Note**: While the provider may function on older versions, only Kubernetes versions currently receiving security updates are tested and supported.

---

## Installation

### Coming to Registry Soon
```hcl
terraform {
  required_providers {
    k8sconnect = {
      source  = "jmorris0x0/k8sconnect"
      version = ">= 0.1.0"
    }
  }
}
```

### Local Development
```bash
git clone https://github.com/jmorris0x0/terraform-provider-k8sconnect.git
cd terraform-provider-k8sconnect
make install
```

---

## License

Apache 2.0 - see [LICENSE](./LICENSE)

This project is not affiliated with the Kubernetes project or CNCF.

