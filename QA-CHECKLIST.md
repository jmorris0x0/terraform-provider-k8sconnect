# QA Checklist

## Mission: Break EVERYTHING or polish EVERYTHING

**PRIMARY GOALS:**
1. **World-Class UX** - Every error and warning must be helpful, clear, and actionable
2. **No Surprises** - Behavior must be predictable and well-communicated
3. **Enterprise Quality** - This is production software, not a prototype

DO NOT STOP until EVERY item is checked off.

---

## IMPORTANT: How to Use This Document

**DO NOT modify this file during testing.** This is a reusable template. Checking off boxes, adding notes, or editing this document in any way defeats its purpose for future releases.

**For each test run, create a single results file:**

1. Create ONE file: `QA-RESULTS-<description>.md` (e.g., `QA-RESULTS-ADR023.md`)
2. At the top, record metadata: date, provider version/commit, Terraform version, Kubernetes version
3. ALL phases go into this one file. Do not create separate files per phase or section.
4. For each phase tested, log:
   - Phase name
   - Each step attempted with **PASS** or **FAIL**
   - For FAILs: exact error output, expected vs actual behavior, severity
   - Any observations, UX concerns, or notes worth capturing
5. Update the same file as you progress through phases — it is the single living record of the entire test run
6. At the bottom, record a summary: total pass/fail counts, blocking issues, and overall assessment

**Process: run all phases, then fix, then certify.**

Do NOT restart from Phase 0 after every bug found — that wastes time re-running passing phases. Instead:

1. Run through ALL phases, collecting every bug and UX issue
2. Fix everything in one batch (with unit tests for each fix)
3. Do ONE final clean run from Phase 0 on the release commit to certify

The final clean run is the one that matters for release. Intermediate runs are for bug discovery only.

One test run = one results file. This checklist is the reusable reference.

---

## Phase 0: Setup and Happy Path (MUST PASS FIRST!)

### Build and Install
- [ ] Run `make install` from project root (builds and installs provider)
- [ ] Verify provider installed to `~/.terraform.d/plugins/`

### Environment Setup
- [ ] Navigate to `scenarios/kind-validation/`
- [ ] Run `./reset.sh` to reset environment
- [ ] Review `main.tf` - does it test all core behaviors and edge cases?
- [ ] Read any relevant docs for features being tested

### Initial Apply (The Happy Path)
- [ ] Run `terraform init` - succeeds without errors?
- [ ] Run `terraform plan` - output looks correct? No unexpected changes?
- [ ] Run `terraform apply` - all ~62 resources created successfully?
- [ ] Review apply output - any warnings or concerning messages?
- [ ] Check pod status: `KUBECONFIG=./kind-validation-config kubectl get pods -A`
- [ ] All pods running/completed (except intentional test failures)?

### Zero-Diff Stability Test
- [ ] Run `terraform apply` again immediately
- [ ] Should show "No changes" (zero diff)
- [ ] If diff appears, investigate before proceeding!
- [ ] Common culprits: nodePort randomness, timestamp fields, HPA replicas

### 🎯 Behavioral Expectations (NO SURPRISES!)
Verify these behaviors are consistent:
- [ ] CREATE never silently adopts existing resources
- [ ] Import is always required for unmanaged resources
- [ ] Warnings appear for drift, not errors (unless fatal)
- [ ] Resource removal is always explicit (no silent deletions)
- [ ] Ownership transitions are clearly communicated
- [ ] State remains consistent after errors

**STOP HERE if Phase 0 fails. Fix issues before continuing.**

---

## Phase 1: k8sconnect_object Error Testing

**🎯 UX FOCUS**: Every error must tell the user WHAT went wrong, WHY it happened, and HOW to fix it

### Invalid Resource Discovery
- [ ] Invalid kind (completely made up)
  - [ ] ✅ Error clearly states the kind doesn't exist?
  - [ ] ✅ Suggests checking spelling or available kinds?
- [ ] Invalid API version (non-existent group)
  - [ ] ✅ Error explains API version format?
  - [ ] ✅ Shows example of correct format?
- [ ] Malformed API version (invalid format)
- [ ] CRD that doesn't exist yet
  - [ ] ✅ Error suggests checking if CRD is installed?
  - [ ] ✅ Provides kubectl command to list CRDs?
- [ ] Valid kind but wrong API version

### Schema Validation Errors
- [ ] Missing required field (Deployment without selector)
- [ ] Invalid field name (not in schema)
- [ ] Wrong field type (string instead of int)
- [ ] Invalid nested field
- [ ] Extra fields not in schema

### Naming and Format Validation
- [ ] Invalid resource name (uppercase)
- [ ] Invalid resource name (too long >253 chars)
- [ ] Invalid resource name (special characters)
- [ ] Invalid namespace name (uppercase)
- [ ] Invalid label key (starts with number)
- [ ] Invalid label key (invalid characters)
- [ ] Invalid annotation key format

### Value Validation
- [ ] Negative value for positive-only field (replicas: -5)
- [ ] Value out of range
- [ ] Invalid enum value
- [ ] Invalid port number (>65535)
- [ ] Invalid protocol value

### Resource Constraints
- [ ] Resource in non-existent namespace
- [ ] Namespace-scoped resource without namespace
- [ ] Cluster-scoped resource with namespace specified
- [ ] Immutable field modification
- [ ] Identity field change (triggers replacement)

### YAML Issues
- [ ] Empty yaml_body
- [ ] yaml_body with only comments
- [ ] YAML with unknown interpolations during plan
- [ ] Very large YAML payload

---

## Phase 2: k8sconnect_patch Error Testing

### Target Validation
- [ ] Patch non-existent resource
- [ ] Patch with invalid target kind
- [ ] Patch with invalid target API version
- [ ] Patch with invalid target namespace
- [ ] Patch cluster-scoped resource with namespace

### Patch Data Issues
- [ ] Invalid JSON in patch
- [ ] Empty patch
- [ ] Patch removes required field
- [ ] Patch modifies immutable field
- [ ] Strategic merge patch on resource that doesn't support it

### Conflict Scenarios
- [ ] Patch field owned by different manager
- [ ] Patch field that doesn't exist
- [ ] Patch with field path typo

---

