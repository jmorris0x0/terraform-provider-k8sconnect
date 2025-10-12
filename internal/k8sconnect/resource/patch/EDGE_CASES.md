# Patch Resource Edge Cases Test Coverage

## Phase 1 MVP - Critical Safety Tests

### 1. Self-Patching Prevention (CRITICAL!)
- [ ] **1.1** Try to patch resource with `k8sconnect.terraform.io/terraform-id` annotation
- [ ] **1.2** Try to patch resource managed by `k8sconnect` field manager
- [ ] **1.3** Try to patch resource with legacy `k8sconnect.io/owned-by` annotation
- [ ] **1.4** Verify clear, actionable error message
- [ ] **1.5** Verify suggests using ignore_fields instead

### 2. take_ownership Validation (CRITICAL!)
- [ ] **2.1** Create patch without `take_ownership` field
- [ ] **2.2** Create patch with `take_ownership = false`
- [ ] **2.3** Create patch with `take_ownership = true` (should succeed)
- [ ] **2.4** Verify error explains forceful takeover

### 3. Target Resolution
- [ ] **3.1** Patch non-existent resource (should fail with "resource not found")
- [ ] **3.2** Patch cluster-scoped resource (no namespace)
- [ ] **3.3** Patch namespaced resource
- [ ] **3.4** Provide namespace for cluster-scoped resource (should be ignored)
- [ ] **3.5** Invalid api_version format
- [ ] **3.6** Invalid kind

### 4. Patch Type Validation
- [ ] **4.1** Specify both `patch` and `json_patch` (should fail)
- [ ] **4.2** Specify all three patch types (should fail)
- [ ] **4.3** Specify no patch type (should fail)
- [ ] **4.4** Valid strategic merge patch
- [ ] **4.5** Valid JSON patch
- [ ] **4.6** Valid merge patch

### 5. Patch Content Validation
- [ ] **5.1** Malformed YAML in `patch`
- [ ] **5.2** Malformed JSON in `json_patch`
- [ ] **5.3** Empty patch content
- [ ] **5.4** Patch with only metadata (should it be allowed?)

## Phase 1 MVP - Ownership Transfer Tests

### 6. Single Previous Owner
- [ ] **6.1** All patched fields from same controller (e.g., all EKS)
- [ ] **6.2** Destroy transfers all fields back to that controller
- [ ] **6.3** Verify field manager changes from `k8sconnect-patch-{id}` to original
- [ ] **6.4** Verify values unchanged after transfer

### 7. Multiple Previous Owners
- [ ] **7.1** Patch fields from 2 different controllers
- [ ] **7.2** Patch fields from 3+ different controllers
- [ ] **7.3** Destroy transfers each field to correct original owner
- [ ] **7.4** Verify mixed ownership transfers correctly

### 8. No Previous Owners (New Fields)
- [ ] **8.1** Add completely new field that didn't exist
- [ ] **8.2** Destroy logs warning about orphaned ownership
- [ ] **8.3** Verify destroy doesn't fail

### 9. Conflicting Ownership
- [ ] **9.1** Patch field currently owned by external controller
- [ ] **9.2** Verify warning about "Field Ownership Override"
- [ ] **9.3** Verify force=true takes ownership
- [ ] **9.4** Verify external controller listed in warning

## Phase 1 MVP - CRUD Tests

### 10. Create Operations
- [ ] **10.1** Basic create with valid patch
- [ ] **10.2** Verify ID generated
- [ ] **10.3** Verify managed_fields populated
- [ ] **10.4** Verify field_ownership populated
- [ ] **10.5** Verify previous_owners captured

### 11. Read/Drift Detection
- [ ] **11.1** No drift - values unchanged
- [ ] **11.2** External controller modifies one field
- [ ] **11.3** External controller modifies all fields
- [ ] **11.4** Resource deleted externally (patch removed from state)
- [ ] **11.5** Verify drift reflected in managed_fields

