# Manifest Resource Maturity Patterns
## Adoption Roadmap for Patch Resource & Datasource

**Date**: 2025-01-12
**Status**: Living Document
**Author**: Deep-dive analysis comparing manifest resource (production-grade) with patch resource and datasource

---

## Executive Summary

**Code Metrics:**
- **Manifest**: 40 files, 14,755 LOC, 20 test files — **Production-grade maturity**
- **Patch**: 7 files, 5,185 LOC, 3 test files — **Beta-level maturity**
- **Datasource**: 3 files, 444 LOC, 1 test file — **Alpha-level maturity**

The manifest resource embodies **years of battle-tested patterns** that significantly improve user experience, reliability, and debuggability. This document identifies 11 patterns and provides a prioritized adoption roadmap.

---

## Pattern Catalog

### CRITICAL PRIORITY (Adopt Immediately)

#### 1. ✅ Error Classification [COMPLETED]
**Status**: Already done in this session (2025-01-12)

**What it is**: Universal use of `k8serrors.AddClassifiedError()` for all Kubernetes API interactions

**Manifest implementation**:
```go
// Common pattern throughout CRUD operations
k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Read Resource", resourceDesc)
```

**Adoption status**:
- ✅ Manifest: Full coverage (internal/k8sconnect/resource/manifest/crud.go)
- ✅ Patch: Added in refactoring session (internal/k8sconnect/resource/patch/crud.go)
- ✅ Datasource: Added in refactoring session (internal/k8sconnect/datasource/resource/resource.go)

**Benefits**:
- User-friendly error messages with resolution steps
- Categorizes errors: NotFound, Forbidden, Conflict, Timeout, CRD not found, etc.
- Provides actionable guidance (e.g., "add to ignore_fields" for conflicts)

---

#### 2. ✅ API Warning Surfacing [COMPLETED]
**Status**: Completed 2025-01-12

**What it is**: `surfaceK8sWarnings()` after every K8s API call to show deprecation warnings, policy violations, etc.

**Manifest implementation** (manifest/crud.go:410-422):
```go
// Helper function
func surfaceK8sWarnings(ctx context.Context, client k8sclient.K8sClient, diagnostics *diag.Diagnostics) {
    warnings := client.GetWarnings()
    for _, warning := range warnings {
        diagnostics.AddWarning(
            "Kubernetes API Warning",
            fmt.Sprintf("The Kubernetes API server returned a warning:\n\n%s", warning),
        )
        tflog.Warn(ctx, "Kubernetes API warning", map[string]interface{}{
            "warning": warning,
        })
    }
}
```

**Why it matters**:
- Kubernetes API can return warnings about:
  - Deprecated APIs (e.g., "v1beta1 is deprecated, use v1")
  - Admission policy violations
  - Resource quota warnings
  - Security policy alerts
- Users need to see these warnings BEFORE resources break in future K8s versions
- Without this, users are blind to upcoming breaking changes

**Example user impact**:
```
Warning: Kubernetes API Warning

The Kubernetes API server returned a warning:

networking.k8s.io/v1beta1 Ingress is deprecated in v1.19+, unavailable in v1.22+; use networking.k8s.io/v1 Ingress
```

**Adoption status**:
- ✅ Manifest: Full coverage (internal/k8sconnect/resource/manifest/crud.go)
- ✅ Patch: Added to all CRUD operations (internal/k8sconnect/resource/patch/crud.go)
  - Create: After Get (line 69), After Patch (line 104)
  - Read: After Get (line 176)
  - Update: After Get (line 254), After Patch (line 265)
  - Delete: After Get (line 335), After Get for GVR (line 362), After Get in loop (line 388), After ownership transfer (line 403)
- ✅ Datasource: Added to Read operation (internal/k8sconnect/datasource/resource/resource.go:181)

**Actual effort**: 2 hours (as estimated)

---

### HIGH PRIORITY (Significant UX/Reliability Improvements)

#### 3. ✅ Comprehensive Config Validators [COMPLETED]
**Status**: Completed 2025-01-12

**What it is**: Resource-level validation beyond schema constraints

**Manifest validators** (manifest/validators.go - 473 lines):

**Implemented validators**:
1. `clusterConnectionValidator` - Ensures exactly one connection mode (inline vs kubeconfig)
2. `execAuthValidator` - Validates exec auth has required fields (api_version, command)
3. `conflictingAttributesValidator` - Prevents `delete_protection=true` + `force_destroy=true`
4. `requiredFieldsValidator` - Validates YAML structure, checks for interpolations
5. `jsonPathValidator` - Validates JSONPath syntax for `wait_for.field`
6. `jsonPathMapKeysValidator` - Validates JSONPath in `wait_for.field_value` map keys
7. `ignoreFieldsValidator` - Blocks attempts to ignore provider internal annotations
8. `serverManagedFieldsValidator` - Prevents status/managedFields/resourceVersion in yaml_body

**Validator patterns**:
```go
// Resource-level validators (run during plan)
func (r *manifestResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
    return []resource.ConfigValidator{
        &clusterConnectionValidator{},
        &execAuthValidator{},
        &conflictingAttributesValidator{},
        &requiredFieldsValidator{},
    }
}

// Field-level validators (in schema)
"wait_for": schema.SingleNestedAttribute{
    Attributes: map[string]schema.Attribute{
        "field": schema.StringAttribute{
            Validators: []validator.String{
                jsonPathValidator{},
            },
        },
    },
}
```

**Why they matter**:
- **Fail fast**: Catch errors during plan, not apply
- **Better error messages**: Explain what's wrong and how to fix it
- **Prevent common mistakes**: Block copy-pasted `kubectl get -o yaml` output
- **Security**: Prevent accidental exposure of internal annotations

**Patch validators** (patch/validators.go - 264 lines):

**Currently implemented**:
1. ✅ `patchClusterConnectionValidator` - Connection mode validation
2. ✅ `patchExecAuthValidator` - Exec auth validation
3. ✅ `takeOwnershipValidator` - Requires `take_ownership=true` acknowledgment

**Patch validators** (patch/validators.go - 264 lines):

**Currently implemented**:
1. ✅ `patchClusterConnectionValidator` - Connection mode validation
2. ✅ `patchExecAuthValidator` - Exec auth validation
3. ✅ `takeOwnershipValidator` - Requires `take_ownership=true` acknowledgment
4. ✅ **JSONPath validation** for `wait_for` fields (ADDED 2025-01-12)
   - `wait_for.field` now validates JSONPath syntax
   - `wait_for.field_value` map keys validated as JSONPath
   - Uses common validators from `internal/k8sconnect/common/validators`

**Still missing from patch**:
5. ❌ **Patch content syntax validation** (Future enhancement)
   - `patch` field should validate YAML/JSON syntax
   - `json_patch` should validate as JSON array
   - `merge_patch` should validate as JSON object
   - Currently accepts any string, fails at apply time

**Datasource validators** (datasource/resource/validators.go):

**Currently implemented**:
1. ✅ `resourceDSClusterConnectionValidator` - Connection mode validation
2. ✅ `resourceDSExecAuthValidator` - Exec auth validation

**Note**: Datasource validators were already implemented (documented incorrectly as missing).

**Implementation Summary**:

**What was done (2025-01-12)**:
1. ✅ Created `internal/k8sconnect/common/validators/jsonpath.go`
2. ✅ Moved `JSONPath` validator from manifest to common package
3. ✅ Moved `JSONPathMapKeys` validator from manifest to common package
4. ✅ Updated manifest to import validators from common
5. ✅ Added `validators.JSONPath{}` to patch `wait_for.field`
6. ✅ Added `validators.JSONPathMapKeys{}` to patch `wait_for.field_value`
7. ✅ All tests passing (unit + acceptance + examples)

