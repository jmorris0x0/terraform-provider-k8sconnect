# terraform-provider-k8sinline

A Terraform provider for applying Kubernetes YAML manifests **with inline, per‑resource connection settings**.

Traditional providers force cluster configuration into the provider block; **k8sinline** pushes it down into each resource, freeing you to target *any* cluster from *any* module without aliases, workspaces, or wrapper hacks.

---

## Why `k8sinline`

| Pain point                            | Conventional providers                                                      | **`k8sinline`**                                                             |
| ------------------------------------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Multi‑phase apply requirement         | ❌ Requires staging: infra apply, then manifest apply                        | ✅ All resources in one plan — no phase split                                |
| Cluster‑first dependency hell         | ❌ Providers require the cluster to exist at plan time                       | ✅ Connections defer auth resolution to apply time                           |
| Multi‑cluster support                 | ❌ Requires provider aliases or separate states per cluster                  | ✅ Inline connection per resource — all clusters in one plan                 |

> **In short:** if you've ever copy‑pasted the same manifest into five workspaces just to hit five clusters, this provider removes that overhead.
> `k8sinline` ends the chicken‑and‑egg problem. Clusters and manifests live in a **single plan** — no staged applies, no token hacks, no wrappers.

---

## Getting Started

```hcl
    terraform {
      required_providers {
        k8sinline = {
          source  = "jmorris0x0/k8sinline"
          version = ">= 0.1.0"
        }
      }
    }

    provider "k8sinline" {}

    resource "k8sinline_manifest" "nginx" {
      yaml = file("${path.module}/manifests/nginx.yaml")

      # inline connection (all attrs are Sensitive)
      cluster {
        server      = var.cluster_endpoint
        certificate = var.cluster_ca
        token       = var.cluster_token
      }

      delete_protection = true
    }
```
---

## Importing Existing Resources

The k8sinline provider supports importing existing Kubernetes resources using standard Terraform import commands. The import process uses your **KUBECONFIG environment variable** and requires specifying the kubeconfig context in the import ID.

### Import ID Format

**Namespaced resources:** `<context>/<namespace>/<kind>/<name>`  
**Cluster-scoped resources:** `<context>/<kind>/<name>`

### Examples

```bash
# Set your kubeconfig (if not already set)
export KUBECONFIG=~/.kube/config

# Import a namespaced resource (Pod)
terraform import k8sinline_manifest.nginx "prod/default/Pod/nginx-deployment-abc123"

# Import a cluster-scoped resource (Namespace)  
terraform import k8sinline_manifest.my_ns "prod/Namespace/my-namespace"

# Import from different contexts
terraform import k8sinline_manifest.staging_app "staging/kube-system/Service/coredns"
terraform import k8sinline_manifest.dev_role "dev/ClusterRole/admin"
```

### Import Process

1. **Set KUBECONFIG**: Ensure your `KUBECONFIG` environment variable points to a valid kubeconfig file
2. **Find your context**: Use `kubectl config get-contexts` to see available contexts
3. **Import the resource**: Use the format `<context>/<namespace>/<kind>/<name>`
4. **Configure connection**: After import, add a `cluster_connection` block to your resource configuration

### Post-Import Configuration

After importing, you **must** configure the `cluster_connection` block in your Terraform configuration:

```hcl
resource "k8sinline_manifest" "nginx" {
  yaml_body = "# Populated by import"
  
  # Choose your preferred connection method for normal operations
  cluster_connection {
    # Option 1: Use the same kubeconfig
    kubeconfig_file = "~/.kube/config"
    context         = "prod"
  }
  
  # OR Option 2: Use inline connection for dynamic credentials
  # cluster_connection {
  #   host                   = var.cluster_endpoint
  #   cluster_ca_certificate = var.cluster_ca
  #   exec = {
  #     api_version = "client.authentication.k8s.io/v1"
  #     command     = "aws"
  #     args        = ["eks", "get-token", "--cluster-name", "prod"]
  #   }
  # }
}
```

### Import with Terraform Import Blocks

The new Terraform import blocks also work seamlessly:

