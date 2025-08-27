# terraform-provider-k8sinline

![Tests](https://github.com/jmorris0x0/terraform-provider-k8sinline/actions/workflows/test.yml/badge.svg)
![Security](https://github.com/jmorris0x0/terraform-provider-k8sinline/actions/workflows/security.yml/badge.svg)
![Release](https://github.com/jmorris0x0/terraform-provider-k8sinline/actions/workflows/release.yml/badge.svg)

A Terraform provider for applying Kubernetes YAML manifests **with inline, per‑resource connection settings**.

Traditional providers force cluster configuration into the provider block; **k8sinline** pushes it down into each resource, freeing you to target *any* cluster from *any* module without aliases, workspaces, or wrapper hacks.


## ⚠️ ALPHA RELEASE

**This provider is in alpha and not suitable for production use.** Breaking changes may occur without notice. Use at your own risk.

---

---

## Why `k8sinline`

| Pain point                            | Conventional providers                                                      | **`k8sinline`**                                                             |
| ------------------------------------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Cluster‑first dependency hell         | ❌ Two-phase workflow: deploy cluster, then configure provider, then deploy apps | ✅ Single apply handles cluster creation and workloads together |
| Multi‑cluster support                 | ❌ Requires provider aliases or separate states per cluster                  | ✅ Inline connection per resource — all clusters in one plan                 |

---

## Getting Started

```hcl
terraform {
  required_providers {
    k8sinline = {
      source  = "github.com/jmorris0x0/terraform-provider-k8sinline"
      version = ">= 0.1.0"
    }
  }
}

provider "k8sinline" {}

# Deploy to AWS EKS with dynamic credentials
resource "k8sinline_manifest" "nginx" {
  yaml_body = file("${path.module}/manifests/nginx.yaml")

  cluster_connection = {
    host                   = data.aws_eks_cluster.prod.endpoint
    cluster_ca_certificate = data.aws_eks_cluster.prod.certificate_authority[0].data
    
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", "prod-cluster"]
    }
  }
}

# Deploy to staging with kubeconfig
resource "k8sinline_manifest" "staging_app" {
  yaml_body = file("${path.module}/manifests/app.yaml")

  cluster_connection = {
    kubeconfig_raw = aws_ssm_parameter.staging_kubeconfig.value
    context        = "staging"
  }
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
data "k8sinline_yaml_split" "app" {
  content = file("${path.module}/app-manifests.yaml")
}

# Apply each manifest individually  
resource "k8sinline_manifest" "app" {
  for_each = data.k8sinline_yaml_split.app.manifests
  
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

---

## Importing Resources

Import existing Kubernetes resources into Terraform management:

```bash
# Set your kubeconfig
export KUBECONFIG=~/.kube/config

# Namespaced resources: context/namespace/Kind/name  
terraform import k8sinline_manifest.nginx "prod/default/Pod/nginx-abc123"

# Cluster-scoped resources: context/Kind/name
terraform import k8sinline_manifest.namespace "prod/Namespace/my-namespace"
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

### From GitHub (Available Soon)
```hcl
terraform {
  required_providers {
    k8sinline = {
      source  = "jmorris0x0/k8sinline"
      version = ">= 0.1.0"
    }
  }
}
```

### Local Development
```bash
git clone https://github.com/jmorris0x0/terraform-provider-k8sinline.git
cd terraform-provider-k8sinline
make install
```

---

## License

Apache 2.0 - see [LICENSE](./LICENSE)

This project is not affiliated with the Kubernetes project or CNCF.

