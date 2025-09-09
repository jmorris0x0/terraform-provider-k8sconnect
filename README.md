# terraform-provider-k8sconnect

![Tests](https://github.com/jmorris0x0/terraform-provider-k8sconnect/actions/workflows/test.yml/badge.svg)
![Security](https://github.com/jmorris0x0/terraform-provider-k8sconnect/actions/workflows/security.yml/badge.svg)
![Release](https://github.com/jmorris0x0/terraform-provider-k8sconnect/actions/workflows/release.yml/badge.svg)

A modern Terraform provider for applying Kubernetes YAML manifests **with inline, per‑resource connection settings**.

Traditional providers force cluster configuration into the provider block; **k8sconnect** pushes it down into each resource, freeing you to target *any* cluster from *any* module without aliases, workspaces, or wrapper hacks.


> ### ⚠️ ALPHA RELEASE
> **This provider is in alpha and not suitable for production use.** Breaking changes may occur without notice. Use at your own risk.


---

## Why `k8sconnect`

| Pain point                            | Conventional providers                                                      | **`k8sconnect`**                                                            |
| ------------------------------------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Cluster‑first dependency hell         | ❌ Two-phase workflow: deploy cluster, configure provider, then deploy apps | ✅ Single apply handles cluster creation and workloads together             |
| Module & multi-cluster limits         | ❌ Providers at root only, requires aliases for multiple clusters                  | ✅ Self-contained resources work in any module, any cluster               |
| Static provider configuration         | ❌ Provider config must be hardcoded at plan time                             | ✅ Use outputs, computed values, and loops dynamically                    |


**Stop fighting [Terraform's provider model](https://news.ycombinator.com/item?id=27434363). Start deploying to any cluster, from any module, in any order.**

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

# Store connections for multiple clusters
locals {
  prod_connection = {
    host                   = aws_eks_cluster.prod.endpoint
    cluster_ca_certificate = base64decode(aws_eks_cluster.prod.certificate_authority[0].data)
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", "prod"]
    }
  }
  
  # Use multiple connection methods for different environements
  staging_connection = {
    kubeconfig_raw = file("~/.kube/staging-config")
    context        = "staging"
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

That's it. No provider aliases, no separate workspaces, no chicken-and-egg dependency issues.

---

## Connection Methods

The provider supports three ways to connect to clusters:

**Inline with exec auth** (AWS EKS, GKE, etc.)
```hcl
cluster_connection = {
  host                   = "https://k8s.example.com"
  cluster_ca_certificate = base64encode(file("ca.pem"))
  exec = {
    api_version = "client.authentication.k8s.io/v1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", "my-cluster"]
  }
}
```

**Kubeconfig file**
```hcl
cluster_connection = {
  kubeconfig_file = "~/.kube/config"
  context         = "production"
}
```

**Raw kubeconfig** (CI-friendly)
```hcl
cluster_connection = {
  kubeconfig_raw = var.kubeconfig_content
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
    kubeconfig_raw = var.kubeconfig
  }
}
```

The `yaml_split` data source creates stable IDs like `deployment.my-app.nginx` and `service.my-app.nginx`, preventing unnecessary resource recreation when manifests are reordered.

**→ [Complete examples and patterns](docs/guides/multi-document-yaml.md)**

---

## Key Features

- ✅ **Multi-cluster support** - Each resource can connect to a different cluster, no provider aliases needed
- ✅ **True field management** - Only diffs and manages fields you define, coexists with other controllers
- ✅ **Module-friendly** - Resources with connections work inside modules, apply everything in one phase
- ✅ **Native YAML support** - Use your existing Kubernetes YAML directly, no HCL conversion needed
- ✅ **Server-side apply only** - No client-side logic, uses Kubernetes' native conflict resolution
- ✅ **Accurate drift detection** - Dry-run ensures diffs always show exactly what will change
- ✅ **Ownership tracking** - Prevents conflicts between Terraform states and unmanaged resources
- ✅ **Status tracking** - Optional access to resource status fields (LoadBalancer IPs, conditions, etc.)

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

---

## Security Considerations 🔐

Connection credentials are stored in Terraform state. Mitigate by:
- Using dynamic credentials (exec auth) instead of static tokens
- Encrypting remote state (S3 + KMS, Terraform Cloud, etc.) 

*You should probably be doing these things regardless.*


All `cluster_connection` fields are marked sensitive and won't appear in logs or plan output.

---

## Documentation

- **[Resource Reference](docs/resources/manifest.md)** - Complete field documentation
- **[Provider Configuration](docs/index.md)** - Provider-level settings

---

## Requirements

- Terraform 1.6+
- Kubernetes 1.17+ (uses only server-side apply)

---

## Installation

### From Registry (Available Soon)
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

