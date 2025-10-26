---
page_title: "Bootstrap Patterns - k8sconnect Provider"
subcategory: "Guides"
description: |-
  Bootstrap Kubernetes clusters and workloads in a single terraform apply with inline connections.
---

# Bootstrap Patterns

## The Problem

Traditional providers can't bootstrap clusters + workloads in one apply:

```terraform
# ❌ DOESN'T WORK - provider config can't reference resource outputs
provider "kubernetes" {
  host = aws_eks_cluster.main.endpoint  # ERROR
}
```

Provider configuration is evaluated before resources exist. You can't reference cluster outputs.

**Traditional workarounds (all bad):**
- Run `terraform apply` twice
- Separate modules with `terraform_remote_state`
- Null resources with provisioners

## k8sconnect Solution

Use **per-resource inline connections** instead of provider-level config:

```terraform
provider "k8sconnect" {}  # No configuration needed

resource "aws_eks_cluster" "main" {
  name = "production"
  # ...
}

resource "aws_eks_node_group" "main" {
  cluster_name = aws_eks_cluster.main.name
  # ...
}

# Define connection once, reuse everywhere
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

# Deploy immediately in same apply!
resource "k8sconnect_object" "app" {
  yaml_body          = file("app.yaml")
  cluster = local.cluster
  depends_on         = [aws_eks_node_group.main]
}
```

Terraform resolves outputs at apply time. Everything works in one run.

## AWS EKS

### Basic Bootstrap

```terraform
provider "k8sconnect" {}
provider "aws" {
  region = "us-west-2"
}

# Create EKS cluster (simplified - add VPC, IAM, etc.)
resource "aws_eks_cluster" "main" {
  name     = "production"
  role_arn = aws_iam_role.cluster.arn

  vpc_config {
    subnet_ids = var.subnet_ids
  }
}

resource "aws_eks_node_group" "main" {
  cluster_name    = aws_eks_cluster.main.name
  node_group_name = "main-nodes"
  node_role_arn   = aws_iam_role.nodes.arn
  subnet_ids      = var.subnet_ids

  scaling_config {
    desired_size = 3
    max_size     = 5
    min_size     = 3
  }
}

# Connection config (reusable)
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

# Deploy app
resource "k8sconnect_object" "app" {
  yaml_body = file("app.yaml")

  cluster = local.cluster
  depends_on         = [aws_eks_node_group.main]
}
```

### With Core Infrastructure

Bootstrap cert-manager + ingress + app together:

```terraform
# ... EKS cluster from above ...

# Deploy cert-manager
data "http" "cert_manager" {
  url = "https://github.com/cert-manager/cert-manager/releases/download/v1.12.0/cert-manager.yaml"
}

data "k8sconnect_yaml_split" "cert_manager" {
  content = data.http.cert_manager.response_body
}

resource "k8sconnect_object" "cert_manager" {
  for_each = data.k8sconnect_yaml_split.cert_manager.documents

  yaml_body          = each.value
  cluster = local.cluster
  depends_on         = [aws_eks_node_group.main]
}

# Deploy ingress-nginx
data "http" "ingress_nginx" {
  url = "https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.8.1/deploy/static/provider/aws/deploy.yaml"
}

data "k8sconnect_yaml_split" "ingress_nginx" {
  content = data.http.ingress_nginx.response_body
}

resource "k8sconnect_object" "ingress_nginx" {
  for_each = data.k8sconnect_yaml_split.ingress_nginx.documents

  yaml_body          = each.value
  cluster = local.cluster
  depends_on         = [aws_eks_node_group.main]
}

# Wait for LoadBalancer
resource "k8sconnect_wait" "ingress_lb" {
  object_ref = k8sconnect_object.ingress_nginx["Service/ingress-nginx-controller/ingress-nginx"].object_ref

  wait_for = {
    field   = "status.loadBalancer.ingress"
    timeout = "10m"
  }

  cluster = local.cluster
}

# Create DNS record
resource "aws_route53_record" "ingress" {
  zone_id = var.route53_zone_id
  name    = "*.example.com"
  type    = "A"
  ttl     = 300
  records = [k8sconnect_wait.ingress_lb.result.status.loadBalancer.ingress[0].hostname]
}

# Deploy app with ingress
resource "k8sconnect_object" "app" {
  yaml_body = file("app-with-ingress.yaml")

  cluster = local.cluster
  depends_on         = [k8sconnect_wait.ingress_lb, k8sconnect_object.cert_manager]
}
```

