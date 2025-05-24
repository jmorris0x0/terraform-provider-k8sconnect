# terraform‑provider‑k8sinline 🚧 Roadmap

> **Status legend:**  
> **✅ shipped** 🛠 in progress 📝 planned

---

## 🎉 Recent Progress Update

**Major accomplishments since last update:**
- ✅ **Structured Error Handling**: Kubernetes API errors now classified into user-friendly diagnostics
- ✅ **Core CRUD Operations**: All Create/Read/Update/Delete methods working with proper error handling
- ✅ **Comprehensive Testing**: Unit tests + acceptance tests with OIDC e2e setup
- ✅ **Multi-mode connections**: Inline, kubeconfig-file, and kubeconfig-raw all working

**Current status: 8/15 MVP features complete** 🎯

---

## MVP overview (v 0.1.0 target)

| #   | Feature                                      | Status | Notes                                                                                           |
|-----|----------------------------------------------|--------|-------------------------------------------------------------------------------------------------|
| 1   | Client-go Dynamic Client engine (server-side apply)| ✅     | ✅ `K8sClient` interface defined; Dynamic Client + ApplyPatchType; stub and real implementations working. |
| 2   | Real DynamicClient & connection variants     | ✅     | ✅ All connection modes implemented: inline, kubeconfig-file, kubeconfig-raw with context support.   |
| 3   | `Create` method in `manifest.go`             | ✅     | ✅ Full implementation with structured error handling, client injection, server-side apply.  |
| 4   | Write Create-level tests                     | ✅     | ✅ Unit tests with `stubK8sClient` + table-driven tests; TF_ACC e2e tests with OIDC setup.  |
| 5   | Future-proofing & additional notes           | ✅     | ✅ `Update` reuses `Create` logic; interface designed for evolution; field manager support.        |
| 6   | Read & Refresh State                         | ✅     | ✅ Dynamic Client `Get()` with 404→absent handling; structured error classification. |
| 7   | Delete                                       | ✅     | ✅ Dynamic Client `Delete()` with 404 tolerance; proper cleanup and error handling.               |
| 8   | Deferred Diff & Live Diff                    | 📝     | Server-side apply dry-run in plan if reachable; defer to local diff/hash if unreachable.       |
| 9   | Sensitive Attributes & Schema                | ✅     | ✅ All `cluster.*` fields marked sensitive; schema validation for connection modes.                          |
| 10  | RBAC Pre-flight                              | 📝     | Use SelfSubjectAccessReview API to check apply permissions in `Configure()`.                   |
| 11  | Delete Protection                            | 📝     | `delete_protection` attr; abort destroy unless disabled.                                        |
| 12  | Import Support                               | 🛠     | Basic `ImportState` method exists; need full `<ns>/<kind>/<n>` parsing and live object fetch.   |
| 13  | Concurrency Safety & Connection Management   | 📝     | One REST client per cluster; connection pooling; `max_parallel` limit.                         |
| 14  | CI, Security & Licensing                     | 🛠     | OIDC e2e test setup working; need GitHub Actions matrix, checksums/SBOM, security scanning.                    |
| 15  | Acceptance Tests                             | 🛠     | Basic tests working; need `TestAcc*_DeleteProtection`, `*_Import`, multi-cluster scenarios.             |

---

## Detailed MVP work breakdown

### 1. Client-go Dynamic Client engine (server‑side apply) ✅
* Define `K8sClient` interface:
```go    
    type K8sClient interface {
        Apply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) error
        Get(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error)
        Delete(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, options DeleteOptions) error
        DryRunApply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) (*unstructured.Unstructured, error)
        SetFieldManager(name string) K8sClient                    // chainable
        WithServerSide() K8sClient       // toggle server‑side mode
        WithForceConflicts(force bool) K8sClient // handle conflicts
    }
```
* Create `ApplyOptions` and `DeleteOptions` structs:
```go
    type ApplyOptions struct {
        FieldManager   string
        Force          bool
        DryRun         []string
    }
    
    type DeleteOptions struct {
        GracePeriodSeconds *int64
        PropagationPolicy  *metav1.DeletionPropagation
    }
```
* Implement stubs (`stubK8sClient`) that record operations for assertions.
* Unit‑test interface satisfaction:
```go
    var _ K8sClient = (*DynamicK8sClient)(nil)
    var _ K8sClient = (*stubK8sClient)(nil)
```
### 2. Implement real DynamicK8sClient ✅
* Provide constructor with REST config:
```go
    func NewDynamicK8sClient(config *rest.Config) (*DynamicK8sClient, error)
```
* Build REST config from cluster connection:
    - Inline: construct rest.Config from host, CA, exec auth
    - Kubeconfig: use clientcmd to load and build config
    - Support context switching and exec credential plugins
