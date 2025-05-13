# terraform‑provider‑k8sinline 🚧 Roadmap

> **Status legend:**  
> **✅ shipped** 🛠 in progress 📝 planned

---

## MVP overview (v 0.1.0 target)

| #   | Feature                                      | Status | Notes                                                                                           |
|-----|----------------------------------------------|--------|-------------------------------------------------------------------------------------------------|
| 1   | In-process kubectl engine (server-side apply)| 📝     | Define `Kubectl` interface; import `apply` + `diff`; chainable flags; stub and real implementations. |
| 2   | Real LibKubectl & Exec variants              | 📝     | Constructors `NewLibKubectl`/`NewExecKubectl`; wire up `ApplyOptions`/`DiffOptions`; temp-file handling. |
| 3   | `Create` method in `manifest.go`             | 📝     | DI of `Kubectl`; extract/validate `cluster_connection`; build inline kubeconfig; stream YAML; apply; ID & state. |
| 4   | Write Create-level tests                     | 📝     | `stubKubectl` assertions; table-driven tests for inline/file/raw; golden fixtures; TF_ACC e2e.  |
| 5   | Future-proofing & additional notes           | 📝     | Reuse logic for `Update`; add `Delete`; support `--prune`; document interface evolution.        |
| 6   | Read & Refresh State                         | 📝     | `kubectl get … -o yaml`; parse via `sigsyaml`; 404→absent; populate state; parser edge-case tests. |
| 7   | Delete                                       | 📝     | `kubectl.Delete`; handle 404; `force` flag; delete+recreate cycle tests.                        |
| 8   | Deferred Diff & Live Diff                    | 📝     | `kubectl.Diff` in plan if reachable; defer to local diff/hash if unreachable.                   |
| 9   | Sensitive Attributes & Schema                | 📝     | Mark `cluster.*` sensitive; validate non-empty core fields in schema.                          |
| 10  | RBAC Pre-flight                              | 📝     | Run `kubectl auth can-i apply --server-side -f -` in `Configure()`; fail fast on missing verbs. |
| 11  | Delete Protection                            | 📝     | `delete_protection` attr; abort destroy unless disabled.                                        |
| 12  | Import Support                               | 📝     | `Importer` parses `<ns>/<kind>/<name>`; fetch live YAML; populate state.                        |
| 13  | Concurrency Safety & Temp-Dir Hygiene        | 📝     | One temp dir per resource; cleanup on panic; `max_parallel` limit.                              |
| 14  | CI, Security & Licensing                     | 📝     | GitHub Actions matrix; checksums/SBOM; Trivy SARIF; Apache 2.0 NOTICE in `LICENSES/`.           |
| 15  | Acceptance Tests                             | 📝     | Kind cluster in CI; `TestAccManifest_Basic`, `..._DeleteProtection`, `..._Import`.             |

---

## Detailed MVP work breakdown

### 1. In‑process kubectl engine (server‑side apply)
* Define `Kubectl` interface:
    
    type Kubectl interface {
        Apply(ctx context.Context, yaml []byte) error
        Diff(ctx context.Context, yaml []byte) (string, error)
        Delete(ctx context.Context, yaml []byte) error            // new: delete support
        SetFieldManager(name string) Kubectl                    // chainable
        WithServerSide() Kubectl       // toggle server‑side mode
        WithFlags(flags Flags) Kubectl // apply common flags
        WithTimeout(d time.Duration) Kubectl // new: per‑call timeout
        WithStdin(r io.Reader) Kubectl   // new: stream manifest stdin
    }

* Create `Flags` struct:

    type Flags struct {
        ServerSide     bool
        FieldManager   string
        Namespace      string
        ForceConflicts bool
        KubeconfigPath string   // new: “--kubeconfig”
        Context        string   // new: “--context”
        ExtraArgs      []string // new: passthrough flags (merged last)
    }

* Implement stubs (`stubKubectl`) that record both the command slice and raw stdin bytes into a slice for assertions.
* Bundle common flags in an `ApplyOptions` builder rather than scattering literals.
* Unit‑test interface satisfaction:

    var _ Kubectl = (*LibKubectl)(nil)
    var _ Kubectl = (*ExecKubectl)(nil)
    var _ Kubectl = (*stubKubectl)(nil)

### 2. Implement real LibKubectl and exec variant
* Provide constructor options:

    func NewLibKubectl(opts ...KubectlOption) *LibKubectl
    func NewExecKubectl(opts ...KubectlOption) *ExecKubectl

* Internally wire up `apply.NewApplyOptions` and `diff.NewDiffOptions`.
* Enforce `--server-side` and field‑manager via `Flags` or `WithServerSide()` calls.
* Early validation in builder (before Apply):
    - Reject unsupported `Exec.APIVersion`
    - Reject malformed `Args` or non‑string values
* Temp‑file management:

    f, err := os.CreateTemp("", "kubeconfig-*.yaml")
    if err != nil { return err }
    defer func() {
        f.Close()
        os.Remove(f.Name())
    }()

* Ensure cleanup even on panic or context cancellation (use `defer`).

### 3. `Create` method implementation in `manifest.go`
* **Dependency injection**: accept a `Kubectl` instance via resource constructor.
* **Extract & validate** `cluster_connection`:
    1. Inline (`host` + `cluster_ca_certificate` ± `exec`)
    2. Kubeconfig file (`kubeconfig_file`)
    3. Raw kubeconfig (`kubeconfig_raw`)
    - Guard against empty PEM blocks in `cluster_ca_certificate`.
    - Wrap base64 decode errors with field context.
