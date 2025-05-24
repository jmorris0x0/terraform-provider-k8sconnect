# k8sinline Provider – Client-go Dynamic Client Engine Notes  
_Last updated: 2025‑05‑23_

## 0 · Summary of architectural decision

**Key change:** Moved from kubectl in-process integration to **client-go Dynamic Client** approach.

* **Eliminated headline risks**  
  1. ~~Fragile internal `kubectl` APIs~~ → **Stable client-go APIs with compatibility guarantees**  
  2. ~~Binary size and dependency bloat~~ → **Smaller binary, fewer dependencies**  
  3. ~~CLI-specific complexity~~ → **Clean programmatic API**  
* **Remaining considerations**  
  * Terraform UX friction from deferred / server‑diff (unchanged)  
  * Inline kube‑configs in state files (unchanged)  
* **New benefits**  
  * Better error handling and structured responses  
  * Native Go integration without CLI abstractions  
  * Terraform Cloud compatibility (no external binaries needed)  

---

## 1 · Purpose  
Capture design decisions, risks, and tasks for using **client-go Dynamic Client** with server-side apply.

---

## 2 · Core client-go components  

| Component | Package | Purpose |
|-----------|---------|---------|
| **Dynamic Client** | `k8s.io/client-go/dynamic` | Runtime resource operations without generated types |
| **Discovery Client** | `k8s.io/client-go/discovery` | GVK → GVR mapping and API resource discovery |
| **REST Config** | `k8s.io/client-go/rest` | Connection configuration and authentication |
| **Clientcmd** | `k8s.io/client-go/tools/clientcmd` | Kubeconfig parsing and context handling |
| **Unstructured** | `k8s.io/apimachinery/pkg/apis/meta/v1/unstructured` | Type-safe handling of arbitrary YAML |

No kubectl code vendoring required.

---

## 3 · Version compatibility strategy  
* **Client-go has strong compatibility guarantees** – can connect to clusters ±1 minor version  
* Track Kubernetes minors conservatively: target K8s N-1 for maximum compatibility  
* CI tests against multiple K8s versions (1.31, 1.32, 1.33)  
* `go mod` updates only require client-go, not kubectl ecosystem  

---

## 4 · Memory and concurrency model  
* **One REST client per cluster endpoint** (cached by host+ca hash)  
* **Discovery cache** kept in-process with TTL refresh  
* **10 concurrent operations** max to avoid API server saturation  
* **Connection pooling** via client-go's built-in HTTP transport reuse  

---

## 5 · Server-side apply implementation  
1. Parse YAML → `unstructured.Unstructured`  
2. Discover GVR using `DiscoveryClient.ServerResourcesForGroupVersion()`  
3. Apply via `DynamicClient.Resource(gvr).Apply()` with `ApplyOptions`  
4. Handle conflicts with `Force: true` if configured  

Dry-run diff via `ApplyOptions{DryRun: []string{"All"}}`.

---

## 6 · Security and compliance  
* **Client-go is officially supported** → established security practices  
* Generate SBOM for all dependencies:  

      syft dir:. --scope all-layers -o cyclonedx-json > sbom.json

* **No kubectl binaries** → reduced attack surface  
* Mark `cluster.*` attributes **Sensitive**; recommend encrypted remote state  

---

## 7 · Release workflow (CI)  
1. Matrix build: linux/amd64, darwin/amd64, linux/arm64, darwin/arm64  
2. Static link (`CGO_ENABLED=0`) – **smaller binaries without kubectl**  
3. EnvTest integration tests per build  
4. Publish binaries + checksums to Terraform Registry  
5. Attach `sbom.json` to GitHub release  

---

## 8 · Error handling advantages  

Client-go provides structured errors:
```go
    // Network/timeout errors
    if errors.IsTimeout(err) { /* retry */ }
    
    // RBAC errors  
    if errors.IsForbidden(err) { /* clear permission error */ }
    
    // Conflict errors
    if errors.IsConflict(err) { /* field manager conflict */ }
    
    // Not found
    if errors.IsNotFound(err) { /* object doesn't exist */ }
```
Better than parsing kubectl stderr output.

---

## 9 · Configuration patterns  

### Inline connection (exec auth)
```go
config := &rest.Config{
    Host: hostURL,
    TLSClientConfig: rest.TLSClientConfig{
        CAData: caBytes,
    },
    ExecProvider: &api.ExecConfig{
        APIVersion: "client.authentication.k8s.io/v1beta1",
        Command:    "aws",
        Args:       []string{"eks", "get-token", "--cluster-name", clusterName},
    },
}
```

### Kubeconfig loading
```go
config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
```

### Context switching  
```go
clientConfig := clientcmd.NewDefaultClientConfig(kubeconfig, 
    &clientcmd.ConfigOverrides{CurrentContext: contextName})
config, err := clientConfig.ClientConfig()
```

---

## 10 · Open questions  
* **Discovery cache TTL** – 5 minutes or watch-based invalidation?  
* **Connection pooling limits** – per-cluster or global?  
* **Exec credential caching** – delegate to client-go or custom logic?  

---

## 11 · Next actions  
1. Implement core `K8sClient` interface with Dynamic Client backend  
2. Add comprehensive error mapping (HTTP → Terraform diagnostics)  
3. Performance test with 200+ resources  
4. Document client-go version compatibility matrix  

---

## 12 · Migration benefits summary  

| Aspect | kubectl approach | **client-go approach** |
|--------|------------------|----------------------|
| **API stability** | ❌ CLI internals break | ✅ Stable, versioned APIs |
| **Binary size** | ❌ ~45MB with kubectl | ✅ ~15MB without kubectl |
| **Error handling** | ❌ Parse stderr text | ✅ Structured Go errors |
| **Testing** | ❌ Mock CLI commands | ✅ Mock Go interfaces |
| **Deployment** | ❌ Need kubectl binary | ✅ Single binary |
| **Debugging** | ❌ CLI flags and streams | ✅ Native Go stack traces |

**Bottom line:** Client-go eliminates the major risks while preserving all the user-facing benefits of k8sinline.

