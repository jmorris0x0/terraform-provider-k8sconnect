# ADR-010: Preventing Orphan Resources on Identity Changes

## Status
Accepted - Implemented (2025-10-06)

## Context

The k8sconnect provider currently has a **critical bug** that allows orphan resources to be created when users change resource identity fields (Kind, apiVersion, metadata.name, or metadata.namespace) in the `yaml_body`.

### The Problem

When a user modifies the YAML body to change resource identity:

```hcl
# Before
resource "k8sconnect_manifest" "example" {
  yaml_body = <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: my-app
  namespace: default
YAML
}

# After - user changes to ConfigMap
resource "k8sconnect_manifest" "example" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-app
  namespace: default
YAML
}
```

**Current behavior:**
1. Update operation applies the ConfigMap YAML
2. Kubernetes creates a ConfigMap (different resource type can have same name)
3. Terraform ID remains the same, now pointing to ConfigMap
4. **Pod is orphaned** - still running in Kubernetes, no longer tracked by Terraform

**The same issue occurs when changing:**
- `kind`: Pod → ConfigMap (creates new resource type)
- `metadata.name`: old-name → new-name (creates resource with new name)
- `metadata.namespace`: default → production (creates resource in new namespace)
- `apiVersion`: apps/v1beta1 → apps/v1 (may create duplicate depending on API server)

### Current Implementation Gap

**From ADR-003:** We use random UUIDs for Terraform resource IDs to maintain stability across configuration changes (multi-cluster support). This correctly solves the multi-cluster problem but creates a gap for identity changes.

**From ADR-002:** We discuss handling immutable _fields_ (like PVC storage size) but don't address resource _identity_ changes.

**In crud.go:145-213 (Update operation):**
```go
func (r *manifestResource) Update(...) {
    // 1. Get state and plan
    var state, plan manifestResourceModel

    // 2. Setup context from PLAN (new YAML)
    rc, err := r.prepareContext(ctx, &plan, false)

    // 3. PRESERVE the same ID - THIS IS THE BUG
    plan.ID = state.ID  // ← Same Terraform ID, different K8s resource
    r.setOwnershipAnnotation(rc.Object, plan.ID.ValueString())

    // 4. Apply the new resource - creates new, leaves old orphaned
    r.applyResourceWithConflictHandling(ctx, rc, rc.Data, resp, "Update")
}
```

**No detection mechanism exists** for resource identity changes anywhere in the codebase.

### Security Implications

This bug has security implications:

```hcl
# Malicious or accidental privilege escalation
resource "k8sconnect_manifest" "app" {
  yaml_body = <<YAML
apiVersion: v1
kind: ServiceAccount
metadata:
  name: admin-sa
  namespace: kube-system
---
# Later changed to innocent ConfigMap
# ServiceAccount remains with elevated privileges - orphaned
YAML
}
```

### Research: How Other Providers Solve This

#### kubectl Provider (Gavin Bunney)

**Approach:** Schema-level `ForceNew` on computed fields

```go
// Schema definition
"kind": {
    Type:     schema.TypeString,
    Computed: true,
    ForceNew: true,  // ← Any change triggers destroy/create
},
"api_version": {
    Type:     schema.TypeString,
    Computed: true,
    ForceNew: true,
},
"name": {
    Type:     schema.TypeString,
    Computed: true,
    ForceNew: true,
},
"namespace": {
    Type:     schema.TypeString,
    Computed: true,
    ForceNew: true,
},

// CustomizeDiff extracts values from YAML
CustomizeDiff: func(ctx, d, meta) {
    parsedYaml := yaml.ParseYAML(d.Get("yaml_body"))
    d.SetNew("kind", parsedYaml.GetKind())          // Change detected → triggers ForceNew
    d.SetNew("api_version", parsedYaml.GetAPIVersion())
    d.SetNew("namespace", parsedYaml.GetNamespace())
    d.SetNew("name", parsedYaml.GetName())
}
```

**Pros:**
- Terraform Core handles destroy/create orchestration automatically
- Clean separation of concerns
- Well-tested framework pattern

**Cons:**
- Only available in `terraform-plugin-sdk-v2` (legacy SDK)
- **We use `terraform-plugin-framework`** which doesn't support `ForceNew` schema attribute

#### kubernetes Provider (HashiCorp)

