# terraform‑provider‑k8sinline 🚧 Roadmap

> **Status legend:**  
> **✅ shipped** 🛠 in progress 📝 planned

---

## MVP overview (v 0.1.0 target)

| # | Feature | Status | Notes |
|---|---------|--------|-------|
| 1 | **In‑process kubectl engine** | 📝 | Import `k8s.io/kubectl/pkg/cmd/apply` + `diff`, server‑side apply only. |
| 2 | Read & refresh state | 📝 | `kubectl get … -o yaml` → Terraform attributes. |
| 3 | Delete (`kubectl delete`) | 📝 | Handles 404 as success; `force` flag. |
| 4 | Deferred diff & live diff | 📝 | `kubectl diff` when cluster reachable; local fallback TBD. |
| 5 | Sensitive attrs + schema validation | 📝 | All `cluster.*` marked `Sensitive: true`. |
| 6 | RBAC pre‑flight (`auth can‑i`) | 📝 | Fails fast on missing verbs. |
| 7 | `delete_protection` flag | 📝 | Opt‑in safety switch for critical objects. |
| 8 | Importer support | 📝 | `terraform import k8sinline_manifest.this <ns>/<kind>/<name>`. |
| 9 | Concurrency guard | 📝 | Worker pool / temp‑dir isolation to avoid API throttling. |
|10 | CI matrix + SARIF scan + license bundle | 📝 | mac / linux (amd64, arm64); Trivy SARIF badge; Apache 2.0 notice. |
|11 | Basic acceptance test | 📝 | Kind cluster in CI; `TestAccManifest_Basic` green. |

---

## Detailed MVP work breakdown

### 1. In‑process kubectl engine (server‑side apply)
* **Sub‑tasks**
  1. Add `libkubectl.go` that wires `apply.NewApplyOptions` and `diff.NewDiffOptions`.
  2. Wrap behind interface:  
     ```go
     type Kubectl interface {
       Apply(ctx context.Context, yaml []byte) error
       Diff (ctx context.Context, yaml []byte) (string, error)
     }
     ```  
     Implement `LibKubectl` (default) and **optional** `ExecKubectl` (enabled by `K8SINLINE_FORK=1` env) for debugging.
  3. Force `--server-side --field-manager=k8sinline` on every apply.
  4. Unit test: run against `envtest` fake‑apiserver, assert zero fork count.

### 2. Read & refresh state
1. Use `kubectl get <kind> <name> --namespace=<ns> -o yaml`.
2. Parse to `map[string]any` with `sigsyaml` > `json`.
3. Populate Terraform state; treat 404 as absent.
4. Unit tests for parser edge‑cases.

### 3. Delete
1. `kubectl delete <kind> <name> --namespace=<ns>`.
2. Support `force = true` attribute → `--grace-period=0 --force`.
3. Acceptance test: delete + recreate cycle.

### 4. Deferred diff & live diff
1. During `plan`, attempt `Diff(ctx, yaml)`.  
   * If cluster reachable → show server diff in plan.
   * Else → emit “(diff deferred, cluster unreachable)” and store rendered YAML hash in state.
2. On subsequent plans, compute local diff if cluster still unreachable.

### 5. Sensitive attributes & schema
* Mark all `cluster.*` fields `Sensitive: true`.
* Validate `server`, `token`, `certificate` non‑empty with `plan.Check`.

### 6. RBAC pre‑flight
* In `Configure()`, run `kubectl auth can-i apply --server-side -f -`.
* On failure: provider error with verb list required.

### 7. Delete protection
* Resource attr `delete_protection = true` (default `false`).
* Destroy step aborts unless attr toggled off.

### 8. Import support
* `Importer` parses ID `<namespace>/<kind>/<name>`.
* Fetch live YAML → state.

### 9. Concurrency safety
* One temporary dir per resource (`os.MkdirTemp` with UUID).  
* Optional provider attr `max_parallel = 8` to cap workers.

### 10. CI, security & licensing
* GitHub Actions matrix (`darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`).
* Upload checksums + SBOM.  
* Trivy SARIF upload → README badge.  
* Copy Kubernetes Apache 2.0 NOTICE into `LICENSES/`.

### 11. Acceptance tests
* Kind cluster spun in CI with `action‑kind`.  
* `TestAccManifest_Basic`, `*_DeleteProtection`, `*_Import`.

---

## Post‑MVP / future design areas

| Feature                     | Notes / Options                                             | LOE |
|-----------------------------|-------------------------------------------------------------|-----|
| Waiters / readiness         | `kstatus` or exponential poll attribute                     | Med |
| Batch apply optimisation    | Pipe multi‑doc YAML to single ApplyOptions call             | Low |
| Kustomize render            | `kustomize build` pre‑processor                             | High|
| Windows support             | Cross‑compile + GitHub release                              | Low |
| Structured field‑level diff | JSON‑patch pretty print                                     | Med |
| Drift‑detection opt‑out     | Document `lifecycle.ignore_changes = ["yaml_body"]`         | Low |

---

## Architectural Decision Records (ADRs)

| ADR | Decision | Status / Rationale |
|-----|----------|--------------------|
| **ADR‑001** | Import kubectl code instead of per‑resource execs | Removes fork overhead; full kubectl feature set. |
| **ADR‑002** | Server‑side apply only | Clear ownership model; no three‑way merge drift. |
| **ADR‑003** | `Kubectl` interface abstraction | Allows future switch to dynamic client‑go. |
| **ADR‑004** | Inline credentials accepted but marked Sensitive | Flexibility > state size; mitigation documented. |
| **ADR‑005** | `delete_protection` attribute | Safeguard for prod objects. |
| **ADR‑006** | Limit parallelism config | Prevent API rate‑limits in large plans. |

---

## Open questions 🤔

1. **Deferred diff storage** – keep full YAML or only hash + last‑applied?
2. **Waiter coverage** – which resource kinds beyond Deployment/StatefulSet?
3. **Windows binaries** – compile now or wait for user demand?


