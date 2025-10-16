# Acceptance Test Gaps

**Date**: 2025-10-15
**Status**: Actionable test gaps that can be filled with normal acceptance tests
**Source**: Derived from PRE_LAUNCH_REVIEW.md verification

---

## Overview

This document lists **21 acceptance test gaps** that can be filled with standard `TestAcc*` tests running in parallel with existing tests. All tests listed here are:

✅ **Pure provider logic** - Not testing Terraform core or K8s API behavior
✅ **Runnable in parallel** - Use `t.Parallel()` for efficient execution
✅ **No special infrastructure** - No chaos testing, network manipulation, or cluster killing required

**Excluded from this list**:
- Chaos/failure testing (network failures, SIGINT/SIGKILL, etc.)
- Scale testing (100+ resources, 10MB manifests)
- Concurrency testing (parallel applies, race conditions)
- Tests that would primarily validate K8s API or Terraform core behavior

---

## Summary Statistics

| Priority | Count | Estimated Effort | Impact |
|----------|-------|------------------|--------|
| 🔴 High (Pre-launch) | 4 | 1 day | Critical for ADR validation |
| 🟡 Medium | 10 | 2-3 days | Important coverage gaps |
| 🟢 Lower | 7 | 2 days | Nice-to-have completeness |
| **TOTAL** | **21** | **5-6 days** | **Significant coverage improvement** |

---

## 🔴 High Priority (Pre-Launch)

These tests fill critical gaps in ADR validation and bootstrap scenarios.

### 1. apiVersion Identity Change

**ADR**: ADR-010 (Prevent Orphan Resources - Identity Changes)
**Current Status**: Only 3 of 4 identity fields tested (Kind, Name, Namespace)
**Gap**: apiVersion changes not tested

**Test Description**:
- Create a CRD with version v1
- Create a custom resource using v1
- Update CRD to add v1beta1, deprecate v1
- Change CR to use v1beta1 (apiVersion change)
- Verify old resource deleted, new resource created (replacement)

**Location**: `internal/k8sconnect/resource/object/lifecycle_test.go`
**Function**: `TestAccObjectResource_IdentityChange_ApiVersion`

**Why Critical**: Completes ADR-010 validation (currently 75%, should be 100%)

---

### 2. Unknown Connection Host (Bootstrap)

**ADR**: ADR-011 (Bootstrap-Aware Projection)
**Current Status**: Only deferred auth tested, not unknown host
**Gap**: Cluster doesn't exist scenario

**Test Description**:
- Set `cluster_connection.host = var.eks_endpoint` where EKS cluster is created in same apply
- During plan, host is "known after apply"
- Verify projection falls back to YAML (not dry-run)
- Verify plan succeeds without error
- Apply creates cluster, then creates resources

**Location**: `internal/k8sconnect/resource/object/basic_test.go`
**Function**: `TestAccObjectResource_UnknownConnectionHost`

**Why Critical**: Core bootstrap use case mentioned in ADR-011 but not validated

---

### 3. Unparseable YAML with Interpolations

**ADR**: ADR-011 (Bootstrap-Aware Projection)
**Current Status**: Not tested
**Gap**: YAML with `${...}` that can't be parsed at plan time

**Test Description**:
- Create YAML body with `name: ${random_uuid.id.result}` where random is computed
- At plan time, YAML is unparseable (contains literal `${...}`)
- Verify projection = unknown (graceful degradation)
- Verify no parse errors
- Apply succeeds with computed value

**Location**: `internal/k8sconnect/resource/object/interpolation_test.go`
**Function**: `TestAccObjectResource_UnparseableYAMLInterpolation`

**Why Critical**: Common pattern in bootstrap scenarios, ADR-011 mentions but not tested

---

### 4. Unknown ignore_fields During Plan

**ADR**: ADR-011 (Bootstrap-Aware Projection)
**Current Status**: All ignore_fields tests use known values
**Gap**: Computed ignore_fields not tested

**Test Description**:
- Set `ignore_fields = [var.dynamic_field]` where var is computed
- At plan time, ignore_fields is unknown
- Verify projection handles gracefully (unknown projection or fallback)
- Apply succeeds with computed ignore_fields

**Location**: `internal/k8sconnect/resource/object/ignore_fields_test.go`
**Function**: `TestAccObjectResource_IgnoreFieldsUnknown`