**Approach:** `RequiresReplace` in `PlanResourceChange`

```go
func (s *RawProviderServer) PlanResourceChange(...) {
    // Mark identity paths as requiring replacement
    resp.RequiresReplace = append(resp.RequiresReplace,
        tftypes.NewAttributePath().WithAttributeName("manifest").WithAttributeName("apiVersion"),
        tftypes.NewAttributePath().WithAttributeName("manifest").WithAttributeName("kind"),
        tftypes.NewAttributePath().WithAttributeName("manifest").WithAttributeName("metadata").WithAttributeName("name"),
        tftypes.NewAttributePath().WithAttributeName("manifest").WithAttributeName("namespace"),
    )
}
```

**Notes:**
- Uses low-level protocol (`terraform-plugin-go` with `tfprotov5`)
- Works with structured manifest attribute
- **We use higher-level framework and have single `yaml_body` string attribute**

## Decision

**We will use Option A: ModifyPlan with RequiresReplace in the Terraform Framework.**

This is the correct approach for `terraform-plugin-framework` providers and aligns with our architecture.

## Proposed Solution

### Implementation in ModifyPlan

Add identity change detection to `plan_modifier.go` in the `ModifyPlan` function:

```go
func (r *manifestResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
    // Skip during destroy
    if req.Plan.Raw.IsNull() {
        return
    }

    // Get planned data
    var plannedData manifestResourceModel
    diags := req.Plan.Get(ctx, &plannedData)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    // NEW: Detect resource identity changes (UPDATE operations only)
    if !req.State.Raw.IsNull() {
        if requiresReplacement := r.checkResourceIdentityChanges(ctx, req, &plannedData, resp); requiresReplacement {
            // Early return - no need for dry-run when replacing
            return
        }
    }

    // ... existing dry-run and projection logic continues ...
}
```

### Identity Change Detection Function

```go
// checkResourceIdentityChanges detects changes to Kubernetes resource identity fields
// and marks the resource for replacement if any identity field changes.
// Returns true if replacement is required.
func (r *manifestResource) checkResourceIdentityChanges(
    ctx context.Context,
    req resource.ModifyPlanRequest,
    plannedData *manifestResourceModel,
    resp *resource.ModifyPlanResponse,
) bool {
    // Get current state
    var stateData manifestResourceModel
    diags := req.State.Get(ctx, &stateData)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return false
    }

    // Parse both YAML bodies
    stateObj, err := r.parseYAML(stateData.YAMLBody.ValueString())
    if err != nil {
        // Can't compare - let Update handle the error
        tflog.Warn(ctx, "Failed to parse state YAML for identity check",
            map[string]interface{}{"error": err.Error()})
        return false
    }

    planObj, err := r.parseYAML(plannedData.YAMLBody.ValueString())
    if err != nil {
        // Invalid YAML in plan - will be caught by validators
        return false
    }

    // Check each identity field
    identityChanges := r.detectIdentityChanges(stateObj, planObj)

    if len(identityChanges) > 0 {
        // Build detailed message about what changed
        changeDetails := []string{}
        for _, change := range identityChanges {
            changeDetails = append(changeDetails,
                fmt.Sprintf("  %s: %q → %q", change.Field, change.OldValue, change.NewValue))
        }

        tflog.Info(ctx, "Resource identity changed, triggering replacement",
            map[string]interface{}{
                "changes": identityChanges,
                "old":     formatResourceIdentity(stateObj),
                "new":     formatResourceIdentity(planObj),
            })

        // Mark yaml_body for replacement
        resp.RequiresReplace = append(resp.RequiresReplace, path.Root("yaml_body"))

        // Add informative diagnostic
        resp.Diagnostics.AddWarning(
            "Resource Identity Changed - Replacement Required",
            fmt.Sprintf("The following Kubernetes resource identity fields have changed:\n%s\n\n"+
                "Terraform will delete the old resource and create a new one.\n\n"+
                "Old: %s\n"+
                "New: %s",
                strings.Join(changeDetails, "\n"),
                formatResourceIdentity(stateObj),
                formatResourceIdentity(planObj)),
        )

        return true
    }

    return false
}

// IdentityChange represents a change to a resource identity field
type IdentityChange struct {
    Field    string
    OldValue string
    NewValue string
}

// detectIdentityChanges compares identity fields between state and plan
func (r *manifestResource) detectIdentityChanges(stateObj, planObj *unstructured.Unstructured) []IdentityChange {
    var changes []IdentityChange

    // Check Kind
    if stateObj.GetKind() != planObj.GetKind() {
        changes = append(changes, IdentityChange{
            Field:    "kind",
            OldValue: stateObj.GetKind(),
            NewValue: planObj.GetKind(),
        })
    }

    // Check apiVersion
    if stateObj.GetAPIVersion() != planObj.GetAPIVersion() {
        changes = append(changes, IdentityChange{
            Field:    "apiVersion",
            OldValue: stateObj.GetAPIVersion(),
            NewValue: planObj.GetAPIVersion(),
        })
    }

    // Check metadata.name
    if stateObj.GetName() != planObj.GetName() {
        changes = append(changes, IdentityChange{
            Field:    "metadata.name",
            OldValue: stateObj.GetName(),
            NewValue: planObj.GetName(),
        })
    }

    // Check metadata.namespace
    // NOTE: Both cluster-scoped and namespaced resources are handled
    // For cluster-scoped, both will return "" which is correct
    if stateObj.GetNamespace() != planObj.GetNamespace() {
        changes = append(changes, IdentityChange{
            Field:    "metadata.namespace",
            OldValue: stateObj.GetNamespace(),
            NewValue: planObj.GetNamespace(),
        })
    }

    return changes
}

// formatResourceIdentity creates a human-readable resource identity string
func formatResourceIdentity(obj *unstructured.Unstructured) string {
    if obj.GetNamespace() != "" {
        return fmt.Sprintf("%s/%s %s/%s",
            obj.GetAPIVersion(), obj.GetKind(),
            obj.GetNamespace(), obj.GetName())
    }
    return fmt.Sprintf("%s/%s %s",
        obj.GetAPIVersion(), obj.GetKind(),
        obj.GetName())
}
```

