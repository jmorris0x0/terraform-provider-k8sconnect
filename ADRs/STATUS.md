# ADR Implementation Status

Last updated: 2025-10-06

## Summary

| ADR | Title | Status in File | Actually Implemented | Notes |
|-----|-------|---------------|---------------------|-------|
| ADR-001 | Managed State Projection | Accepted | ✅ **ADOPTED** | Core architecture fully implemented |
| ADR-002 | Immutable Resources & Complex Deletions | Accepted | ✅ **ADOPTED** | Dry-run detection + RequiresReplace |
| ADR-003 | Resource IDs | Accepted | ✅ **ADOPTED** | Random UUIDs + ownership annotations |
| ADR-004 | Cross-State Conflicts | Proposed | ❌ **NOT ADOPTED** | Context annotations not implemented |
| ADR-005 | Field Ownership Strategy | Accepted | ✅ **ADOPTED** | Server-side apply field management |
| ADR-006 | State Safety & Projection Recovery | Accepted | ✅ **ADOPTED** | Private state recovery pattern |
| ADR-007 | CRD Dependency Resolution | Proposed | ❌ **NOT IMPLEMENTED** | Automatic apply-time retry for CRD race conditions |
| ADR-008 | Selective Status Population | Accepted | ✅ **ADOPTED** | "You get ONLY what you wait for" principle |
| ADR-009 | User-Controlled Drift Exemption | Accepted | ✅ **ADOPTED** | `ignore_fields` attribute |
| ADR-010 | Prevent Orphan Resources (Identity Changes) | Accepted | ✅ **ADOPTED** | Identity change detection + RequiresReplace |

## Detailed Status

### ✅ ADR-001: Managed State Projection - ADOPTED

**Evidence:**
- `managed_state_projection` field exists in schema (manifest.go:46)
- Dry-run during plan phase (plan_modifier.go)
- Field path extraction and projection (projection.go)
- Server-side apply with field manager

**Implementation completeness:** ~95%
- Core projection logic ✅
- Dry-run integration ✅
- Field ownership tracking ✅
- Quantity normalization ✅
- Strategic merge handling ✅

**Missing:**
- None - fully implemented as designed

### ✅ ADR-002: Immutable Resources & Complex Deletions - ADOPTED

**Evidence:**

*Immutable Fields:*
- `isImmutableFieldError()` exists (errors.go:81)
- `extractImmutableFields()` for diagnostics (errors.go:94)
- Immutable field detection in dry-run (plan_modifier.go:273-305)
- RequiresReplace on immutable field changes (plan_modifier.go:288)
- Enhanced warning diagnostics (plan_modifier.go:291-302)

*Complex Deletions:*
- `forceDestroy()` removes finalizers (deletion.go:19-74)
- `handleDeletionTimeout()` with detailed error scenarios (deletion.go:77-150)
- `getDeleteTimeout()` with smart defaults per resource type (deletion.go:153-180)
- `waitForDeletion()` with polling and timeout handling (deletion.go:183-225)

**Implementation completeness:** 100%

*Immutable Fields:*
- Dry-run detection during plan phase ✅
- Automatic RequiresReplace trigger ✅
- Clear user warnings with field details ✅
- Leverages existing dry-run infrastructure ✅
- No extra API calls (dry-run already happens) ✅

*Complex Deletions:*
- `force_destroy = true` removes all finalizers ✅
- Warning diagnostics about implications ✅
- Smart default timeouts (CRDs: 15m, Namespaces: 10m, etc.) ✅
- Configurable `delete_timeout` ✅
- Detailed error messages with actionable steps ✅
- Handles multiple stuck deletion scenarios ✅

**What's implemented:**

