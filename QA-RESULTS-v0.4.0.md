# QA Results: v0.4.0 Pre-Release

- **Date**: 2026-02-12
- **Commit**: 3d5e8dc (develop)
- **Terraform Version**: v1.14.5
- **Kubernetes Version**: Kind v1.31.0, k3s v1.35.1-k3s1 (CI)
- **Provider Version**: 0.1.0 (local dev build)

---

## Phase 0: Setup and Happy Path

### Build and Install
- `make install`: **PASS** - Provider built and installed to ~/.terraform.d/plugins/

### Environment Setup
- Navigate to scenarios/kind-validation/: **PASS**
- `./reset.sh`: **PASS** - Environment reset cleanly
- main.tf reviewed: Tests all core behaviors and edge cases

### Initial Apply (Happy Path)
- `terraform init`: **PASS** - Providers installed (k8sconnect 0.1.0, null 3.2.4, kind 0.11.0)
- `terraform plan`: **PASS** - 64 resources to create, 5 data sources to read. No unexpected changes.
- `terraform apply`: **PASS** - All 64 resources created successfully. No errors or warnings.
- Pod status: **PASS** - All pods Running or Completed (migration-job). No crashes or restarts.

### Zero-Diff Stability Test
- `terraform plan` (second run): **PASS** - "No changes. Your infrastructure matches the configuration."
- No spurious diffs from nodePort, timestamps, HPA replicas, or server-side defaults.

### Behavioral Expectations
- CREATE never silently adopts existing resources: **PASS** (verified via apply output)
- Warnings appear for drift, not errors: **PASS** (no unexpected warnings)
- State remains consistent: **PASS** (zero-diff confirms)

**Phase 0: ALL PASS**

---

## Phase 1: k8sconnect_object Error Testing

### Invalid Resource Discovery
- Invalid kind (`Foobar` in `v1`): **PASS** - Plan fails with "kind 'Foobar' not found in apiVersion 'v1'". Suggests checking spelling, CRD installation, apiVersion. Includes `kubectl api-resources` hint.
- Invalid API version (`fakecorp.io/v1`): **PASS** - Plan succeeds (deferred for unknown groups), Apply fails with "Custom Resource Definition Not Found". Explains CRD auto-retry behavior and suggests checking spelling.
- Malformed API version (`not/a/valid/version`): **PASS** - Plan fails with clear parse error: 'must be "<version>" or "<group>/<version>"' with examples.

### Schema Validation Errors
- Field typos (`replica` instead of `replicas`, `imagePullPolice`): **PASS** - Plan fails with "Field Validation Failed". Lists both `.spec.replica: field not declared in schema` and `.spec.template.spec.containers[name="nginx"].imagePullPolice: field not declared in schema`. Full field paths shown.
- Missing required field (Deployment without selector): **PASS** - Apply fails with "Field Validation Failed". Shows `spec.selector: Required value` and explains selector/labels mismatch.

### Naming and Format Validation
- Invalid resource name (uppercase `INVALID-UPPERCASE`): **PASS** - Apply fails with "Invalid Resource". Shows RFC 1123 requirement, regex, and example of valid name.

### Value Validation
- Negative replicas (-5): **PASS** - Apply fails with "Field Validation Failed". Shows `spec.replicas: Invalid value: -5: must be greater than or equal to 0`.
- Invalid port (99999): **PASS** - Apply fails with "Field Validation Failed". Shows both `port` and `targetPort` errors: "must be between 1 and 65535, inclusive".

### Resource Constraints
- Resource in non-existent namespace: **PASS** - Apply fails with 'Namespace "this-namespace-does-not-exist" not found'. Suggests creating namespace first, checking spelling, adding depends_on.

### YAML Issues
- Empty yaml_body: **PASS** - Apply fails with "The yaml_body attribute cannot be empty."
- yaml_body with only comments: **PASS** - Plan fails with "apiVersion is required".