## Per-Field Analysis

### 1. metadata.name

**Behavior in Kubernetes:** Different resources with different names are completely separate entities.

**Change Impact:**
```yaml
# Before: default/app-v1
# After:  default/app-v2
```
- Kubernetes creates new resource `app-v2`
- Old resource `app-v1` remains (orphan)

**Decision:** ✅ **MUST trigger replacement**

**Reasoning:** Name is fundamental resource identity in Kubernetes.

### 2. metadata.namespace

**Behavior in Kubernetes:** Namespaces isolate resources. Same name in different namespaces are different resources.

**Change Impact:**
```yaml
# Before: default/myapp
# After:  production/myapp
```
- Kubernetes creates new resource in `production` namespace
- Old resource in `default` namespace remains (orphan)

**Special Case - Cluster-scoped resources:**
```yaml
kind: ClusterRole  # No namespace
# Both GetNamespace() return "" - correctly detected as no change
```

**Decision:** ✅ **MUST trigger replacement**

**Reasoning:** Namespace is part of resource identity for namespaced resources. For cluster-scoped resources, both old and new namespace will be empty string, so no false positives.

### 3. kind

**Behavior in Kubernetes:** Different Kinds are different resource types. Can have same name.

**Change Impact:**
```yaml
# Before: Pod my-app
# After:  ConfigMap my-app
```
- Both resources can coexist with the same name
- Server-Side Apply creates ConfigMap
- Pod remains running (orphan)

**Decision:** ✅ **MUST trigger replacement**

**Reasoning:**
- Kind defines the resource schema and behavior
- Different Kinds are fundamentally different resources
- Most critical field to check

### 4. apiVersion

**Behavior in Kubernetes:** Complex - depends on API server configuration.

**Scenarios:**

#### Scenario A: Version aliases (same Group, different Version)
```yaml
# Before: apps/v1beta1 Deployment
# After:  apps/v1 Deployment
```
- If API server aliases them: updates in place (v1beta1 deprecated → v1)
- If API server doesn't alias: creates duplicate resource

**Current behavior in other providers:**
- kubectl provider: Forces replacement
- kubernetes provider: Forces replacement

#### Scenario B: Different API Group
```yaml
# Before: networking.k8s.io/v1 Ingress
# After:  extensions/v1beta1 Ingress
```
- Creates duplicate resource (different API groups = different resources)

**Decision:** ✅ **MUST trigger replacement**

