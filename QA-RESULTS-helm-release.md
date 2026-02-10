# QA Results: k8sconnect_helm_release (Phase 14)

**Date**: 2026-02-10
**Provider**: k8sconnect v0.1.0 (commit 8c73f72 on develop)
**Terraform**: v1.13.4
**Kubernetes**: kind v1.31.0

---

## 14.1 Happy Path and Lifecycle

### Plan for create - PASS
- Plan output is clean and readable
- All defaults shown: `atomic = false`, `timeout = "300s"`, `wait = true`, etc.
- Cluster certs correctly marked `(sensitive value)`
- Computed fields show `(known after apply)`

### Apply - PASS
- Created in 1s
- `helm list -n qa-helm` confirms release installed, status "deployed", revision 1

### Computed attributes - PASS
All populated correctly:
- `revision = 1`
- `status = "deployed"`
- `manifest` = full rendered YAML (deployment template)
- `metadata` = map with `app_version`, `chart_name`, `chart_version`, `description`, `first_deployed`, `last_deployed`

### Zero-diff re-apply - PASS
- `terraform plan` shows "No changes" immediately after apply

### Update values - PASS
- Added `values = yamlencode({ replicaCount = 2 })`
- Plan showed update in-place with manifest diff
- Apply completed in 1s
- Revision incremented to 2
- 2 pods running

### Destroy - PASS
- Clean uninstall, `helm list -n qa-helm` shows nothing after destroy

---

## 14.2 Value Handling

### Precedence (values < set < set_sensitive) - PASS
- `values = yamlencode({ replicaCount = 1 })` + `set = [{ name = "replicaCount", value = "3" }]`
- Result: 3 pods (set wins over values)
- Added `set_sensitive` with value "4": 4 pods (set_sensitive wins over set)
- Precedence chain confirmed: values < set < set_sensitive

### set_sensitive masking - PASS
- Plan output shows `value = (sensitive value)` for set_sensitive entries
- Value never exposed in plan text

### Malformed YAML values - PASS
- `values = "this: is: not: valid: yaml: ["`
- Error: `Failed to Merge Values` / `failed to parse values YAML: yaml: mapping values are not allowed in this context`
- Clear, identifies the problem. Could be improved by showing the offending YAML snippet.

### Empty values - PASS
- `values = ""` treated as no values, release created with chart defaults

