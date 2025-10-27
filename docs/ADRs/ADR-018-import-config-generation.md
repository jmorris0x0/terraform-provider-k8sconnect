# ADR-018: Automatic Configuration Generation for Imports

**Status:** Accepted - Feature Supported with Known Limitations
**Date:** 2025-01-21
**Updated:** 2025-01-23, 2025-10-26 (field_ownership note)
**Deciders:** Architecture Team
**Related:** ADR-001 (Managed State Projection), ADR-005 (Field Ownership), ADR-020 (Field Ownership Display)

**Note (2025-10-26)**: As of ADR-020, `field_ownership` was moved from public schema to private state. References to `field_ownership` in this ADR are historical and describe the schema at the time of testing. The testing results remain valid - Terraform correctly excludes all computed-only fields from generated config.

## Context

Terraform 1.5 introduced experimental support for automatic configuration generation via the `-generate-config-out` flag. This feature allows users to import existing infrastructure without manually writing resource blocks first:

```hcl
# User writes only the import block
import {
  to = k8sconnect_object.nginx
  id = "prod:default:apps/v1/Deployment:nginx"
}
```

```bash
# Terraform generates the full resource block
terraform plan -generate-config-out=generated.tf
```

The generated file contains HCL configuration reverse-engineered from the imported state, eliminating the manual step of writing `yaml_body` and `cluster` before running `terraform import`.

### Current Import Workflow (Manual)

Users must:
1. `kubectl get deployment nginx -o yaml > nginx.yaml`
2. Manually write the resource block with `yaml_body = file("nginx.yaml")`
3. Run `terraform import 'k8sconnect_object.nginx' 'prod:default:apps/v1/Deployment:nginx'`
4. Verify with `terraform plan`

This is cumbersome, especially when importing many resources.

### How Terraform Config Generation Works

When `terraform plan -generate-config-out` runs:

1. **Import Block Processing**: Terraform reads `import {}` blocks in your configuration
2. **Provider ImportState Execution**: Calls the provider's `ImportState` method with the import ID
3. **State Population**: Provider populates Terraform state (we already do this in `import.go`)
4. **HCL Generation**: Terraform core reads the populated state and reverse-engineers HCL based on the provider's schema
5. **File Writing**: Generated HCL is written to the specified file with a `# __generated__ by Terraform` header

**Critical Detail**: The provider doesn't control what gets generated. Terraform core makes all generation decisions based solely on:
- The provider's schema (Required, Optional, Computed attributes)
- The state values populated by `ImportState`
- Heuristics for optional+computed fields

### Provider Requirements

To support config generation, providers must:
1. **Implement `ImportState`** ✅ We have this (`internal/k8sconnect/resource/object/import.go`)
2. **Populate state correctly** ✅ We populate `yaml_body`, `cluster`, etc.
3. **Have a sensible schema** ✅ Our schema is clean (Required, Optional, Computed clearly separated)

**We technically already support this feature.** The question is: does it work well for k8sconnect's use case?

## Problem Statement

### What Would Get Generated Today?

Given our current `ImportState` implementation (import.go:296-306), Terraform would generate:

```hcl
# __generated__ by Terraform
# Please review these resources and move them into your main configuration files.

resource "k8sconnect_object" "nginx" {
  yaml_body = <<-EOT
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  namespace: default
  # ... full resource YAML including server-added fields
spec:
  replicas: 3
  # ... rest of manifest
EOT

  cluster = {
    kubeconfig = <<-EOT
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTi... (base64)
    server: https://api.prod.example.com
  name: prod
contexts:
- context:
    cluster: prod
    user: admin
  name: prod
# ... entire kubeconfig file inlined as string
EOT
    context = "prod"
  }

  delete_protection = false  # Default value
  ignore_fields = []         # Empty list
}
```

### UX Problems

1. **Kubeconfig Inlining**: The entire kubeconfig file contents are inlined as a string literal (see import.go:251)
   - **Why**: We store `kubeconfig` as file contents, not path (intentional for portability)
   - **User expectation**: `kubeconfig = file("~/.kube/config")`
   - **Reality**: Multi-line string with thousands of characters