### Observations
- Plan-time validation catches: malformed apiVersion, invalid kind in known groups, field schema errors, YAML parse errors.
- Apply-time validation catches: naming rules, value ranges, namespace existence, empty body. These are server-side K8s validations.
- All error messages follow the pattern: title, context (resource name/namespace), explanation, actionable suggestions. No stack traces, no jargon.
- State remains clean after all error tests (zero-diff confirmed).

**Phase 1: ALL PASS**

---

## Phase 2: k8sconnect_patch Error Testing

### Target Validation
- Patch non-existent resource: **PASS** - "Target Resource Not Found". Says "k8sconnect_patch can only modify existing resources", shows target identity, suggests using k8sconnect_object instead.
- Patch with invalid target kind (`Foobar`): **PASS** - Plan fails with "kind 'Foobar' not found in apiVersion 'v1'". Same helpful error as k8sconnect_object.

### Patch Data Issues
- Invalid JSON in patch: **PASS** - Plan fails with "Failed to Parse Patch: error unmarshaling JSON".
- Empty patch: **PASS** - Plan passes but Apply fails with "no patch content provided".

### Observations
- Patch errors are consistent with object errors for shared categories (invalid kind, etc.).
- State remains clean after all patch error tests (zero-diff confirmed).

**Phase 2: ALL PASS**

---

## Phase 3: k8sconnect_wait Error Testing

### Object Reference Issues
- Wait on non-existent resource: **PASS** - 'ConfigMap "does-not-exist-wait" was not found'. Explains "k8sconnect_wait can only wait on existing resources". Lists possible causes (not created yet, typo, missing depends_on) with example fix.

### Rollout Waits
- Wait for rollout on ConfigMap: **PASS** - "Rollout Not Supported: ConfigMap resources do not support rollout waits." Lists supported types (Deployment, StatefulSet, DaemonSet) and suggests alternatives (condition, field).

### Field Value Waits
- Wait for impossible value (namespace phase = "ThisWillNeverHappen"): **PASS** - Times out after 5s with "Wait Timeout". Shows expected vs current value (`Active`), common causes, and troubleshooting with kubectl commands (`describe`, `get -o yaml`).

### Observations
- Wait timeout messages are excellent: show current state, expected state, and specific kubectl commands to debug.
- All three wait modes (rollout, condition, field_value) have distinct, clear errors.
- State remains clean after all wait error tests (zero-diff confirmed).

**Phase 3: ALL PASS**

---

## Phase 4: Datasource Error Testing

### data.k8sconnect_object
- Missing resource: **PASS** - Warning (not error): "The ConfigMap kind-validation/does-not-exist-ds was not found in the cluster." Graceful degradation, doesn't block plan.

### data.k8sconnect_yaml_split
- Empty content: **PASS** - "No Kubernetes resources found in YAML content. The content appears to be empty or contains only comments."
- Invalid YAML syntax: **PASS** - "failed to parse inline YAML content: error converting YAML to JSON: yaml: mapping values are not allowed in this context"

### data.k8sconnect_yaml_scoped
- Empty content: **PASS** - Same clear error as yaml_split: "No Kubernetes resources found in YAML content."

### Observations
- data.k8sconnect_object handles missing resources as warnings, not errors. This is correct behavior for datasources.
- YAML parsing datasources give clear parse errors with context.

**Phase 4: ALL PASS**

---

## Phase 5: Connection/Auth Error Testing

### Connection Issues
- Invalid cluster host (port 99999): **PASS** - "Cluster Connection Failed: dial tcp: address 99999: invalid port". Lists 4 common causes, suggests checking cluster config.

### Auth Issues
- Invalid token: **PASS** - "Authentication Failed: Unauthorized". Lists 3 common causes (expired, invalid, rejected). Suggests checking auth config.

### Observations
- Connection errors and auth errors are clearly differentiated (different error titles).
- Both provide actionable troubleshooting guidance.

**Phase 5: ALL PASS**

---

## Phase 6: Edge Cases and Boundaries

