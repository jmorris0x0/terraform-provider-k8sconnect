## Executive‑level verdict

Building **`terraform‑provider‑k8sinline`** *can* work, but three areas could torpedo either engineering velocity or real‑world adoption if they are not tackled up‑front:

1. **Coupling to kubectl internals** – the in‑process engine pins you to a non‑stable API surface that breaks every Kubernetes release.  
2. **Terraform‑workflow friction** – deferred / offline diff, per‑resource credentials, and state growth all deviate from Terraform norms and will trigger security or DX objections in many teams.  
3. **State & security blast‑radius** – YAML snapshots plus embedded **Secrets** risk multi‑MB state files and sensitive data ending up in VCS or plan artifacts.

Everything else (cross‑compile, binary size, SSA edge‑cases, etc.) is solvable with normal elbow‑grease.

---

## 1  Implementation feasibility

| Topic | What works | Hidden (or under‑played) blockers | Mitigations |
|-------|------------|-----------------------------------|-------------|
| **In‑process kubectl (`libkubectl.go`)** | Static link avoids the multi‑binary shipping headache. | • *API churn*: `k8s.io/kubectl/pkg/cmd/...` is **not** covered by K8S compatibility guarantees.<br>• *CLI assumptions*: the code instantiates `cobra.Command`, expects global `flags`, `ioStreams`, and side‑effects on `os.Std*`.<br>• *Size/runtime*: pulls in transitive deps (~45 MB darwin/amd64). | • Wrap in a thin adapter that locally patches breaking changes each release (manual upkeep).<br>• Nightly canary compile against `master`.<br>• Keep an opt‑in `ExecKubectl` path if the static build lags. |
| **Server‑side apply & read‑back** | SSA gives clean history; field‑manager avoids drift. | • *CRD ordering*: SSA fails if a CR is in the same plan as its CRD without `depends_on`.<br>• *Immutable field edits*: SSA surfaces these as opaque 409s that Terraform reports as “apply failed” with no diff context. | • Pre‑apply graph walk that auto‑adds `depends_on` when a CR and its CRD share a plan.<br>• Intercept 409s, parse the `Status`, and map them into TF diagnostics. |
| **Deferred / offline diff** | Hash or YAML fallback keeps single‑phase pipelines possible. | • *Plan accuracy*: when the cluster is down, reviewers see a best‑guess diff that may be **wrong**.<br>• Hash‑only diff gives a binary Yes/No answer – useless in PRs. | • Default to `diff_history = yaml` despite state size hit; warn loudly about drift.<br>• Allow a post‑cluster‑up `terraform plan` re‑run (document workflow). |
| **Refresh (`kubectl get … -o yaml`)** | Straightforward. | • *State bloat*: storing full YAML inflates `terraform.tfstate` (1000 × 3 KB ≈ 3 MB). | • Store only metadata + SHA256 in state; keep full YAML in a side‑car (e.g. S3) addressed by hash. |
| **Cross‑compile matrix** | Go 1.22 static builds cover darwin/arm64, darwin/amd64, linux/amd64/arm64. | • Windows users excluded at launch.<br>• CGO transitive deps can break musl static builds. | • Document Windows as “exec‑kubectl only” for now.<br>• `go build -trimpath -ldflags "-s -w"` plus `upx` to keep binaries < 25 MB. |

---

## 2  Maintenance & lifecycle risk

* **Kubernetes release skew** – Must cut a provider release every 12 months (±1 version skew).  
* **Terraform SDK upgrades** – `terraform-plugin-framework` still evolves; budget 2–3 days per major bump.  
* **CI cost explosion** – Five GOOS/GOARCH × static/exec variants × envtest; pre‑warm images and cache modules.

---

## 3  Security & compliance watch‑outs (updated)

| Vector | Risk level | Notes / Mitigation |
|--------|-----------|--------------------|
| **Inline kube‑config** (`server`, `certificate_authority_data`, `exec`) | **Low** | Only a public CA bundle and API URL in Git. Teach users to put private material in the exec helper or external files marked `sensitive = true`. |
| **Exec credential helper output** | **Medium** | Helpers often print tokens to `stdout`; if CI captures logs they can leak. Recommend helpers that write tokens to `stderr` or JSON and suppress in TF logs. |
| **State file manifest snapshots** | **Medium–High** | `Secret` objects can land in state during refresh. Implement secret‑scrubbing that zeros `.data`, `.stringData`, and `.env[*].value`. |
| **ManagedFields collisions** | **Medium** | Permit `field_manager` override and document coexistence patterns with Argo/Flux. |

---

## 4  Adoption friction

1. **“Why not the official provider?”** – Provide a crisp comparison table (multi‑cluster pain points, field ownership).  
2. **Security teams dislike per‑resource creds** – Offer an optional `default_cluster` provider block for incremental adoption.  
3. **Plan accuracy guarantee** – Some orgs gate merges on `terraform plan`; provide a helper that reruns diff post‑apply.  
4. **Registry trust** – Ship reproducible, signed builds (`cosign`, `goreleaser‑sbom`).  
5. **Docs burden** – Cookbook examples, SSA primers, migration guides.

---

## 5  Non‑obvious engineering tasks to add to Roadmap

| Priority | Task | Rationale |
|----------|------|-----------|
| **🔥** | Auto‑add `depends_on` between CRDs and CRs. | Avoids #1 user‑reported failure. |
| **🔥** | Secret‑scrubbing middleware on refresh. | Prevents credential exfiltration into state. |
| **⚠️** | Nightly CI compile against kubectl `master`. | Detect upstream breaks early. |
| **⚠️** | `k8sinline validate --offline` lint. | Gives reviewers confidence when diff is hashed. |
| **🛈** | Windows support plan (exec‑only or mingw). | Expands install base. |
| **🛈** | Terraform Cloud run‑task that toggles `diff_mode=server` post‑apply. | Shows true drift in SaaS pipelines. |

---

## 6  Go / build‑time land‑mines

* **CGO** – Force `CGO_ENABLED=0`; `json-patch/v5` toggles CGO on some OSes.  
* **Module replaces** – K8S 1.30+ rewires `go.opentelemetry.io/otel`; pin via `replace` to avoid breakage.  
* **UPX on arm64** – Older UPX corrupts static ARM binaries; test compression per arch in CI.

---

## Bottom line

*Technically doable*, but you must invest in **API‑churn shields**, **secret hygiene**, and **workflow UX** from day one or risk shipping a brittle novelty that only works for its author. If that mitigation plan is acceptable, green‑light the MVP; otherwise, reconsider the in‑process kubectl strategy, because that choice drives 80 % of downstream complexity.