**Why Critical**: Third condition in ADR-011 smart projection logic, currently untested

---

## 🟡 Medium Priority

Important coverage gaps that improve robustness.

### 5. Service clusterIP Immutability

**ADR**: ADR-002 (Immutable Resources and Complex Deletions)
**Current Status**: PVC storage immutability tested, other immutable fields not tested
**Gap**: Service clusterIP is immutable in K8s

**Test Description**:
- Create Service with `spec.clusterIP: "10.96.0.100"` (explicit IP)
- Change to `spec.clusterIP: "10.96.0.200"`
- Verify provider detects immutability and triggers replacement
- Verify old Service deleted, new Service created

**Location**: `internal/k8sconnect/resource/object/lifecycle_test.go`
**Function**: `TestAccObjectResource_ImmutableFieldChange_ServiceClusterIP`

**Why Important**: Tests provider's immutability detection for common resource type

---

### 6. Job spec Immutability

**ADR**: ADR-002 (Immutable Resources and Complex Deletions)
**Current Status**: Only PVC tested
**Gap**: Job spec template is immutable

**Test Description**:
- Create Job with `spec.template.spec.restartPolicy: Never`
- Change to `spec.template.spec.restartPolicy: OnFailure`
- Verify provider triggers replacement
- Verify job recreated with new spec

**Location**: `internal/k8sconnect/resource/object/lifecycle_test.go`
**Function**: `TestAccObjectResource_ImmutableFieldChange_JobSpec`

**Why Important**: Jobs are common, spec immutability is a frequent issue

---

### 7. Update with ignore_fields Changes

**ADR**: ADR-009 (User-Controlled Drift Exemption)
**Current Status**: ignore_fields add/remove tested, not during UPDATE
**Gap**: Changing ignore_fields during update operation

**Test Description**:
- Create Deployment with `ignore_fields = ["spec.replicas"]`
- HPA externally modifies replicas to 5
- In same update, change image AND remove replicas from ignore_fields
- Verify ownership correctly reclaimed, replicas reverted to original
- Verify no plan/apply inconsistency errors

**Location**: `internal/k8sconnect/resource/object/ignore_fields_test.go`
**Function**: `TestAccObjectResource_UpdateWithIgnoreFieldsChange`

**Why Important**: Complex ownership transition scenario, potential for bugs

---

### 8. Update Triggering Immutable Field Recreation

**ADR**: ADR-002 (Immutable Resources)
**Current Status**: Immutable field changes tested, not during UPDATE
**Gap**: Update with both mutable and immutable changes

**Test Description**:
- Create PVC with `storage: 1Gi`, `labels: {env: dev}`
- Update to `storage: 2Gi`, `labels: {env: prod}` in same apply
- Verify replacement triggered by storage change
- Verify labels updated on new resource

**Location**: `internal/k8sconnect/resource/object/lifecycle_test.go`
**Function**: `TestAccObjectResource_UpdateTriggeringImmutableRecreation`

**Why Important**: Common scenario, tests interaction of mutable/immutable changes

---

### 9. Non-CRD Errors Fail Immediately

**ADR**: ADR-007 (CRD Dependency Resolution)
**Current Status**: CRD retry tested, but not non-CRD error path
**Gap**: Verify non-CRD errors don't trigger 30s retry

**Test Description**:
- Create ConfigMap with invalid YAML (not CRD error)
- Verify immediate failure (< 5 seconds)
- Verify no retry attempts in logs
- Verify clear error message

**Location**: `internal/k8sconnect/resource/object/crd_test.go`
**Function**: `TestAccObjectResource_NonCRDErrorFailsImmediately`

**Why Important**: Prevents 30s wait on simple errors, UX improvement

---

### 10. Kubeconfig File Path

**ADR**: N/A (Auth configuration)
**Current Status**: Kubeconfig raw tested, file path not tested
**Gap**: `cluster_connection.kubeconfig_path` attribute

**Test Description**:
- Write kubeconfig to temp file
- Use `cluster_connection = { kubeconfig_path = "/path/to/kubeconfig" }`
- Verify connection succeeds
- Create resource, verify it exists

**Location**: `internal/k8sconnect/resource/object/auth_test.go`
**Function**: `TestAccObjectResource_KubeconfigFilePath`

**Why Important**: Common configuration method, should be tested

---

### 11. Context Switching