```hcl
# Define your resource configuration first
resource "k8sinline_manifest" "nginx" {
  yaml_body = "# Will be populated during import"
  
  cluster_connection {
    kubeconfig_file = "~/.kube/config"
    context         = "prod"
  }
}

# Declare the import
import {
  to = k8sinline_manifest.nginx  
  id = "prod/default/Pod/nginx-deployment-abc123"
}
```

### Requirements for Import

- **KUBECONFIG**: Environment variable must be set or `~/.kube/config` must exist
- **Valid Context**: The context specified in the import ID must exist in your kubeconfig
- **Cluster Access**: You must have read permissions for the resource you're importing
- **Resource Exists**: The Kubernetes resource must exist in the specified cluster/namespace

### Troubleshooting Import

**Context not found:**
```bash
kubectl config get-contexts
```

**Resource not found:**
```bash
kubectl get <kind> <name> -n <namespace> --context <context>
```

**Permission issues:**
```bash
kubectl auth can-i get <resource> --context <context>
```

### Import vs Runtime Connections

- **Import**: Uses KUBECONFIG environment variable (one-time operation)
- **Runtime**: Uses the `cluster_connection` block in your Terraform configuration
- **Flexibility**: After import, you can configure any connection method for normal Terraform operations

## Security caveats 🔐  

Storing cluster credentials in the resource body means they **land in your Terraform
state file**. Mitigate by:

* Encrypting remote state (S3 + KMS, Terraform Cloud, etc.).
* Supplying the sensitive values via Vault/Secrets Manager data sources so they never
  appear in plaintext HCL.
* Rotating or redacting historical state snapshots.

All `cluster.*` attributes are flagged **`Sensitive: true`** so they are redacted
in CLI output and logs, but the bytes still exist in the state blob.

---

## RBAC pre‑flight check ⚙️  

During resource operations, the provider validates that the configured credentials have sufficient permissions for server-side apply operations against the target resources.

If permissions are insufficient, Terraform aborts with a clear error message indicating the missing RBAC permissions.

---

## Delete protection 🛑  

Add `delete_protection = true` to any `k8sinline_manifest`.  
Terraform will refuse to destroy the object unless you set the flag to
`false` first. Use this for databases, CRDs and other critical resources.

---

## Requirements

| Component      | Minimum version | Notes                                                                                                                                                                                                                 |
| -------------- | --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Terraform      | 1.6+            | Tested on Terraform 1.6 and 1.7                                                                                                                                                                                       |
| Kubernetes     | 1.17            | This resource uses Server-side Apply to carry out apply operations.|
| Execution host | N/A             | Compatible with any environment that can run Terraform, including Terraform Cloud, GitHub Actions, and other CI/CD platforms                                                                                          |

---

## Limitations & Caveats

* **CRD ordering** – Server-side apply fails if a resource refers to a CRD that is not yet registered. Use `depends_on` or split your plan to avoid race conditions.
* **Parallelism safety** – The provider serializes operations on `(cluster,namespace,kind,name)` to prevent races **within a single plan**. However, concurrent `terraform apply` runs may still overwrite each other. Use state locking or serialized workflows for cross-run safety.
* **Policy engines** – Because connection settings live inside the resource, Sentinel or OPA rules that introspect *provider blocks* will not see them.
* **Hash-based diff** – Plan output shows full manifest replacement when `yaml_body` changes; Terraform does not show line-by-line diffs (yet).
* **Ownership annotation guard** – Every object applied by `k8sinline` receives `metadata.annotations["k8sinline.hashicorp.com/id"]` set to the Terraform resource ID. If an object **already exists** without that annotation, the provider aborts the operation to avoid unintentionally overwriting resources it does not own. This guard works even when connection attributes are unknown at plan time, eliminating the silent‑overwrite risk while preserving single‑phase pipelines.

## Installation

*Coming soon to the Terraform Registry.*

Until then:

```bash
git clone https://github.com/jmorris0x0/terraform-provider-k8sinline.git
cd terraform-provider-k8sinline
make install
```

---

## Resource: `k8sinline_manifest`

The resource applies **one** Kubernetes YAML document to a target cluster.
Multi‑document YAML files must be split upstream.

