# Deferred & Offline Diff Strategy (`DEFERRED_DIFF.md`)

> Design notes for handling server-side apply diffs when the target cluster (or CRDs) are  
> **not yet reachable** at plan time, while preserving one‑phase Terraform  
> workflows, avoiding provider‑level taints, and enabling future structured diffs.

---

## 1 Goals

1. **Single‑phase pipeline** – users can `plan` + `apply` a brand‑new cluster and its manifests in one run.  
2. **Meaningful PR review output** even when the cluster is down.  
3. **Accurate SSA diff** automatically resumes once the cluster is reachable.  
4. **No silent overwrites** of unmanaged objects (ownership annotation guard).  
5. **Scalable to field‑level ("structured") diffs** without redesign.  
6. **No taints when connection attributes are *unknown* during plan**  
   (e.g., EKS endpoint or token supplied by a data source that isn't resolved yet).

---

## 2 Terminology

| Term             | Meaning                                                        |
|------------------|----------------------------------------------------------------|
| **SSA**          | Server‑Side Apply (client-go `Apply()` with `ApplyPatchType`). |
| **Server diff**  | Dry-run server-side apply against live cluster state.          |
| **Offline diff** | Any diff produced without contacting the cluster.              |
| **Diff history** | Data persisted in state to enable offline diff (`hash` / `yaml`). |

---

## 3 Provider‑level settings

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

## 4 Runtime decision tree

    PLAN
    └─ probe cluster
       ├─ reachable → server diff
       └─ unreachable
          ├─ diff_mode = server → error
          └─ diff_mode = auto/off
             ├─ diff_history = hash → text diff
             └─ diff_history = yaml → structured offline diff

---

## 5 Implementation details

### 5.1 Hash storage (`diff_history = "hash"`)

    norm  := normalizeYAML(rendered)
    hash  := sha256.Sum256(norm)
    state.LastRenderedHash = hex.EncodeToString(hash[:])

* Offline **text** diff = unified diff of YAML strings when hashes differ.

### 5.2 Full YAML storage (`diff_history = "yaml"`)

    b := normalizeYAML(rendered)
    state.LastRenderedYAML = gzipCompress(b)

* Enables **structured** diff:

    prev  := kyaml.Parse(stateYAML)
    curr  := kyaml.Parse(rendered)
    patch := smd.CreateTwoWayMergePatch(prev, curr)   // sigs.k8s.io/structured-merge-diff
    attrDiff := jsonPatchToTerraform(patch)

### 5.3 Plan output (offline structured diff)

    ~ yaml_body (offline structured diff)
        spec.replicas: "2" => "3"
        metadata.labels["release"]: "v1" => "v2"

### 5.4 State bloat mitigation

* Gzip compression: typical Deployment ≈ 4 KB.  
* 2 000 objects ≈ 8 MB in state.

---

## 6 Ownership annotation guard

    if objectExists && !annotationMatches {
        abort("unmanaged object would be overwritten")
    }

* Annotation key: `k8sinline.hashicorp.com/id`.  
* Guard runs even when first plan deferred diff.

---

## 7 Preventing taints when connection values are unknown (or change)

**Problem**  
In classic providers, if `host`, CA, or exec token is **unknown at plan time**  
– even just because a `data.*` source hasn't resolved yet – Terraform marks every  
resource for replace.

**k8sinline approach**

1. Connection lives in *each resource* (`cluster_connection {}`) – scope is isolated.  
2. Schema uses `UseStateForUnknown` and sets `RequiresReplace(false)` to ensure unknown or changed values do **not** taint.  
3. Deferred diff handles unknowns – no live read, no replace during plan.  
4. At apply, new connection resolves → in‑place SSA patch via client-go.  
5. Annotation guard aborts if the connection now points at a different cluster.

**Plan when `host` unknown**

    ~ k8sinline_manifest.nginx
        (connection change – diff deferred)

**Apply once `host` resolves**

    k8sinline_manifest.nginx: updating in‑place  
    k8sinline_manifest.nginx: no changes (still managed)

---

## 8 Client-go implementation details

### 8.1 Server-side diff via dry-run apply

```go
// Attempt server-side diff
dryRunResult, err := k8sClient.DryRunApply(ctx, desiredObj, ApplyOptions{
    FieldManager: "k8sinline",
    DryRun:       []string{metav1.DryRunAll},
})
if err != nil {
    // Cluster unreachable - defer diff
    return deferredDiff(state, desiredObj)
}

// Compare dry-run result with current state
currentObj, err := k8sClient.Get(ctx, gvr, namespace, name)
if err != nil && !errors.IsNotFound(err) {
    return fmt.Errorf("failed to get current state: %w", err)
}

diff := computeStructuredDiff(currentObj, dryRunResult)
```

### 8.2 Error classification

```go
import "k8s.io/apimachinery/pkg/api/errors"

switch {
case errors.IsTimeout(err), errors.IsServerTimeout(err):
    // Network issue - defer diff
    return deferredDiff(state, desiredObj)
case errors.IsForbidden(err):
    // RBAC issue - fail fast
    return fmt.Errorf("insufficient permissions: %w", err)  
case errors.IsConflict(err):
    // Field manager conflict - surface to user
    return fmt.Errorf("apply conflict (try force=true): %w", err)
}
```

### 8.3 Discovery and GVR resolution

```go
// Map GVK to GVR using discovery
gvk := obj.GroupVersionKind()
resources, err := discoveryClient.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
if err != nil {
    // Discovery failed - cluster likely unreachable
    return deferredDiff(state, obj)
}

gvr := findGVRForKind(resources, gvk.Kind)
```

---

## 9 Roadmap

| Milestone | Deliverable                                                                       |
|-----------|-----------------------------------------------------------------------------------|
| MVP +1    | Hash fallback (`diff_history = hash`), text diff, single fallback warning.        |
| MVP +2    | `diff_history = yaml`, gzip storage, structured‑merge‑diff vendored.              |
| v2        | Optional `diff_engine = "envtest"` – in‑process API server for perfect local SSA. |

---

## 10 Open questions

1. Should `diff_history = yaml` remain opt‑in or become the default?  
2. gzip vs zstd compression for stored YAML?  
3. Per‑resource or global warning when falling back?  
4. **Discovery cache invalidation** – time-based or event-driven?  

---

## 11 References

* Kubernetes structured‑merge‑diff: https://github.com/kubernetes-sigs/structured-merge-diff  
* Client-go dynamic client: https://pkg.go.dev/k8s.io/client-go/dynamic  
* Server-side apply: https://kubernetes.io/docs/reference/using-api/server-side-apply/