* Early validation in builder:
    - Reject unsupported `Exec.APIVersion`
    - Validate TLS settings and reachability
* Connection management:
    - Cache REST clients by cluster endpoint
    - Reuse dynamic clients across resources

### 3. `Create` method implementation in `manifest.go` ✅
* **Dependency injection**: accept a `K8sClient` instance via resource constructor.
* **Extract & validate** `cluster_connection`:
    1. Inline (`host` + `cluster_ca_certificate` + `exec`)
    2. Kubeconfig file (`kubeconfig_file`)
    3. Raw kubeconfig (`kubeconfig_raw`)
    - Guard against invalid PEM blocks in `cluster_ca_certificate`.
    - Wrap configuration errors with field context.
* **Parse YAML** into `unstructured.Unstructured`:
    - Single document validation
    - Extract GVK for Dynamic Client operations
    - Validate required fields (apiVersion, kind, metadata.name)
* **Server-side apply**:
```go
    client := k8sClient.
        WithServerSide().
        SetFieldManager("k8sinline")

    err := client.Apply(ctx, obj, ApplyOptions{
        FieldManager: "k8sinline",
        Force: false,
    })
    if err != nil {
        resp.Diagnostics.AddError("apply failed", err.Error())
        return
    }
```
* **ID generation**: compute SHA‑256 of normalized `{cluster,namespace,kind,name}`:
```go
    id := fmt.Sprintf("%s/%s/%s/%s", 
        clusterHash, obj.GetNamespace(), 
        obj.GetKind(), obj.GetName())
```
* **Set state**:
```go
    model.ID = types.StringValue(id)
    model.YAMLBody = data.YAMLBody
    resp.State.Set(ctx, &model)
```
### 4. Write Create‑level tests ✅
* **Fake K8sClient**: `stubK8sClient` records operations, returns controlled responses.
* **Table‑driven tests** for inline vs file vs raw modes.
* **Assertions**:
    - Correct GVR extracted from YAML
    - Server-side apply called with proper options
    - Field manager set correctly
    - Error handling for malformed YAML
    - Correct ID generation
* **EnvTest e2e** under `TF_ACC=1` with real Kubernetes API server.

### 5. Future‑proofing & additional notes ✅
* Reuse `Create` logic in `Update` (server-side apply is idempotent).
* Implement `Delete` via Dynamic Client `Delete()`.
* Consider adding conflict resolution strategies.
* Keep apply defaults centralized in `ApplyOptions`.
* Document how to evolve `K8sClient` interface.

### 6. Read & Refresh State ✅
1. Use Dynamic Client `Get()` to fetch current object state.
2. Handle 404 as "absent" and clear Terraform state.
3. Compare server state with desired state for drift detection.
4. Update Terraform state from live object.
5. Unit‑test edge‑cases (missing fields, unknown GVK).

### 7. Delete ✅
1. Call Dynamic Client `Delete()` with proper options.
2. Support `force = true` → set grace period to 0.
3. Handle 404 during delete (already gone).
4. Table‑driven tests including force deletion.

### 8. Deferred Diff & Live Diff (plan‑time enhancement) 📝
1. In `Plan`, attempt dry-run server-side apply.  
   - If reachable → compare dry-run result with current state.  
   - If unreachable → emit "(diff deferred, cluster unreachable)" and store hash.
2. Use structured-merge-diff for accurate field-level comparison.

### 9. Sensitive Attributes & Schema ✅
1. Mark all `cluster.*` fields `Sensitive: true`.
2. Schema validation for required fields per connection mode.

### 10. RBAC Pre‑flight (in `Configure()`) 📝
1. Use `SelfSubjectAccessReview` API to check permissions:
```go  
    auth.SelfSubjectAccessReview{
        Spec: auth.SelfSubjectAccessReviewSpec{
            ResourceAttributes: &auth.ResourceAttributes{
                Verb:     "patch",
                Group:    "*",
                Resource: "*",
            },
        },
    }
```
### 11. Delete Protection 📝
1. Add resource attr `delete_protection = true` (default `false`).
2. In `Delete`, abort if protection is enabled.

