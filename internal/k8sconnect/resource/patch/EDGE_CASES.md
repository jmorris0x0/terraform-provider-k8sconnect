# Patch Resource Edge Cases Test Coverage

**Note:** Many tests from Phase 2 sections (19-21, 23) are duplicates of manifest resource tests,
since patch reuses the same connection/auth/client mechanisms. Focus on patch-specific behaviors.

## Phase 1 MVP - Critical Safety Tests (Patch-Specific)

### 1. Self-Patching Prevention (CRITICAL!)
- [ ] **1.1** Try to patch resource with `k8sconnect.terraform.io/terraform-id` annotation
- [ ] **1.2** Try to patch resource managed by `k8sconnect` field manager
- [ ] **1.3** Try to patch resource with legacy `k8sconnect.io/owned-by` annotation
- [ ] **1.4** Verify clear, actionable error message
- [ ] **1.5** Verify suggests using ignore_fields instead

### 2. Target Resolution
- [ ] **3.1** Patch non-existent resource (should fail with "resource not found")
- [ ] **3.2** Patch cluster-scoped resource (no namespace)
- [ ] **3.3** Patch namespaced resource
- [ ] **3.4** Provide namespace for cluster-scoped resource (should be ignored)
- [ ] **2.5** Invalid api_version format
- [ ] **2.6** Invalid kind

### 3. Patch Type Validation
- [ ] **4.1** Specify both `patch` and `json_patch` (should fail)
- [ ] **4.2** Specify all three patch types (should fail)
- [ ] **4.3** Specify no patch type (should fail)
- [ ] **3.4** Valid strategic merge patch
- [ ] **3.5** Valid JSON patch
- [ ] **3.6** Valid merge patch

### 4. Patch Content Validation
- [ ] **5.1** Malformed YAML in `patch`
- [ ] **5.2** Malformed JSON in `json_patch`
- [ ] **4.3** Empty patch content
- [ ] **4.4** Patch with only metadata (should it be allowed?)

## Phase 1 MVP - Ownership Transfer Tests

### 5. Single Previous Owner
- [ ] **6.1** All patched fields from same controller (e.g., all EKS)
- [ ] **6.2** Destroy transfers all fields back to that controller
- [ ] **5.3** Verify field manager changes from `k8sconnect-patch-{id}` to original
- [ ] **5.4** Verify values unchanged after transfer

### 6. Multiple Previous Owners
- [ ] **7.1** Patch fields from 2 different controllers
- [ ] **7.2** Patch fields from 3+ different controllers
- [ ] **6.3** Destroy transfers each field to correct original owner
- [ ] **6.4** Verify mixed ownership transfers correctly

### 7. No Previous Owners (New Fields)
- [ ] **8.1** Add completely new field that didn't exist
- [ ] **7.2** Destroy logs warning about orphaned ownership
- [ ] **7.3** Verify destroy doesn't fail

### 8. Conflicting Ownership
- [ ] **9.1** Patch field currently owned by external controller
- [ ] **9.2** Verify warning about "Field Ownership Override"
- [ ] **8.3** Verify force=true takes ownership
- [ ] **8.4** Verify external controller listed in warning

## Phase 1 MVP - CRUD Tests

### 9. Create Operations
- [ ] **10.1** Basic create with valid patch
- [ ] **10.2** Verify ID generated
- [ ] **10.3** Verify managed_fields populated
- [ ] **9.4** Verify field_ownership populated
- [ ] **9.5** Verify previous_owners captured

### 10. Read/Drift Detection
- [ ] **11.1** No drift - values unchanged
- [ ] **11.2** External controller modifies one field
- [ ] **11.3** External controller modifies all fields
- [ ] **10.4** Resource deleted externally (patch removed from state)
- [ ] **10.5** Verify drift reflected in managed_fields

### 11. Update Operations
- [ ] **12.1** Change patch content (add field)
- [ ] **12.2** Change patch content (remove field)
- [ ] **12.3** Change patch content (modify value)
- [ ] **12.4** Change connection details
- [ ] **11.5** Preserve ID across updates
- [ ] **11.6** Preserve previous_owners across updates