**All in one apply:** EKS + nodes + cert-manager + ingress-nginx + app + DNS

## GCP GKE

```terraform
provider "k8sconnect" {}
provider "google" {
  project = "my-project"
  region  = "us-central1"
}

resource "google_container_cluster" "main" {
  name     = "production"
  location = "us-central1"

  remove_default_node_pool = true
  initial_node_count       = 1
}

resource "google_container_node_pool" "main" {
  name       = "main-nodes"
  location   = "us-central1"
  cluster    = google_container_cluster.main.name
  node_count = 3

  node_config {
    machine_type = "e2-medium"
    oauth_scopes = ["https://www.googleapis.com/auth/cloud-platform"]
  }
}

locals {
  cluster = {
    host                   = "https://${google_container_cluster.main.endpoint}"
    cluster_ca_certificate = google_container_cluster.main.master_auth[0].cluster_ca_certificate
    exec = {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "gke-gcloud-auth-plugin"
    }
  }
}

resource "k8sconnect_object" "app" {
  yaml_body          = file("app.yaml")
  cluster = local.cluster
  depends_on         = [google_container_node_pool.main]
}
```

## Azure AKS

```terraform
provider "k8sconnect" {}
provider "azurerm" {
  features {}
}

resource "azurerm_kubernetes_cluster" "main" {
  name                = "production"
  location            = "West US 2"
  resource_group_name = azurerm_resource_group.main.name
  dns_prefix          = "production"

  default_node_pool {
    name       = "default"
    node_count = 3
    vm_size    = "Standard_D2_v2"
  }

  identity {
    type = "SystemAssigned"
  }
}

locals {
  cluster = {
    host                   = azurerm_kubernetes_cluster.main.kube_config[0].host
    cluster_ca_certificate = azurerm_kubernetes_cluster.main.kube_config[0].cluster_ca_certificate
    client_certificate     = azurerm_kubernetes_cluster.main.kube_config[0].client_certificate
    client_key             = azurerm_kubernetes_cluster.main.kube_config[0].client_key
  }
}

resource "k8sconnect_object" "app" {
  yaml_body          = file("app.yaml")
  cluster = local.cluster
}
```

## Dependency Patterns

### Explicit Chain

```terraform
resource "k8sconnect_object" "namespace" {
  yaml_body = file("namespace.yaml")
  cluster = local.cluster

  depends_on = [aws_eks_node_group.main]
}

resource "k8sconnect_object" "config" {
  yaml_body = file("config.yaml")
  cluster = local.cluster

  depends_on = [k8sconnect_object.namespace]
}

resource "k8sconnect_object" "app" {
  yaml_body = file("app.yaml")
  cluster = local.cluster

  depends_on = [k8sconnect_object.config]
}
```

### Implicit (via references)

```terraform
resource "k8sconnect_object" "secret" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Secret
    stringData:
      password: ${random_password.db.result}
  YAML

  cluster = local.cluster
}

resource "k8sconnect_object" "app" {
  yaml_body = <<-YAML
    env:
    - name: DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: ${k8sconnect_object.secret.object_ref.name}
  YAML

  cluster = local.cluster
}
```

Terraform auto-orders: `random_password` → `secret` → `app`

### Parallel Deployment

```terraform
# These deploy simultaneously (no dependencies)
resource "k8sconnect_object" "monitoring_namespace" {
  yaml_body = file("monitoring-ns.yaml")
  cluster = local.cluster
}

resource "k8sconnect_object" "app_namespace" {
  yaml_body = file("app-ns.yaml")
  cluster = local.cluster
}

# Each stack deploys in parallel too
resource "k8sconnect_object" "monitoring" {
  for_each = data.k8sconnect_yaml_split.prometheus.documents
  yaml_body = each.value
  cluster = local.cluster

  depends_on = [k8sconnect_object.monitoring_namespace]
}

resource "k8sconnect_object" "app" {
  for_each = data.k8sconnect_yaml_split.app.documents
  yaml_body = each.value
  cluster = local.cluster

  depends_on = [k8sconnect_object.app_namespace]
}
```