**Future enhancement (Optional)**:
- Patch content syntax validation (YAML/JSON parsing, RFC 6902/7386 validation)
- Estimated effort: 4 hours

**Actual effort**: 2 hours (as estimated for JSONPath validators)

---

#### 4. 🟡 Private State Flags for Recovery Patterns [MISSING FROM PATCH]

**What it is**: Using private state to track transient failures and enable graceful recovery (ADR-006)

**Manifest implementation** (manifest/crud.go:326-408):

**Flags used**:
```go
// "pending_projection" - Projection calculation failed, retry on next apply
// "imported_without_annotations" - Imported resource needs annotation on first update
```

**Helper functions**:
```go
// Read flags
func checkPendingProjectionFlag(ctx context.Context, getter interface{...}) bool

// Set flags
func setPendingProjectionFlag(ctx context.Context, setter interface{...})

// Clear flags
func clearPendingProjectionFlag(ctx context.Context, setter interface{...})

// Handle failures
func handleProjectionFailure(ctx, rc, privateSetter, stateSetter, diagnostics, operation, err)

// Handle success
func handleProjectionSuccess(ctx, hasPendingProjection, privateSetter, operation)
```

**Recovery pattern flow**:

**Create/Update operation**:
1. Apply resource successfully
2. Try to calculate projection from managedFields
3. **If projection fails** (network timeout, API error):
   - Save state with empty projection
   - Set `pending_projection=true` flag in private state
   - Return error to fail CI/CD (forces retry)

**Read operation (terraform refresh)**:
1. Check for `pending_projection` flag
2. If found, log "attempting recovery"
3. Try projection calculation again
4. **If succeeds**: Clear flag, update state
5. **If fails**: Keep flag, log warning, continue refresh

**Next Update operation**:
1. Check for `pending_projection` flag
2. If found, log "retrying pending projection"
3. After apply, calculate projection
4. Clear flag on success

**Why it matters**:
- **Prevents orphaned resources**: Resource is created even if projection fails
- **Graceful degradation**: Temporary network issues don't break workflows
- **Opportunistic recovery**: Next refresh/apply automatically fixes it
- **Clear user feedback**: Error message explains what happened and how to recover

**Example error message**:
```
Error: Projection Calculation Failed

Resource was created successfully but projection calculation failed: connection timeout

This is typically caused by network issues. Run 'terraform apply' again to complete the operation.
```

**Patch equivalent scenarios**:

The patch resource has similar transient failure scenarios:
1. Patch applied successfully, but reading `managed_fields` fails
2. Field ownership calculation fails due to API timeout
3. Wait condition succeeds but reading final state fails

**Missing from patch**:
- ❌ No private state flags
- ❌ No recovery patterns
- ❌ Failures leave state incomplete without retry mechanism

**Action Required**:

1. Identify transient failure points in patch CRUD:
   - `managed_fields` calculation (patch/crud.go)
   - `field_ownership` extraction
   - `previous_owners` tracking

2. Implement private state helpers:
   ```go
   // patch/crud.go
   func checkPendingFieldsFlag(ctx context.Context, getter ...) bool
   func setPendingFieldsFlag(ctx context.Context, setter ...)
   func clearPendingFieldsFlag(ctx context.Context, setter ...)
   ```

3. Add recovery pattern to Create/Update:
   ```go
   // After successful patch
   if err := extractManagedFields(obj); err != nil {
       handleFieldsFailure(ctx, rc, resp.Private, &resp.State, &resp.Diagnostics, err)
       return
   }
   ```

4. Add opportunistic recovery to Read:
   ```go
   hasPendingFields := checkPendingFieldsFlag(ctx, req.Private)
   if hasPendingFields {
       tflog.Info(ctx, "Attempting recovery of pending field extraction")
       // Try extraction again
   }
   ```

**Estimated effort**: 2 days

**Testing requirements**:
- Simulate network timeout during field extraction
- Verify state is saved with flag
- Verify next apply retries successfully
- Verify refresh attempts recovery

---

#### 5. 🟡 Test Coverage Gaps [CRITICAL FOR STABILITY]

**Manifest test coverage** (20 test files):

**Organized by concern**:

**Core functionality**:
- `basic_test.go` - CRUD operations, namespaced and cluster-scoped resources
- `lifecycle_test.go` - Edge cases (delete protection, force_destroy, etc.)
- `import_test.go` - Import scenarios (new, already-managed, conflicts)

**Advanced features**:
- `drift_test.go` - Drift detection, external changes, false positives
- `ignore_fields_test.go` - Field ignoring, ownership release
- `field_ownership_test.go` - Ownership tracking, conflicts, takeover
- `wait_test.go` - All wait types (field, condition, rollout, timeout)
- `crd_test.go` - CRD scenarios (not found, waiting for establishment)
- `cluster_scoped_test.go` - Cluster-scoped resources (namespace handling)

**Edge cases & data handling**:
- `quantity_test.go` - Kubernetes quantity normalization (500m vs 0.5)
- `projection_test.go` - Projection calculation accuracy
- `identity_changes_test.go` - Identity change detection (requires replacement)
- `field_parsing_test.go` - Field path parsing edge cases
- `interpolation_test.go` - Terraform interpolation handling
- `formatting_test.go` - YAML formatting preservation
- `status_pruner_test.go` - Status field handling

**Authentication & connection**:
- `auth_test.go` - Authentication modes (token, exec, client cert, kubeconfig)
- `token_refresh_test.go` - Token expiration and refresh

**Unit tests**:
- `validators_test.go` - Validator logic without cluster
- `unit_test.go` - Pure unit tests (parsing, helpers)
- `field_ownership_unit_test.go` - Field ownership parsing logic

**Patch test coverage** (3 test files):
- `patch_test.go` - Basic patching operations
- `patch_advanced_test.go` - Advanced scenarios (multiple patches, conflicts)
- `patch_unit_test.go` - Unit tests

**Datasource test coverage** (1 test file):
- `resource_test.go` - Basic read operations

---

**Missing test scenarios for Patch** (Priority order):

**P0 - Critical missing tests**:
1. ❌ **Authentication mode tests** (HIGH RISK - auth failures in production)
   - Token authentication
   - Exec authentication
   - Client certificate authentication
   - Kubeconfig with multiple contexts
   - Invalid credentials scenarios

2. ❌ **Error classification tests** (RECENTLY ADDED - needs verification)
   - Verify classified errors for all K8s error types
   - Forbidden (RBAC) errors
   - NotFound errors
   - Conflict errors
   - Timeout errors

**P1 - Important functionality tests**:
3. ❌ **Wait condition tests**
   - `wait_for.field` - Wait for field to exist
   - `wait_for.field_value` - Wait for specific value
   - `wait_for.condition` - Wait for condition=True
   - `wait_for.rollout` - Wait for deployment rollout
   - Timeout scenarios
   - Invalid JSONPath handling

4. ❌ **Field ownership conflict tests**
   - Patch field owned by another controller
   - Verify `previous_owners` tracking
   - Verify `field_ownership` after patch
   - Multiple controllers fighting for same field

5. ❌ **Multi-cluster scenarios**
   - Patch resources in different clusters
   - Different credentials per patch
   - Connection failures

**P2 - Edge cases & validation**:
6. ❌ **Validator tests**
   - Cluster connection validator
   - Exec auth validator
   - take_ownership validator
   - JSONPath validator (when added)
   - Patch content validator (when added)

