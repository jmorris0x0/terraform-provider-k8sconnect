# K8sconnect Kubernetes Terraform Provider

![Tests](https://github.com/jmorris0x0/terraform-provider-k8sconnect/actions/workflows/test.yml/badge.svg)
[![codecov](https://codecov.io/github/jmorris0x0/terraform-provider-k8sconnect/graph/badge.svg?token=8B1UTHRK71)](https://codecov.io/github/jmorris0x0/terraform-provider-k8sconnect)
![Security](https://github.com/jmorris0x0/terraform-provider-k8sconnect/actions/workflows/security.yml/badge.svg)
![Release](https://github.com/jmorris0x0/terraform-provider-k8sconnect/actions/workflows/release.yml/badge.svg)

Bootstrap Kubernetes clusters and workloads in a **single `terraform apply`**. No two-phase deployments.

**k8sconnect** uses inline, per-resource connections to break Terraform's provider dependency hell. Create a cluster and deploy to it immediately, target multiple clusters from one module, or use dynamic outputs for authentication.

---

## Why `k8sconnect`

| Pain point                            | Conventional providers                                                      | **`k8sconnect`**                                                            |
| ------------------------------------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Cluster‑first dependency hell         | ❌ Two-phase workflow, providers at root only                               | ✅ Single, dynamic apply handles cluster creation and workloads together    |
| CRD + CR in single apply              | ❌ Manual workaround or requires config                                     | ✅ Auto-retry, zero configuration                                           |
| Controller coexistence                | ⚠️ SSA optional or no ignore_fields                                         | ✅ Always-on SSA + ignore_fields for HPA, webhooks, operators               |
| Unpredictable plan diffs              | ❌ Plan shows what you send, not what K8s will do                           | ✅ Dry-run projections show exact changes before apply                      |
| Typos and invalid fields              | ❌ Always out-of-date typed resources                                       | ✅ Dry-run + field validation makes typed resources obsolete                |
| Surgical patches on managed resources | ❌ Import or take full ownership                                            | ✅ Patch EKS/GKE/Helm/operator resources                                    |
| Wait timeout behavior                 | ⚠️ Taints resource, forces recreate on retry                                | ✅ Separate wait resource, retry in-place                                   |


**Stop fighting [Terraform's provider model](https://news.ycombinator.com/item?id=27434363). Create clusters and bootstrap workloads in one apply.**

---

## Getting Started

```hcl
provider "k8sconnect" {}  # No configuration needed

# Create a new cluster
resource "aws_eks_cluster" "main" {
  name     = "my-cluster"
  # ... (cluster configuration)
}

resource "aws_eks_node_group" "main" {
  cluster_name = aws_eks_cluster.main.name
  # ... (node group configuration)
}

# Connection can be reused across resources
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

# Deploy workloads immediately - no waiting for provider configuration!
resource "k8sconnect_object" "app" {
  yaml_body          = file("deployment.yaml")
  cluster = local.cluster

  # For EKS: ensure nodes are ready before deploying workloads
  depends_on = [aws_eks_node_group.main]
}
```

That's it. The connection is defined once and reused—Terraform can resolve the outputs, and everything applies in one run.

---

## Connection Methods

The provider supports three ways to connect to clusters:

**Inline with token auth**
```hcl
cluster = {
  host                   = "https://k8s.example.com"
  cluster_ca_certificate = file("ca.pem")
  token                  = var.cluster_token
}
```

**Inline with exec auth** (AWS EKS, GKE, etc.)
```hcl
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

**kubeconfig**
```hcl
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
resource "k8sconnect_object" "prod_app" {
  yaml_body          = file("app.yaml")
  cluster = local.prod_connection
}

resource "k8sconnect_object" "staging_app" {
  yaml_body          = file("app.yaml")
  cluster = local.staging_connection
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
resource "k8sconnect_object" "app" {
  for_each = data.k8sconnect_yaml_split.app.manifests
  
  yaml_body = each.value
  
  cluster = {
    kubeconfig = var.kubeconfig
  }
}
```

The `yaml_split` data source creates stable IDs like `deployment.my-app.nginx` and `service.my-app.nginx`, preventing unnecessary resource recreation when manifests are reordered.

**→ [YAML split examples](examples/#yaml-split-data-source)** - Inline, file patterns, templated

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

  patch = jsonencode({
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

  cluster = var.cluster
}
```

Perfect for EKS/GKE defaults, Helm deployments, and operator-managed resources. On destroy, ownership transfers back cleanly—current values are left unchanged for safety.

**→ [Patch examples](examples/#patch-resource)** - Strategic Merge, JSON Patch, Merge Patch | **[Documentation](docs/resources/patch.md)**

---

## Wait for Resources and Use Status

Wait for resources and extract status fields without drift. The separate `k8sconnect_wait` resource gives you fine-grained control over what you track and works with resources you don't even manage:

<!-- runnable-test: readme-wait-for-loadbalancer -->
```hcl
# Create the LoadBalancer Service
resource "k8sconnect_object" "service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: my-service
      namespace: prod
    spec:
      type: LoadBalancer
      ports:
      - port: 80
        targetPort: 8080
      selector:
        app: myapp
  YAML

  cluster = local.cluster
}

# Wait for LoadBalancer IP to be assigned
resource "k8sconnect_wait" "service" {
  object_ref = k8sconnect_object.service.object_ref

  wait_for = {
    field   = "status.loadBalancer.ingress"
    timeout = "5m"
  }

  cluster = local.cluster
}

# Use the LoadBalancer IP immediately
resource "k8sconnect_object" "config" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: endpoints
      namespace: prod
    data:
      service_url: "${k8sconnect_wait.service.result.status.loadBalancer.ingress[0].ip}:80"
  YAML

  cluster = local.cluster
  depends_on         = [k8sconnect_wait.service]
}
```
<!-- /runnable-test -->

**Choosing a wait strategy:**

**Infrastructure resources** need status values for DNS, outputs, chaining:
- LoadBalancer Services, Ingress, cert-manager Certificates, Crossplane resources, Custom CRDs with important status
- Use `field` waits → populates `.result` attribute for use in other resources
- Example: `wait_for = { field = "status.loadBalancer.ingress" }`

**Workload resources** just need readiness confirmation:
- Deployments, Jobs, StatefulSets, DaemonSets
- Use `rollout`, `condition`, or `field_value` waits → no `.result` output, use `depends_on` for sequencing
- Example: `wait_for = { rollout = true }`

**Why this matters:**
- **Selective field tracking** - Only `field` waits populate `.result`, preventing drift from volatile fields
- **No more provisioners** - Native Terraform waiting and data flow
- **Works with any CRD** - If it has status fields you need, use `field` waits

**→ [Wait resource documentation](docs/resources/wait.md)** | **[Wait strategy examples](examples/#wait-for-feature)** - field, field_value, condition, rollout

---

## How It Works: SSA + Dry-Run = Predictable Infrastructure

k8sconnect uses **Server-Side Apply with Dry-Run** for every operation, giving you:

1. **Accurate plan diffs** - The `managed_state_projection` attribute shows exactly what Kubernetes will change, computed via dry-run. No surprises between plan and apply.

2. **Field validation** - Strict validation during plan catches typos and invalid fields before apply (`replica` vs `replicas`, `imagePullPolice` vs `imagePullPolicy`, etc.).

3. **SSA-aware field ownership** - The `field_ownership` attribute tracks which controller owns each field. See when HPA takes over replicas, when webhooks modify annotations, or when another Terraform state conflicts with yours. When conflicts occur, warnings show exact `ignore_fields` configuration to resolve them. **→ [Field ownership guide](docs/guides/field-ownership.md)**

4. **True drift detection** - Only diffs fields you actually manage. If a controller updates status or another field manager changes something, you'll see it clearly separated:
   - `yaml_body` diffs = Changes you made to your config
   - `managed_state_projection` diffs = External changes that will be corrected
   - `field_ownership` diffs = Ownership changes between controllers

This provider combines Server-Side Apply field ownership tracking with dry-run projections during plan, enabling accurate diffs and multi-controller coexistence patterns via ignore_fields.

---

## Security Considerations 🔐

Connection credentials are stored in Terraform state. Mitigate by:
- Using dynamic credentials (exec auth) instead of static tokens
- Encrypting remote state (S3 + KMS, Terraform Cloud, etc.) 

*You should probably be doing these things regardless.*


All `cluster` fields are marked sensitive and won't appear in logs or plan output.

---

## Resources & Data Sources

**Resources:**
- `k8sconnect_object` - Full lifecycle management for any Kubernetes resource ([docs](docs/resources/manifest.md))
- `k8sconnect_wait` - Wait for resources to reach desired state with extractable results ([docs](docs/resources/wait.md))
- `k8sconnect_patch` - Surgical modifications to existing resources ([docs](docs/resources/patch.md))

**Data Sources:**
- `k8sconnect_yaml_split` - Parse multi-document YAML files ([docs](docs/data-sources/yaml_split.md))
- `k8sconnect_yaml_scoped` - Filter resources by category ([docs](docs/data-sources/yaml_scoped.md))
- `k8sconnect_object` - Read existing cluster resources ([docs](docs/data-sources/resource.md))

**→ [Browse all 16 runnable examples](examples/README.md)** with test coverage

---

## Requirements
- Terraform >= 1.0.11
- Kubernetes >= 1.28

> **Note**: While the provider may function on older versions, only Kubernetes versions currently receiving security updates are tested and supported.

---

## Installation

### From Terraform Registry
```hcl
terraform {
  required_providers {
    k8sconnect = {
      source  = "jmorris0x0/k8sconnect"
      version = ">= 0.1.2"
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