**ADR**: N/A (Auth configuration)
**Current Status**: Single context tested
**Gap**: Multiple contexts in kubeconfig

**Test Description**:
- Create kubeconfig with two contexts (both pointing to same cluster for simplicity)
- Create resource with `context = "context-a"`
- Create resource with `context = "context-b"`
- Verify both succeed

**Location**: `internal/k8sconnect/resource/object/auth_test.go`
**Function**: `TestAccObjectResource_ContextSwitching`

**Why Important**: Multi-cluster/multi-context is core feature

---

### 12. Import Cluster-Scoped Resources

**ADR**: ADR-003 (Resource IDs)
**Current Status**: Namespace-scoped import tested
**Gap**: Cluster-scoped resource import

**Test Description**:
- Create ClusterRole manually with kubectl
- Import with `terraform import k8sconnect_object.cr "ClusterRole//my-role"`
- Verify namespace is empty in state
- Verify resource imported correctly

**Location**: `internal/k8sconnect/resource/object/import_test.go`
**Function**: `TestAccObjectResource_ImportClusterScoped`

**Why Important**: Cluster-scoped resources are common (Roles, PVs, etc.)

---

### 13. Import with Ownership Conflicts

**ADR**: ADR-004 (Cross-State Conflicts), ADR-005 (Field Ownership)
**Current Status**: Clean import tested
**Gap**: Import resource with different field manager

**Test Description**:
- Create ConfigMap with kubectl (field manager = "kubectl")
- Import with k8sconnect
- Verify force ownership takeover (manager = "k8sconnect")
- Verify no errors about ownership conflicts

**Location**: `internal/k8sconnect/resource/object/import_test.go`
**Function**: `TestAccObjectResource_ImportWithOwnershipConflict`

**Why Important**: Common scenario when adopting existing resources

---

### 14. Import with Custom Field Managers

**ADR**: ADR-014 (Patch Resource - previous_owners)
**Current Status**: Standard import tested
**Gap**: Import captures previous_owners for later patch use

**Test Description**:
- Create ConfigMap with custom field manager "helm"
- Import with k8sconnect
- Verify `previous_owners` NOT populated (only patch resource uses this)
- Verify import succeeds cleanly

**Location**: `internal/k8sconnect/resource/object/import_test.go`
**Function**: `TestAccObjectResource_ImportWithCustomFieldManager`

**Why Important**: Documents import behavior vs patch resource

---

## 🟢 Lower Priority

Nice-to-have tests for completeness.

### 15. Zero Timeout Behavior

**ADR**: ADR-008 (Selective Status Population)
**Current Status**: Normal timeouts tested
**Gap**: Edge case of timeout="0s"

**Test Description**:
- Create Deployment with `wait_for = { field = "status.replicas", timeout = "0s" }`
- Verify behavior (immediate check or sensible error)
- Document actual behavior

**Location**: `internal/k8sconnect/resource/object/wait_test.go`
**Function**: `TestAccObjectResource_WaitForZeroTimeout`

**Why Low Priority**: Edge case, but good to document

---

### 16. Resource Already Exists

**ADR**: ADR-015 (Actionable Error Messages)
**Current Status**: Not explicitly tested
**Gap**: Error UX when resource exists

**Test Description**:
- Create ConfigMap manually with kubectl
- Try to create same ConfigMap with Terraform (no import)
- Verify clear error message mentioning import
- Verify error is actionable

**Location**: `internal/k8sconnect/resource/object/basic_test.go`
**Function**: `TestAccObjectResource_ResourceAlreadyExists`

**Why Low Priority**: Error case, but good UX validation

---

### 17. Cluster-Scoped with Invalid Namespace (Verify Existing)

**ADR**: N/A
**Current Status**: Test exists at lifecycle_test.go:221
**Gap**: Verify comprehensive coverage

**Action**: Review existing `TestAccObjectResource_ClusterScopedWithInvalidNamespace` test
**Location**: `internal/k8sconnect/resource/object/lifecycle_test.go`

**Why Low Priority**: Already exists, just verify coverage

---

### 18. wait_for Chaining

**ADR**: ADR-008 (Selective Status Population)
**Current Status**: Single wait_for tested
**Gap**: Multiple resources with wait_for dependencies

**Test Description**:
- Create Deployment A with `wait_for.field = "status.readyReplicas"`
- Create Service B that `depends_on = [A]` and references A's status
- Verify ordering: A must complete wait before B starts
- Verify status available for reference