* **Build inline kubeconfig** via `builder.GenerateKubeconfigFromInline`, returning `(path, cleanup, err)` so you `defer cleanup()`.
* **Handle raw vs file**:
    - Raw: write to temp file with `defer` cleanup.
    - File: validate existence and readability.
* **Stream** `YAMLBody` via `WithStdin(strings.NewReader(data.YAMLBody.ValueString()))` to avoid extra temp files.
* **Invoke**:

    kc := kubectl.
        WithServerSide().
        WithFlags(Flags{
            FieldManager: "k8sinline",
            KubeconfigPath: kubeconfigPath,
            Context: data.ClusterConnection.Context.ValueString(),
        }).
        WithTimeout(30 * time.Second)

    err := kc.Apply(ctx, []byte(data.YAMLBody.ValueString()))
    if err != nil {
        resp.Diagnostics.AddError("apply failed", err.Error())
        return
    }

* **ID generation**: compute SHA‑256 of normalized `kubeconfigBytes || YAMLBody`:

    h := sha256.Sum256(append(kubeconfigBytes, []byte(data.YAMLBody.ValueString())...))
    id := hex.EncodeToString(h[:])

* **Set state**:

    model.ID = types.StringValue(id)
    model.YAMLBody = data.YAMLBody
    resp.State.Set(ctx, &model)

* **Diagnostics**: convert each error into `resp.Diagnostics.AddError("context", err.Error())`, distinguishing user vs system errors.

### 4. Write Create‑level tests
* **Fake Kubectl**: `stubKubectl` records flags, stdin, call count.
* **Table‑driven tests** for inline vs file vs raw modes to catch exclusivity mistakes.
* **Assertions**:
    - Flags passed correctly
    - Stdin content matches `YAMLBody`
    - Diagnostics on invalid input modes
    - Correct ID derived from hash
    - No leftover temp files
* **Golden tests**: compare built `ApplyOptions.Args()` against fixtures.
* **EnvTest e2e** under `TF_ACC=1` for inline+exec path.

### 5. Future‑proofing & additional notes
* Reuse `Create` logic in `Update` (SSA is idempotent).
* Implement `Delete` via `kubectl.Delete(ctx, yaml)`.
* Consider adding `WithPrune(labelSelector)` for `--prune`.
* Keep flag defaults centralized in `Flags` struct.
* Document how to evolve `Kubectl` interface when adding new flags or methods.

### 6. Read & Refresh State
1. Run `kubectl get <kind> <name> --namespace=<ns> -o yaml`.
2. Parse via `sigsyaml` → JSON (`map[string]any`).
3. Treat HTTP 404 as “absent” and clear the state.
4. Populate Terraform state fields from the parsed map.
5. Unit‑test parser edge‑cases (missing fields, unknown types).

### 7. Delete
1. Call `kubectl.Delete(ctx, yaml)` (your `Kubectl.Delete`).
2. Support `force = true` → add `--grace-period=0 --force` in `Flags`.
3. Table‑driven tests on deletion, including force, and a delete+recreate cycle.

### 8. Deferred Diff & Live Diff (plan‑time enhancement)
1. In `Plan`, attempt `kubectl.Diff(ctx, yaml)`.  
   - If reachable → embed server‑side diff in the plan.  
   - If unreachable → emit “(diff deferred, cluster unreachable)” and store a hash of the last applied YAML.
2. On subsequent plans, if still unreachable → compute local diff against stored YAML.

### 9. Sensitive Attributes & Schema
1. Mark all `cluster.*` fields `Sensitive: true`.
2. In schema, validate non‑empty for `host`, `token`, `certificate` via `plan.Check`.

### 10. RBAC Pre‑flight (in `Configure()`)
1. On provider startup, run:
    
        kubectl auth can-i apply --server-side -f -
2. On failure → provider‑level error listing required verbs.

### 11. Delete Protection
1. Add resource attr `delete_protection = true` (default `false`).
2. In `Delete`, abort if `delete_protection` is still on.

### 12. Import Support
1. `Importer` accepts ID format `<namespace>/<kind>/<name>`.
2. Fetch live YAML via `kubectl get ... -o yaml` → populate state.

### 13. Concurrency Safety & Temp‑Dir Hygiene
1. Create one temp directory per resource with `os.MkdirTemp("", "<uuid>")`.
2. Write all temp files (kubeconfig, raw YAML) into that dir.
3. `defer` a cleanup func that removes the entire dir, even on panic or cancellation.
4. Optional provider attr `max_parallel = 8` to cap simultaneous operations.

### 14. CI, Security & Licensing
1. GitHub Actions matrix for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`.
2. Build/upload checksums + SBOM; run Trivy and upload SARIF → badge in README.
3. Copy Kubernetes Apache 2.0 NOTICE into a `LICENSES/` folder.

### 15. Acceptance Tests
1. Spin up a Kind cluster in CI (`action-kind`).
2. Test cases:
   - `TestAccManifest_Basic`
   - `TestAccManifest_DeleteProtection`
   - `TestAccManifest_Import`
3. Cover inline, file, raw and exec‑auth scenarios under `TF_ACC=1`.

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