### 12. Update Operations
- [ ] **12.1** Change patch content (add field)
- [ ] **12.2** Change patch content (remove field)
- [ ] **12.3** Change patch content (modify value)
- [ ] **12.4** Change connection details
- [ ] **12.5** Preserve ID across updates
- [ ] **12.6** Preserve previous_owners across updates

### 13. Target Change (Replacement)
- [ ] **13.1** Try to change api_version (should fail)
- [ ] **13.2** Try to change kind (should fail)
- [ ] **13.3** Try to change name (should fail)
- [ ] **13.4** Try to change namespace (should fail)
- [ ] **13.5** Verify error mentions replacement required

### 14. Delete Operations
- [ ] **14.1** Basic delete with ownership transfer
- [ ] **14.2** Delete when target already deleted
- [ ] **14.3** Delete when connection fails (graceful)
- [ ] **14.4** Delete with no previous_owners
- [ ] **14.5** Verify values remain on resource
- [ ] **14.6** Verify ownership transferred back

## Phase 2 - Advanced Scenarios

### 15. Array Handling
- [ ] **15.1** Patch container by name (strategic merge key)
- [ ] **15.2** Patch env var by name
- [ ] **15.3** Simple array replacement
- [ ] **15.4** Array with complex objects

### 16. Deep Nesting
- [ ] **16.1** `spec.template.spec.containers[0].env[0].value`
- [ ] **16.2** `spec.template.spec.volumes[0].configMap.items[0].path`
- [ ] **16.3** Verify nested field extraction works
- [ ] **16.4** Verify nested ownership transfer works

### 17. Special Values
- [ ] **17.1** Empty string value
- [ ] **17.2** Null value (field removal)
- [ ] **17.3** Boolean values
- [ ] **17.4** Numeric values
- [ ] **17.5** Large string values

### 18. JSON Patch Operations
- [ ] **18.1** Add operation
- [ ] **18.2** Remove operation
- [ ] **18.3** Replace operation
- [ ] **18.4** Move operation
- [ ] **18.5** Copy operation
- [ ] **18.6** Test operation

### 19. Connection Types
- [ ] **19.1** Kubeconfig from file
- [ ] **19.2** Kubeconfig inline
- [ ] **19.3** Token auth
- [ ] **19.4** Exec auth (aws eks get-token)
- [ ] **19.5** Client certificate auth
- [ ] **19.6** Context switching

### 20. Error Handling
- [ ] **20.1** Network timeout during create
- [ ] **20.2** Network timeout during delete
- [ ] **20.3** Invalid credentials
- [ ] **20.4** Cluster unreachable
- [ ] **20.5** API server returns 5xx

### 21. State Management
- [ ] **21.1** Import attempt (should fail with clear message)
- [ ] **21.2** State file corruption recovery
- [ ] **21.3** Upgrade from future version

### 22. Real-World Scenarios
- [ ] **22.1** Patch EKS aws-node DaemonSet
- [ ] **22.2** Patch GKE kube-proxy
- [ ] **22.3** Patch Helm-deployed Service
- [ ] **22.4** Patch operator-managed CRD instance
- [ ] **22.5** Multiple patches on same resource

### 23. Performance
- [ ] **23.1** Large patch (100+ fields)
- [ ] **23.2** Patch with large string values (10KB+)
- [ ] **23.3** Multiple patches in parallel

### 24. Ownership Edge Cases
- [ ] **24.1** Controller deletes field we patched
- [ ] **24.2** Controller adds field to our patch area
- [ ] **24.3** Two patches fighting for same field
- [ ] **24.4** Patch → manifest takeover (should fail)
- [ ] **24.5** Manifest → patch takeover (should fail)

## Test Priority

**P0 (Must have for MVP):**
- All of section 1, 2, 3, 4, 5, 6, 7, 8, 10, 11, 12, 13, 14

**P1 (Should have):**
- 15, 16, 19, 22

**P2 (Nice to have):**
- 17, 18, 20, 21, 23, 24