**Reasoning:**
- Safer to replace than risk duplicates
- Aligns with ecosystem (kubectl and kubernetes providers both replace)
- User can manually handle if they want in-place upgrade via external process
- Even if API server would alias them, explicit recreation is clearer

### 5. Fields NOT Checked

**metadata.labels:** Not part of identity - can change freely

**metadata.annotations:** Not part of identity - can change freely

**metadata.uid:** Read-only, managed by Kubernetes

**spec.*** and **status.***: Not part of identity - handled by drift detection

## Edge Cases and Considerations

### Edge Case 1: Empty vs Unset Namespace

```yaml
# Cluster-scoped resource (e.g., ClusterRole)
kind: ClusterRole
# No namespace field - GetNamespace() returns ""

# User accidentally adds namespace
kind: ClusterRole
metadata:
  namespace: default  # Invalid for cluster-scoped resources
```

**Behavior:**
- First GetNamespace(): ""
- Second GetNamespace(): "default"
- Detected as identity change → triggers replacement
- Create will fail with Kubernetes validation error (correct)

**Decision:** ✅ **Correct behavior** - let Kubernetes validate cluster-scoped resources

### Edge Case 2: YAML Parsing Failures

**Scenario:** State has valid YAML, plan has unparseable YAML (unlikely due to validators)

**Handling:**
```go
if err != nil {
    // Can't compare - let Update handle the error
    tflog.Warn(ctx, "Failed to parse YAML for identity check")
    return false  // Don't trigger replacement
}
```

**Decision:** Return false, let validators catch the error

### Edge Case 3: During Import

**Import workflow:**
1. User runs `terraform import k8sconnect_manifest.example "namespace/name"`
2. Provider reads existing resource
3. First plan after import

**Consideration:** After import, if user's YAML has different identity than imported resource, should we replace?

**Current behavior with this ADR:** YES - would trigger replacement on first apply after import if identity differs

**Is this correct?**
- ✅ **YES** - if user imported wrong resource, we should tell them
- The warning message will clearly show old vs new identity
- User can fix their YAML or re-import correct resource

### Edge Case 4: Unknown Values During Plan

**Scenario:** YAML body contains interpolation from computed value

```hcl
resource "k8sconnect_manifest" "example" {
  yaml_body = templatefile("app.yaml", {
    name = some_resource.computed_name  # Unknown during plan
  })
}
```

**Handling:**
```go
yamlStr := plannedData.YAMLBody.ValueString()
if yamlStr == "" || strings.Contains(yamlStr, "${") {
    // Can't parse yet - skip identity check
    plannedData.ManagedStateProjection = types.StringUnknown()
    return false
}
```

**Decision:** ✅ **Already handled** by existing code in ModifyPlan (lines 44-60)

### Edge Case 5: Case Sensitivity

**Kubernetes behavior:**
- `kind`: Case-sensitive (Pod ≠ pod)
- `apiVersion`: Case-sensitive (apps/v1 ≠ Apps/v1)
- `metadata.name`: Case-sensitive in comparison
- `metadata.namespace`: Lowercase enforced by Kubernetes

**Our comparison:**
```go
stateObj.GetKind() != planObj.GetKind()  // Direct string comparison
```

**Decision:** ✅ **Use case-sensitive comparison** - matches Kubernetes behavior

## Interaction with Immutable Fields (ADR-002)

This ADR complements but is distinct from ADR-002:

| ADR | Scope | Example | Kubernetes Behavior | Our Behavior |
|-----|-------|---------|---------------------|--------------|
| **ADR-002** | Immutable _fields_ | PVC storage size | Returns 422 Invalid error | Enhanced error message (current) |
| **ADR-010** | Resource _identity_ | Pod → ConfigMap | Creates new resource | Force replacement (this ADR) |

**Key difference:**
- **ADR-002:** Same resource, trying to change immutable field → Kubernetes rejects
- **ADR-010:** Different resource identity → Kubernetes accepts, creates new resource

## Implementation Plan

### Phase 1: Add Identity Change Detection ✅
- [ ] Add `checkResourceIdentityChanges()` to `plan_modifier.go`
- [ ] Add `detectIdentityChanges()` helper
- [ ] Add `formatResourceIdentity()` helper
- [ ] Add `IdentityChange` struct