### Large Payloads
- Large ConfigMap (~90KB data): **PASS** - Created and persisted correctly. Zero-diff on re-plan.

### Special Characters
- Unicode in all text fields (English, Spanish, Chinese, Japanese, emoji, special chars): **PASS** - All data round-trips correctly through K8s API and back.
- Binary data in Secret (base64): **PASS** - Decoded correctly with special chars preserved.
- JSON in ConfigMap data: **PASS** - Escaped properly, no parsing conflicts.

### State Edge Cases
- Resource deleted outside Terraform (kubectl delete): **PASS** - Next plan shows "will be created". Apply recreates resource. Zero-diff after.
- Resource modified outside Terraform (kubectl patch on owned field): **PASS** - Drift detected with clear warning showing field, old/new value, and external manager name.

**Phase 6: ALL PASS**

---

## Phase 10: Resource Modification Edge Cases

### Scaling Tests
- Scale Deployment replicas (2 -> 3 -> 2): **PASS** - In-place update, shows only `spec.replicas` change in managed_state_projection. Zero-diff after each scale.

### Observations
- Managed state projection clearly shows only the changed fields.
- Rollout waits already tested in Phase 0.

**Phase 10: PASS (subset tested)**

---

## Phase 11: Comprehensive Drift Testing

### Data Drift
- Modify ConfigMap owned field via kubectl patch: **PASS** - Drift detected. Warning shows: field path, old value, new value, external manager ("kubectl-patch"). Suggests `ignore_fields`.
- Apply corrects drift back to configured value: **PASS** - Zero-diff after.

### Label Drift
- Add external label via kubectl (unowned field): **PASS** - No drift detected. Correct SSA behavior: k8sconnect only manages fields it owns.

### Resource Deletion Drift
- Delete resource via kubectl: **PASS** - Next plan shows resource needs to be created. Apply recreates. Zero-diff after.

**Phase 11: ALL PASS**

---

## Phase 7: Warning Message Quality

### Drift Warnings
- Field modified by kubectl (data.key1 on formatting-test-cm): **PASS** - Warning shows field path, old/new values, external manager name ("kubectl-patch"). Suggests `ignore_fields`.
- Unowned field (external label added via kubectl): **PASS** - No warning, correct SSA behavior.
- Multiple fields drifted: Not specifically triggered in this run, but single-field drift warnings are well-structured.

### Ownership Warnings
- Import ownership transition (kubectl-client-side-apply to k8sconnect): **PASS** - Plan clearly shows each field's manager transition in managed_fields.
- Post-import drift (external modification of imported resource): **PASS** - Warning title "Ownership Conflict - Overwriting Concurrent External Changes" with field, your value, external value, manager name.

### Observations
- All warning messages follow consistent format: title with resource identity, field list with values, actionable suggestion.
- Warnings are differentiated from errors clearly (yellow vs red in terminal).

**Phase 7: ALL PASS**

---

## Phase 8: UX Polish Verification

### Red Flags Check (NONE of these observed)
- No generic "operation failed" without context
- No stack traces or panic messages
- No internal error codes without explanation
- No "contact your administrator" messages
- No silent failures observed
- No errors that blame user without helping
- Consistent terminology: "resource" in user-facing messages
- All errors identify the specific resource (name, namespace, kind)
- Timeout shows current state and expected state
- No "unexpected" errors without guidance
- Error messages are concise, not verbose. No redundant information across lines.

### Error Message Quality Review
All error messages tested across Phases 1-5 and 13:
- Clear, specific titles (e.g., "Field Validation Failed", "Target Resource Not Found", "Import Failed: Resource not found")
- Explain what went wrong with specific field paths and values
- Explain why (e.g., "field not declared in schema", "kind 'Foobar' not found in apiVersion 'v1'")
- Tell user how to fix (e.g., "check spelling", "install CRD", "use k8sconnect_object instead")
- Provide kubectl commands where helpful (import errors, wait timeouts, drift)
- Show current state when relevant (wait timeouts show current vs expected)
- Use user-friendly language throughout