2. **Server-Added Fields in YAML**: The `yaml_body` includes K8s server-added fields
   - `metadata.creationTimestamp`
   - `metadata.resourceVersion`
   - `metadata.uid`
   - `metadata.managedFields`
   - `status` block
   - These aren't in user's original config and cause confusion

3. **No Semantic Understanding**: Terraform doesn't understand YAML structure
   - Can't strip server fields from `yaml_body`
   - Can't convert inline kubeconfig to `file()` reference
   - Just dumps state values as-is

### Comparison to hashicorp/kubernetes

The `kubernetes_manifest` resource uses an HCL map for `manifest`, not YAML:

```hcl
manifest = {
  apiVersion = "apps/v1"
  kind       = "Deployment"
  # ...
}
```

Generated config for `kubernetes_manifest` is cleaner because:
- Terraform understands HCL structure
- Can filter computed vs. configured fields
- No multi-line string issues

**But**: We deliberately chose YAML over HCL maps (no schema translation, copy-paste from kubectl, universal CRD support). We're not changing that.

## Terraform Core Limitations

### Issue #33438: No Provider Hooks for Generation

GitHub issue [#33438](https://github.com/hashicorp/terraform/issues/33438) discusses this exact problem:

> "Terraform can only guess what portion of the imported state should be used as configuration...there are many cases where optional+computed fields are validated differently depending on whether they originate from configuration or the provider."

**Proposed solutions** (none implemented yet):
1. **Declarative**: New "import schema" separate from resource schema
2. **Code-Driven**: Provider function that transforms state → config (apparentlymart's proposal, Dec 2023)

**Status**: Open since June 2023, no implementation in progress

### What This Means for k8sconnect

We cannot:
- Tell Terraform to strip server fields from `yaml_body`
- Tell Terraform to convert kubeconfig string → `file()` reference
- Customize the generated HCL in any way

Terraform will dump our state values as-is, which produces suboptimal (but technically valid) config.

## Design Options

### Option 1: Do Nothing (Current State)

**What happens**: Config generation works but produces ugly output

**Generated code quality**:
- ❌ Kubeconfig inlined as huge string
- ❌ YAML includes server-added fields
- ✅ Technically correct and importable
- ✅ User can manually clean it up

**Pros**:
- Zero work
- Feature technically works today
- Users who try it aren't blocked (config is valid)

**Cons**:
- Bad UX compared to manual workflow
- May confuse users ("why is there a 500-line kubeconfig string?")
- Doesn't actually save much work vs. `kubectl get ... > file.yaml`

### Option 2: Optimize ImportState for Better Generation

**Change what we store in state during import** to produce cleaner generated config:

#### 2a. Store Kubeconfig Path Instead of Contents

**Change**: import.go:251
```go
// Current
Kubeconfig: types.StringValue(string(kubeconfigData))

// New
Kubeconfig: types.StringValue(kubeconfigPath)
```

**Generated config would be**:
```hcl
cluster = {
  kubeconfig = "/Users/you/.kube/config"  # Much better!
  context    = "prod"
}
```

**Pros**:
- Cleaner generated config
- Still valid

**Cons**:
- **BREAKS PORTABILITY**: State now has absolute paths
- Moving kubeconfig file breaks Terraform
- Sharing state across machines breaks
- **Violates our design principle**: cluster should be self-contained

**Verdict**: **NO**. We intentionally store contents, not paths (see ADR discussion in provider auth design).

#### 2b. Strip Server-Added Fields from yaml_body

**Detect and remove**:
- `metadata.resourceVersion`
- `metadata.uid`
- `metadata.creationTimestamp`
- `metadata.generation`
- `metadata.managedFields`
- `status` block

**Implementation**: Add `cleanImportedYAML()` function in import.go

**Generated yaml_body would be**:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  namespace: default
  labels:
    app: nginx
spec:
  replicas: 3
  # ... user config only
```

**Pros**:
- Cleaner, more user-friendly YAML
- Matches what user would write manually
- Easy to implement (~50 lines of code)

**Cons**:
- **Semantic difference from state**: Imported state has more fields than generated config
- May confuse advanced users expecting 1:1 state→config mapping
- Need to maintain list of "server-only" fields

**Verdict**: **MAYBE**. Improves UX significantly with low risk.

### Option 3: Post-Generation Cleanup Tool

**Build `k8sconnect-import-cleanup` CLI tool**:

```bash
# User runs normal import
terraform plan -generate-config-out=generated.tf

# Run cleanup tool
k8sconnect-import-cleanup generated.tf > cleaned.tf
```

**What it does**:
- Strips server fields from YAML
- Detects kubeconfig paths, suggests `file()` replacement
- Adds helpful comments

**Pros**:
- Full control over output format
- Can be more sophisticated (detect patterns, suggest best practices)
- Doesn't affect core provider behavior

**Cons**:
- Extra tool to install/maintain
- Users must learn about it
- Two-step process (not seamless)

**Verdict**: **OVER-ENGINEERED**. Not worth maintaining separate tooling.

### Option 4: Enhanced ImportState + Documentation

**Combine**:
1. Implement Option 2b (strip server fields from yaml_body)
2. Document known limitations in migration guide
3. Provide cleanup script in docs (sed/yq one-liners)

**Documentation would say**:
```markdown
## Import with Auto-Generated Config

Terraform 1.5+ can auto-generate resource blocks:

```hcl
import {
  to = k8sconnect_object.nginx
  id = "prod:default:apps/v1/Deployment:nginx"
}
```

```bash
terraform plan -generate-config-out=generated.tf
```

**What gets generated**:
- `yaml_body`: Clean manifest (server fields removed)
- `cluster`: Kubeconfig inlined as string ⚠️

**Recommended cleanup**:
```bash
# Replace kubeconfig string with file() reference
sed -i 's|kubeconfig = <<-EOT.*EOT|kubeconfig = file("~/.kube/config")|' generated.tf
```
```

**Pros**:
- Best balance of UX improvement vs. complexity
- Clear about limitations
- Gives users tools to fix remaining issues

**Cons**:
- Still requires manual cleanup for kubeconfig
- Documentation must be very clear

## Critical Issues

### Issue 1: Import YAML Cleaning Was Incomplete (FIXED)

**PROBLEM** (discovered via testing): The old `cleanObjectForExport()` function was too conservative, leaving server-added annotations and nested metadata in `yaml_body`.

**What we were storing**:
- ✅ Correctly removed: uid, resourceVersion, generation, managedFields, status
- ❌ NOT removed: `deployment.kubernetes.io/revision` annotation
- ❌ NOT removed: `kubectl.kubernetes.io/last-applied-configuration` annotation
- ❌ NOT removed: `spec.template.metadata.creationTimestamp` in pod templates

**What yaml_body should be**: User's desired configuration (what they would write)

**Fix applied** (yaml.go):
1. Renamed `cleanObjectForExport` → `cleanObjectForImport` (correct semantic meaning)
2. Added removal of server-added annotations:
   - `kubectl.kubernetes.io/last-applied-configuration`
   - `deployment.kubernetes.io/revision`
   - `statefulset.kubernetes.io/revision`
   - `deprecated.daemonset.template.generation`
3. Added `cleanPodTemplateMetadata()` to remove `creationTimestamp` from nested pod templates
4. Remove empty annotations map if no user annotations remain

**Result**: `yaml_body` after import now contains clean, plausible user config.

### Issue 2: Computed-Only State Fields in Generated Config

**ADDITIONAL PROBLEM**: Our state contains Computed-only map fields that may break config generation:

```go
// object.go schema
"managed_state_projection": schema.MapAttribute{
  Computed:    true,  // No Optional - computed only
  ElementType: types.StringType,
}

"field_ownership": schema.MapAttribute{
  Computed:    true,  // No Optional - computed only
  ElementType: types.StringType,
}
```

**What's in state after import**:
- `managed_state_projection`: 100+ key-value pairs (`"spec.replicas": "3"`)
- `field_ownership`: 100+ key-value pairs (`"spec.replicas": "k8sconnect"`)
- `object_ref`: Nested object with apiVersion/kind/name/namespace

### Unknown Behavior

**Does Terraform skip Computed-only fields?**
- Docs say: Computed-only fields shouldn't be generated
- Reality: **We don't know** - never tested with complex map types
- Risk: Generator might crash or produce invalid HCL

**Possible outcomes**:
1. ✅ **Works**: Terraform skips these fields, generates only yaml_body + cluster
2. ❌ **Crashes**: Generator fails on map serialization
3. ❌ **Invalid HCL**: Generates unparseble configuration
4. ❌ **Generates them anyway**: Produces config with hundreds of lines of computed maps

### Why This Matters

If Terraform tries to generate these fields, the output would be:
```hcl
resource "k8sconnect_object" "nginx" {
  yaml_body = "..."
  cluster = {...}

  # ERROR: Can't set computed-only fields in config!
  managed_state_projection = {
    "metadata.name" = "nginx"
    "metadata.namespace" = "default"
    "metadata.labels.app" = "nginx"
    "spec.replicas" = "3"
    "spec.selector.matchLabels.app" = "nginx"
    # ... 100+ more lines
  }

  field_ownership = {
    "metadata.name" = "k8sconnect"
    "spec.replicas" = "k8sconnect"
    # ... 100+ more lines
  }
}
```

This config would be **invalid** (computed fields can't be set in configuration).

## Decision

**ACCEPTED** - Feature fully supported with documented limitations.

### Status: Issue 1 - FIXED ✅

**What was done**:
- Renamed `cleanObjectForExport()` → `cleanObjectForImport()` (yaml.go:91)
- Added removal of server annotations (yaml.go:106-119)
- Added `cleanPodTemplateMetadata()` to clean nested pod templates (yaml.go:130-149)
- Tested empirically with kind cluster import

**Verification**:
```bash
# Created deployment via kubectl
kubectl apply -f deployment.yaml

# Imported into terraform
terraform import 'k8sconnect_object.test' 'kind-kind-validation:default:apps/v1/Deployment:test'

# Checked yaml_body in state
terraform state show k8sconnect_object.test
```

**Result**: `yaml_body` contains clean YAML without:
- ❌ deployment.kubernetes.io/revision annotation
- ❌ kubectl.kubernetes.io/last-applied-configuration annotation
- ❌ creationTimestamp in pod template
- ❌ uid, resourceVersion, generation, managedFields, status

✅ K8s defaults (progressDeadlineSeconds, etc.) are kept (acceptable)

### Status: Issue 2 - VERIFIED ✅

**Testing conducted** (2025-01-23):

Tested `terraform plan -generate-config-out` with import blocks on kind cluster:

```bash
# Created ConfigMap with kubectl
kubectl create configmap test-import -n import-test --from-literal=key1=value1

# Import block only
import {
  to = k8sconnect_object.imported_cm
  id = "kind-kind-validation:import-test:v1/ConfigMap:test-import"
}

# Generated config
terraform plan -generate-config-out=generated.tf
```

**Results**:
- ✅ No crashes
- ✅ Valid HCL generated
- ✅ Computed fields (managed_state_projection, object_ref) **correctly excluded**
  - Note: field_ownership was also excluded at time of testing; it's now in private state per ADR-020
- ✅ Only yaml_body and cluster generated
- ✅ yaml_body is clean (server fields removed)
- ⚠️ cluster has kubeconfig inlined as multi-line string (expected limitation)

**Generated config quality:**
```hcl
resource "k8sconnect_object" "imported_cm" {
  yaml_body = <<-EOT
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-import
  namespace: import-test
  labels:
    created-by: kubectl
data:
  key1: value1
  key2: value2
EOT

  cluster = {
    kubeconfig = <<-EOT
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1...
    server: https://127.0.0.1:63072
  name: kind-kind-validation
contexts:
- context:
    cluster: kind-kind-validation
    user: kind-kind-validation
  name: kind-kind-validation
current-context: kind-kind-validation
kind: Config
preferences: {}
users:
- name: kind-kind-validation
  user:
    client-certificate-data: LS0tLS1...
    client-key-data: LS0tLS1...
EOT
    context = "kind-kind-validation"
  }
}
```

**Known limitation**: Users must manually replace kubeconfig heredoc with `file()` reference for cleaner code.

**Recommendation**: Document this as a supported feature with caveat about kubeconfig cleanup.

### Implementation Status

**✅ COMPLETE** - All phases implemented and verified:

**Phase 1: Import Cleanup** ✅
- Implemented `cleanObjectForImport()` in yaml.go
- Removes server-added fields, annotations, and nested metadata
- Acceptance tests pass: `TestAccObjectResource_ImportInconsistentState`

**Phase 2: Config Generation Testing** ✅
- Verified with real import blocks on kind cluster
- Terraform correctly excludes computed-only fields
- Generated config is valid and importable

**Phase 3: Decision** ✅
- Feature works well enough to support
- Known limitation (kubeconfig inlining) is acceptable
- Users can fix in <1 minute with find/replace

**Phase 4: Documentation** ✅
- Updated docs/resources/object.md with three import workflows
- Updated docs/guides/migration-from-hashicorp-kubernetes.md
- Documented kubeconfig cleanup requirement

### User Guidance

**Recommended cleanup after generation:**
```bash
# Replace inlined kubeconfig with file reference
sed -i '' 's/kubeconfig = <<-EOT.*EOT/kubeconfig = file("~\/.kube\/config")/g' generated.tf

# Or manually edit to replace the heredoc with:
cluster = {
  kubeconfig = file("~/.kube/config")
  context    = "your-context"
}
```

This is a one-time manual step that takes less than a minute.

## Consequences

### If We Support It (Option 4)

**Positive**:
- Users get auto-generation "for free" (Terraform core does it)
- Improves import UX for multi-resource migrations
- Keeps k8sconnect competitive with other providers

**Negative**:
- Must maintain `cleanImportedYAML()` logic
- Documentation complexity (explaining limitations)
- User expectations may exceed reality

### If We Don't Support It (Option 1)

**Positive**:
- Zero maintenance burden
- No risk of confusing users with bad generation

**Negative**:
- Users who try `-generate-config-out` get ugly results
- May generate support questions ("why is my config 500 lines?")
- Missed opportunity to improve import UX

## Implementation Notes

### If We Implement Option 2b

**File**: `internal/k8sconnect/resource/object/import.go`

**New function**:
```go
// cleanImportedYAML removes server-added fields from imported resource YAML
// to produce cleaner configuration for -generate-config-out
func cleanImportedYAML(obj *unstructured.Unstructured) {
  // Remove metadata fields that K8s adds
  unstructured.RemoveNestedField(obj.Object, "metadata", "resourceVersion")
  unstructured.RemoveNestedField(obj.Object, "metadata", "uid")
  unstructured.RemoveNestedField(obj.Object, "metadata", "creationTimestamp")
  unstructured.RemoveNestedField(obj.Object, "metadata", "generation")
  unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")

  // Remove entire status block (always server-managed)
  unstructured.RemoveNestedField(obj.Object, "status")

  // Remove annotations added by kubectl/k8s
  annotations, _, _ := unstructured.NestedStringMap(obj.Object, "metadata", "annotations")
  if annotations != nil {
    delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
    unstructured.SetNestedStringMap(obj.Object, annotations, "metadata", "annotations")
  }
}
```

**Call site**: import.go, after fetching resource (line ~185):
```go
liveObj, ok := r.fetchImportResource(ctx, client, apiVersion, kind, namespace, name, kubeContext, resp)
if !ok {
  return
}

// Clean YAML for better config generation
cleanImportedYAML(liveObj)
```

**Testing**:
- Acceptance test verifies cleaned YAML imports successfully
- Compare generated config to manually-written config
- Ensure projection still works correctly

## Related Work

- **ADR-001**: Managed State Projection - explains why we track fields
- **ADR-005**: Field Ownership - managedFields parsing is unaffected
- **Terraform Issue #33438**: Provider influence over config generation (open)
- **Terraform 1.5 Announcement**: Config generation feature launch

## Future Considerations

If Terraform implements provider hooks for config generation (issue #33438):

**We could**:
1. Provide custom HCL generation logic
2. Convert kubeconfig to `file()` references programmatically
3. Generate `ignore_fields` suggestions based on field ownership

**This ADR would be superseded** by a new design leveraging those hooks.

Until then, we work within Terraform core's limitations.

## References

- [Terraform Import Config Generation Docs](https://developer.hashicorp.com/terraform/language/import/generating-configuration)
- [Issue #33438: Provider influence over generation](https://github.com/hashicorp/terraform/issues/33438)
- [Plugin Framework Import Tutorial](https://developer.hashicorp.com/terraform/tutorials/providers-plugin-framework/providers-plugin-framework-resource-import)
- Migration Guide (this repo): `docs/guides/migration-from-hashicorp-kubernetes.md`
