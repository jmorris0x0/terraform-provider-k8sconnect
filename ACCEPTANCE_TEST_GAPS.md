# Acceptance Test Gaps

**Date**: 2025-10-17 (verified and updated)
**Status**: Actionable test gaps that can be filled with normal acceptance tests
**Source**: Derived from PRE_LAUNCH_REVIEW.md verification, verified against actual tests

---

## Overview

This document lists **14 acceptance test gaps** that can be filled with standard `TestAcc*` tests running in parallel with existing tests. All tests listed here are:

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
| 🔴 High (Pre-launch) | 1 | ~0.5 day | Critical for ADR validation |
| 🟡 Medium | 9 | ~2 days | Important coverage gaps |
| 🟢 Lower | 4 | ~1.5 days | Nice-to-have completeness |
| **TOTAL** | **14** | **~4 days** | **Completes pre-launch validation** |

---

## 🔴 High Priority (Pre-Launch)

These tests fill critical gaps in ADR validation and bootstrap scenarios.

### 1. Unknown Connection Host (Bootstrap)

**ADR**: ADR-011 (Bootstrap-Aware Projection)
**Current Status**: Not tested (existing `TestAccObjectResource_ConnectionWithVariable` only tests known values)
**Gap**: Cluster doesn't exist scenario with unknown host

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

### 2. Unknown ignore_fields During Plan

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

### 3. Service clusterIP Immutability

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

### 4. Job spec Immutability

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

### 5. Update with ignore_fields Changes

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

### 6. Update Triggering Immutable Field Recreation

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

### 7. Non-CRD Errors Fail Immediately

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

### 8. Context Switching

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

### 9. Import with Ownership Conflicts

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

### 10. Import with Custom Field Managers

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

### 11. Zero Timeout Behavior

**ADR**: ADR-008 (Selective Status Population)
**Current Status**: Normal timeouts tested
**Gap**: Edge case of timeout="0s"

**Test Description**:
- Create Deployment with `wait_for = { field = "status.replicas", timeout = "0s" }`
- Verify behavior (immediate check or sensible error)
- Document actual behavior

**Location**: `internal/k8sconnect/resource/wait/wait_test.go`
**Function**: `TestAccWaitResource_ZeroTimeout`

**Why Low Priority**: Edge case, but good to document

---

### 12. Wait Resource Chaining

**ADR**: ADR-008 (Selective Status Population)
**Current Status**: Single wait resource tested
**Gap**: Multiple wait resources with dependencies and status chaining

**Test Description**:
- Create object A with k8sconnect_object
- Create wait resource B with `wait_for.field = "status.loadBalancer.ingress"` for object A
- Create object C that references wait B's status output (e.g., `k8sconnect_wait.b.status.loadBalancer.ingress[0].ip`)
- Verify ordering: A → wait B completes → C references B's status
- Verify status available for reference in dependent resources

**Location**: `internal/k8sconnect/resource/wait/wait_test.go`
**Function**: `TestAccWaitResource_Chaining`

**Why Low Priority**: Complex scenario, but wait blocking and status output are already tested separately

---

### 13. Context Hash Stability

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

### 14. Partial Merge Key Matching

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