**Location**: `internal/k8sconnect/resource/object/wait_test.go`
**Function**: `TestAccObjectResource_WaitForChaining`

**Why Low Priority**: Complex scenario, but wait_for blocking is already tested

---

### 19. Context Hash Stability

**ADR**: ADR-003 (Resource IDs)
**Current Status**: Not explicitly tested
**Gap**: Resource ID determinism

**Test Description**:
- Create ConfigMap with config A
- Note resource ID
- Destroy
- Create with config B (same values, different YAML formatting)
- Verify same resource ID (same context hash)

**Location**: `internal/k8sconnect/resource/object/basic_test.go`
**Function**: `TestAccObjectResource_ContextHashStability`

**Why Low Priority**: Important for ID stability, but likely works

---

### 20. Invalid Field Path (Verify Existing)

**ADR**: ADR-008 (Selective Status Population)
**Current Status**: Test exists
**Gap**: Verify comprehensive coverage

**Action**: Review existing wait_for validation tests
**Location**: `internal/k8sconnect/resource/object/wait_test.go`

**Why Low Priority**: Already tested, just verify

---

### 21. Partial Merge Key Matching

**ADR**: ADR-005 (Field Ownership Strategy)
**Current Status**: Full merge keys tested
**Gap**: Partial key matching (K8s adds defaults)

**Test Description**:
- User YAML: `ports: [{port: 80}]` (no protocol specified)
- K8s adds default: `protocol: TCP`
- Later, user updates port to 8080
- Verify field matching works (finds correct port despite partial key)
- Verify ownership tracking correct

**Location**: `internal/k8sconnect/resource/object/field_ownership_test.go`
**Function**: `TestAccObjectResource_PartialMergeKeyMatching`

**Why Low Priority**: Advanced edge case in strategic merge patch

---

## Implementation Guide

### Running Tests

```bash
# Run individual test
TEST=TestAccObjectResource_IdentityChange_ApiVersion make testacc

# Run all new high-priority tests (once implemented)
TEST="TestAccObjectResource_IdentityChange_ApiVersion|UnknownConnectionHost|UnparseableYAML|IgnoreFieldsUnknown" make testacc

# Run all tests in category
TEST=TestAccObjectResource_IdentityChange make testacc  # Runs all identity tests
```

### Test Pattern

All tests should follow this pattern:

```go
func TestAccObjectResource_NewTest(t *testing.T) {
    t.Parallel()  // Always run in parallel

    raw := os.Getenv("TF_ACC_KUBECONFIG")
    if raw == "" {
        t.Fatal("TF_ACC_KUBECONFIG must be set")
    }

    ns := fmt.Sprintf("new-test-ns-%d", time.Now().UnixNano()%1000000)
    k8sClient := testhelpers.CreateK8sClient(t, raw)

    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
            "k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
        },
        Steps: []resource.TestStep{
            // Test steps here
        },
        CheckDestroy: // Cleanup check
    })
}
```

### Tracking Progress

Update this document as tests are added:
- Change ⬜ to ✅ when test implemented
- Note any findings or deviations from expected behavior
- Update effort estimates if different than predicted

---

## Benefits of Completing These Tests

**Coverage improvements**:
- ADR-010: 75% → 100% (identity changes)
- ADR-011: 60% → 90% (bootstrap scenarios)
- ADR-002: 70% → 85% (immutable fields)
- ADR-009: 95% → 98% (ignore_fields edge cases)
- Overall acceptance test count: 65 → 86 (+32%)

**Risk reduction**:
- Bootstrap scenarios fully validated
- Identity change detection complete
- Import edge cases covered
- Update operation coverage improved

**Time investment vs. value**:
- 5-6 days effort
- No special infrastructure needed
- High confidence increase for launch
- Documentation of edge case behavior

---

## Notes

- All tests use isolated namespaces (no conflicts between parallel runs)
- k3d cluster in CI is sufficient (no cloud resources needed)
- Tests document provider behavior in edge cases
- Some "lower priority" items are verifying existing tests - quick wins

---

## Related Documents

- **PRE_LAUNCH_REVIEW.md**: Full pre-launch review with all gaps (including chaos/scale)
- **ADRs**: See individual ADRs for architectural context on each test
- **CLAUDE.md**: Build and test commands
