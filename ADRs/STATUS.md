# ADR Implementation Status

Last updated: 2025-10-05

## Summary

| ADR | Title | Status in File | Actually Implemented | Notes |
|-----|-------|---------------|---------------------|-------|
| ADR-001 | Managed State Projection | Accepted | ✅ **ADOPTED** | Core architecture fully implemented |
| ADR-002 | Immutable Resources & Complex Deletions | Deferred | ⚠️ **PARTIAL** | Enhanced error messages only |
| ADR-003 | Resource IDs | Accepted | ✅ **ADOPTED** | Random UUIDs + ownership annotations |
| ADR-004 | Cross-State Conflicts | Proposed | ❌ **NOT ADOPTED** | Context annotations not implemented |
| ADR-005 | Field Ownership Strategy | Accepted | ✅ **ADOPTED** | Server-side apply field management |
| ADR-006 | State Safety & Projection Recovery | Accepted | ✅ **ADOPTED** | Private state recovery pattern |
| ADR-010 | Prevent Orphan Resources (Identity Changes) | Proposed | ❌ **NOT IMPLEMENTED** | **CRITICAL BUG** - needs immediate attention |

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

### ⚠️ ADR-003: Immutable Resources - PARTIAL

**Evidence:**
- `isImmutableFieldError()` exists (errors.go:80)
- Enhanced error messages with resolution steps (errors.go:46-60)
- Field extraction for diagnostics (errors.go:93)

**What's implemented:**
- Better error messages when immutable fields change
- Detection of immutability errors (422 Invalid)
- User guidance on resolution options

**What's NOT implemented:**
- Dry-run detection of immutability *before* apply
- Automatic recreation strategy
- `recreate_on_immutable_change` configuration option
- Proactive immutability checking

**Status:** Enhanced error UX only, not full ADR-003 vision

**Reason:** ADR marked as "Draft - Open Questions" suggesting deliberate non-implementation

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

### ❌ ADR-010: Prevent Orphan Resources (Identity Changes) - NOT IMPLEMENTED ⚠️ CRITICAL

**Status:** Proposed (2025-10-05)

**Problem:** Provider has a critical bug where changing resource identity fields (kind, apiVersion, metadata.name, metadata.namespace) in yaml_body creates orphan resources in Kubernetes cluster.

**Evidence of bug:**
- `ModifyPlan()` performs dry-run but never checks identity changes (plan_modifier.go:21-108)
- `Update()` preserves same Terraform ID but applies different K8s resource (crud.go:170)
- No `RequiresReplace` logic anywhere in codebase
- Orphan scenario confirmed via code analysis and comparison with kubectl/kubernetes providers

**What's NOT implemented:**
- Identity change detection in ModifyPlan
- RequiresReplace on yaml_body when identity changes
- Diagnostic messages explaining replacement
- Protection against accidental orphan creation

**Security impact:**
- Users can accidentally leave privileged resources orphaned (ServiceAccounts, ClusterRoles)
- Silent failure mode - no warning that old resource still exists
- Orphaned resources remain in cluster indefinitely

**Comparison with other providers:**
- kubectl provider: Uses `ForceNew: true` on computed kind/name/namespace fields ✅
- kubernetes provider: Uses `RequiresReplace` in PlanResourceChange ✅
- k8sconnect: No protection ❌

**Proposed solution:** Add identity change detection to ModifyPlan, set RequiresReplace when kind/apiVersion/name/namespace changes

**Priority:** **CRITICAL** - This is a security and correctness issue

**Relationship to ADR-002:**
- ADR-002 addresses immutable _fields_ (K8s returns 422 error)
- ADR-010 addresses resource _identity_ (K8s creates new resource, orphans old one)
- Both should be implemented but ADR-010 is more critical

## Recommendations

### Critical Priority 🚨

1. **Implement ADR-010 immediately**
   - **CRITICAL BUG** - orphan resources are a security and correctness issue
   - Affects all users who change resource identity in yaml_body
   - Other providers (kubectl, kubernetes) already solve this
   - Solution is well-defined: RequiresReplace in ModifyPlan
   - Estimated effort: 1-2 days (implementation + tests)
   - Zero breaking changes - only fixes existing bug

### High Priority

2. **Update ADR-002 status** to reflect current implementation
   - Currently marked "Deferred - Open Questions"
   - Enhanced error messages are implemented
   - Full dry-run detection and automatic recreation not implemented
   - Related to ADR-010 but distinct concerns

3. **Consider ADR-004 implementation** (Cross-State Conflicts)
   - Cross-state conflicts are a real problem
   - Low implementation cost (just context annotations)
   - High value for multi-state/multi-team scenarios
   - Could be added non-breaking

### Low Priority

4. **Update ADR-003 status in file**
   - Currently marked "Accepted" - correct
   - Fully implemented with random UUIDs + ownership annotations

## Migration Path

If implementing ADR-005 later:
1. Add context annotations to new resources
2. Existing resources continue with basic ownership
3. No breaking changes required
4. Graceful degradation for old resources

## Notes

- ADR-001 and ADR-006 form the core safety architecture
- ADR-003 provides stable resource identity across configuration changes
- **ADR-010 is a critical gap** - orphan resources are a security/correctness issue
- ADR-002 and ADR-010 are related but distinct:
  - ADR-002: Immutable fields (K8s rejects with 422) - enhanced errors implemented
  - ADR-010: Identity changes (K8s accepts, creates orphan) - NOT implemented
- ADR-004 (cross-state conflicts) is a gap but not critical for single-state usage