All cluster credentials are provided via a **required** `cluster_connection {}` block, which supports exactly **one** of three mutually exclusive modes.

### Cluster Connection Modes

| Mode              | Required fields                               | Notes                                                |
| ----------------- | --------------------------------------------- | ---------------------------------------------------- |
| `inline`          | `host`, `cluster_ca_certificate`, and  `exec` | Direct connection info; best for dynamic credentials |
| `kubeconfig_file` | `kubeconfig_file`                             | Loads config from file at plan time                  |
| `kubeconfig_raw`  | `kubeconfig_raw`                              | Loads config from string (CI‑friendly)               |

The `context` field may optionally be set when using `kubeconfig_file` or `kubeconfig_raw`.

---

### Arguments

| Name                 | Type    | Required | Notes                                                      |
| -------------------- | ------- | -------- | ---------------------------------------------------------- |
| `yaml_body`          | string  | Yes      | UTF‑8, single YAML document. Multi‑doc files will fail.    |
| `cluster_connection` | block   | Yes      | Contains connection info. Exactly one mode must be chosen. |
| `delete_protection`  | boolean | No       | When enabled, prevents Terraform from deleting this resource. Must be disabled before destruction. Defaults to false. |

---

### `cluster_connection` Block Arguments

| Field                    | Type   | Required | Mode              | Notes                                           |
| ------------------------ | ------ | -------- | ----------------- | ----------------------------------------------- |
| `host`                   | string | No       | `inline`          | Kubernetes API URL                              |
| `cluster_ca_certificate` | string | No       | `inline`          | PEM‑encoded CA bundle                           |
| `exec`                   | object | No       | `inline`          | Credential exec block                           |
| `kubeconfig_file`        | string | No       | `kubeconfig_file` | Path to existing kubeconfig                     |
| `kubeconfig_raw`         | string | No       | `kubeconfig_raw`  | Raw kubeconfig YAML as a string                 |
| `context`                | string | Optional | `kubeconfig_*`    | Overrides default context when using kubeconfig |

---

### `exec` Sub-block (inline mode only)

The `exec` block is a typed object, not an open map — only the following fields are allowed:

| Field         | Type         | Required | Notes                                         |
| ------------- | ------------ | -------- | --------------------------------------------- |
| `api_version` | string       | Yes      | e.g. `client.authentication.k8s.io/v1`        |
| `command`     | string       | Yes      | e.g. `aws`                                    |
| `args`        | list(string) | Optional | Marked sensitive; passed as command-line args |

---

### Sensitive Field Detection

All fields in `cluster_connection` are marked `sensitive`, including:

* `host`
* `cluster_ca_certificate`
* `context`
* `exec.args`
* `kubeconfig_raw`

This ensures no cluster information is leaked in plan output or logs.

---

## Provider Setup

To use `k8sinline`, include the provider block in your root module:

```hcl
terraform {
  required_providers {
    k8sinline = {
      source  = "jmorris0x0/k8sinline"
      version = "0.1.0"
    }
  }
}

provider "k8sinline" {}
```

## Usage Examples

### 1. Inline `cluster_connection` with `exec` (AWS EKS)

```hcl
provider "k8sinline" {}

data "aws_eks_cluster" "this" {
  name = var.cluster_name
}

resource "k8sinline_manifest" "eks" {
  yaml_body = file("deployment.yaml")

  cluster_connection {
    host                   = data.aws_eks_cluster.this.endpoint
    cluster_ca_certificate = base64decode(data.aws_eks_cluster.this.certificate_authority[0].data)

    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", var.cluster_name]
    }
  }
}
```

### 2. Load kubeconfig from file

```hcl
provider "k8sinline" {}

resource "k8sinline_manifest" "filecfg" {
  yaml_body = file("deployment.yaml")

  cluster_connection {
    kubeconfig_file = "${path.module}/kubeconfig.yaml"
    context         = "it1"
  }
}
```

### 3. Load kubeconfig as raw bytes

```hcl
provider "k8sinline" {}

resource "k8sinline_manifest" "rawcfg" {
  yaml_body = file("deployment.yaml")

  cluster_connection {
    kubeconfig_raw = file("${path.module}/kubeconfig.yaml")
    context        = "it1"
  }
}
```
---