## Phase 3: k8sconnect_wait Error Testing

### Object Reference Issues
- [ ] Wait on non-existent resource 
- [ ] Wait with invalid kind in object_ref
- [ ] Wait with invalid API version
- [ ] Wait with wrong namespace

### Condition Waits
- [ ] Wait for condition that never exists
- [ ] Wait for condition with wrong status
- [ ] Wait for condition on resource that doesn't support conditions
- [ ] Condition timeout (with good error showing current state)

### Field Waits
- [ ] Wait for non-existent field
- [ ] Wait for field with typo in path
- [ ] Wait for field that never gets populated
- [ ] Field timeout showing current value

### Field Value Waits
- [ ] Wait for impossible value
- [ ] Wait for value with wrong type
- [ ] Multiple fields, one impossible
- [ ] Field value timeout showing all current values

### Rollout Waits
- [ ] Wait for rollout on non-rollout resource (ConfigMap)
- [ ] Wait for rollout with bad image (shows pod errors)
- [ ] Wait for rollout timeout

### Timeout Scenarios
- [ ] Zero timeout
- [ ] Invalid timeout format
- [ ] Very short timeout (<1s)

---

## Phase 4: Datasource Error Testing

### data.k8sconnect_object
- [ ] Missing resource 
- [ ] Invalid kind 
- [ ] Invalid API version
- [ ] Invalid namespace
- [ ] Resource exists but wrong namespace specified