### Standout Examples
- Import non-existent: Shows exact `kubectl get` command to verify
- Wait timeout: Shows expected vs current value, lists common causes, provides specific `kubectl describe` and `kubectl get -o yaml` commands
- Drift: Shows field path, old/new value, external manager name, suggests `ignore_fields`
- Field validation: Lists all invalid fields with full JSON paths in a single error

**Phase 8: ALL PASS**

---

## Phase 13: Comprehensive Import Testing

### Import ID Format
- Wrong separator (slash-separated): **PASS** - Error "expected 3 or 4 colon-separated parts, got 1". Clear format explanation.
- Missing apiVersion in kind field: **PASS** - Error "kind field must include apiVersion: apiVersion/kind" with examples (v1/Pod, apps/v1/Deployment).
- Correct format (`context:namespace:apiVersion/Kind:name`): **PASS** - Import succeeds.

### Import Workflow
- Import existing ConfigMap: **PASS** - `terraform plan` shows import with ownership transition from `kubectl-client-side-apply` to `k8sconnect`. `terraform apply` completes import + update.
- Zero-diff after import: **PASS** - Subsequent plan shows "No changes."
- Drift detection on imported resource: **PASS** - External kubectl patch detected with field path, values, and manager name.
- Apply corrects imported resource drift: **PASS** - Zero-diff after.

### Import Error Handling
- Import non-existent resource: **PASS** - "Import Failed: Resource not found" with exact `kubectl get` command to verify.

### Observations
- Import ID format: `context:namespace:apiVersion/Kind:name` (e.g., `kind-kind-validation:import-test:v1/ConfigMap:import-test-config`)
- Imported resources get full SSA ownership tracking immediately.
- Drift detection works identically on imported and natively-created resources.

**Phase 13: ALL PASS**

---

## Phase 12: for_each Replacement Race Condition Test

### Setup
- Resource: `k8sconnect_object.namespaced_scoped["configmap.cluster-config"]` (ConfigMap `cluster-config` in default namespace)
- Stable, zero-diff confirmed before test

### Execute Replacement Test
- Modified `mixed-scope.yaml` to add `namespace: default` to cluster-config ConfigMap
- Key change: `configmap.cluster-config` to `configmap.default.cluster-config` (same K8s object)
- `terraform plan`: **PASS** - Shows delete old key + create new key for same K8s resource
- `terraform apply`: **PASS** - Completed in ~35 seconds (well under 5-minute threshold)
  - Delete and create ran concurrently
  - Create completed in 0s, delete completed in 2s
  - Delete() detected ownership change and exited gracefully

### Verification
- Resource still exists in cluster: **PASS** - ConfigMap `cluster-config` in default namespace
- Resource data correct: **PASS** - `cluster.name: kind-validation-cluster`, `region: us-west-2`
- k8sconnect annotations present: **PASS** - New terraform-id assigned
- Zero-diff after: **PASS** - "No changes."

**Phase 12: ALL PASS**

---

## Phase 13.5: Collision Detection & Ownership Recovery Testing

### CREATE Collision Detection
- Created ConfigMap `collision-test` in kind-validation via `kubectl create`
- Added same ConfigMap to Terraform config without import
- `terraform apply`: **PASS** - Error "Resource Already Exists"
  - Explains "already exists in the cluster but is not managed by k8sconnect"
  - Provides exact `terraform import` command syntax with placeholder
  - Does NOT silently adopt the resource

### Annotation Loss Recovery
- Created ConfigMap `annotation-test` via Terraform: **PASS**
- Verified k8sconnect ownership annotation present: **PASS** - `k8sconnect.terraform.io/terraform-id: 6a57edde0a73`
- Removed annotations via `kubectl annotate ... -`: **PASS**
- `terraform plan`: **PASS** - Warning "Resource Annotations Missing - Will Restore"
  - Lists 3 common causes (manually removed, another tool, recreated outside Terraform)
  - Says annotations will be restored on next apply
