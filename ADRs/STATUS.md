# ADR Implementation Status

Last updated: 2025-10-03

## Summary

| ADR | Title | Status in File | Actually Implemented | Notes |
|-----|-------|---------------|---------------------|-------|
| ADR-001 | Managed State Projection | Accepted | ✅ **ADOPTED** | Core architecture fully implemented |
| ADR-003 | Immutable Resources | Draft | ⚠️ **PARTIAL** | Enhanced error messages only |
| ADR-004 | Random ID Strategy | Proposed | ✅ **ADOPTED** | Random IDs + ownership annotations |
| ADR-005 | Cross-State Ownership | Proposed | ❌ **NOT ADOPTED** | Context annotations not implemented |
| ADR-006 | State Safety & Projection Recovery | Accepted | ✅ **ADOPTED** | Private state recovery pattern |

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

## Recommendations

### High Priority

1. **Update ADR-003 status** to "Rejected" or "Deferred"
   - Current "Draft" status is misleading
   - Enhanced errors are good enough for now
   - Full implementation would add significant complexity

2. **Consider ADR-005 implementation**
   - Cross-state conflicts are a real problem
   - Low implementation cost (just context annotations)
   - High value for multi-state/multi-team scenarios
   - Could be added non-breaking

### Low Priority

3. **Mark ADR-004 as "Accepted"**
   - Currently says "Proposed" but fully implemented
   - Should match implementation reality

## Migration Path

If implementing ADR-005 later:
1. Add context annotations to new resources
2. Existing resources continue with basic ownership
3. No breaking changes required
4. Graceful degradation for old resources

## Notes

- ADR-001 and ADR-006 form the core safety architecture
- ADR-004 provides stable resource identity
- ADR-003 partial implementation is acceptable (error messages help)
- ADR-005 is the main gap but not critical for single-state usage