7. ❌ **CRD patching tests**
   - Patch custom resources
   - CRD not found scenarios
   - CRD being established

8. ❌ **Drift detection tests**
   - Detect external changes to patched fields
   - Verify drift in managed_fields
   - Ignore drift in non-patched fields

9. ❌ **Patch type tests**
    - Strategic merge patch
    - JSON patch (RFC 6902)
    - Merge patch (RFC 7386)
    - Invalid patch content
    - Mixing patch types (should error)

---

**Missing test scenarios for Datasource** (Priority order):

**P0 - Critical missing tests**:
1. ❌ **Authentication mode tests**
   - All auth modes (token, exec, client cert, kubeconfig)
   - Invalid credentials
   - Expired tokens

2. ❌ **Error classification tests**
   - NotFound handling
   - Forbidden errors
   - Connection errors
   - Invalid resource types

**P1 - Important functionality tests**:
3. ❌ **Multi-cluster reads**
   - Read from different clusters
   - Different credentials per datasource

4. ❌ **Resource type discovery tests**
   - Standard resources (Pod, Service, etc.)
   - Custom resources
   - Invalid kinds
   - GVR resolution errors

5. ❌ **Namespace handling tests**
   - Namespaced resources
   - Cluster-scoped resources
   - Invalid namespace
   - Namespace not found

**P2 - Edge cases**:
6. ❌ **Validator tests** (when validators added)
   - Connection validation
   - Exec auth validation

7. ❌ **CRD reading tests**
   - Read custom resources
   - CRD not found

8. ❌ **Output format tests**
   - Verify manifest (JSON) output
   - Verify yaml_body output
   - Verify object (dynamic) output

---

**Action Required**:

**For patch resource** (Prioritized):
1. **Week 1**: Authentication & error classification tests (P0)
   - Write auth_test.go covering all modes
   - Write error_classification_test.go
   - Run against real cluster with various error scenarios

2. **Week 2**: Core functionality tests (P1)
   - Expand patch_test.go with wait conditions
   - Write field_ownership_test.go
   - Write multi_cluster_test.go

3. **Week 3**: Edge cases & validators (P2)
   - Write validator_test.go
   - Write crd_test.go
   - Write drift_test.go
   - Write patch_types_test.go

**For datasource** (Prioritized):
1. **Week 1**: Auth & error tests (P0)
   - Expand resource_test.go with auth modes
   - Add error classification tests

2. **Week 2**: Core functionality (P1)
   - Multi-cluster tests
   - Resource type discovery tests
   - Namespace handling tests

3. **Week 3**: Edge cases (P2)
   - Validator tests (after validators added)
   - CRD tests
   - Output format tests

**Estimated effort**:
- Patch tests: 3 weeks (parallel with feature development)
- Datasource tests: 1-2 weeks

**Success criteria**:
- All P0 tests passing before next release
- Test coverage > 70% for patch, > 80% for datasource
- No untested auth modes in production

---

### MEDIUM PRIORITY (Quality of Life Improvements)

#### 6. ✅ YAML Validation Enhancements [COMPLETED]
**Status**: Completed 2025-01-12

**What manifest has** (manifest/yaml.go):

**Multi-document detection**:
```go
func isMultiDocumentYAML(yamlStr string) bool {
    // Uses yaml.NewYAMLOrJSONDecoder to properly detect "---" separators
    // Prevents confusing errors when users provide multi-doc YAML
}
```

**Container name validation** (critical for strategic merge):
```go
func validateContainerNames(obj *unstructured.Unstructured) error {
    // Validates containers at multiple paths:
    // - spec.containers
    // - spec.initContainers
    // - spec.template.spec.containers
    // - spec.template.spec.initContainers

    // Strategic merge REQUIRES container names or it fails silently
}
```

**Server-managed field detection**:
```go
type serverManagedFieldsValidator struct{}

// Blocks these fields in yaml_body:
var serverManagedMetadataFields = []string{
    "uid",
    "resourceVersion",
    "generation",
    "creationTimestamp",
    "managedFields",
}

// Also blocks:
// - status field (read-only subresource)
// - k8sconnect.terraform.io/* annotations (provider internal)
```

**Clean export for state**:
```go
func (r *manifestResource) cleanObjectForExport(obj *unstructured.Unstructured) *unstructured.Unstructured {
    // Removes server-managed fields before storing in state
    // Ensures yaml_body is clean and reusable
}
```

**Why these matter**:

**Container names are critical**:
```yaml
# ❌ This will fail silently with strategic merge patch
spec:
  containers:
  - image: nginx:latest  # Missing name!

# ✅ This works correctly
spec:
  containers:
  - name: nginx  # Strategic merge uses this as merge key
    image: nginx:latest
```

**Server-managed fields cause confusion**:
```yaml
# User copies from kubectl get -o yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: example
  uid: abc-123  # ❌ Server-managed
  resourceVersion: "12345"  # ❌ Server-managed
  creationTimestamp: "2024-01-01T00:00:00Z"  # ❌ Server-managed
data:
  key: value
```

Without validation, this YAML is accepted, causes apply errors, user is confused why "valid" YAML fails.