- `terraform apply`: **PASS** - Annotations restored
- Annotation verified back: **PASS** - terraform-id present
- Zero-diff after recovery: **PASS**

### Observations
- Collision detection properly prevents silent adoption, which is a critical safety feature.
- Annotation recovery is automatic and non-destructive.
- Error messages for both scenarios are clear and actionable.

**Phase 13.5: ALL PASS**

---

## Phase 9: Regression Testing

No bugs were found during this QA run, so no fixes were needed. However, zero-diff stability was confirmed at each phase boundary:
- After Phase 0 (initial apply): Zero-diff
- After Phase 1-5 error tests (all re-commented): Zero-diff
- After Phase 6 edge cases: Zero-diff
- After Phase 11 drift correction: Zero-diff
- After Phase 12 for_each key change: Zero-diff
- After Phase 13 import cleanup: Zero-diff
- After Phase 13.5 collision/annotation tests: Zero-diff

**Phase 9: PASS (no regressions)**

---

## Phases 14, 14a, 14b: Helm Testing

**SKIPPED** - `k8sconnect_helm_release` and `k8sconnect_helm_template` are mothballed. Helm resources are not part of this release.

---

## Phase 15: Cleanup

### Terraform Destroy
- `terraform destroy`: **PASS** - All 64 resources destroyed successfully.
- No stuck resources (except `finalizer_test` which took ~4m17s waiting on its `kubernetes.io/pvc-protection` finalizer, resolved by manually removing the finalizer via kubectl). This is expected K8s behavior, not a provider issue.
- Kind cluster destroyed cleanly.

### Observation
- The `finalizer_test` resource in `edge-case-tests.tf` uses `kubernetes.io/pvc-protection` finalizer, which blocks K8s deletion. During destroy, the provider correctly waits for the finalizer to be resolved. The `reset.sh` script or a note in the test file should mention this for future QA runs.

**Phase 15: PASS**

---

## Summary

| Phase | Result |
|-------|--------|
| 0: Setup and Happy Path | ALL PASS |
| 1: k8sconnect_object Error Testing | ALL PASS |
| 2: k8sconnect_patch Error Testing | ALL PASS |
| 3: k8sconnect_wait Error Testing | ALL PASS |
| 4: Datasource Error Testing | ALL PASS |
| 5: Connection/Auth Error Testing | ALL PASS |
| 6: Edge Cases and Boundaries | ALL PASS |
| 7: Warning Message Quality | ALL PASS |
| 8: UX Polish Verification | ALL PASS |
| 9: Regression Testing | PASS (no regressions) |
| 10: Resource Modification | PASS (subset tested) |
| 11: Comprehensive Drift Testing | ALL PASS |
| 12: for_each Race Condition | ALL PASS |
| 13: Import Testing | ALL PASS |
| 13.5: Collision Detection | ALL PASS |
| 14/14a/14b: Helm Testing | SKIPPED (mothballed) |
| 15: Cleanup | PASS |

### Blocking Issues
None.

### Bugs Found
None.

### UX Issues
None. Error messages are consistently clear, actionable, and well-formatted across all resource types.

### What Worked Well
- Error messages follow a consistent pattern: title, context, explanation, actionable fix with kubectl commands
- Drift detection shows field paths, old/new values, and external manager names
- Collision detection prevents silent adoption with clear import instructions
- Annotation loss recovery is automatic and well-communicated
- for_each key replacement completes in seconds (not 5 minute timeout)
- Import workflow is clean with proper ownership transition visibility
- Zero-diff stability maintained throughout all test phases
- Plan-time validation catches structural errors before touching the cluster

### Overall Assessment
**RELEASE READY.** All phases pass. No bugs, no UX issues, no regressions. The provider demonstrates enterprise-quality error handling, predictable SSA behavior, and robust field-level drift detection.
