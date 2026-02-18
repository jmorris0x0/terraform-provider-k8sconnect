# ADR-022: Helm Integration Strategy

**Status:** Deferred (2026-02-12)

## Context

Users want to deploy Helm charts through k8sconnect. The provider's SSA foundation (ADR-001) requires per-resource projections, field-level drift detection, and ownership tracking. Two approaches were explored.

## Decision Timeline

### Phase 1: helm_release Resource (Abandoned)

A `k8sconnect_helm_release` resource was implemented that managed Helm releases as opaque bundles via the Helm SDK's `action.Install` / `action.Upgrade`.

**Why it was abandoned:**

- Violates ADR-001. The resource manages an entire release as one Terraform resource with no SSA projections, no field-level drift detection, and no ownership tracking.
- Plan output shows the Helm values diff, not what Kubernetes will actually do. This contradicts the provider's core value proposition of accurate dry-run plans.
- Helm's release management (revision tracking, rollback, hooks) conflicts with Terraform's state model. Two state systems fighting over the same resources.

### Phase 2: helm_template Data Source (Deferred)

A `k8sconnect_helm_template` data source was implemented that renders charts client-side via `action.NewInstall()` with `DryRunStrategy: DryRunClient`, then categorizes output into `crds`, `cluster_scoped`, and `namespaced` maps (matching `yaml_scoped` output format). Users apply rendered manifests via `k8sconnect_object` with `for_each`, getting full SSA projections for every resource.

The implementation was completed and all tests passed. However, fundamental issues with Helm hooks make this unsafe for enterprise release:

**Helm hooks are incompatible with the template-and-apply pattern:**

1. **Job immutability.** Helm hooks are frequently Jobs. Kubernetes Jobs are immutable after creation. On second `terraform apply`, the Job spec hasn't changed but the completed Job exists, causing failures on immutable fields. Helm normally handles this via hook delete policies, but those policies are lost in the rendering step.

2. **Hook delete policies not honored.** `helm.sh/hook-delete-policy: before-hook-creation` tells Helm to delete the old hook resource before creating a new one. When resources are managed by Terraform instead of Helm, nothing implements this lifecycle.

3. **`lookup` template function.** Many charts use `lookup` to conditionally create resources based on existing cluster state. Client-side rendering returns empty for all `lookup` calls, producing incorrect output for these charts.

4. **Intra-phase ordering impossible.** Helm hooks define ordering within an install/upgrade lifecycle (pre-install, post-install, etc.). Terraform's `for_each` applies resources in parallel within each group. There's no way to enforce "run this Job before creating that Deployment" within the same `for_each` block without manual intervention.

**The reliability concern:** Having to tell users "check if your chart uses hooks, otherwise you can't use this" is unacceptable for an enterprise provider. Even a 0.01% failure rate from obscure hook interactions creates disproportionate support burden and erodes trust.

## Requirements (Preserved for Future Work)

These requirements were validated through research and remain relevant:

- Must provide per-resource SSA projections (ADR-001 compliance)
- Must handle CRD dependency ordering (CRDs before custom resources)
- Must support all Helm value-setting mechanisms (values YAML, set, set_list, set_sensitive)
- Must support OCI registries, HTTP repos, and local charts
- Must produce stable resource IDs for `for_each` (no unnecessary recreation)
- Should handle hook lifecycle if hooks are supported at all
- Should support `api_versions` and `kube_version` for chart capability detection

## HashiCorp Helm Provider Issues (Research)

The official Helm provider has known issues that motivated this work:

- Resources created by Helm are invisible to Terraform's plan
- No field-level drift detection on Helm-managed resources
- Provider-level authentication prevents single-apply cluster bootstrapping
- CRD installation timing issues with `skip_crds` workarounds
- No SSA support, leading to conflicts with controllers

## Lessons Learned

1. **Helm's release model and Terraform's resource model are fundamentally different state systems.** Trying to bridge them (helm_release) creates two sources of truth. Decomposing releases into resources (helm_template) avoids this but loses Helm's lifecycle management.

2. **Hook lifecycle is the critical gap.** Without hooks, the template-and-apply pattern works well (ArgoCD proves this). With hooks, it requires either reimplementing Helm's hook lifecycle or accepting a "no hooks" restriction.

3. **ArgoCD validates the pattern but has different constraints.** ArgoCD renders charts and applies as individual resources, mapping hooks to sync waves. However, ArgoCD has a continuous reconciliation loop and its own ordering system. Terraform's plan/apply model doesn't have this.

4. **Client-side rendering has inherent limitations.** The `lookup` function, server-side template functions, and capabilities detection all degrade when rendering without a cluster connection.

## Success Criteria (for Future Resumption)

To revisit this work, a solution must:

1. Handle Helm hooks safely (at minimum: detect and warn, ideally: manage lifecycle)
2. Handle Job immutability (delete-before-recreate or ownership release)
3. Work with charts that use `lookup` (server-side rendering or graceful degradation)
4. Maintain the per-resource SSA projection guarantee
5. Not require users to audit chart internals before using the data source

## Implementation Notes

The helm_template implementation is complete on the `develop` branch (commit `675163a`). The helmutil package extracts chart loading and values merging from helm_release into shared utilities. All unit and acceptance tests pass with ~85% coverage. This code can be restored if the hook lifecycle issues are resolved.
