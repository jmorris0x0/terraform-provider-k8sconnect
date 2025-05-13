# k8sinline Provider – In‑Process `kubectl` Engine Notes  
_Last updated: 2025‑05‑12_

## 0 · Summary of feedback on **PROJECT_REVIEW.md**  
* **Agreed headline risks**  
  1. Fragile internal `kubectl` APIs  
  2. Terraform UX friction from deferred / server‑diff  
  3. Inline kube‑configs widen blast‑radius in state files  
* **Extra risks not in review**  
  * Server‑side‑apply (SSA) field‑ownership conflicts (`409`)  
  * Go / protobuf churn every Kubernetes minor  
  * Discovery‑cache memory footprint  
* **Potentially overstated items**  
  * CI cost (manageable with small matrix)  
  * Plan‑accuracy objections (acceptable if limits documented)  
* **Success hinges on**  
  * Compatibility shim around `kubectl` internals  
  * Honest `terraform plan` story  
  * Clear docs + SBOM for security sign‑off  

---

## 1 · Purpose  
Capture design decisions, risks, and tasks for linking `kubectl` code **in‑process** (no embedded binary).

---

## 2 · Minimal `kubectl` slice to vendor  

| Need                            | Package(s)                                                         | Notes                                       |
|---------------------------------|--------------------------------------------------------------------|---------------------------------------------|
| Build objects from YAML         | `k8s.io/cli-runtime/pkg/resource`                                  | Multi‑doc YAML, GVK checks                  |
| Shared helpers (client, mapper) | `k8s.io/cli-runtime/pkg/genericclioptions`, `cmd/util`             | Import only what is called directly         |
| Server‑side apply logic         | `k8s.io/kubectl/pkg/apply`                                         | SSA patch + wait helpers                    |

CI step `go mod why` ensures no extra CLI‑only deps creep in.

---

## 3 · Version pinning strategy  
* Track **every Kubernetes minor** one‑to‑one.  
* CI target `make bump-k8s VERSION=v0.NN.0` updates `go.mod`, runs tests, tags release.  
* Docs page “Kubernetes compatibility” maps provider tags to K8s minors.

---

## 4 · Memory and concurrency model  
* **One REST client** per `{server, clusterUID}` per Terraform run (cached).  
* Discovery cache kept in‑process.  
* Limit to **10 concurrent Apply workers** to avoid API saturation.  
* Respect Terraform‑supplied context deadlines.

---

## 5 · Accurate Terraform diff  
1. Build desired object.  
2. PATCH with `dry‑run=server` + `fieldManager=k8sinline`.  
3. If HTTP 200 and no managed‑field delta → report “no‑op”.  
4. Surface HTTP 409 conflicts so drift is visible.  

Retry 409s with exponential back‑off (max 5 attempts).

---

## 6 · Security and compliance  
* Source vendoring → provenance already in `go.sum`.  
* Generate CycloneDX SBOM each release:  

      syft dir:. --scope all-layers -o cyclonedx-json > sbom.json

* Mark `cluster.*` attributes **Sensitive**; recommend encrypted remote state (TFC, S3 + KMS).

---

## 7 · Release workflow (CI)  
1. Matrix build: linux/amd64, darwin/amd64, linux/arm64, darwin/arm64  
2. Static link (`CGO_ENABLED=0`)  
3. Acceptance tests against KinD per build  
4. Publish binaries + checksums to Terraform Registry  
5. Attach `sbom.json` to GitHub release

---

## 8 · Optional shell‑out escape hatch  

    provider "k8sinline" {
      use_kubectl_binary = true   # default false
      kubectl_path       = "/usr/local/bin/kubectl"
    }

Disabled by default; exists for edge cases needing raw `kubectl` flags.

---

## 9 · Open questions  
* Expose `max_parallel_applies` or auto‑tune?  
* Best UX for SSA field‑ownership conflicts?  
* Additional org‑specific compliance docs beyond SBOM?

---

## 10 · Next actions  
1. Prototype diff flow (`dry‑run=server`) and benchmark at 200 resources.  
2. Implement automatic Kubernetes minor‑bump CI job.  
3. Draft docs: “Security model & state‑file handling”.  
4. Finalize default back‑off policy for 409 retries.