### data.k8sconnect_yaml_split
- [ ] Empty YAML input (content = "")
- [ ] Invalid YAML syntax
- [ ] YAML with no resources (only comments)
- [ ] YAML with unsupported resource types
- [ ] Empty pattern (no files match)
- [ ] Invalid kustomize path (directory doesn't exist)
- [ ] Kustomize path without kustomization.yaml
- [ ] Kustomize with broken configuration (invalid patch)
- [ ] Valid kustomize build (happy path)

### data.k8sconnect_yaml_scoped
- [ ] Empty YAML input (content = "")
- [ ] Invalid YAML syntax
- [ ] YAML with no cluster-scoped resources
- [ ] YAML with no namespaced resources
- [ ] Empty pattern (no files match)
- [ ] Invalid kustomize path (directory doesn't exist)
- [ ] Kustomize with all three categories (CRDs + cluster + namespaced)
- [ ] Valid kustomize build categorization (verify CRDs separate from CRs)

---

## Phase 5: Connection/Auth Error Testing

### Connection Issues
- [ ] Invalid cluster host
- [ ] Invalid port
- [ ] Cluster doesn't exist
- [ ] Network timeout

### Auth Issues
- [ ] Invalid client certificate
- [ ] Invalid client key
- [ ] Invalid CA certificate
- [ ] Invalid token
- [ ] Expired credentials

### ADR-023: Resilient Read Auth Behavior
- [ ] Test with `token` — invalid token during Read should produce WARNING (not error)
  - [ ] Warning says "Using Prior State — Authentication Failed"?
  - [ ] Warning includes the actual error detail?
  - [ ] Warning suggests checking cluster authentication?
  - [ ] Resource NOT removed from state (prior state preserved)?
- [ ] Test k8sconnect_patch Read with expired token — warning, not error?
- [ ] Test k8sconnect_wait Read with expired token — warning, not error?
- [ ] Verify: auth errors during Create/Update/Delete are still HARD ERRORS (not warnings)
- [ ] Verify: connection errors (bad host, network timeout) are still HARD ERRORS during Read

---

## Phase 6: Edge Cases and Boundaries

### Large Payloads
- [ ] ConfigMap with 1MB of data
- [ ] Secret with large binary data
- [ ] Resource with 100 labels
- [ ] Resource with very long annotation values

### Special Characters
- [ ] Unicode in all text fields
- [ ] Emoji in labels/annotations
- [ ] Special YAML characters (quotes, colons, etc.)
- [ ] Newlines and tabs in data

### Concurrency and Race Conditions
- [ ] Multiple resources depending on same resource
- [ ] for_each with resource replacement
- [ ] Rapid apply/destroy cycles

### State Edge Cases
- [ ] Resource deleted outside Terraform (drift detection)
- [ ] Resource modified outside Terraform (drift warning)
- [ ] Import then modify
- [ ] Resource with finalizer (deletion behavior)

---

## Phase 7: Warning Message Quality

### Drift Warnings
- [ ] Field modified by kubectl (shows warning with manager)
- [ ] Field modified by HPA (correctly ignores if not owned)
- [ ] Multiple fields drifted (clear list)

### Ownership Warnings
- [ ] Patch taking ownership (only shows on actual change)
- [ ] Field ownership transition (clear explanation)

---

## Phase 8: UX Polish Verification

### 🚨 UX RED FLAGS - These should NEVER happen:
- [ ] ❌ Generic "operation failed" without context
- [ ] ❌ Stack traces or panic messages shown to user
- [ ] ❌ Internal error codes without explanation
- [ ] ❌ "Contact your administrator" (we ARE the administrators!)
- [ ] ❌ Silent failures (operation fails but no error shown)
- [ ] ❌ Errors that blame the user without helping
- [ ] ❌ Inconsistent terminology (mixing "resource"/"object"/"manifest")
- [ ] ❌ Missing resource identification (which resource failed?)
- [ ] ❌ Timeout without showing current state
- [ ] ❌ "Unexpected" errors without guidance
- [ ] ❌ Errors and warnings that are pointlessly verbose. We don't need a book.
- [ ] ❌ Errors and warnings that contain redundent information on different lines.


### For EVERY error message found:
- [ ] Is the error title clear and specific?
- [ ] Does it explain what went wrong?
- [ ] Does it explain why it might have happened?
- [ ] Does it tell the user how to fix it?
- [ ] Are the suggestions actionable?
- [ ] Is the formatting clean and readable?
- [ ] Does it use user-friendly language (not internal jargon)?
- [ ] Does it provide kubectl commands when helpful?
- [ ] Does it show current state when relevant?
- [ ] Is it as concise as possible without sacrficing utility?
- [ ] Does it follow existing patterns?

### World-Class Error Examples:
✅ GOOD: "Resource Already Exists: The ConfigMap 'app-config' already exists in namespace 'production'. To manage this existing resource, use 'terraform import k8sconnect_object.config <import-id>'"

❌ BAD: "apply failed: conflict"

---

## Phase 9: Regression Testing

After ANY fix:
- [ ] Run full happy path again (62 resources)
- [ ] Verify zero-diff still works
- [ ] Run ALL error tests again
- [ ] Check for any new issues introduced

---

## Phase 10: Resource Modification Edge Cases

**Goal**: Verify updates work correctly and maintain zero-diff stability

### Scaling Tests
- [ ] Scale Deployment replicas (2 → 5 → 1)
- [ ] Run `terraform apply` after each change
- [ ] Verify zero-diff after scaling completes
- [ ] Scale StatefulSet replicas (1 → 3 → 2)
- [ ] Scale ReplicaSet replicas
- [ ] Verify HPA respects minReplicas/maxReplicas changes

### Image and Container Changes
- [ ] Change Deployment image (nginx:latest → nginx:1.25)
- [ ] Add new environment variable to container
- [ ] Remove environment variable
- [ ] Modify existing environment variable value
- [ ] Change container command/args
- [ ] Add new container to pod spec
- [ ] Remove container from pod spec

### Labels and Annotations
- [ ] Add new label to existing resource
- [ ] Modify existing label value
- [ ] Remove label
- [ ] Add annotation with long value (test large data)
- [ ] Add annotation with special characters (unicode, emoji)
- [ ] Verify selector-related labels trigger replacement when appropriate

### Resource Limits and Requests
- [ ] Modify memory request (64Mi → 128Mi)
- [ ] Modify CPU limit (100m → 200m)
- [ ] Test quantity normalization:
  - Change 32Mi to 33554432 (bytes equivalent)
  - Run `terraform apply` - should show zero diff
  - Change back to 32Mi - should show zero diff
- [ ] Add limits where none existed
- [ ] Remove limits

### ConfigMap and Secret Updates
- [ ] Modify ConfigMap data key
- [ ] Add new data key to ConfigMap
- [ ] Remove data key from ConfigMap
- [ ] Update Secret data (base64 encoded)
- [ ] Verify pods referencing them handle updates

### YAML Formatting Variations
- [ ] Change YAML style (flow → block, quotes → no quotes)
- [ ] Add comments to YAML
- [ ] Change indentation (2 spaces → 4 spaces)
- [ ] Use different string quoting styles
- [ ] Verify all format changes show zero-diff (format shouldn't matter)

### Volume and Storage Changes
- [ ] Modify PVC storage size (note: may require new PVC if not expandable)
- [ ] Change StorageClass (requires replacement)
- [ ] Modify volume mount paths

### Delete Protection and Data Persistence
**Goal**: Verify critical resources can't be accidentally destroyed

#### PVC/PV Delete Protection Testing
- [ ] Create PVC with `delete_protection = true`:
  ```hcl
  resource "k8sconnect_object" "protected_pvc" {
    yaml_body = <<-YAML
      apiVersion: v1
      kind: PersistentVolumeClaim
      metadata:
        name: protected-data
        namespace: default
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 1Gi
    YAML
    cluster = local.cluster
    delete_protection = true
  }
  ```
- [ ] Verify PVC is created and bound
- [ ] Attempt `terraform destroy` - should FAIL with clear error
- [ ] ✅ Error message explains delete_protection is enabled?
- [ ] ✅ Error message shows how to disable protection?
- [ ] Set `delete_protection = false` and run `terraform apply`
- [ ] Now run `terraform destroy` - should succeed

#### StorageClass Reclaim Policy Testing
- [ ] Create StorageClass with `reclaimPolicy: Retain`:
  ```hcl
  resource "k8sconnect_object" "retain_storage" {
    yaml_body = <<-YAML
      apiVersion: storage.k8s.io/v1
      kind: StorageClass
      metadata:
        name: retain-test
      provisioner: rancher.io/local-path  # or your cluster's provisioner
      reclaimPolicy: Retain
    YAML
    cluster = local.cluster
  }
  ```
- [ ] Create PVC using this StorageClass
- [ ] Write test data to a pod using this PVC
- [ ] Remove PVC from Terraform config (or destroy)
- [ ] Verify PV still exists: `kubectl get pv`
- [ ] ✅ PV should be in "Released" state, not deleted
- [ ] Clean up manually: `kubectl delete pv <pv-name>`

#### Finalizer Handling
- [ ] Create resource with finalizer:
  ```hcl
  resource "k8sconnect_object" "finalized" {
    yaml_body = <<-YAML
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: finalized-resource
        namespace: default
        finalizers:
          - example.com/test-finalizer
      data:
        test: value
    YAML
    cluster = local.cluster
  }
  ```
- [ ] Attempt `terraform destroy`
- [ ] ✅ Destroy should hang waiting for finalizer to be removed
- [ ] ✅ Check timeout message quality (should explain finalizer blocking deletion)
- [ ] Manually remove finalizer: `kubectl patch configmap finalized-resource -p '{"metadata":{"finalizers":null}}'`
- [ ] Verify destroy completes successfully

#### Force Destroy Testing
- [ ] Create resource with finalizer AND `force_destroy = true`
- [ ] Run `terraform destroy`
- [ ] ✅ Should force-delete despite finalizer
- [ ] Verify resource is removed from cluster

---

## Phase 11: Comprehensive Drift Testing

**Goal**: Verify drift detection and correction for fields we own

### Prerequisites
- [ ] Ensure `KUBECONFIG=./kind-validation-config` is set or prefix commands
- [ ] Identify resources managed by k8sconnect (check managedFields)

### Replica Drift Tests
- [ ] Scale Deployment via kubectl: `kubectl scale deployment web-deployment --replicas=5 -n kind-validation`
- [ ] Run `terraform plan` - should detect drift (if replicas not ignored)
- [ ] Run `terraform apply` - should correct drift back to configured value
- [ ] Verify warning message mentions "kubectl" as the manager who modified it

### Label and Annotation Drift
- [ ] Add label via kubectl: `kubectl label deployment web-deployment test=drift -n kind-validation`
- [ ] Run `terraform plan` - should show drift
- [ ] Run `terraform apply` - should remove unexpected label
- [ ] Modify existing label via kubectl
- [ ] Verify drift detected and corrected

### Image Drift Tests
- [ ] Change container image via kubectl: `kubectl set image deployment/web-deployment nginx=nginx:1.24 -n kind-validation`
- [ ] Run `terraform plan` - should detect image drift
- [ ] Run `terraform apply` - should correct back to configured image
- [ ] Check error message quality

### Data Drift (ConfigMap/Secret)
- [ ] Modify ConfigMap data via kubectl: `kubectl edit configmap app-config -n kind-validation`
- [ ] Run `terraform plan` - should detect drift
- [ ] Run `terraform apply` - should restore correct data
- [ ] Modify Secret data via kubectl
- [ ] Verify drift detection and correction

### HPA Managed Fields (Ignore Test)
- [ ] If HPA is managing replicas, verify we DON'T show drift on replica count
- [ ] HPA modifies deployment replicas - should NOT trigger warning
- [ ] Verify ignore_fields works correctly for HPA scenario
- [ ] Check managedFields shows HPA as owner of spec.replicas

### Resource Deletion Drift
- [ ] Delete resource via kubectl: `kubectl delete deployment web-deployment -n kind-validation`
- [ ] Run `terraform plan` - should show resource needs to be created
- [ ] Run `terraform apply` - should recreate resource
- [ ] Verify resource comes back with correct configuration

### Multi-Field Drift
- [ ] Modify multiple fields via kubectl (replicas + image + labels)
- [ ] Run `terraform plan` - should show all drifted fields clearly
- [ ] Verify error message lists all changes with responsible managers
- [ ] Run `terraform apply` - should correct all drifts

### Ownership Transfer Testing
- [ ] Create resource with kubectl first
- [ ] Import into Terraform (covered in Phase 13)
- [ ] Modify via Terraform - should take ownership cleanly
- [ ] Check managedFields shows k8sconnect as owner

### Ownership Annotation Drift
- [ ] Remove k8sconnect ownership annotation via kubectl:
  ```bash
  kubectl annotate configmap <name> -n kind-validation k8sconnect.terraform.io/terraform-id-
  ```
- [ ] Run `terraform plan` - should detect the missing annotation
- [ ] Verify WARNING message: "Resource Annotations Missing - Will Restore"
- [ ] Run `terraform apply` - should restore the annotation
- [ ] Verify annotation is restored:
  ```bash
  kubectl get configmap <name> -n kind-validation -o yaml | grep "k8sconnect.terraform.io/terraform-id"
  ```
- [ ] Check warning message quality and clarity

---

## Phase 12: for_each Replacement Race Condition Test

**Goal**: Verify Delete() exits gracefully when ownership changes during wait

### Setup
- [ ] Identify a resource using for_each in main.tf (e.g., split_resources)
- [ ] Note the current for_each key (e.g., `"configmap.cluster-config"`)
- [ ] Verify resource is currently applied and stable

### Execute Replacement Test
- [ ] Change the for_each key to a NEW key
- [ ] Example: `"configmap.cluster-config"` → `"configmap.default.cluster-config"`
- [ ] IMPORTANT: Both keys must map to the SAME Kubernetes object
  - Same metadata.name
  - Same metadata.namespace
  - Only the Terraform resource identifier changes
- [ ] Run `terraform plan` - should show delete + create
- [ ] Run `terraform apply` with timer running
- [ ] Apply should complete in seconds (NOT timeout at 5 minutes)
- [ ] Monitor for "ownership changed" detection in logs

### Verification
- [ ] Verify resource still exists: `kubectl get configmap cluster-config -n kind-validation`
- [ ] Check resource data is correct (not recreated, just adopted)
- [ ] Run `terraform apply` again - should show zero diff
- [ ] Check managedFields - should show k8sconnect as manager

### Expected Behavior
- [ ] Delete() should detect ownership change during wait loop
- [ ] Should log graceful exit (not timeout error)
- [ ] Create() should adopt existing resource with updated data
- [ ] Total operation completes in < 30 seconds (not 5 minutes)

**If timeout occurs**: The race condition fix is broken!

---

## Phase 13: Comprehensive Import Testing

**Goal**: Test complete import workflow including config generation

### Setup Import Test Environment
- [ ] Create import test namespace: `kubectl create namespace import-test`
- [ ] Verify namespace exists: `kubectl get namespace import-test`

### Import Test 1: Deployment with Generated Config
- [ ] Create deployment via kubectl:
  ```bash
  kubectl apply -f - <<EOF
  apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: import-test-deployment
    namespace: import-test
    labels:
      app: import-test
  spec:
    replicas: 3
    selector:
      matchLabels:
        app: import-test
    template:
      metadata:
        labels:
          app: import-test
      spec:
        containers:
        - name: nginx
          image: nginx:latest
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
  EOF
  ```
- [ ] Create new file `import-test.tf` with import block only:
  ```hcl
  import {
    to = k8sconnect_object.imported_deployment
    id = "apps/v1//Deployment/import-test/import-test-deployment"
  }
  ```
- [ ] Run `terraform plan -generate-config-out=generated.tf`
- [ ] Verify generated.tf was created and contains deployment config
- [ ] Edit generated.tf to add cluster connection (copy from main.tf)
- [ ] Run `terraform plan` - should show import with no changes after
- [ ] Run `terraform apply` - import should succeed

### Import Verification
- [ ] Check managedFields: `kubectl get deployment import-test-deployment -n import-test -o yaml | grep -A 5 managedFields`
- [ ] Verify "k8sconnect" appears as manager
- [ ] Verify "operation: Apply" and "fieldsType: FieldsV1"
- [ ] Run `terraform apply` again - should show zero diff (critical!)

### Import Test 2: Service Import
- [ ] Create service via kubectl
- [ ] Import using same workflow
- [ ] Verify zero-diff after import

### Import Test 3: ConfigMap Import
- [ ] Create configmap with multiple data keys via kubectl
- [ ] Import and verify all data is captured
- [ ] Verify zero-diff

### Post-Import Modification Tests
- [ ] Modify imported deployment (change replicas)
- [ ] Run `terraform apply` - should update successfully
- [ ] Verify k8sconnect maintains ownership
- [ ] Scale via kubectl (create drift)
- [ ] Verify Terraform detects and corrects drift

### Import Test 4: CRD Import (if applicable)
- [ ] Import existing Cactus CR
- [ ] Verify CRD-based resources import correctly
- [ ] Test updates to imported CR

### Cleanup Import Tests
- [ ] Remove import test resources from Terraform configs
- [ ] Run `terraform apply` - should destroy import-test resources
- [ ] Delete namespace: `kubectl delete namespace import-test`
- [ ] Remove import-test.tf and generated.tf files

---

## Phase 13.5: Collision Detection & Ownership Recovery Testing

**Goal**: Verify collision detection prevents silent adoption and annotation loss recovery works

### CREATE Collision Detection
- [ ] Create ConfigMap with kubectl:
  ```bash
  kubectl create configmap collision-test -n kind-validation --from-literal=key=value
  ```
- [ ] Add same ConfigMap to Terraform config (without import):
  ```hcl
  resource "k8sconnect_object" "collision_test" {
    yaml_body = <<-YAML
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: collision-test
        namespace: kind-validation
      data:
        key: value
    YAML
    cluster = local.cluster
  }
  ```
- [ ] Run `terraform apply` - should ERROR with "Resource Already Exists"
- [ ] Verify error message includes import instructions
- [ ] Verify it does NOT silently adopt the resource
- [ ] Remove the collision_test resource from config

### Annotation Loss Recovery
- [ ] Create new ConfigMap via Terraform:
  ```hcl
  resource "k8sconnect_object" "annotation_test" {
    yaml_body = <<-YAML
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: annotation-test
        namespace: kind-validation
      data:
        test: annotation-recovery
    YAML
    cluster = local.cluster
  }
  ```
- [ ] Apply to create the resource
- [ ] Verify ownership annotation exists:
  ```bash
  kubectl get configmap annotation-test -n kind-validation -o yaml | grep "k8sconnect.terraform.io/terraform-id"
  ```
- [ ] Remove annotation manually:
  ```bash
  kubectl annotate configmap annotation-test -n kind-validation k8sconnect.terraform.io/terraform-id-
  ```
- [ ] Run `terraform plan` - should show changes needed
- [ ] Verify WARNING message about "Resource Annotations Missing - Will Restore"
- [ ] Run `terraform apply` - should restore annotations
- [ ] Verify annotation is back:
  ```bash
  kubectl get configmap annotation-test -n kind-validation -o yaml | grep "k8sconnect.terraform.io/terraform-id"
  ```
- [ ] Run `terraform apply` again - should show zero diff

### Multi-State Collision Testing
- [ ] Create resource with Terraform in current workspace (state A)
- [ ] Create new workspace: `terraform workspace new collision-test`
- [ ] Add same resource to config in new workspace
- [ ] Run `terraform apply` - should ERROR with ownership conflict
- [ ] Verify error explains the terraform-id mismatch
- [ ] Switch back to default workspace: `terraform workspace select default`
- [ ] Clean up test workspace: `terraform workspace delete collision-test`

### Post-Collision Import Test
- [ ] After collision detection, use import to adopt kubectl-created resource:
  ```bash
  terraform import k8sconnect_object.collision_test "//v1/ConfigMap/kind-validation/collision-test"
  ```
- [ ] Run `terraform apply` - should succeed and add ownership annotations
- [ ] Verify k8sconnect now owns the resource
- [ ] Clean up imported resource

---

## Phase 14: k8sconnect_helm_release Testing

**Goal**: Test Helm release lifecycle, error handling, and edge cases

### Prerequisites
- [ ] Helm CLI installed on system
- [ ] Test chart available (use test/testdata/charts/simple-test)
- [ ] Provider rebuilt and installed with Helm support

### Basic Helm Release Tests
- [ ] Create basic Helm release from local chart
- [ ] Verify release installed: `helm list -A --kubeconfig=./kind-validation-config`
- [ ] Verify pods deployed by release are running
- [ ] Run `terraform apply` again - should show zero diff
- [ ] Update release (change values, upgrade chart version if applicable)
- [ ] Verify revision incremented
- [ ] Destroy release - verify uninstall clean

### Helm Values Testing
- [ ] Deploy with inline `values` YAML
- [ ] Deploy with `set` parameters
- [ ] Deploy with `set_string` parameters
- [ ] Deploy with combination of values + set + set_string
- [ ] Verify precedence: set_string > set > values > chart defaults
- [ ] Test values merging works correctly
- [ ] Verify zero-diff with different YAML formatting styles

### Helm Repository Testing
- [ ] Install from OCI repository (e.g., public.ecr.aws)
- [ ] Install from HTTP repository (e.g., https://charts.bitnami.com/bitnami)
- [ ] Test invalid repository URL - check error message quality
- [ ] Test repository requiring authentication
- [ ] Test chart not found in repository - check error message

### Chart Path and Source Testing
- [ ] Install from local chart path (relative)
- [ ] Install from local chart path (absolute)
- [ ] Test non-existent local path - check error message
- [ ] Test path to directory without Chart.yaml - check error
- [ ] Test chart with dependencies - verify dependencies downloaded
- [ ] Test chart with missing dependencies - check error

### Helm Error Scenarios
- [ ] Invalid release name (uppercase, special chars)
- [ ] Release name too long (>53 chars)
- [ ] Invalid namespace
- [ ] Non-existent namespace (without create_namespace)
- [ ] Invalid chart version
- [ ] Chart version not found in repository
- [ ] Malformed values YAML
- [ ] Invalid set parameter format
- [ ] Conflicting values in set vs set_string
- [ ] Install timeout (bad image that never starts)
  - [ ] ✅ Error shows pod status and events?
  - [ ] ✅ Error explains why pods aren't ready?
  - [ ] ✅ Suggests kubectl commands to debug?

### Helm Wait and Timeout Testing
- [ ] Deploy with `wait = true` and good image
- [ ] Deploy with `wait = true` and bad image (should timeout)
- [ ] Deploy with `wait = false` (should not wait for rollout)
- [ ] Test custom timeout values (30s, 5m, 10m)
- [ ] Test zero timeout - check error message
- [ ] Test negative timeout - check error message
- [ ] Test invalid timeout format - check error message

### Helm Upgrade and Rollback
- [ ] Deploy release v1
- [ ] Upgrade to v2 (change values)
- [ ] Verify revision = 2
- [ ] Upgrade to v3 (change chart version if using versioned chart)
- [ ] Verify revision = 3
- [ ] Check helm history shows all revisions
- [ ] Modify Terraform to go back to v1 values
- [ ] Verify Terraform applies changes (not manual rollback)

### Helm Force and Replace
- [ ] Deploy release normally
- [ ] Deploy with `force = true` - should force update
- [ ] Deploy with `replace = true` - should delete and recreate
- [ ] Verify both work without errors

### Helm Namespace Handling
- [ ] Deploy with existing namespace
- [ ] Deploy with `create_namespace = true` (non-existent namespace)
- [ ] Verify namespace created
- [ ] Destroy release - verify namespace NOT deleted (Helm behavior)
- [ ] Deploy to kube-system namespace - should work
- [ ] Deploy to default namespace - should work

### Helm State and Import
- [ ] Create Helm release manually with `helm install`
- [ ] Import into Terraform
- [ ] Verify Terraform adopts release
- [ ] Modify via Terraform
- [ ] Verify Terraform can manage imported release
- [ ] Check managedFields ownership

### Helm Drift Detection
- [ ] Deploy release via Terraform
- [ ] Modify release with `helm upgrade` manually
- [ ] Run `terraform plan` - should detect drift?
- [ ] Run `terraform apply` - should correct drift?
- [ ] Delete release with `helm uninstall`
- [ ] Run `terraform plan` - should show recreate needed
- [ ] Run `terraform apply` - should reinstall

### Helm Annotations and Labels
- [ ] Deploy with custom annotations on release
- [ ] Deploy with custom labels
- [ ] Verify annotations appear in deployed resources
- [ ] Modify annotations - verify update works

### Helm Multi-Cluster
- [ ] Deploy same release to multiple clusters
- [ ] Use different cluster configs per release
- [ ] Verify releases isolated per cluster
- [ ] Destroy one cluster's release - other unaffected

### Helm Sensitive Values
- [ ] Deploy with sensitive values (passwords, tokens)
- [ ] Verify sensitive values not shown in plan output
- [ ] Verify sensitive values not in state (if using sensitive attribute)
- [ ] Modify sensitive value - verify update works

### Helm Large Charts
- [ ] Deploy chart with many resources (50+)
- [ ] Verify all resources created
- [ ] Verify timeout sufficient for large deployments
- [ ] Update large chart - verify performance acceptable

### Helm Edge Cases
- [ ] Release name with hyphens
- [ ] Release name with numbers
- [ ] Chart with uppercase letters in name
- [ ] Very long values YAML (10KB+)
- [ ] Values with special characters (quotes, newlines)
- [ ] Values with unicode and emoji
- [ ] Empty values = "" vs null

### Helm Cleanup Testing
- [ ] Verify `terraform destroy` cleanly uninstalls all releases
- [ ] Check no orphaned Helm releases: `helm list -A`
- [ ] Check no orphaned namespaces (if create_namespace used)
- [ ] Verify clean removal even with dependencies

### Helm Error Message Quality
For EVERY error encountered:
- [ ] Error clearly states what failed?
- [ ] Error explains why it failed?
- [ ] Error suggests how to fix it?
- [ ] Error includes relevant Helm/kubectl commands?
- [ ] Error shows current state (release status, pod status)?
- [ ] Error avoids Helm internal errors (translates to user terms)?

---

## Phase 14a: HashiCorp Helm Provider Issues (CRITICAL - Release Blocker)

**Goal**: Verify we FIXED all known HashiCorp helm provider issues documented in ADR-022

**Context**: These are REAL production issues users hit with hashicorp/helm. We must prove our implementation doesn't have these problems.

### State Management Issues (CRITICAL)

**Issue #1669: Resources Randomly Removed from State**
- [ ] Deploy helm release successfully
- [ ] Run terraform apply 100+ times in loop
- [ ] Verify resource NEVER disappears from state file
- [ ] Check state file after each apply for release presence
- [ ] Success criteria: 100% state persistence across all applies

**Issue #472: Failed Releases Update State**
- [ ] Deploy release that will fail (bad image, crash loop)
- [ ] Verify release creation FAILS as expected
- [ ] Check terraform state - should NOT contain failed release
- [ ] Run terraform apply again - should retry (not show "no changes")
- [ ] Fix the error and apply - should succeed
- [ ] Verify state only updated after successful deploy

### Drift Detection Issues

**Issue #1349: No Drift Detection After Manual Rollback**
- [ ] Deploy release at revision 1
- [ ] Upgrade to revision 2 via Terraform
- [ ] Manually rollback to revision 1: `helm rollback <release> 1`
- [ ] Run `terraform plan` - MUST show drift detected
- [ ] Run `terraform apply` - MUST re-upgrade to revision 2
- [ ] Verify Terraform detects and corrects manual rollbacks

**Issue #1307: OCI Chart Drift Not Detected**
- [ ] Deploy chart from OCI registry with specific digest
- [ ] Note the digest in state
- [ ] Manually upgrade with different digest (same version tag)
- [ ] Run `terraform plan` - MUST detect digest change
- [ ] Success criteria: Digest changes trigger drift detection

### Wait Logic Issues

**Issue #1364: Doesn't Wait for DaemonSets**
- [ ] Create chart with DaemonSet workload
- [ ] Deploy with `wait = true`
- [ ] Verify Terraform WAITS until DaemonSet is ready on all nodes
- [ ] Check kubectl during deploy - should see "waiting for DaemonSet"
- [ ] Success criteria: Apply doesn't complete until DaemonSet ready

**Issue #672: First Deploy Always Succeeds (Timeout Ignored)**
- [ ] Deploy chart with bad image and timeout = 30s
- [ ] Verify FIRST deployment respects timeout and fails
- [ ] Should NOT succeed after timeout
- [ ] Error should occur at ~30s, not succeed
- [ ] Success criteria: Timeout enforced on first deployment

**Issue #463: Timeout Parameter Ignored**
- [ ] Deploy chart with wait = true and timeout = 10s
- [ ] Use image that takes 20s to become ready
- [ ] Verify deployment FAILS at ~10s (not 30s default)
- [ ] Try with timeout = 60s - should succeed
- [ ] Success criteria: User-specified timeout always respected

### Security Issues (CRITICAL - Cannot ship if these fail)

**Issue #1287: Sensitive Values Leaked in Metadata**
- [ ] Deploy with `set_sensitive = [{ name = "password", value = "secret123" }]`
- [ ] Run `terraform plan` - check output
- [ ] Verify "secret123" does NOT appear in ANY plan output
- [ ] Check metadata field - should be marked sensitive or excluded
- [ ] Run terraform show - verify secret not visible
- [ ] Success criteria: ZERO sensitive value exposure in any output

**Issue #1221: Sensitive Attribute Not Respected**
- [ ] Deploy with sensitive = true on secret values
- [ ] Verify state file marks values as sensitive
- [ ] Verify `terraform output` respects sensitivity
- [ ] Modify sensitive value - plan should show (sensitive value changed)
- [ ] Success criteria: Sensitivity propagated through entire lifecycle

### Import Issues

**Issue #1613: Cannot Import Existing Releases**
- [ ] Manually create release: `helm install test-import <chart>`
- [ ] Write terraform config for same release
- [ ] Import: `terraform import k8sconnect_helm_release.test <context>:<namespace>:<release>`
- [ ] Run `terraform plan` - MUST show zero diff (no description field drift)
- [ ] Run `terraform apply` - MUST succeed without changes
- [ ] Success criteria: Clean import with no permanent drift

### OCI and Chart Issues

**Issue #1596: Digest-Based Charts Not Supported**
- [ ] Deploy chart with version = "1.0.0@sha256:abc123..."
- [ ] Verify deployment succeeds
- [ ] Deploy chart with ONLY digest (no version tag)
- [ ] Both must work without validation errors
- [ ] Success criteria: Full digest support for immutable deploys

**Issue #1645/#1660: OCI Registry Authentication**
- [ ] Test OCI chart from public registry (public.ecr.aws)
- [ ] Test OCI chart requiring auth (if available)
- [ ] Verify auth tokens refresh properly
- [ ] No intermittent auth failures
- [ ] Success criteria: Reliable OCI authentication

### Dependency Management

**Issue #576: Dependencies Not Downloaded on Local Chart Update**
- [ ] Create local chart with Chart.yaml dependencies
- [ ] Deploy with `dependency_update = true`
- [ ] Modify chart (change values, version)
- [ ] Run terraform apply
- [ ] Verify dependencies RE-DOWNLOADED (not stale)
- [ ] Success criteria: dependency_update always works on chart changes

### Values Handling

**Issue #524: Values and Set Arguments Mixed, Changes Ignored**
- [ ] Deploy with both `values` YAML and `set` parameters
- [ ] Modify only the `set` parameter
- [ ] Run terraform plan - MUST show the set change
- [ ] Run terraform apply - MUST apply set change
- [ ] Verify precedence: set/set_sensitive > values YAML
- [ ] Success criteria: All value sources work together correctly

**Issue #906: Manifest Always Triggers Recreate**
- [ ] Deploy helm release
- [ ] Run terraform plan (no config changes)
- [ ] MUST show "No changes" (not revision increment)
- [ ] Apply 10 times in a row
- [ ] Verify revision stays the same
- [ ] Success criteria: No unnecessary revision increments

### Acceptance Criteria

**ALL of the above tests must PASS before we can release helm_release.**

If ANY test fails:
1. Document the failure in QA-RESULTS.md
2. Fix the implementation
3. Re-run ALL tests
4. Do NOT proceed until 100% pass rate

---

## Phase 14b: Bootstrap and Unknown Value Handling (CRITICAL)

**Goal**: Verify helm_release handles bootstrap scenarios like k8sconnect_object

**Context**: This provider's PRIMARY differentiator is bootstrapping clusters + workloads in one apply. Unknown value handling MUST work perfectly.

### Unknown Cluster Connection Values

**Scenario 1: Cluster doesn't exist yet**
- [ ] Create TF config with EKS/kind cluster + helm release in same config
- [ ] Cluster endpoint is "known after apply"
- [ ] Cluster CA cert is "known after apply"
- [ ] Run `terraform plan` - should NOT error
- [ ] Plan should show helm release will be created (not fail)
- [ ] Should defer evaluation until apply
- [ ] Run `terraform apply` - cluster created first, then helm release
- [ ] Verify helm release deploys successfully after cluster exists

**Scenario 2: Cluster exists, connection values known**
- [ ] Use existing cluster with known endpoint/certs
- [ ] Deploy helm release with all values known at plan time
- [ ] Should do full validation during plan
- [ ] Should show accurate preview of what will be deployed
- [ ] No "known after apply" for manifests or outputs

### Unknown Values in Chart/Repository

**Scenario 3: Chart version from data source**
- [ ] Chart version comes from data source: `data.helm_repo.app.version`
- [ ] Version is unknown at plan time
- [ ] Plan should succeed (defer to apply)
- [ ] Apply should resolve version and deploy
- [ ] No errors about unknown values

**Scenario 4: Repository URL from resource**
- [ ] Repository URL comes from another resource (e.g., ECR registry URL)
- [ ] URL is "known after apply"
- [ ] Plan must succeed
- [ ] Apply must resolve and deploy
- [ ] Verify chart downloaded from computed URL

### Unknown Values in Helm Values

**Scenario 5: Values contain unknown interpolations**
- [ ] values YAML contains ${resource.output}
- [ ] Output is "known after apply"
- [ ] Plan should succeed (not error on YAML parsing)
- [ ] Apply should substitute values and deploy
- [ ] Verify deployed resources have correct substituted values

**Scenario 6: Set parameters with unknown values**
- [ ] set = [{ name = "foo", value = aws_resource.id }]
- [ ] Value is "known after apply"
- [ ] Plan should show (known after apply) for affected resources
- [ ] Apply should resolve and deploy correctly
- [ ] Success criteria: Defers gracefully, no errors

**Scenario 7: Sensitive values with unknowns**
- [ ] set_sensitive with values from secrets manager (unknown at plan)
- [ ] Should defer and mark as sensitive
- [ ] Apply should resolve without exposing values
- [ ] Verify no leakage in logs

### Comparison with k8sconnect_object Behavior

**Consistency Test: Same Unknown Handling**
- [ ] Create k8sconnect_object with unknown cluster
- [ ] Create k8sconnect_helm_release with unknown cluster
- [ ] BOTH should handle unknowns identically
- [ ] Same "known after apply" behavior
- [ ] Same deferral semantics
- [ ] No surprising differences between resources

### Bootstrap Workflow Integration Test

**End-to-End Bootstrap Test**
- [ ] Create complete bootstrap scenario:
  - [ ] kind_cluster resource (cluster created)
  - [ ] k8sconnect_helm_release (installs CNI - Cilium)
  - [ ] k8sconnect_helm_release (installs cert-manager)
  - [ ] k8sconnect_object (creates CRD-based resources using cert-manager CRDs)
- [ ] ALL in same terraform apply (no two-phase)
- [ ] Run `terraform plan` - no errors
- [ ] Run `terraform apply` - everything deploys in correct order
- [ ] Verify:
  - [ ] Cluster created first
  - [ ] CNI deployed second
  - [ ] Cert-manager deployed third
  - [ ] CRD resources created last
- [ ] Success criteria: Single apply, correct dependency order, no errors

### Unknown Value Error Handling

**Error Scenario: Chart doesn't exist (unknown at plan)**
- [ ] Chart path comes from unknown value
- [ ] At apply time, path resolves to non-existent chart
- [ ] Should show clear error during apply (not cryptic unknown value error)
- [ ] Error should explain the resolved value failed

**Error Scenario: Invalid values after substitution**
- [ ] Values YAML has unknown interpolation
- [ ] At apply time, substitution creates invalid YAML
- [ ] Should show clear YAML parsing error
- [ ] Should show the resolved YAML causing the error

### Acceptance Criteria for Bootstrap Support

**MUST PASS:**
1. [ ] Unknown cluster connection values don't cause plan errors
2. [ ] Unknown chart/repo/version values defer gracefully
3. [ ] Unknown values in helm values YAML don't break parsing
4. [ ] Complete bootstrap (cluster + helm) works in single apply
5. [ ] Behavior matches k8sconnect_object unknown handling
6. [ ] Error messages remain clear even with unknown values

**If ANY fail**: This breaks the core value proposition. FIX BEFORE RELEASE.

---

## Phase 15: Cleanup and Documentation

### Final Cleanup
- [ ] Run `terraform destroy` from kind-validation directory
- [ ] Watch output - all resources destroyed cleanly?
- [ ] Check for any stuck resources with finalizers
- [ ] Verify cluster is clean: `KUBECONFIG=./kind-validation-config kubectl get all -A`
- [ ] Check for any remaining namespaces: `kubectl get namespaces`
- [ ] Verify only kube-system, default, local-path-storage remain

### Comprehensive Documentation
- [ ] Open/create `QA-RESULTS.md` in project root
- [ ] Document test round metadata:
  - Date and time
  - Provider version tested
  - Terraform version
  - Kubernetes version (kind node)

### 📝 UX Issues (DOCUMENT THESE FIRST!)
- [ ] **Poor Error Messages** - Include exact text and suggested improvement
  - What it said vs. what it SHOULD say
  - Missing context that would help
  - Jargon that could be simplified
- [ ] **Surprising Behavior** - Anything that wasn't expected
  - Silent adoptions/failures
  - Unexpected state changes
  - Confusing operation order
- [ ] **Missing Guidance** - Where users get stuck
  - Errors without solutions
  - Warnings without context
  - Timeouts without current state

### Other Issues to Document:
- [ ] Bugs (unexpected behavior, crashes, panics)
- [ ] Performance issues (slow operations, timeouts)
- [ ] Document error message quality issues:
  - Missing context (what/why/how to fix)
  - Unclear language or jargon
  - Poor formatting
  - Missing kubectl command suggestions
- [ ] Document what WORKED WELL:
  - Features that exceeded expectations
  - Great error messages
  - Smooth workflows
  - Helpful warnings

### Quality Questions to Answer
- [ ] Were there any surprising behaviors?
- [ ] Are there UX improvements that would help users?
- [ ] Did you encounter any workarounds needed?
- [ ] Are there missing features that would be valuable?
- [ ] How was the overall developer experience?

### Final Verification
- [ ] Review QA-RESULTS.md for completeness
- [ ] Ensure every issue has:
  - Clear description
  - Steps to reproduce
  - Expected vs actual behavior
  - Severity (blocker/major/minor/enhancement)

---

## Completion Criteria

✅ Checklist is complete when:
1. EVERY checkbox above is checked (Phases 0-14)
2. EVERY error message has been evaluated for quality
3. EVERY issue found has been documented in QA-RESULTS.md
4. ZERO items remain untested
5. Documentation is thorough and actionable

### 🏆 UX Quality Bar
Before signing off, ask yourself:
- Would a Kubernetes beginner understand every error?
- Are there ANY surprises in the behavior?
- Does every error help the user succeed?
- Would you be proud to show this UX to customers?
- Is this genuinely BETTER than competing providers?

**If any answer is "no", we're not done.**

DO NOT declare "done" until this is 100% complete.

---

## Quick Reference Commands

```bash
# Build and install provider
cd /Users/jonathan/code/terraform-provider-k8sconnect
make install

# Setup test environment
cd scenarios/kind-validation
./reset.sh

# Terraform workflow
terraform init
terraform plan
terraform apply
terraform destroy

# Kubectl with kind cluster
export KUBECONFIG=./kind-validation-config
kubectl get all -A
kubectl get pods -n kind-validation
kubectl describe deployment <name> -n kind-validation
kubectl logs <pod-name> -n kind-validation

# Create drift
kubectl scale deployment web-deployment --replicas=5 -n kind-validation
kubectl label deployment web-deployment test=drift -n kind-validation
kubectl set image deployment/web-deployment nginx=nginx:1.24 -n kind-validation

# Check managedFields
kubectl get deployment <name> -n kind-validation -o yaml | grep -A 20 managedFields
```
