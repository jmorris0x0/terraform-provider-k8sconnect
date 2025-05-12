# Deferred & Offline Diff Strategy (`DEFERRED_DIFF.md`)

> Design notes for handling `kubectl diff` when the target cluster (or CRDs) are  
> **not yet reachable** at plan time, while preserving one‑phase Terraform  
> workflows, avoiding provider‑level taints, and enabling future structured diffs.

---

## 1 Goals

1. **Single‑phase pipeline** – users can `plan` + `apply` a brand‑new cluster and its manifests in one run.  
2. **Meaningful PR review output** even when the cluster is down.  
3. **Accurate SSA diff** automatically resumes once the cluster is reachable.  
4. **No silent overwrites** of unmanaged objects (ownership annotation guard).  
5. **Scalable to field‑level (“structured”) diffs** without redesign.  
6. **No mass taints when connection values change** (e.g., EKS endpoint rotation).

---

## 2 Terminology

| Term             | Meaning                                                        |
|------------------|----------------------------------------------------------------|
| **SSA**          | Server‑Side Apply (`kubectl apply --server-side`).             |
| **Server diff**  | `kubectl diff --server-side` against live cluster state.       |
| **Offline diff** | Any diff produced without contacting the cluster.              |
| **Diff history** | Data persisted in state to enable offline diff (`hash` / `yaml`). |

---

## 3 Provider‑level settings

    provider "k8sinline" {
      diff_mode    = "auto"   # auto | server | off
      diff_history = "hash"   # hash | yaml | none
    }

* **diff_mode**  
  * `auto` (default) – attempt server diff; fallback if unreachable.  
  * `server` – fail plan when cluster unreachable.  
  * `off` – never probe the cluster.  

* **diff_history**  
  * `hash` (default) – store SHA‑256 of rendered YAML.  
  * `yaml` – store full rendered YAML (gzip‑compressed).  
  * `none` – store nothing.

---

## 4 Runtime decision tree

    PLAN
    └─ probe cluster
       ├─ reachable → server diff
       └─ unreachable
          ├─ diff_mode = server → error
          └─ diff_mode = auto/off
             ├─ diff_history = hash → text diff
             └─ diff_history = yaml → structured offline diff

---

## 5 Implementation details

### 5.1 Hash storage (`diff_history = "hash"`)

    norm  := normalizeYAML(rendered)
    hash  := sha256.Sum256(norm)
    state.LastRenderedHash = hex.EncodeToString(hash[:])

* Offline **text** diff = unified diff of YAML strings when hashes differ.

### 5.2 Full YAML storage (`diff_history = "yaml"`)

    b := normalizeYAML(rendered)
    state.LastRenderedYAML = gzipCompress(b)

* Enables **structured** diff:

    prev  := kyaml.Parse(stateYAML)
    curr  := kyaml.Parse(rendered)
    patch := smd.CreateTwoWayMergePatch(prev, curr)   // sigs.k8s.io/structured-merge-diff
    attrDiff := jsonPatchToTerraform(patch)

### 5.3 Plan output (offline structured diff)

    ~ yaml_body (offline structured diff)
        spec.replicas: "2" => "3"
        metadata.labels["release"]: "v1" => "v2"

### 5.4 State bloat mitigation

* Gzip compression: typical Deployment ≈ 4 KB.  
* 2 000 objects ≈ 8 MB in state.

---

## 6 Ownership annotation guard

    if objectExists && !annotationMatches {
        abort("unmanaged object would be overwritten")
    }

* Annotation key: `k8sinline.hashicorp.com/id`.  
* Guard runs even when first plan deferred diff.

---

## 7 Preventing mass taints on connection changes

**Problem**  
Traditional providers store connection in the *provider* block; any change (endpoint, CA, exec token) taints every resource → delete/recreate.

**k8sinline approach**

1. Connection lives in *each resource* (`cluster_connection {}`) – scope is isolated.  
2. Schema uses `UseStateForUnknown` and sets `RequiresReplace(false)` to ensure unknown or changed values do **not** taint.  
3. Deferred diff handles unknowns – no live read, no replace during plan.  
4. At apply, new connection resolves → in‑place SSA patch.  
5. Annotation guard aborts if the connection now points at a different cluster.

**Plan when `host` unknown**

    ~ k8sinline_manifest.nginx
        (connection change – diff deferred)

**Apply once `host` resolves**

    k8sinline_manifest.nginx: updating in‑place  
    k8sinline_manifest.nginx: no changes (still managed)

---

## 8 Roadmap

| Milestone | Deliverable                                                                       |
|-----------|-----------------------------------------------------------------------------------|
| MVP +1    | Hash fallback (`diff_history = hash`), text diff, single fallback warning.        |
| MVP +2    | `diff_history = yaml`, gzip storage, structured‑merge‑diff vendored.              |
| v2        | Optional `diff_engine = "envtest"` – in‑process API server for perfect local SSA. |

---

## 9 Open questions

1. Should `diff_history = yaml` remain opt‑in or become the default?  
2. gzip vs zstd compression for stored YAML?  
3. Per‑resource or global warning when falling back?

---

## 10 References

* Kubernetes structured‑merge‑diff: https://github.com/kubernetes-sigs/structured-merge-diff  
* Helm diff plugin, Terraform Helm provider, Pulumi Kubernetes provider.