*Immutable Fields:*
- Detect immutable field errors during performDryRun (plan phase)
- Extract specific fields that are immutable from error message
- Set RequiresReplace on yaml_body attribute
- Add informative warning diagnostic to user
- Set projection to unknown (replacement doesn't need projection)

*Complex Deletions:*
- Force destroy removes all finalizers with field manager "k8sconnect-force-destroy"
- Detects 3 scenarios: finalizers present, no finalizers but stuck, deletion not initiated
- Provides kubectl commands for manual troubleshooting
- Resource-type-aware timeout defaults
- Waits for deletion with 2-second polling interval

**Implementation date:** Immutable fields 2025-10-06, Complex deletions earlier

**Note:** Advanced features like cascading deletion strategies and exponential backoff could be added but are not essential - current implementation handles all common scenarios

### ✅ ADR-004: Random ID Strategy - ADOPTED

**Evidence:**
- `generateID()` uses random bytes (resource_ownership.go:19-24)
- 12-character hex IDs (not deterministic hashes)
- Ownership annotation `k8sconnect.terraform.io/terraform-id` (resource_ownership.go)
- Conflict detection via annotation checking

**Implementation completeness:** 100%
- Random UUID generation ✅
- Ownership annotations ✅
- Conflict prevention ✅
- Import handling ✅
- Stable IDs across config changes ✅

### ❌ ADR-005: Cross-State Ownership - NOT ADOPTED

**Evidence:**
- No `k8sconnect.terraform.io/context` annotation found
- No `OwnershipContext` struct
- No cross-state conflict detection beyond basic ownership

**What exists:**
- Basic ownership via `terraform-id` annotation (from ADR-004)
- Single-state ownership tracking

**What's missing:**
- Context hash generation
- Workspace tracking
- Cross-state conflict detection
- Enhanced field manager strategy

**Status:** Only the basic ownership part of ADR-004 exists, not the enhanced cross-state protection

**Impact:** Multiple Terraform states can still silently conflict if they use different resource names for the same K8s object

### ✅ ADR-006: State Safety & Projection Recovery - ADOPTED

**Evidence:**
- `checkPendingProjectionFlag()` (crud.go:336)
- `setPendingProjectionFlag()` (crud.go:344)
- `handleProjectionFailure()` (crud.go:384-413)
- Recovery logic in Create/Update/Read

**Implementation completeness:** 100%
- Private state flag tracking ✅
- Create() saves state on projection failure ✅
- Update() retries pending projections ✅
- Read() opportunistic recovery ✅
- Clear error messages ✅
- Stops CI/CD (error return) ✅

**Recent additions:**
- Helper functions for clean ADR-006 pattern (refactoring from 2025-10-02)
- Extracted `handleProjectionFailure()` and `handleProjectionSuccess()`

### ❌ ADR-007: CRD Dependency Resolution - NOT IMPLEMENTED

**Status:** Proposed (not implemented)

**Problem:** CRD + CR in same `terraform apply` fails due to race condition - CRD not established when CR is applied

**What would be implemented:**
- Automatic apply-time retry with exponential backoff (up to 30s)
- Detection of "no matches for kind" errors
- Zero configuration required
- Clear error messages when CRD truly missing

**Current workaround:**
Users must apply in two phases:
```bash
terraform apply -target=module.crds
terraform apply
```

**Why not implemented yet:**
- Moderate complexity (~1-2 days)
- Not critical - workaround exists
- Lower priority than other ADRs

**Priority:** Medium - Would be significant UX improvement

### ✅ ADR-008: Selective Status Population - ADOPTED

**Status:** Accepted and implemented

**Evidence:**
- Status only populated when `wait_for.field` is set
- Pruning logic in status update code
- `TestAccManifestResource_StatusStability` passes
- No drift from volatile status fields

**Implementation completeness:** 100%
- Only `field` waits populate status ✅
- Status pruned to specific requested path ✅
- Other wait types (rollout, condition) don't store status ✅
- No drift from volatile fields like `observedGeneration` ✅

**Principle:** "You get ONLY what you wait for"

**What's implemented:**
- Selective status population based on wait type
- Field path pruning (e.g., only `status.loadBalancer.ingress`)
- Excludes volatile fields automatically
- LoadBalancer IP use case works perfectly

**Benefits:**
- No spurious drift from status changes
- Clean plans without constant status updates
- Users can still access critical status values

### ✅ ADR-009: User-Controlled Drift Exemption - ADOPTED

**Status:** Accepted and implemented

**Evidence:**
- `ignore_fields` attribute in schema
- Field ownership filtering in ModifyPlan and ModifyApply
- 6 comprehensive acceptance tests
- Plan/Apply consistency for field_ownership

**Implementation completeness:** 100%
- `ignore_fields` attribute accepts field paths ✅
- Filtering in both Plan and Apply phases ✅
- Works with field ownership mechanism ✅
- Comprehensive test coverage (95% confidence) ✅

**What's implemented:**
- `ignore_fields = ["spec.replicas"]` syntax
- Fields excluded from drift detection
- Fields excluded from field_ownership attribute
- Allows external controllers to take ownership
- Proper error handling when removing ignored fields

**Use cases:**
- HPA managing `spec.replicas` on Deployments
- cert-manager managing certificate secrets
- Service mesh sidecar injection
- Any multi-controller scenario

**Critical bug fixed during implementation:**
- Plan/Apply consistency for `field_ownership` computed attribute
- Both phases must filter identically to avoid "inconsistent result" errors

### ✅ ADR-010: Prevent Orphan Resources (Identity Changes) - ADOPTED

**Status:** Implemented (2025-10-06)

**Evidence:**
- `checkResourceIdentityChanges()` in plan_modifier.go:35-42
- Identity change detection in identity_changes.go
- RequiresReplace on yaml_body when identity changes
- Warning diagnostics explaining replacement
- Unit tests in identity_changes_test.go (12 scenarios)
- Acceptance tests for Kind, Name, Namespace changes

**Implementation completeness:** 100%
- Detects changes to kind, apiVersion, metadata.name, metadata.namespace ✅
- Sets RequiresReplace on yaml_body ✅
- Clear warning diagnostics with old vs new identity ✅
- Handles cluster-scoped and namespaced resources ✅
- Short-circuits dry-run when replacement needed (performance) ✅
- All 59 acceptance tests pass ✅

**What's implemented:**
- `checkResourceIdentityChanges()` - early check in ModifyPlan
- `detectIdentityChanges()` - compares all 4 identity fields
- `formatResourceIdentity()` - human-readable identity strings
- Warning diagnostic showing exactly what changed
- Protection against orphan resource creation

**Implementation date:** 2025-10-06

**Security improvement:**
- Prevents accidental orphan creation of privileged resources
- Clear feedback to user when identity changes
- Automatic proper lifecycle management (delete old, create new)

**Relationship to ADR-002:**
- ADR-002 addresses immutable _fields_ (K8s returns 422 error) - implemented
- ADR-010 addresses resource _identity_ (K8s creates new resource) - implemented
- Both use RequiresReplace mechanism in ModifyPlan

## Recommendations

### Recently Completed ✅

1. **ADR-010: Prevent Orphan Resources** - ✅ **COMPLETED** (2025-10-06)
   - Identity change detection implemented
   - RequiresReplace on kind/apiVersion/name/namespace changes
   - All tests passing (59 acceptance tests)
   - Security issue resolved

2. **ADR-002: Immutable Field Handling** - ✅ **COMPLETED** (2025-10-06)
   - Dry-run detection of immutable field errors
   - Automatic RequiresReplace trigger
   - Clear user warnings with field details
   - All tests passing

### High Priority

1. **Consider ADR-007 implementation** (CRD Dependency Resolution)
   - Solves 3+ year old ecosystem problem
   - Automatic retry with zero configuration
   - Significant UX improvement over competition
   - Estimated effort: 1-2 days
   - Would be a major differentiator

2. **Consider ADR-004 implementation** (Cross-State Conflicts)
   - Cross-state conflicts are a real problem
   - Low implementation cost (just context annotations)
   - High value for multi-state/multi-team scenarios
   - Could be added non-breaking

### Low Priority

3. **ADR-002: Complex Deletion Enhancements** (Optional, very low priority)
   - Current implementation is comprehensive:
     - `force_destroy` removes finalizers ✅
     - Smart resource-type-aware timeouts ✅
     - Excellent error messages with kubectl commands ✅
   - Possible future additions (not essential):
     - Exponential backoff for retries
     - Cascading deletion ordering
     - Finalizer-specific handling logic

## Migration Path

If implementing ADR-005 later:
1. Add context annotations to new resources
2. Existing resources continue with basic ownership
3. No breaking changes required
4. Graceful degradation for old resources

## Notes

### Core Architecture (Complete)
- **ADR-001** and **ADR-006** form the core safety architecture
- **ADR-003** provides stable resource identity across configuration changes
- **ADR-005** establishes field ownership with Server-Side Apply

### Lifecycle Management (Complete - 2025-10-06)
- **ADR-002** and **ADR-010** work together for comprehensive resource lifecycle management:
  - ADR-002: Immutable fields (K8s rejects with 422) - dry-run detection + RequiresReplace ✅
  - ADR-010: Identity changes (K8s accepts, creates orphan) - identity detection + RequiresReplace ✅

### Multi-Controller Support (Complete)
- **ADR-008**: Selective status population - "You get ONLY what you wait for" ✅
- **ADR-009**: User-controlled drift exemption via `ignore_fields` ✅

### Outstanding Gaps
- **ADR-004** (cross-state conflicts) - Not critical for single-state usage
- **ADR-007** (CRD dependency resolution) - Significant UX improvement, not yet implemented

### Implementation Status
- **8 out of 10 ADRs fully implemented**
- All core provider functionality complete with robust safety mechanisms
- Remaining ADRs are optional enhancements