### Phase 2: Integration with ModifyPlan ✅
- [ ] Call identity check early in ModifyPlan (line ~40)
- [ ] Short-circuit ModifyPlan when replacement required (skip dry-run)
- [ ] Set `resp.RequiresReplace` with diagnostic message

### Phase 3: Testing 🧪
- [ ] Unit tests for each identity field
- [ ] Acceptance tests for replacement scenarios
- [ ] Test cluster-scoped vs namespaced resources
- [ ] Test edge cases (empty namespace, parsing failures)

### Phase 4: Documentation 📝
- [ ] Update resource documentation
- [ ] Add examples of replacement scenarios
- [ ] Security guidance on avoiding accidental orphans

## Testing Strategy

### Unit Tests

```go
func TestDetectIdentityChanges(t *testing.T) {
    tests := []struct{
        name     string
        stateYAML string
        planYAML  string
        expected  []IdentityChange
    }{
        {
            name: "kind changed",
            stateYAML: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test",
            planYAML:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test",
            expected: []IdentityChange{
                {Field: "kind", OldValue: "Pod", NewValue: "ConfigMap"},
            },
        },
        {
            name: "name changed",
            stateYAML: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: old",
            planYAML:  "apiVersion: v1\nkind: Pod\nmetadata:\n  name: new",
            expected: []IdentityChange{
                {Field: "metadata.name", OldValue: "old", NewValue: "new"},
            },
        },
        {
            name: "namespace changed",
            stateYAML: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test\n  namespace: default",
            planYAML:  "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test\n  namespace: production",
            expected: []IdentityChange{
                {Field: "metadata.namespace", OldValue: "default", NewValue: "production"},
            },
        },
        {
            name: "apiVersion changed",
            stateYAML: "apiVersion: apps/v1beta1\nkind: Deployment\nmetadata:\n  name: test",
            planYAML:  "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: test",
            expected: []IdentityChange{
                {Field: "apiVersion", OldValue: "apps/v1beta1", NewValue: "apps/v1"},
            },
        },
        {
            name: "multiple changes",
            stateYAML: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: old\n  namespace: default",
            planYAML:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: new\n  namespace: production",
            expected: []IdentityChange{
                {Field: "kind", OldValue: "Pod", NewValue: "ConfigMap"},
                {Field: "metadata.name", OldValue: "old", NewValue: "new"},
                {Field: "metadata.namespace", OldValue: "default", NewValue: "production"},
            },
        },
        {
            name: "no changes",
            stateYAML: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test",
            planYAML:  "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test\n  labels:\n    new: label",
            expected: []IdentityChange{},
        },
        {
            name: "cluster-scoped resource - no namespace",
            stateYAML: "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: test",
            planYAML:  "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: test",
            expected: []IdentityChange{},
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

### Acceptance Tests

```go
func TestAccManifestResource_IdentityChange_Kind(t *testing.T) {
    resource.Test(t, resource.TestCase{
        Steps: []resource.TestStep{
            {
                Config: testAccManifestConfig_Pod,
                Check: resource.ComposeTestCheckFunc(
                    resource.TestCheckResourceAttr("k8sconnect_manifest.test", "yaml_body", ...),
                ),
            },
            {
                Config: testAccManifestConfig_ConfigMap,
                Check: resource.ComposeTestCheckFunc(
                    resource.TestCheckResourceAttr("k8sconnect_manifest.test", "yaml_body", ...),
                    testAccCheckPodDeleted(t, "default", "test"),  // Old Pod should be gone
                    testAccCheckConfigMapExists(t, "default", "test"),  // New ConfigMap exists
                ),
            },
        },
    })
}
```

## Performance Impact

**Minimal overhead:**
- Parse YAML: Already done in validators
- String comparisons: 4 comparisons (kind, apiVersion, name, namespace)
- Only runs on UPDATE operations (not CREATE)
- Skipped when dry-run already planned

**Optimization:** Short-circuit ModifyPlan when replacement detected (skip dry-run)

## Migration and Backward Compatibility

**Impact on existing users:**

### Scenario 1: Users who never changed identity
- **Impact:** None - no behavior change

### Scenario 2: Users who changed identity and have orphans
- **Impact:**
  - First plan after upgrade will show replacement
  - May highlight existing orphans they weren't aware of
  - Orphans will need manual cleanup

### Scenario 3: Users who intentionally change identity
- **Impact:**
  - Previously: silent orphan creation
  - After this ADR: Terraform manages lifecycle properly with clear warnings
  - **Better behavior** - no migration needed

**Breaking change?**
- **NO** - this fixes a bug
- Users who depended on the bug were creating orphans (unintended)

## Alternatives Considered

### Alternative 1: Computed Fields with ForceNew (SDK v2 Pattern)

**Approach:** Add computed fields for identity, mark ForceNew

**Rejected because:**
- Not available in `terraform-plugin-framework`
- Would require migrating to `terraform-plugin-sdk-v2`
- Framework is the future, SDK is legacy

### Alternative 2: Custom Validation in Update

**Approach:** Detect changes in Update() and return error

```go
func (r *manifestResource) Update(...) {
    if identityChanged {
        resp.Diagnostics.AddError("Cannot change identity",
            "Please destroy and recreate resource")
        return
    }
}
```

**Rejected because:**
- ❌ Poor UX - error at apply time, not plan time
- ❌ User has to manually destroy and recreate
- ❌ Doesn't leverage Terraform's replacement mechanism

### Alternative 3: Separate Computed Attributes

**Approach:** Store kind, apiVersion, name, namespace as separate computed attributes

```go
type manifestResourceModel struct {
    // ...
    Kind       types.String `tfsdk:"kind"`
    APIVersion types.String `tfsdk:"api_version"`
    Name       types.String `tfsdk:"name"`
    Namespace  types.String `tfsdk:"namespace"`
}
```

**Rejected because:**
- Duplicates data (already in yaml_body)
- Increases state size
- Framework still doesn't support ForceNew on these
- Would still need RequiresReplace in ModifyPlan anyway

### Alternative 4: Do Nothing (Document Limitation)

**Approach:** Document that users shouldn't change identity

**Rejected because:**
- ❌ Security risk (orphan resources)
- ❌ Poor user experience
- ❌ Other providers solve this correctly
- ❌ Violates principle of least surprise

## Relationship to Other ADRs

### ADR-003: Resource IDs
- **Connection:** Random UUIDs enable stable IDs but create gap for identity changes
- **Impact:** This ADR fills the gap left by ADR-003

### ADR-002: Immutable Fields
- **Connection:** Both deal with changes that Kubernetes can't handle in-place
- **Distinction:**
  - ADR-002: Same resource, immutable field → K8s rejects (422)
  - ADR-010: Different resource → K8s accepts, creates new

### Future ADRs
- Could inform ADR-002 implementation (dry-run detection)
- Both use RequiresReplace mechanism

## References

1. [Terraform Plugin Framework - ModifyPlan](https://developer.hashicorp.com/terraform/plugin/framework/resources/plan-modification)
2. [kubectl Provider Source - ForceNew Implementation](https://github.com/gavinbunney/terraform-provider-kubectl/blob/main/kubernetes/resource_kubectl_manifest.go#L353-372)
3. [kubernetes Provider Source - RequiresReplace Implementation](https://github.com/hashicorp/terraform-provider-kubernetes/blob/main/manifest/provider/plan.go)
4. [Kubernetes API Conventions - Resource Identity](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#objects)
5. [Server-Side Apply Field Management](https://kubernetes.io/docs/reference/using-api/server-side-apply/#field-management)

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2025-10-05 | Use RequiresReplace in ModifyPlan | Framework-native approach, aligns with terraform-plugin-framework best practices |
| 2025-10-05 | Check all 4 identity fields | All can cause orphans; comprehensive protection needed |
| 2025-10-05 | Show warning diagnostic | Inform users why replacement is happening |
| 2025-10-05 | Short-circuit dry-run on replacement | Performance optimization |

## Conclusion

This ADR addresses a critical bug where changing resource identity fields (kind, apiVersion, metadata.name, metadata.namespace) creates orphan resources in the Kubernetes cluster. By implementing RequiresReplace in ModifyPlan, we leverage Terraform's built-in replacement mechanism to properly manage resource lifecycle while providing clear user feedback about why replacement is occurring.

This approach:
- ✅ Aligns with terraform-plugin-framework best practices
- ✅ Matches behavior of kubectl and kubernetes providers
- ✅ Prevents security issues from orphaned resources
- ✅ Provides clear user feedback via diagnostic messages
- ✅ Has minimal performance overhead
- ✅ Requires no breaking changes or migration
