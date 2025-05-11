# terraform-provider-k8sinline

A Terraform provider for applying Kubernetes manifests **with inline, per‑resource connection settings**.

Traditional providers force cluster configuration into the provider block; **k8sinline** pushes it down into each resource, freeing you to target *any* cluster from *any* module without aliases, workspaces, or wrapper hacks.

---

## Why `k8sinline`

| Pain point                            | Conventional providers                                                      | **`k8sinline`**                                                             |
| ------------------------------------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Multi‑phase apply requirement         | ❌ Requires staging: infra apply, then manifest apply                        | ✅ All resources in one plan — no phase split                                |
| Cluster‑first dependency hell         | ❌ Providers require the cluster to exist at plan time                       | ✅ Connections defer auth resolution to apply time                           |
| Multi‑cluster support                 | ❌ Requires provider aliases or separate states per cluster                  | ✅ Inline connection per resource — all clusters in one plan                 |
| Plan-time unknown inputs cause taints | ❌ Provider taints resources when connection values are unknown at plan time | ✅ No tainting — deferred diffing skips live reads and lets the plan proceed |

> **In short:** if you’ve ever copy‑pasted the same manifest into five workspaces just to hit five clusters, this provider removes that overhead.
> `k8sinline` ends the chicken‑and‑egg problem. Clusters and manifests live in a **single plan** — no staged applies, no token hacks, no wrappers.

---

## Requirements

| Component      | Minimum version | Notes                                                                                                                                                                                                                 |
| -------------- | --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Terraform      | 1.6+            | Tested on Terraform 1.6 and 1.7                                                                                                                                                                                       |
| Kubectl        | 1.27            | Must be in `$PATH` on the machine running `terraform apply`                                                                                                                                                           |
| Execution host | N/A             | Must be a self-hosted runner or CI agent **with `kubectl` installed**.<br/>Compatible with GitHub Actions and other runners **if** `kubectl` is available or installed. Not compatible with Terraform Cloud (hosted). |

---

## Limitations & Caveats

* **CRD ordering** – `kubectl apply` fails if a resource refers to a CRD that is not yet registered. Use `depends_on` or split your plan to avoid race conditions.
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

## How Deferred Diffing Works

> `k8sinline` supports live diffing *only when every field in `cluster_connection` is known at plan time.*
> If any field is `unknown`, the provider skips live reads to avoid destroying unrelated resources.

When any `cluster_connection` field is *unknown* on the first run, `k8sinline` will:

1. Plan the manifest for **create** (no live diff yet)
2. Leave every other manifest alone
3. Store the final connection in state so **future** plans get full server‑side diffing
4. Require no extra pipeline stages
5. Protect existing cluster objects via the **ownership annotation guard** described above — if the target object already exists and is not annotated as managed by this Terraform state, the provider aborts.

#### First‑apply adoption of existing objects

When any `cluster_connection` field is **unknown** at plan time the resource is shown as `create`.
During **apply** the provider:

1. Resolves the final connection and runs `kubectl get <kind>/<name> -n <namespace> -o json`.
2. **If the object exists *and* contains&#x20;
   `metadata.annotations["k8sinline.hashicorp.com/id"]` that matches the&#x20;
   Terraform resource ID,** the provider *adopts* the object instead of&#x20;
   re‑applying it. State is populated from the live object and the plan&#x20;
   becomes clean on the next run.
3. **If the object exists without the annotation** the provider aborts with&#x20;
   an error, explaining that the object is unmanaged and would have been&#x20;
   overwritten.
4. **If the object is missing** the provider proceeds with `kubectl apply`.

This behavior prevents “accidental taint” while still blocking silent
overwrites.  Users will see `Creating… adopted existing object` in the
CLI output the first time the resource runs.

### Destroy‑cascade blocker

* Skip live diff if `cluster_connection` is unknown (first plan)
* Affect **only** the manifest being created — siblings stay untouched
* Enable drift detection on the very next plan once cluster outputs are known
* Abort before modification if the object is un‑annotated and therefore unmanaged

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

| Name                 | Type   | Required | Notes                                                      |
| -------------------- | ------ | -------- | ---------------------------------------------------------- |
| `yaml_body`          | string | Yes      | UTF‑8, single YAML document. Multi‑doc files will fail.    |
| `cluster_connection` | block  | Yes      | Contains connection info. Exactly one mode must be chosen. |

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
| `api_version` | string       | Yes      | e.g. `client.authentication.k8s.io/v1beta1`   |
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

```
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
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", var.cluster_name]
    }
  }
}
```

### 2. Load kubeconfig from file

```
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

```
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

## Plan vs Apply Matrix