## Security Considerations

* Sensitive fields are automatically masked
* yaml\_body is stored as a hash in state, not plaintext, to reduce noise and protect sensitive content
* TLS verification is always enforced (no `insecure_skip_tls_verify`)
* Connection details (e.g. `kubeconfig_raw`) are marked `sensitive` and not shown in CLI output, but they **are still stored in Terraform state**. Evaluate whether this fits your security model.
* **Ownership annotation guard** prevents accidental overwrites of unmanaged objects, even when live diffing is deferred.

---

## Example: Module usage

```hcl
provider "k8sinline" {}

module "frontend" {
  source = "./modules/k8s_manifest"

  manifests = [
    {
      yaml_body = file("ns-frontend.yaml")

      cluster_connection = {
        host                   = data.aws_eks_cluster.prod.endpoint
        cluster_ca_certificate = base64decode(data.aws_eks_cluster.prod.certificate_authority[0].data)

        exec = {
          api_version = "client.authentication.k8s.io/v1"
          command     = "aws"
          args        = [
            "eks",
            "get-token",
            "--cluster-name", "prod"
          ]
        }
      }
    },
    {
      yaml_body = file("ingress.yaml")

      cluster_connection = {
        kubeconfig_raw = aws_ssm_parameter.prod_kubeconfig.value
        context        = "prod"
      }
    }
  ]
}
```

---

## Decision Log & Open Questions (internal, non‑spec)

### Decisions made

* **Uses client-go Dynamic Client** — leverages the stable client-go APIs for server-side apply operations with ApplyPatchType, ensuring compatibility with all Kubernetes versions and reducing binary size.
* **Diff strategy:** Server-side apply dry-run is used to perform accurate, server-side diffs without implementing merge-patch logic.
* **Concurrency:** Resources are serialized by `(cluster,namespace,name,kind)` to prevent apply-time race conditions. Parallelism may be user-configurable in future.
* **Field naming** follows K8s REST / exec‑auth spec verbatim.
* **Namespace handling** stays in `yaml_body`; provider does not add implicit namespaces.
* **TLS verification** must pass; skip‑verify will not be supported.
* **Implement `lifecycle { replace_triggered_by = [yaml_body] }` (requires Terraform ≥ 1.6).**
* **Sensitive defaults** for `exec.args`, `kubeconfig_raw`.
* **Validation**: UTF‑8, single‑doc; parsed during `Validate()`.
* **Checksum tag**: provider stores last‑applied SHA‑256, *not* the original `kubectl` annotation.
* **Comparison table** added to "Why" section for quick salesmanship.
* **Single-process concurrency safety** is built in. The provider serializes resource operations by `(cluster,namespace,kind,name)` to prevent apply-time collisions from multiple resources targeting the same object within a single plan.
* **Cross-process locking is not supported**. Users must avoid running concurrent `terraform apply` operations that target the same cluster and object set.

---

## Post-MVP / Future Design Areas

| Topic                       | Notes / Options                                                                                                           | LOE  |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------- | ---- |
| **Waiters / readiness**     | Expose `wait_for = ["condition:Available", "generationObserved"]` for CRDs                                                | Med  |
| **Import support**          | Syntax: `<cluster-hash>/<namespace>/<kind>/<name>`                                                                        | Med  |
| **Delete protection**       | Skip destroy if already missing; useful for GitOps parity                                                                 | Low  |
| **Drift‑detection opt‑out** | Support `lifecycle.ignore_changes = ["yaml_body"]`                                                                        | Low  |
| **Multi-doc YAML support**  | Use sigs.k8s.io/kustomize/kyaml to loop over yaml\_body                                                                   | High |
| **Structured diff output**  | Replace current hash-only behavior with field-level diffing via server-side apply dry-run and structured-merge-diff      | Med  |
| **Testing matrix**          | Cover ≥ 4 K8s versions and ≥ 4 auth flows                                                                                 | Low  |

---

## Legal

This project is licensed under the [Apache 2.0 License](./LICENSE).

This project is not affiliated with or endorsed by the Kubernetes project or the Cloud Native Computing Foundation.