### Wait for Readiness

```terraform
# Deploy DB
resource "k8sconnect_object" "postgres" {
  yaml_body = file("postgres.yaml")
  cluster = local.cluster
}

# Wait for ready
resource "k8sconnect_wait" "postgres" {
  object_ref = k8sconnect_object.postgres.object_ref
  wait_for   = { rollout = true, timeout = "10m" }
  cluster = local.cluster
}

# Run migration
resource "k8sconnect_object" "migration" {
  yaml_body  = file("migration-job.yaml")
  cluster = local.cluster

  depends_on = [k8sconnect_wait.postgres]
}

# Wait for migration
resource "k8sconnect_wait" "migration" {
  object_ref = k8sconnect_object.migration.object_ref
  wait_for   = { field = "status.succeeded", field_value = "1", timeout = "15m" }
  cluster = local.cluster
}

# Deploy app
resource "k8sconnect_object" "app" {
  yaml_body  = file("app.yaml")
  cluster = local.cluster

  depends_on = [k8sconnect_wait.migration]
}
```

## Common Pitfalls

### Deploying Before Nodes Ready

```terraform
# ❌ BAD: Might deploy before nodes exist
resource "k8sconnect_object" "app" {
  depends_on = [aws_eks_cluster.main]  # Cluster exists, but no nodes!
}

# ✅ GOOD: Wait for nodes
resource "k8sconnect_object" "app" {
  depends_on = [aws_eks_node_group.main]
}
```

### Unknown After Apply

```terraform
# ❌ BAD: LoadBalancer IP is unknown until created
resource "k8sconnect_object" "config" {
  yaml_body = <<-YAML
    endpoint: "${k8sconnect_object.lb.status.loadBalancer.ingress[0].ip}"
  YAML
}

# ✅ GOOD: Use wait to extract value
resource "k8sconnect_wait" "lb" {
  object_ref = k8sconnect_object.lb.object_ref
  wait_for   = { field = "status.loadBalancer.ingress" }
}

resource "k8sconnect_object" "config" {
  yaml_body = <<-YAML
    endpoint: "${k8sconnect_wait.lb.result.status.loadBalancer.ingress[0].ip}"
  YAML

  depends_on = [k8sconnect_wait.lb]
}
```

### Operator Not Running

```terraform
# ❌ BAD: CR created before operator is running
resource "k8sconnect_object" "operator" {
  yaml_body = file("operator.yaml")
}

resource "k8sconnect_object" "cr" {
  depends_on = [k8sconnect_object.operator]  # Deployed, not running!
}

# ✅ GOOD: Wait for operator rollout
resource "k8sconnect_wait" "operator" {
  object_ref = k8sconnect_object.operator.object_ref
  wait_for   = { rollout = true }
}

resource "k8sconnect_object" "cr" {
  depends_on = [k8sconnect_wait.operator]
}
```

## Best Practices

**1. Use local variables for connection**

```terraform
locals {
  cluster = { /* ... */ }
}
```

**2. Always depend on node group, not cluster**

```terraform
depends_on = [aws_eks_node_group.main]
```

**3. Stage critical infrastructure**

```
Cluster + Nodes → Core (cert-manager, ingress) → Apps
```

**4. Use waits for critical dependencies**

- Operators before CRs
- Databases before migrations
- LoadBalancers before DNS

## Related Resources

- [Wait Strategies guide](wait-strategies.md)
- [CRD + CR Management guide](crd-cr-management.md)

## Summary

- Inline connections solve provider dependency hell
- Bootstrap EKS/GKE/AKS + workloads in one apply
- Always depend on node groups
- Use waits for critical dependencies
- Parallelize independent resources