### Not tested
- set_list JSON array parsing (chart doesn't have list-type values to test against)
- Escaped dots in keys (chart doesn't have dotted keys)
- YAML anchors (would need a chart that exercises them)

---

## 14.3 Namespace Handling

### create_namespace = true - PASS
- Non-existent namespace `qa-helm` created automatically

### create_namespace = false (default) to non-existent namespace - FAIL (UX)
Error message:
```
Could not install Helm release 'qa-bad-ns': create: failed to create:
namespaces "this-namespace-does-not-exist" not found
```
**UX Issue**: Error does not suggest setting `create_namespace = true` as a fix. A user seeing this for the first time would have to look up the docs. Should say something like:
```
Namespace 'this-namespace-does-not-exist' does not exist. Either create
the namespace first, or set create_namespace = true to have Helm create it.
```

### Namespace NOT deleted on destroy - PASS (expected Helm behavior)
- Destroying a release with `create_namespace = true` leaves the namespace behind
- This is standard Helm behavior, not a bug

---

## 14.4 Wait and Timeout Behavior

### wait = true with good image - PASS
- Apply waits for pods ready, then succeeds (1-2s for simple chart)

### wait = true with bad image + timeout = 30s - FAIL (UX)
- Timeout fires at ~30s (correct!)
- Error message:
```
Could not install Helm release 'qa-timeout': resource
Deployment/qa-helm/qa-timeout not ready. status: InProgress, message:
Available: 0/1
context deadline exceeded
```
**UX Issues (3)**:
1. `context deadline exceeded` is a raw Go error leaked to the user. Should say "timed out after 30s" or similar.
2. No pod events shown. Should include WHY the deployment isn't ready (e.g., "ImagePullBackOff: this-image-definitely-does-not-exist:v99.99.99")
3. No suggested debug commands (e.g., `kubectl describe pod -n qa-helm -l app=qa-timeout`)

### wait = false with bad image - PASS
- Created instantly (0s), no wait
- Status in state: "deployed" (Helm considers it deployed even though pods aren't ready)

### atomic = true with bad image - PASS (functional), FAIL (UX)
- Release was rolled back automatically after timeout
- No orphaned "failed" release left behind
- Terraform state not polluted
- Error message:
```
Could not install Helm release 'qa-atomic': release qa-atomic failed, and has
been uninstalled due to rollback-on-failure being set: resource
Deployment/qa-helm/qa-atomic not ready. status: InProgress, message:
Available: 0/1
context deadline exceeded
```
- Good: mentions "rollback-on-failure being set"
- Bad: same `context deadline exceeded` raw Go error, no pod events

---

## 14.5 Delete Behavior

### Normal destroy - PASS
- Clean uninstall, <1s

### Already-removed release destroy - PASS
- Manually `helm uninstall` then `terraform destroy`
- Read detected release gone, removed from state
- Destroy was a clean no-op: "No objects need to be destroyed"
- No error about "release not found"

### force_destroy = true - PASS
- Created and destroyed successfully
- Hooks skipped during uninstall

---

## 14.6 Failed Release Recovery

### Prior failed release blocks create - PASS
- Manually created failed release with `helm install` + bad-image-test
- `helm list` showed "failed" status
- Terraform `apply` detected failed release, cleaned it up, installed fresh with good chart
- Completed in 2s, no "release already exists" error
- This is a MAJOR improvement over the HashiCorp provider

### Failed release not in state - PASS
- When `wait = true` and install times out, release is NOT added to Terraform state
- Subsequent `terraform apply` will retry (not show "no changes")
- Correct behavior per HashiCorp Issue #472

---

## 14.7 Drift Detection

### kubectl-level drift (replica scaling) - Expected: Not detected
- `kubectl scale deployment qa-basic --replicas=5`
- `terraform plan` shows "No changes"
- This is correct: helm_release tracks Helm metadata (revision, values, chart), not individual K8s resources

### Manual helm uninstall - PASS
- `helm uninstall` then `terraform plan`
- Read detected release gone, resource removed from state
- Plan shows `+ create` (re-create needed)

### Manual helm upgrade - Not fully tested
- `helm upgrade` was blocked by SSA field ownership conflict
- This is actually a GOOD thing: SSA prevents external tools from silently modifying owned fields
- Could not test without `--force`, which is incompatible with SSA

---

## 14.8 ADR-023 Auth Resilience

**Not tested** - Would require invalidating cluster credentials mid-session. Skipped for this round. Covered by acceptance tests.

---

## 14.9 Import

### Import existing release - PASS
- `terraform import k8sconnect_helm_release.test "kind-kind-validation:qa-helm:qa-import"`
- Import succeeded
- Helpful warning shown:
```
Helm release 'qa-import' imported successfully.
The following fields were imported: chart, version, revision
You must add to your Terraform configuration: repository, values
```
- This is excellent UX. Tells the user exactly what to do next.

### Post-import plan - Expected diffs
- Chart path diff (`simple-test` -> `../../test/testdata/charts/simple-test`) - expected
- Cluster connection diff (kubeconfig -> client certs) - expected since import uses KUBECONFIG

### Import non-existent release - PASS (excellent error)
```
Helm release 'this-release-does-not-exist' not found in namespace 'qa-helm'
(context: kind-kind-validation).

Verify the release exists:
  helm list -n qa-helm --kube-context kind-kind-validation
```
- Clear, shows release name, namespace, context
- Suggests exact `helm list` command to debug

### Import with wrong format - PASS (excellent error)
```
Import ID must be in format 'context:namespace:release-name' or
'context:release-name'.

Examples:
  prod:kube-system:cilium
  prod:cert-manager  (uses default namespace)

Got: wrong-format
```
- Shows expected format, gives examples, shows what was received

---

## 14.10 Chart Sources

### Local chart (relative path) - PASS
- `chart = "../../test/testdata/charts/simple-test"` works

### Non-existent local path - PASS
```
Could not load Helm chart: failed to load local chart: stat
/nonexistent/chart/path: no such file or directory
```
- Error title "Failed to Load Chart" is clear
- Path shown in error

### OCI chart, invalid repository URL, chart not found - Not tested
- Would require network access to external registries

---

## 14.11 Skip CRDs and Dependency Update

**Not tested** - Test charts don't have CRDs or dependencies. Would need more complex test charts.

---

## 14.12 Error Message Quality

### Excellent errors (enterprise quality)
1. **Import non-existent release** - Shows release/namespace/context, suggests `helm list`
2. **Import wrong format** - Shows expected format with examples
3. **Bad chart path** - "Failed to Load Chart" + path in error
4. **Malformed YAML** - "Failed to Merge Values" + YAML parse error
5. **Failed release recovery** - Silently cleans up and installs, no confusing error
6. **Import success warning** - Tells user exactly what fields to configure next

### UX issues needing improvement

**Issue 1: Timeout errors leak raw Go error** (Severity: Major)
All timeout scenarios include `context deadline exceeded` at the end. This is a raw Go error string that means nothing to users. Should be replaced with human-readable text like "timed out after 30s waiting for resources to become ready."

**Issue 2: Timeout errors don't show pod events** (Severity: Major)
When a deployment times out, the error says `status: InProgress, message: Available: 0/1` but doesn't explain WHY. Should include:
- Pod status (ImagePullBackOff, CrashLoopBackOff, etc.)
- Recent events
- Suggested kubectl command to debug

**Issue 3: Namespace-not-found error lacks fix suggestion** (Severity: Minor)
Error says `namespaces "X" not found` but doesn't suggest `create_namespace = true`.

---

## Summary

| Section | Result | Notes |
|---------|--------|-------|
| 14.1 Happy Path | **PASS** | Create, update, zero-diff, destroy all clean |
| 14.2 Values | **PASS** | Precedence correct, sensitive masking works |
| 14.3 Namespace | **PASS** (UX issue) | Missing fix suggestion in error |
| 14.4 Wait/Timeout | **PASS** (UX issues) | Timeout works but error messages need work |
| 14.5 Delete | **PASS** | All scenarios clean including already-removed |
| 14.6 Recovery | **PASS** | Major improvement over HashiCorp provider |
| 14.7 Drift | **PASS** | External uninstall detected, kubectl changes expected not-detected |
| 14.8 Auth Resilience | Not tested | Covered by acceptance tests |
| 14.9 Import | **PASS** | Excellent error messages across all scenarios |
| 14.10 Chart Sources | **PASS** (partial) | Local path tested, OCI not tested |
| 14.11 CRDs/Deps | Not tested | Need more complex test charts |
| 14.12 Error Quality | Mixed | Some excellent, some need improvement |

### Blocking Issues

None. All functional tests pass.

### UX Issues (Fix Before Release)

1. **Timeout errors leak `context deadline exceeded`** - Replace with human-readable timeout message
2. **Timeout errors don't show pod events/status** - Include WHY the deployment isn't ready
3. **Namespace-not-found error missing fix suggestion** - Add `create_namespace = true` hint

### Observations

1. **SSA field ownership prevents manual `helm upgrade`** on managed releases. This is actually a feature, not a bug. It prevents external tools from silently overriding Terraform-managed values. Users who need to manually upgrade would need to use `--force-conflicts` (Helm v4).

2. **Failed release recovery is excellent.** The provider detects pre-existing failed releases and cleans them up automatically before installing. This solves a major pain point from the HashiCorp provider.

3. **Import UX is best-in-class.** The import format error, non-existent release error, and post-import guidance warning are all excellent.

4. **The `metadata` computed attribute is very useful.** Having `chart_name`, `chart_version`, `app_version`, `first_deployed`, `last_deployed` all in one map is convenient for outputs and monitoring.

5. **helm_release does not detect K8s-level drift** (only Helm-level drift). This is by design since Helm itself doesn't track individual field changes. For field-level drift detection, users should use `k8sconnect_object` directly.