### 12. Target Change (Replacement)
- [ ] **13.1** Try to change api_version (should fail)
- [ ] **13.2** Try to change kind (should fail)
- [ ] **13.3** Try to change name (should fail)
- [ ] **12.4** Try to change namespace (should fail)
- [ ] **12.5** Verify error mentions replacement required

### 13. Delete Operations
- [ ] **14.1** Basic delete with ownership transfer
- [ ] **14.2** Delete when target already deleted
- [ ] **14.3** Delete when connection fails (graceful)
- [ ] **14.4** Delete with no previous_owners
- [ ] **13.5** Verify values remain on resource
- [ ] **13.6** Verify ownership transferred back

## Phase 2 - Advanced Scenarios

### 14. Array Handling
- [ ] **15.1** Patch container by name (strategic merge key)
- [ ] **15.2** Patch env var by name
- [ ] **14.3** Simple array replacement
- [ ] **14.4** Array with complex objects

### 15. Deep Nesting
- [ ] **16.1** `spec.template.spec.containers[0].env[0].value`
- [ ] **16.2** `spec.template.spec.volumes[0].configMap.items[0].path`
- [ ] **15.3** Verify nested field extraction works
- [ ] **15.4** Verify nested ownership transfer works

### 16. Special Values
- [ ] **17.1** Empty string value
- [ ] **17.2** Null value (field removal)
- [ ] **17.3** Boolean values
- [ ] **16.4** Numeric values
- [ ] **16.5** Large string values

### 17. JSON Patch Operations
- [ ] **18.1** Add operation
- [ ] **18.2** Remove operation
- [ ] **18.3** Replace operation
- [ ] **18.4** Move operation
- [ ] **17.5** Copy operation
- [ ] **17.6** Test operation

### 18. Connection Types ⚠️ **DUPLICATE - Already tested in manifest resource**
- [ ] ~~18.1-18.6~~ Connection mechanisms already tested in manifest_test.go

### 19. Error Handling ⚠️ **DUPLICATE - Already tested in manifest resource**
- [ ] ~~19.1-19.5~~ Network/auth errors already tested in manifest_test.go

### 20. State Management ⚠️ **DUPLICATE - Already tested in manifest resource**
- [ ] ~~20.1-20.3~~ State operations already tested in manifest_test.go

### 21. Real-World Scenarios
- [ ] **22.1** Patch EKS aws-node DaemonSet
- [ ] **22.2** Patch GKE kube-proxy
- [ ] **22.3** Patch Helm-deployed Service
- [ ] **21.4** Patch operator-managed CRD instance
- [ ] **21.5** Multiple patches on same resource

### 22. Performance
- [ ] **23.1** Large patch (100+ fields)
- [ ] **22.2** Patch with large string values (10KB+)
- [ ] **22.3** Multiple patches in parallel

### 23. Ownership Edge Cases
- [ ] **24.1** Controller deletes field we patched
- [ ] **24.2** Controller adds field to our patch area
- [ ] **24.3** Two patches fighting for same field
- [ ] **23.4** Patch → manifest takeover (should fail)
- [ ] **23.5** Manifest → patch takeover (should fail)

## Test Priority

**P0 (Must have for MVP - ~34 unique tests):**
- Sections 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13
- **Unit tests:** 1 (helper functions)
- **Acceptance tests:** 1-13

**P1 (Should have - ~15 unique tests):**
- Sections 14, 15, 21
- Section 18-20 are **DUPLICATES** - skip (already tested in manifest)

**P2 (Nice to have - ~15 unique tests):**
- Sections 16, 17, 23
- Section 22 mostly **DUPLICATE** - only test patch-specific performance scenarios

**Current Test Coverage:**
- ✅ Unit tests: 21 tests (5 test functions covering helpers and safety)
- ✅ Acceptance tests: 7 test functions covering critical P0 scenarios
  - Self-patching prevention
  - Basic patch operations
  - Non-existent target
  - Single owner ownership transfer
  - Multiple owner ownership transfer
  - Patch type validation
  - Update patch content
  - Target change replacement