Live diffing is supported only when **all** connection fields are known at plan time. If any field is `unknown`, the provider defers the diff, plans a create, and skips the live read.

| Input Type            | Live Diff at Plan?          | Required at Apply? |
| --------------------- | --------------------------- | ------------------ |
| `inline` fields       | ✅ If all values are known   | ✅                  |
| `kubeconfig_file`     | ✅ Always                    | ✅                  |
| `kubeconfig_raw`      | ✅ If value is known         | ✅                  |
| **Any unknown field** | ❌ Defers diff, plans create | ✅                  |

On the next plan (when all fields are known), full server-side drift detection is re-enabled automatically.

---

## Security Considerations

* Sensitive fields are automatically masked
* yaml\_body is stored as a hash in state, not plaintext, to reduce noise and protect sensitive content
* TLS verification is always enforced (no `insecure_skip_tls_verify`)
* Connection details (e.g. `kubeconfig_raw`) are marked `sensitive` and not shown in CLI output, but they **are still stored in Terraform state**. Evaluate whether this fits your security model.
* **Ownership annotation guard** prevents accidental overwrites of unmanaged objects, even when live diffing is deferred.

---

## Example: Module usage

```
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
          api_version = "client.authentication.k8s.io/v1beta1"
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

* **Boot once, diff forever:** the provider must deliver server-side drift detection **without** forcing multi-phase pipelines. If any `cluster_connection` value is unknown on the first plan, the resource defers its live diff (no graph taint) and stores the final connection in state; subsequent plans perform normal server-side diffs.
* **Leverages `kubectl` internally** — rather than reimplementing 10+ years of edge-case logic, the provider delegates diffing and apply operations to `kubectl`, which is installed in most CI/CD environments and guarantees compatibility with CRDs and Kubernetes server behavior.
* **Diff strategy:** `kubectl apply --dry-run=server -o json` is used to perform accurate, server-side diffs without implementing merge-patch logic.
* **Concurrency:** Resources are serialized by `(cluster,namespace,name,kind)` to prevent apply-time race conditions. Parallelism may be user-configurable in future.
* **Destroy bug (data-source → connection):** Solved via **deferred diff**. If any `cluster_connection` field is unknown, diff is skipped and the final connection is persisted in state. No need for a split connection model.
* **Field naming** follows K8s REST / exec‑auth spec verbatim.
* **Namespace handling** stays in `yaml_body`; provider does not add implicit namespaces.
* **TLS verification** must pass; skip‑verify will not be supported.
* **Implement `lifecycle { replace_triggered_by = [yaml_body] }` (requires Terraform ≥ 1.6).**
* **Sensitive defaults** for `exec.args`, `kubeconfig_raw`.
* **Validation**: UTF‑8, single‑doc; parsed during `Validate()`.
* **Checksum tag**: provider stores last‑applied SHA‑256, *not* the original `kubectl` annotation.
* **Comparison table** added to “Why” section for quick salesmanship.
* **Single-process concurrency safety** is built in. The provider serializes resource operations by `(cluster,namespace,kind,name)` to prevent apply-time collisions from multiple resources targeting the same object within a single plan.
* **Cross-process locking is not supported**. Users must avoid running concurrent `terraform apply` operations that target the same cluster and object set.

---

## Post-MVP / Future Design Areas

| Topic                       | Notes / Options                                                                                                           | LOE  |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------- | ---- |
| **Waiters / readiness**     | Expose `wait_for = ["condition:Available", "generationObserved"]` for CRDs                                                | Med  |
| **Import support**          | Syntax: `<cluster-hash>/<namespace>/<kind>/<name>`                                                                        | Med  |
| **Delete protection**       | Skip destroy if already missing; useful for GitOps parity                                                                 | Low  |
| **Preferred API version**   | Allow user to pin preferred `apiVersion` or rely on kubectl discovery                                                     | Low  |
| **Drift‑detection opt‑out** | Support `lifecycle.ignore_changes = ["yaml_body"]`                                                                        | Low  |
| **Multi-doc YAML support**  | Use sigs.k8s.io/kustomize/kyaml to loop over yaml\_body                                                                   | High |
| **Structured diff output**  | Replace current hash-only behavior with field-level diffing via `kubectl apply --dry-run=server -o json` and `json-patch` | Med  |
| **Testing matrix**          | Cover ≥ 4 K8s versions and ≥ 4 auth flows                                                                                 | Low  |
| **Kubectl version check**   | Add a fail-fast check in `Configure()` to ensure required `kubectl` version is available at runtime                       | Low  |

---

## Legal

This project is licensed under the [Apache 2.0 License](./LICENSE).

This project is not affiliated with or endorsed by the Kubernetes project or the Cloud Native Computing Foundation.