### 12. Import Support 🛠
1. `Importer` accepts ID format `<namespace>/<kind>/<name>`.
2. Use Dynamic Client to fetch live object → populate state.

### 13. Concurrency Safety & Connection Management 📝
1. Create REST client pool keyed by cluster endpoint.
2. Serialize operations per `(cluster,namespace,kind,name)`.
3. Optional provider attr `max_parallel = 8`.

### 14. CI, Security & Licensing 🛠
1. GitHub Actions matrix for multiple platforms.
2. Build/upload checksums + SBOM.
3. Security scanning and compliance checks.

### 15. Acceptance Tests 🛠
1. Use EnvTest or Kind cluster in CI.
2. Test cases covering all connection modes and operations.
3. Error condition testing (RBAC failures, network issues).

---

## Post‑MVP / future design areas

| Feature                     | Notes / Options                                             | LOE |
|-----------------------------|-------------------------------------------------------------|-----|
| Waiters / readiness         | Use client-go conditions or custom readiness checks        | Med |
| Batch apply optimisation    | Apply multiple objects in single server call               | Low |
| Kustomize render            | `kustomize build` pre‑processor                             | High|
| Structured field‑level diff | Use structured-merge-diff for detailed comparison           | Med |
| Drift‑detection opt‑out     | Document `lifecycle.ignore_changes = ["yaml_body"]`         | Low |
| Custom Resource support     | Enhanced CRD discovery and validation                       | Med |

---

## Architectural Decision Records (ADRs)

| ADR | Decision | Status / Rationale |
|-----|----------|--------------------|
| **ADR‑001** | Use client-go Dynamic Client instead of kubectl | Stable API surface; smaller binary; better error handling. |
| **ADR‑002** | Server‑side apply with ApplyPatchType | Clear ownership model; no three‑way merge complexity. |
| **ADR‑003** | `K8sClient` interface abstraction | Enables testing and future client implementations. |
| **ADR‑004** | Inline credentials accepted but marked Sensitive | Flexibility over state size; documented mitigation. |
| **ADR‑005** | `delete_protection` attribute | Safeguard for production objects. |
| **ADR‑006** | Connection pooling by cluster endpoint | Efficient resource usage and connection reuse. |
| **ADR‑007** | **🆕 Structured error classification** | Map client-go errors to actionable Terraform diagnostics for better UX. |

---

## Open questions 🤔

1. **Connection caching** – cache connections indefinitely or implement TTL?
2. **Conflict resolution** – automatic retry with backoff or manual user intervention?
3. **Discovery caching** – how long to cache GVK mappings for CRDs?
4. **Error categorization** – distinguish between transient and permanent API errors?

---

## Implementation Notes

### Client-go Integration Points

* **REST Config Building**: Use `clientcmd` package for kubeconfig parsing
* **Dynamic Client**: `dynamic.NewForConfig()` for runtime resource operations  
* **Discovery**: Use `discovery.NewDiscoveryClientForConfig()` for GVK resolution
* **Exec Auth**: Built-in support via `rest.Config.ExecProvider`
* **Server-side Apply**: `ApplyPatchType` with proper field manager

### Error Handling Strategy ✅

* **Network errors**: Retry with exponential backoff
* **RBAC errors**: Fail fast with clear permission requirements
* **Conflict errors**: Surface to user with resolution options
* **API errors**: Map to appropriate Terraform diagnostics
* **🆕 Structured classification**: NotFound, Forbidden, Conflict, Timeout, Unauthorized, Invalid, AlreadyExists with context-aware messaging

### Testing Strategy ✅

* **Unit tests**: Mock Dynamic Client interface
* **Integration tests**: Use EnvTest for realistic API interactions  
* **E2E tests**: Full provider tests against Kind clusters with OIDC
* **Property-based tests**: Generate random valid YAML for edge cases

---

## 🎯 Recommended Next Steps

Based on current progress (8/15 features complete), the highest-value next priorities are:

1. **Delete Protection** (📝 → 🛠) - 1-2 days, high safety value
2. **Enhanced Import Support** (🛠 → ✅) - 2-3 days, critical for adoption
3. **Deferred Diff & Live Diff** (📝 → 🛠) - 1-2 weeks, enables single-phase pipelines

