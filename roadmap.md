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
- ✅ **Delete Protection**: Complete with `delete_protection` attribute and comprehensive testing
- ✅ **Import Support**: Full ADR-008 implementation with environment variable strategy and context parsing
- ✅ **Connection Pooling**: Provider-level client caching with deterministic cache keys and connection reuse

**Current status: 11/15 MVP features complete** 🎯

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
| 11  | Delete Protection                            | ✅     | ✅ `delete_protection` attr blocks destroy; comprehensive acceptance tests with enable/disable flow.                                        |
| 12  | Import Support                               | ✅     | ✅ ADR-008 implementation: `<context>/<namespace>/<kind>/<name>` parsing, KUBECONFIG env strategy, excellent error messages.   |
| 13  | Concurrency Safety & Connection Management   | ✅     | ✅ Provider-level client caching with SHA-256 cache keys; connection pooling; dependency injection via ClientGetter.                         |
| 14  | CI, Security & Licensing                     | 🛠     | OIDC e2e test setup working; need GitHub Actions matrix, checksums/SBOM, security scanning.                    |
| 15  | Acceptance Tests                             | 🛠     | Basic tests working; need `TestAcc*_DeleteProtection`, `*_Import`, multi-cluster scenarios.             |

---
### 8. Deferred Diff & Live Diff (plan‑time enhancement) 📝
1. In `Plan`, attempt dry-run server-side apply.  
   - If reachable → compare dry-run result with current state.  
   - If unreachable → emit "(diff deferred, cluster unreachable)" and store hash.
2. Use structured-merge-diff for accurate field-level comparison.

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
| **ADR‑008** | **🆕 Environment Variable Import Strategy** | Import uses KUBECONFIG env var with `<context>/<namespace>/<kind>/<name>` format for standard Terraform UX. |

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

Based on current progress (11/15 features complete), the highest-value next priorities are:

1. **Deferred Diff & Live Diff** (📝 → 🛠) - 1-2 weeks, enables single-phase pipelines  
2. **RBAC Pre-flight** (📝 → 🛠) - 3-4 days, improves error experience
3. **CI, Security & Licensing** (🛠 → ✅) - 1 week, production readiness