**Multi-document YAML is common mistake**:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: first
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: second
```

Manifest resource rejects this with clear message pointing to yaml_split datasource.

---

**Patch equivalent**:

**What patch currently has**:
- Basic patch content parsing (YAML/JSON)
- Type detection (strategic merge vs JSON patch vs merge patch)

**Missing from patch**:
1. ❌ **Strategic merge container validation**
   - Patch resource uses strategic merge by default
   - No validation that containers have names
   - Results in silent failures or unexpected behavior

2. ❌ **Server-managed field detection**
   - Users might try to patch `resourceVersion` or `uid`
   - These patches will be rejected by K8s but error is cryptic
   - Should warn during plan phase

3. ❌ **Provider annotation detection**
   - Attempting to patch `k8sconnect.terraform.io/*` annotations
   - Could interfere with resource tracking
   - Should be blocked with clear error

**Implementation completed** (2025-01-12):

1. ✅ **Created comprehensive validators** (`internal/k8sconnect/common/validators/patch.go`):
   - `StrategicMergePatch` - Validates container names, server-managed fields, provider annotations, status
   - `JSONPatchValidator` - Validates RFC 6902 JSON Patch operations structure
   - `MergePatchValidator` - Validates RFC 7386 JSON Merge Patch structure

2. ✅ **Wired into schema** (patch/patch.go):
   ```go
   "patch": schema.StringAttribute{
       Validators: []validator.String{
           validators.StrategicMergePatch{},
       },
   }
   "json_patch": schema.StringAttribute{
       Validators: []validator.String{
           validators.JSONPatchValidator{},
       },
   }
   "merge_patch": schema.StringAttribute{
       Validators: []validator.String{
           validators.MergePatchValidator{},
       },
   }
   ```

3. ✅ **Comprehensive unit tests** (`internal/k8sconnect/common/validators/patch_test.go`):
   - 17 test cases for StrategicMergePatch
   - 20 test cases for JSONPatchValidator
   - 16 test cases for MergePatchValidator
   - 14 test cases for helper functions
   - All tests passing

**Actual effort**: Already completed prior to session

**Benefits achieved**:
- ✅ Fail fast with helpful errors during plan phase
- ✅ Prevents common copy-paste mistakes
- ✅ Educates users about strategic merge requirements
- ✅ Educates users about RFC 6902/7386 requirements
- ✅ Reduces support burden

---

#### 7. 🟢 ModifyPlan Implementation for Patch [SIGNIFICANT FEATURE]

**What it is**: Terraform plan modifier that performs dry-run to show accurate diffs BEFORE apply

**Manifest implementation** (manifest/plan_modifier.go - 722 lines):

**What it does during `terraform plan`**:
1. **Pre-validation**:
   - Checks if YAML is parseable
   - Checks if connection is ready (not "known after apply")
   - Checks if ignore_fields is known
   - Parses desired object from yaml_body

2. **Identity change detection**:
   - Compares plan vs state to detect immutable field changes
   - Triggers automatic replacement if needed
   - Adds helpful warning explaining why replacement is needed

3. **Dry-run execution**:
   - Creates K8s client from connection
   - Performs server-side apply with DryRun=true
   - Gets back prediction of what K8s will do

4. **Projection calculation**:
   - Extracts fields we'll own from managedFields
   - Filters by ignore_fields
   - Creates flat key-value projection
   - Shows accurate diff in plan output

5. **Drift detection**:
   - Compares new projection with state projection
   - Detects external changes
   - Preserves formatting if no actual changes

6. **Field ownership analysis**:
   - Extracts predicted ownership after apply
   - Compares with current ownership
   - Warns about ownership conflicts
   - Shows which controllers will fight back

7. **Status field handling**:
   - Complex logic for wait_for configuration
   - Determines if status will be tracked
   - Handles transitions (wait_for added/removed/changed)

**Key functions**:
```go
func (r *manifestResource) ModifyPlan(ctx, req, resp) {
    // Main orchestration
}

func (r *manifestResource) executeDryRunAndProjection(...) bool {
    // Performs dry-run and calculates projection
}

func (r *manifestResource) checkFieldOwnershipConflicts(...) {
    // Warns about ownership battles
}

func (r *manifestResource) checkResourceIdentityChanges(...) bool {
    // Triggers replacement for immutable fields
}
```

**Why it matters**:

**Without ModifyPlan (current patch behavior)**:
```
$ terraform plan

Terraform will perform the following actions:

  # k8sconnect_patch.example will be updated
  ~ patch = <<-EOT
      - old patch content
      + new patch content
    EOT

Plan: 0 to add, 1 to change, 0 to destroy.
```
User doesn't know:
- Which fields will actually change
- What K8s defaults will be applied
- If there are ownership conflicts
- If the patch is valid

**With ModifyPlan (manifest behavior)**:
```
$ terraform plan

Terraform will perform the following actions:

  # k8sconnect_manifest.example will be updated in-place
  ~ managed_state_projection = {
      + "spec.replicas"              = "3"
      - "spec.replicas"              = "1"
      ~ "spec.template.spec.containers[0].image" = "nginx:1.20" -> "nginx:1.21"
    }
  ~ field_ownership = {
      + "spec.replicas" = "k8sconnect"
      - "spec.replicas" = "horizontal-pod-autoscaler"
    }

Warning: Field Ownership Override

Forcing ownership of fields managed by other controllers:
  - spec.replicas (owned by horizontal-pod-autoscaler)

These fields will be forcibly taken over. The other controllers may fight back.
Consider adding these paths to ignore_fields to release ownership instead.

Plan: 0 to add, 1 to change, 0 to destroy.
```

User sees:
- ✅ Exact fields that will change
- ✅ Old vs new values
- ✅ Ownership changes
- ✅ Conflicts with other controllers
- ✅ Actionable warnings

---

**What this would mean for patch resource**:

**Patch-specific dry-run logic**:
1. Fetch target resource
2. Apply patch with DryRun=true
3. Extract which fields were modified
4. Calculate predicted field ownership
5. Show diff in managed_fields
6. Warn about ownership takeover

**Challenges for patch**:
- Multiple patch types (strategic merge, JSON patch, merge patch)
- JSON patch is sequential operations (harder to predict)
- Strategic merge needs merge key resolution
- Must handle target resource not found

**Benefits**:
- Users see what patch will do before apply
- Ownership conflicts detected during plan
- Invalid patches caught early
- Better CI/CD integration (plan shows real changes)

**Implementation approach**:

1. **Create plan_modifier.go** for patch resource
2. **Implement ResourceWithModifyPlan interface**:
   ```go
   func (r *patchResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
       // Skip if destroy
       if req.Plan.Raw.IsNull() {
           return
       }

       var data patchResourceModel
       resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

       // Check connection ready
       if !r.isConnectionReady(data.ClusterConnection) {
           return
       }

       // Execute dry-run patch
       if !r.executeDryRunPatch(ctx, req, &data, resp) {
           return
       }

       // Check ownership conflicts
       r.checkPatchOwnershipConflicts(ctx, req, &data, resp)

       // Save modified plan
       resp.Plan.Set(ctx, &data)
   }
   ```

3. **Dry-run patch execution**:
   ```go
   func (r *patchResource) executeDryRunPatch(...) bool {
       // 1. Get target resource
       // 2. Determine patch type
       // 3. Apply patch with DryRun=true
       // 4. Extract managed fields for patched paths
       // 5. Calculate predicted field_ownership
       // 6. Update plan with predictions
   }
   ```

4. **Ownership conflict detection**:
   ```go
   func (r *patchResource) checkPatchOwnershipConflicts(...) {
       // Compare predicted ownership with current state
       // Warn if taking ownership from other controllers
       // Show previous_owners transitions
   }
   ```

**Estimated effort**: 1-2 weeks

**Complexity factors**:
- HIGH: Need to handle all patch types differently
- HIGH: JSON patch requires sequential operation simulation
- MEDIUM: Strategic merge needs merge key resolution
- MEDIUM: Must handle target not found gracefully
- LOW: Can reuse connection/client setup from manifest

**Phased approach**:
1. **Phase 1** (1 week): Basic dry-run for strategic merge patches
2. **Phase 2** (3 days): Add ownership conflict detection
3. **Phase 3** (2 days): Support JSON patch and merge patch
4. **Phase 4** (2 days): Handle edge cases (target not found, CRD, etc.)

**Success criteria**:
- terraform plan shows predicted field changes
- Ownership conflicts are warned during plan
- Invalid patches fail during plan, not apply
- All patch types supported

---

#### 8. 🟢 Identity Change Detection [MISSING FROM PATCH]

**What it is**: Automatic replacement when immutable fields change (ADR-010, ADR-002)

**Manifest implementation** (manifest/plan_modifier.go:336-369):

**How it works**:
```go
func (r *manifestResource) performDryRun(...) (*unstructured.Unstructured, error) {
    dryRunResult, err := client.DryRunApply(ctx, objToApply, k8sclient.ApplyOptions{
        FieldManager: "k8sconnect",
        Force:        true,
    })

    if err != nil {
        // Check if this is an immutable field error
        if r.isImmutableFieldError(err) {
            immutableFields := r.extractImmutableFields(err)

            tflog.Info(ctx, "Immutable field changed, triggering replacement",
                map[string]interface{}{
                    "resource": resourceDesc,
                    "fields":   immutableFields,
                })

            // Mark resource for replacement
            resp.RequiresReplace = append(resp.RequiresReplace, path.Root("yaml_body"))

            // Add informative warning
            resp.Diagnostics.AddWarning(
                "Immutable Field Changed - Replacement Required",
                fmt.Sprintf("Cannot modify immutable field(s): %v on %s\n\n"+
                    "Immutable fields cannot be changed after resource creation.\n"+
                    "Terraform will delete the existing resource and create a new one.\n\n"+
                    "This is the correct behavior - Kubernetes does not allow these fields to be modified in-place.",
                    immutableFields, resourceDesc))

            // Set projection to unknown (replacement doesn't need projection)
            plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)

            // Return nil error - replacement is not a failure
            return nil, nil
        }

        // Other errors are real failures
        return nil, err
    }

    return dryRunResult, nil
}
```

**Error detection** (common/k8serrors/classification.go:106-118):
```go
func IsImmutableFieldError(err error) bool {
    if statusErr, ok := err.(*errors.StatusError); ok {
        if statusErr.ErrStatus.Code == 422 { // Unprocessable Entity
            msg := strings.ToLower(statusErr.ErrStatus.Message)
            return strings.Contains(msg, "immutable") ||
                strings.Contains(msg, "forbidden") ||
                strings.Contains(msg, "cannot be changed") ||
                strings.Contains(msg, "may not be modified")
        }
    }
    return false
}

func ExtractImmutableFields(err error) []string {
    // Parses error message to extract field names
    // Example: "spec.storageClassName: Forbidden: field is immutable"
}
```

**Why it matters**:

**Common immutable fields in Kubernetes**:
- PersistentVolumeClaim: `spec.storageClassName`, `spec.volumeName`
- Pod: `spec.containers[*].name`, `spec.nodeName`, `spec.serviceAccountName`
- Service: `spec.clusterIP`, `spec.type` (some transitions)
- Job: Most spec fields after creation
- PersistentVolume: `spec.persistentVolumeReclaimPolicy`

**Without automatic replacement**:
```
$ terraform apply

Error: Update: Immutable Field Changed

Cannot update immutable field(s) [spec.storageClassName] on apps/v1/StatefulSet default/mysql.

Immutable fields cannot be changed after resource creation.

To resolve this:

Option 1 - Revert the change:
  Restore the original value in your YAML

Option 2 - Recreate the resource:
  terraform destroy -target=k8sconnect_manifest.example
  terraform apply

Option 3 - Use replace (Terraform 1.5+):
  terraform apply -replace=k8sconnect_manifest.example
```

User must manually destroy and recreate. Terraform sees update, not replacement.

**With automatic replacement**:
```
$ terraform plan

Terraform will perform the following actions:

  # k8sconnect_manifest.example must be replaced
-/+ resource "k8sconnect_manifest" "example" {
      ~ yaml_body = <<-EOT
          - storageClassName: standard
          + storageClassName: fast
        EOT
    }

Warning: Immutable Field Changed - Replacement Required

Cannot modify immutable field(s): [spec.storageClassName] on PersistentVolumeClaim default/data

Immutable fields cannot be changed after resource creation.
Terraform will delete the existing resource and create a new one.

This is the correct behavior - Kubernetes does not allow these fields to be modified in-place.

Plan: 1 to add, 0 to change, 1 to destroy.
```

Terraform correctly understands this is a replacement. User gets clear explanation.

---

**Missing from patch resource**:

Patch resource does NOT detect immutable fields:
- No check during dry-run (no dry-run at all)
- No automatic replacement triggering
- User gets cryptic K8s API error during apply
- Manual intervention required

**Example failure scenario**:
```hcl
resource "k8sconnect_patch" "pvc_storage_class" {
  target = {
    api_version = "v1"
    kind        = "PersistentVolumeClaim"
    name        = "data"
    namespace   = "default"
  }

  patch = yamlencode({
    spec = {
      storageClassName = "fast"  # Immutable!
    }
  })

  take_ownership = true
}
```

Apply fails with:
```
Error: Update Patch: Kubernetes API Error

An unexpected error occurred while performing Update Patch on v1 PersistentVolumeClaim/data.
Details: PersistentVolumeClaim.spec.storageClassName: Forbidden: field is immutable after creation
```

User is confused - patch should work, why doesn't it?

---

**Action Required**:

**Phase 1: Detection during apply** (Can do now without ModifyPlan):

1. **Wrap patch operations with immutable detection** (patch/crud.go):
   ```go
   func (r *patchResource) Create(ctx context.Context, req, resp) {
       // ... fetch target ...

       // Apply patch
       patchedObj, err := client.Patch(ctx, gvr, namespace, name, patchType, patchData, k8sclient.PatchOptions{
           FieldManager: fieldManager,
           Force:        true,
       })

       if err != nil {
           // Check for immutable field error
           if k8serrors.IsImmutableFieldError(err) {
               immutableFields := k8serrors.ExtractImmutableFields(err)
               resp.Diagnostics.AddError(
                   "Immutable Field in Patch",
                   fmt.Sprintf("Cannot patch immutable field(s): %v on %s\n\n"+
                       "The target resource has immutable fields that cannot be changed after creation.\n\n"+
                       "Options:\n"+
                       "1. Remove the immutable field from your patch\n"+
                       "2. If the field MUST change, recreate the target resource\n"+
                       "3. Consider if you should be using k8sconnect_manifest instead (manages full lifecycle)",
                       immutableFields, formatTarget(target)),
               )
               return
           }

           // Other errors
           k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Apply Patch", formatTarget(target))
           return
       }
   }
   ```

2. **Add to Update() as well** (same pattern)

**Phase 2: Replacement during plan** (Requires ModifyPlan - future):

1. **Implement ModifyPlan first** (see Pattern #8)
2. **Add immutable detection to dry-run**:
   ```go
   func (r *patchResource) executeDryRunPatch(...) bool {
       // Perform dry-run patch
       dryRunResult, err := client.Patch(ctx, gvr, namespace, name, patchType, patchData, k8sclient.PatchOptions{
           FieldManager: fieldManager,
           Force:        true,
           DryRun:       []string{"All"},
       })

       if err != nil {
           if k8serrors.IsImmutableFieldError(err) {
               // Cannot replace a patch - patching is non-destructive
               // Add error explaining limitation
               resp.Diagnostics.AddError(
                   "Immutable Field in Patch",
                   "Your patch attempts to modify immutable fields, which is not allowed.\n\n"+
                   "Patches cannot trigger resource replacement (this is by design - patches are non-destructive).\n\n"+
                   "To modify immutable fields, you must:\n"+
                   "1. Manually delete and recreate the target resource, OR\n"+
                   "2. Use k8sconnect_manifest to manage the full resource lifecycle (supports automatic replacement)",
               )
               return false
           }

           // Other errors
           return false
       }

       return true
   }
   ```

**Key difference from manifest**:

Manifest resource owns the full lifecycle → can replace resource
Patch resource only modifies existing → CANNOT replace target

So for patch, immutable field detection is about:
1. **Better error messages** - Explain what's wrong and why
2. **Early detection** - Catch during plan, not apply
3. **Clear guidance** - Tell user to use manifest instead OR recreate target manually

**Estimated effort**:
- Phase 1 (better errors): 2 hours
- Phase 2 (plan detection): Part of ModifyPlan work (Pattern #7)

---

#### 9. 🟢 Resource-Specific Schema Descriptions [GOOD IN ALL, COULD BE BETTER]

**What it is**: Rich, user-friendly descriptions in schema that explain not just WHAT fields do, but WHY they exist and HOW to use them.

**Manifest strengths** (manifest/manifest.go):

**Example 1 - force_destroy** (lines 132-135):
```go
"force_destroy": schema.BoolAttribute{
    Optional: true,
    MarkdownDescription: `Force deletion by removing finalizers. ⚠️ **WARNING**: Unlike other providers, this REMOVES finalizers after timeout. May cause data loss and orphaned cloud resources. See docs before using.`,
},
```
- Uses emoji for visual warning (⚠️)
- Explains how it's DIFFERENT from other providers
- Warns about consequences
- Directs to docs

**Example 2 - managed_state_projection** (lines 136-144):
```go
"managed_state_projection": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Field-by-field snapshot of managed state as flat key-value pairs with dotted paths. " +
        "Shows exactly which fields k8sconnect manages and their current values. " +
        "Terraform automatically displays only changed keys in diffs for clean, scannable output. " +
        "When this differs from current cluster state, it indicates drift - someone modified your managed fields outside Terraform. " +
        "Computed via Server-Side Apply dry-run for accuracy, enabling precise drift detection without false positives.",
},
```
- Explains WHAT it contains
- Explains WHY it's useful (drift detection)
- Explains HOW Terraform displays it (only changes)
- Explains WHEN it changes (external modifications)
- Explains HOW it's computed (SSA dry-run)

**Example 3 - ignore_fields** (lines 145-156):
```go
"ignore_fields": schema.ListAttribute{
    Optional:    true,
    ElementType: types.StringType,
    Description: "Field paths to exclude from management. On Create, fields are sent to establish initial state; " +
        "on Update, they're omitted from the Apply patch, releasing ownership to other controllers and excluding them from drift detection. " +
        "Supports dot notation (e.g., 'metadata.annotations', 'spec.replicas'), array indices ('webhooks[0].clientConfig.caBundle'), " +
        "and strategic merge keys ('spec.containers[name=nginx].image'). Use for fields managed by controllers (e.g., HPA modifying replicas) " +
        "or when operators inject values.",
    Validators: []validator.List{
        listvalidator.ValueStringsAre(ignoreFieldsValidator{}),
    },
},
```
- Explains behavior difference between Create and Update
- Lists supported formats with examples
- Gives common use cases (HPA, operators)
- Shows actual syntax examples

**Example 4 - status** (lines 165-171):
```go
"status": schema.DynamicAttribute{
    Computed: true,
    Description: "Resource status from the cluster, populated only when using wait_for with field='status.path'. " +
        "Contains resource-specific runtime information like LoadBalancer IPs, Pod conditions, Deployment replicas. " +
        "Follows the principle: 'You get only what you wait for' to avoid storing volatile status fields that cause drift. " +
        "Returns null when wait_for is not configured or uses non-field wait types.",
},
```
- States WHEN it's populated (not always)
- Gives examples of what it contains
- Explains design philosophy ("You get only what you wait for")
- Explains WHY this design (avoid drift)
- States WHEN it's null

---

**Patch strengths** (patch/patch.go):

**Example 1 - Resource description** (lines 109-128):
```markdown
⚠️ **CRITICAL: This resource forcefully takes ownership of fields from other controllers**

**ONLY use k8sconnect_patch for:**
- ✅ Cloud provider defaults (AWS EKS, GCP GKE, Azure AKS system resources)
- ✅ Operator-managed resources (cert-manager, nginx-ingress, etc.)
- ✅ Helm chart deployments
- ✅ Resources created by other tools

**NEVER use k8sconnect_patch for:**
- ❌ Resources managed by k8sconnect_manifest in the same state
- ❌ Resources you want full lifecycle control over
- ❌ Resources where you could use k8sconnect_manifest instead

**Destroy behavior:**
When you `terraform destroy` a patch:
- ✅ Ownership is released
- ✅ Patched values REMAIN on the resource
- ❌ Values are NOT reverted to original state
```

This is EXCELLENT:
- Uses emoji effectively (⚠️, ✅, ❌)
- Clear DO vs DON'T lists
- Explains critical destroy behavior
- Warns about dangerous anti-patterns

**Example 2 - take_ownership** (lines 227-232):
```go
"take_ownership": schema.BoolAttribute{
    Required: true,
    MarkdownDescription: "**Required acknowledgment.** Must be set to `true` to confirm you understand this patch will forcefully " +
        "take field ownership from other controllers. This is not optional - it's a required safety acknowledgment. " +
        "External controllers may fight back for control of these fields.",
},
```

This is GOOD:
- Explains WHY it's required (safety acknowledgment)
- Warns about consequences (fight back)
- Makes it clear it's not optional

---

**Datasource state** (datasource/resource/resource.go):

**Current descriptions are basic but functional**:
```go
"api_version": schema.StringAttribute{
    Required:    true,
    Description: "API version of the resource (e.g., 'v1', 'apps/v1')",
},
"kind": schema.StringAttribute{
    Required:    true,
    Description: "Kind of the resource (e.g., 'ConfigMap', 'Deployment')",
},
"manifest": schema.StringAttribute{
    Computed:    true,
    Description: "JSON representation of the complete resource",
},
"yaml_body": schema.StringAttribute{
    Computed:    true,
    Description: "YAML representation of the complete resource",
},
"object": schema.DynamicAttribute{
    Computed:    true,
    Description: "The resource object for accessing individual fields",
},
```

**What's missing**:
- No examples of JSONPath access for `object`
- No explanation of when to use `manifest` vs `yaml_body` vs `object`
- No examples of real-world usage

---

**Action Required**:

**For datasource (low priority, high impact)**:

Enhance descriptions with examples:

```go
"object": schema.DynamicAttribute{
    Computed:    true,
    Description: "The resource object for accessing individual fields using Terraform's dynamic type access. " +
        "Use this when you need to reference specific fields in other resources.\n\n" +
        "Examples:\n" +
        "  # Access LoadBalancer IP:\n" +
        "  data.k8sconnect_resource.lb.object.status.loadBalancer.ingress[0].ip\n\n" +
        "  # Access ConfigMap data:\n" +
        "  data.k8sconnect_resource.config.object.data.app_config\n\n" +
        "  # Access Pod IP:\n" +
        "  data.k8sconnect_resource.pod.object.status.podIP\n\n" +
        "Use 'manifest' for full JSON representation or 'yaml_body' for YAML format.",
},

"manifest": schema.StringAttribute{
    Computed:    true,
    Description: "JSON representation of the complete resource. " +
        "Use when you need to pass the full resource to another system or tool (e.g., kubectl, CI/CD pipelines). " +
        "For accessing specific fields within Terraform, use the 'object' attribute instead.",
},

"yaml_body": schema.StringAttribute{
    Computed:    true,
    Description: "YAML representation of the complete resource. " +
        "Use when you need human-readable output or to feed into k8sconnect_yaml_split for multi-resource processing. " +
        "For accessing specific fields within Terraform, use the 'object' attribute instead.",
},
```

**Estimated effort**: 1 hour

**Benefits**:
- New users understand how to use datasource outputs
- Reduces support questions
- Shows common patterns
- Documents best practices

---

### LOW PRIORITY / INFORMATIONAL

#### 10. 📘 Wait Condition Sophistication

**Manifest wait logic** (manifest/wait.go - 783 lines):

**Comprehensive implementation**:
- `wait_for.field` - Wait for JSONPath field to exist and be non-empty
- `wait_for.field_value` - Wait for specific field values (map of path → value)
- `wait_for.condition` - Wait for Kubernetes condition to be True
- `wait_for.rollout` - Wait for Deployment/StatefulSet/DaemonSet rollout completion
- Complex timeout handling with backoff
- Status field integration (populate status output when waiting)
- Proper cleanup on timeout
- Detailed logging

**Key features**:
```go
// Wait for LoadBalancer to get external IP
wait_for = {
  field   = "status.loadBalancer.ingress"
  timeout = "5m"
}

// Wait for Pod to be Running
wait_for = {
  field_value = {
    "status.phase" = "Running"
  }
  timeout = "2m"
}

// Wait for Deployment rollout
wait_for = {
  rollout = true
  timeout = "10m"
}

// Wait for custom condition
wait_for = {
  condition = "Ready"
  timeout    = "30s"
}
```

**Rollout logic is sophisticated**:
- Checks `status.observedGeneration` matches `metadata.generation`
- Validates `status.updatedReplicas` == `status.replicas`
- Validates `status.availableReplicas` == `status.replicas`
- Handles DaemonSet-specific fields (`numberReady`, `desiredNumberScheduled`)
- Handles StatefulSet-specific fields (`currentRevision`, `updateRevision`)

**Patch wait logic** (patch/wait.go - simpler but functional):

**Current implementation**:
- Has all wait types (field, field_value, condition, rollout)
- Basic timeout handling
- Less sophisticated status integration
- Simpler rollout checks

**Comparison**:

| Feature | Manifest | Patch | Assessment |
|---------|----------|-------|------------|
| Field wait | ✅ Full | ✅ Full | Equal |
| Field value wait | ✅ Full | ✅ Full | Equal |
| Condition wait | ✅ Full | ✅ Full | Equal |
| Rollout wait | ✅ Sophisticated | ✅ Basic | Manifest better |
| Status integration | ✅ Full | ✅ Partial | Manifest better |
| Timeout handling | ✅ Backoff | ✅ Basic | Manifest better |
| Error messages | ✅ Detailed | ✅ Good | Manifest better |

**Assessment**:
- Patch wait is adequate for most use cases
- Rollout logic could be enhanced but not critical
- Status integration is different by design (patch doesn't manage full resource)

**Action Required**:
- ✅ NO IMMEDIATE ACTION NEEDED
- Consider enhancing rollout logic in future
- Document differences in behavior

---

#### 11. 📘 Field Ownership Tracking Depth

**Manifest field ownership**:

**Tracks ALL managed fields**:
```go
"field_ownership": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Tracks which controller owns each managed field using Server-Side Apply field management. " +
        "Shows as a map of 'field.path': 'controller-name'. Only appears in plan diffs when ownership actually changes " +
        "(e.g., when HPA takes ownership of spec.replicas). Empty/hidden when ownership is unchanged. " +
        "Critical for understanding SSA conflicts and knowing which controller controls what.",
},
```

**Implementation**:
- Parses `managedFields` from K8s API
- Converts `fieldsV1` format to flat path map
- Filters by fields we actually manage
- Shows ownership changes in plan diffs
- Integrates with projection for drift detection
- Warns about conflicts during plan

**Usage in plan output**:
```hcl
# Ownership unchanged - field_ownership not shown
~ resource "k8sconnect_manifest" "example" {
    ~ yaml_body = (updated)
  }

# Ownership changed - shows in diff
~ resource "k8sconnect_manifest" "example" {
    ~ field_ownership = {
        + "spec.replicas" = "k8sconnect"
        - "spec.replicas" = "horizontal-pod-autoscaler"
      }
    ~ yaml_body = (updated)
  }
```

**Patch field ownership**:

**Tracks only patched fields**:
```go
"field_ownership": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Map of field paths to their current owner (field manager). Shows which controller owns each patched field. " +
        "After patch application, patched fields should show this patch's field manager as owner.",
},

"previous_owners": schema.MapAttribute{
    Computed:    true,
    ElementType: types.StringType,
    Description: "Map of field paths to their owners BEFORE this patch was applied. " +
        "Useful for understanding which controllers were managing fields before takeover. " +
        "Only populated during initial patch creation.",
},
```

**Implementation**:
- Tracks only fields modified by patch
- Stores previous owners before takeover
- Shows current owners after patch
- Simpler than manifest (fewer fields to track)

**Comparison**:

| Aspect | Manifest | Patch | Assessment |
|--------|----------|-------|------------|
| Scope | All managed fields | Only patched fields | Different by design |
| Previous owners | No (inferred from history) | Yes (explicit field) | Patch has useful addition |
| Drift integration | Full integration | Not applicable | Different use cases |
| Plan visibility | Shows ownership changes | Shows ownership changes | Equal |
| Use case | Full lifecycle management | Targeted patches | Appropriate for each |

**Assessment**:
- Patch's simpler approach is appropriate for its use case
- `previous_owners` is a nice addition that manifest doesn't have
- Tracking only patched fields makes sense (don't care about non-patched fields)
- No need to change either implementation

**Action Required**:
- ✅ NO ACTION NEEDED
- Both implementations are appropriate for their use cases
- Consider documenting the differences

---

## Implementation Roadmap

### Phase 1: Quick Wins (1-2 days) ✅ COMPLETE
**Goal**: Immediate improvements with minimal effort

1. ✅ **Error classification** - COMPLETED (2025-01-12)
   - Universal k8serrors.AddClassifiedError() usage
   - Patch and datasource now have classified errors

2. ✅ **API warning surfacing** - COMPLETED (2025-01-12)
   - Added `surfaceK8sWarnings()` calls to patch CRUD operations (Create, Read, Update, Delete)
   - Added `surfaceK8sWarnings()` to datasource Read operation
   - All tests passing (unit + acceptance)

3. ✅ **Comprehensive config validators** - COMPLETED (2025-01-12)
   - Created common validators package with JSONPath validators
   - Added JSONPath validator to patch wait_for.field
   - Added JSONPath map keys validator to patch wait_for.field_value
   - All tests passing (unit + acceptance + examples)

**Deliverables**:
- ✅ Patch surfaces K8s API warnings
- ✅ Datasource surfaces K8s API warnings
- ✅ Patch validates JSONPath in wait_for
- ✅ Datasource has connection and exec auth validators (already implemented)

---

### Phase 2: Test Coverage & Validation (1 week)
**Goal**: Comprehensive testing and validation improvements

4. 🟡 **Test coverage expansion** - 3 days
   - **Patch P0 tests**: Auth modes, error classification
   - **Datasource P0 tests**: Auth modes, error scenarios
   - Aim for 70%+ coverage on new tests

5. ✅ **Patch content validators** - COMPLETED (2025-01-12)
   - ✅ Strategic merge container name validation
   - ✅ Server-managed field detection
   - ✅ Provider annotation blocking
   - ✅ RFC 6902 JSON Patch validation
   - ✅ RFC 7386 Merge Patch validation
   - ✅ Comprehensive unit tests (67 test cases)

**Deliverables**:
- Patch has comprehensive auth tests (TODO)
- Datasource has comprehensive tests (TODO)
- All P0 test scenarios covered (TODO)
- ✅ Patch validates content before apply (COMPLETE)

---

### Phase 3: Quality Improvements (2 weeks)
**Goal**: Reliability and user experience enhancements

6. 🟡 **Private state recovery** - 2 days
   - Add pending_fields flag to patch
   - Implement recovery in Read
   - Test transient failure scenarios

7. 🟢 **Identity change detection** - 2 days
   - Phase 1: Better error messages for immutable fields
   - Wrap patch operations with detection
   - Add guidance to use manifest for lifecycle management

8. 🟡 **Test coverage continuation** - 5 days
   - **Patch P1 tests**: Wait conditions, field ownership, multi-cluster
   - **Datasource P1 tests**: Resource discovery, namespace handling
   - Aim for 80%+ overall coverage

**Deliverables**:
- Patch handles transient failures gracefully
- ✅ Better validation and error messages (validators complete)
- Immutable field detection in patch
- Comprehensive test suite

---

### Phase 4: Advanced Features (Future / Optional)
**Goal**: Plan-time validation and advanced UX

10. 🟢 **ModifyPlan for patch** - 1-2 weeks
    - Implement dry-run during plan
    - Show accurate field changes
    - Detect ownership conflicts
    - Support all patch types
    - Extensive testing

11. 🟡 **Remaining test coverage** - 1 week
    - **Patch P2 tests**: Validators, CRDs, drift, patch types
    - **Datasource P2 tests**: Validators, CRDs, output formats
    - Target 85%+ coverage

12. 🟢 **Documentation enhancements** - 3 days
    - Enhance datasource descriptions with examples
    - Document pattern differences
    - Add migration guides

**Deliverables**:
- terraform plan shows accurate patch predictions
- Full test coverage across all resources
- Comprehensive documentation

---

## Success Metrics

### Code Quality
- ✅ Error classification: 100% coverage (ACHIEVED)
- 🎯 Test coverage:
  - Patch resource: >80%
  - Datasource: >80%
  - Currently: ~60% patch, ~40% datasource

### User Experience
- ✅ API warnings surfaced to users: 100% of K8s interactions (ACHIEVED)
- 🎯 Validation: Common mistakes caught during plan, not apply
- 🎯 Plan accuracy: Patch shows predicted changes (Phase 4)

### Reliability
- 🎯 Recovery patterns: Transient failures don't orphan resources
- 🎯 Validation: Common mistakes caught during plan, not apply
- 🎯 Error messages: All errors classified with resolution guidance

### Documentation
- 🎯 Schema descriptions: Examples for all datasource outputs
- 🎯 Pattern documentation: Differences documented
- 🎯 Migration guides: Available for adopting patterns

---

## Decision Log

### Decisions Made

**2025-01-12: Error Classification**
- ✅ Decision: Move error classification to common package
- ✅ Implementation: Created k8serrors.AddClassifiedError() helper
- ✅ Adoption: Applied to manifest, patch, datasource
- Result: 32 lines of boilerplate removed, universal error handling

### Pending Decisions

**API Warning Surfacing**
- Question: Should warnings be errors in CI/CD mode?
- Proposal: Add environment variable `K8SCONNECT_WARNINGS_AS_ERRORS`
- Status: Needs discussion

**ModifyPlan Priority**
- Question: Is ModifyPlan worth 1-2 weeks of effort for patch?
- Tradeoff: High user value vs high implementation complexity
- Status: Deferred to Phase 4, gather user feedback first

**Test Coverage Goals**
- Question: What's the target coverage percentage?
- Proposal: 80% overall, 100% for critical paths (CRUD, auth, errors)
- Status: Agreed, track per-file coverage

---

## Appendix: File Organization Comparison

### Manifest Resource Structure (40 files)

**Core functionality**:
- `manifest.go` - Schema and resource definition
- `crud.go` - Create, Read, Update, Delete operations
- `crud_common.go` - Shared CRUD helper functions
- `plan_modifier.go` - Plan phase logic (dry-run, projection, conflicts)

**Advanced features**:
- `projection.go` - Field projection logic
- `field_ownership.go` - Field ownership tracking
- `wait.go` - Wait condition handling
- `import.go` - Import support
- `identity_changes.go` - Identity change detection

**Validation & parsing**:
- `validators.go` - Resource-level validators
- `yaml.go` - YAML parsing and validation
- `errors.go` - Error classification wrappers

**Helpers**:
- `auth.go` - Authentication helpers
- `id_generation.go` - ID generation
- `formatting.go` - YAML formatting preservation
- `status_pruner.go` - Status field handling

**20 test files**: Comprehensive coverage of all features

---

### Patch Resource Structure (7 files)

**Core functionality**:
- `patch.go` - Schema and resource definition
- `crud.go` - Create, Read, Update, Delete operations
- `wait.go` - Wait condition handling

**Validation**:
- `validators.go` - Resource-level validators

**Helpers**:
- `helpers.go` - Utility functions

**3 test files**: Basic coverage

**Missing compared to manifest**:
- No `plan_modifier.go` (no dry-run during plan)
- No `projection.go` (different approach to state)
- No `identity_changes.go` (no immutable field detection)
- Limited test coverage

---

### Datasource Structure (3 files)

**Core functionality**:
- `resource.go` - Schema, read operation, everything in one file

**2 test files**: Minimal coverage

**Missing compared to manifest**:
- No `validators.go` (no validation)
- No separate error handling
- No separate auth handling
- Minimal test coverage

**Note**: Datasource is simpler by nature (read-only), but still missing validation and tests

---

## References

### Related ADRs
- **ADR-001**: Managed state projection core design
- **ADR-002**: Immutable field detection and automatic replacement
- **ADR-005**: Field ownership strategy
- **ADR-006**: Recovery patterns using private state
- **ADR-010**: Identity change detection
- **ADR-011**: Concise diff format and bootstrap handling
- **ADR-012**: Terraform fundamental contract

### Code Locations

**Common packages** (shared by all):
- `internal/k8sconnect/common/k8serrors/` - Error classification
- `internal/k8sconnect/common/auth/` - Authentication
- `internal/k8sconnect/common/k8sclient/` - K8s client
- `internal/k8sconnect/common/factory/` - Client factory
- `internal/k8sconnect/common/fieldmanagement/` - Field ownership parsing

**Resource implementations**:
- `internal/k8sconnect/resource/manifest/` - Manifest resource (mature)
- `internal/k8sconnect/resource/patch/` - Patch resource (needs work)

**Datasource implementations**:
- `internal/k8sconnect/datasource/resource/` - Resource datasource (needs work)

---

## Changelog

**2025-01-12**:
- Initial document created
- Deep dive analysis completed
- 11 patterns identified (import for patch removed - conceptually invalid)
- 4-phase roadmap defined
- Pattern #1 (Error classification) marked as completed
- Pattern #2 (API Warning Surfacing) implemented and marked as completed
  - Added `surfaceK8sWarnings()` to patch/crud.go with calls after all K8s API operations
  - Added `surfaceK8sWarnings()` to datasource/resource/resource.go with call after Get operation
  - All unit tests and acceptance tests passing
- Removed Pattern #3 (Import for patch) - import makes no sense for patch resources since patches are modifications, not adoptable resources
- Pattern #3 (Comprehensive Config Validators) implemented and marked as completed
  - Created `internal/k8sconnect/common/validators/jsonpath.go` with JSONPath and JSONPathMapKeys validators
  - Moved validators from manifest to common package for reuse
  - Updated manifest to import from common validators
  - Added JSONPath validation to patch wait_for.field
  - Added JSONPath map keys validation to patch wait_for.field_value
  - All unit, acceptance, and example tests passing
- **Phase 1 COMPLETE**: All quick wins implemented (error classification, API warnings, validators)
- Pattern #6 (YAML Validation Enhancements) marked as completed
  - Validators were already implemented in commit f38e218 (2025-01-12)
  - Created comprehensive unit tests with 67 test cases
  - All three patch types validated: StrategicMergePatch, JSONPatchValidator, MergePatchValidator
  - Tests cover: container names, server-managed fields, provider annotations, status fields, RFC compliance
  - All tests passing

---

## Maintenance Notes

**How to update this document**:
1. When implementing a pattern, update its status (🔴 → 🟡 → 🟢 → ✅)
2. Update "Decisions Made" section with outcomes
3. Move completed items to changelog
4. Adjust roadmap phases based on progress
5. Update success metrics with actual measurements

**Review schedule**:
- After each phase completion
- Monthly progress check
- Before major releases

**Document owner**: Jonathan Morris
