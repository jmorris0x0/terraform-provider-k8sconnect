---
page_title: "k8sconnect Provider"
subcategory: ""
description: |-
  Bootstrap Kubernetes clusters in a single apply. Supports inline connections, Server-Side Apply, multi-cluster deployments, and surgical patching of any Kubernetes resource.
---

# k8sconnect Provider

Bootstrap Kubernetes clusters and workloads in a **single `terraform apply`**. No two-phase deployments.

The k8sconnect provider uses inline, per-resource connections instead of provider-level configuration. This bypasses Terraform's provider configuration timing constraints - you can use cluster outputs directly in resource connections. Create a cluster and deploy to it immediately, target multiple clusters from one module, or use dynamic outputs for authentication.

## Example Usage

```terraform
# Create a new EKS cluster
resource "aws_eks_cluster" "main" {
  name     = "my-cluster"
  role_arn = aws_iam_role.cluster.arn
  # ... cluster configuration
}

# Deploy workloads immediately - no waiting for provider configuration!
resource "k8sconnect_object" "cert_manager" {
  yaml_body = file("cert-manager.yaml")

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
```

That's it. The connection is inline—Terraform resolves the outputs, and everything applies in one run.

## Authentication

The provider requires no global configuration. Authentication is specified per-resource via the `cluster` block, supporting three methods:

### Token Authentication

```terraform
cluster = {
  host                   = "https://k8s.example.com"
  cluster_ca_certificate = file("ca.pem")
  token                  = var.cluster_token
}
```

### Exec-based Authentication (AWS EKS, GKE, AKS)

```terraform
cluster = {
  host                   = "https://k8s.example.com"
  cluster_ca_certificate = file("ca.pem")
  exec = {
    api_version = "client.authentication.k8s.io/v1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", "my-cluster"]
  }
}
```

### Kubeconfig

```terraform
# From file
cluster = {
  kubeconfig = file("~/.kube/config")
  context    = "production"  # optional
}

# From variable (CI-friendly)
cluster = {
  kubeconfig = var.kubeconfig_content
}
```

## Key Features

- **Single-apply cluster bootstrapping** - Deploy clusters and workloads together without dependency cycles
- **Accurate dry-run plans** - See exactly what Kubernetes will do before apply, not just what you send
- **Field validation** - Catch typos and invalid fields during plan (`replica` vs `replicas`, `imagePullPolice` vs `imagePullPolicy`)
- **Field ownership tracking** - Coexist with controllers (HPA, operators) via Server-Side Apply
- **Surgical patching** - Modify EKS/GKE defaults, Helm charts, and operator-managed resources without full ownership
- **Multi-cluster support** - Different connections per resource, works in modules
- **Universal CRD support** - No schema translation, works with any Custom Resource Definition

## Resources

- `k8sconnect_object` - Full lifecycle management for any Kubernetes resource
- `k8sconnect_wait` - Wait for resources to reach desired state with extractable results
- `k8sconnect_patch` - Surgical modifications to existing resources

## Data Sources

- `k8sconnect_object` - Read existing cluster resources
- `k8sconnect_yaml_split` - Parse multi-document YAML into individually-addressable resources
- `k8sconnect_yaml_scoped` - Split and categorize resources by scope (CRDs, cluster-scoped, namespaced) for correct dependency ordering. Essential for large manifest sets where Terraform's parallelism limit (~10 concurrent operations) would otherwise cause dependency failures

