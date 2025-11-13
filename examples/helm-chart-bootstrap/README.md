# Helm Chart Bootstrap

Demonstrates bootstrapping clusters with Helm charts by templating them and deploying via k8sconnect instead of the Helm provider.

## Use Case

The Helm provider can't deploy to a cluster that doesn't exist yet because providers are configured before resources. This example shows how to:

1. Create a cluster (EKS, GKE, AKS, etc.)
2. Template Helm charts for foundation services
3. Deploy them via k8sconnect **in the same apply**

Common foundation services to deploy this way:
- **GitOps**: ArgoCD, Flux
- **Networking**: Cilium, Calico, Istio
- **Security**: cert-manager, external-secrets-operator
- **Observability**: Prometheus, Grafana

After the foundation is deployed, GitOps can take over for application workloads.

## Why Not Use the Helm Provider?

The Helm provider requires an already-running cluster:

```hcl
# ❌ Doesn't work - provider configured before cluster exists
provider "helm" {
  kubernetes {
    host = aws_eks_cluster.main.endpoint  # Not available during provider init
  }
}
```

Workarounds like separate stacks or `terraform_remote_state` break the ability to deploy everything in one apply.

## Why Not Just `k8sconnect_object` Directly?

The Helm provider's `helm_template` data source outputs **all manifests as a single string**:

```yaml
apiVersion: v1
kind: ServiceAccount
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
---
apiVersion: apps/v1
kind: Deployment
---
... (20-50 more resources)
```

You need `yaml_scoped` to:
1. **Split** the multi-document YAML into individual manifests
2. **Categorize** by scope (CRDs, cluster-scoped, namespaced)
3. **Order dependencies** (CRDs before resources that use them)
4. **Track each resource** individually in Terraform state

## The Pattern

```hcl
# 1. Template the Helm chart
data "helm_template" "app" {
  chart      = "..."
  repository = "..."
}

# 2. Split and categorize
data "k8sconnect_yaml_scoped" "app" {
  content = data.helm_template.app.manifest
}

# 3. Apply in order: CRDs → cluster-scoped → namespaced
resource "k8sconnect_object" "crds" {
  for_each = data.k8sconnect_yaml_scoped.app.crds
  ...
}

resource "k8sconnect_object" "cluster" {
  for_each   = data.k8sconnect_yaml_scoped.app.cluster_scoped
  depends_on = [k8sconnect_object.crds]
  ...
}

resource "k8sconnect_object" "app" {
  for_each   = data.k8sconnect_yaml_scoped.app.namespaced
  depends_on = [k8sconnect_object.cluster]
  ...
}
```

## Complete Example with Cluster Creation

```hcl
# Create EKS cluster
resource "aws_eks_cluster" "main" {
  name = "my-cluster"
  ...
}

resource "aws_eks_node_group" "main" {
  cluster_name = aws_eks_cluster.main.name
  ...
}

# Define connection (reuse across all resources)
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

# Template cert-manager Helm chart
data "helm_template" "cert_manager" {
  name       = "cert-manager"
  chart      = "cert-manager"
  repository = "https://charts.jetstack.io"
  set {
    name  = "installCRDs"
    value = "true"
  }
}

# Deploy via k8sconnect - all in one apply!
data "k8sconnect_yaml_scoped" "cert_manager" {
  content = data.helm_template.cert_manager.manifest
}

resource "k8sconnect_object" "cert_manager_crds" {
  for_each = data.k8sconnect_yaml_scoped.cert_manager.crds
  yaml_body = each.value
  cluster   = local.cluster
  depends_on = [aws_eks_node_group.main]  # Wait for nodes
}

# ... cluster-scoped and namespaced resources
```

## Running This Example

See [../README.md](../README.md) for setup instructions.

This example uses cert-manager as a demonstration. For production, adjust:
- Chart version to match your requirements
- Helm `set` values for your environment
- Add health checks via `k8sconnect_wait` if needed
