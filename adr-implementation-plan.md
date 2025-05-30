# Implementation Plan: Non-Deterministic IDs with Annotation-Based Ownership

This document outlines the changes needed to implement ADR-002, switching from deterministic to random UUID-based Terraform resource IDs with Kubernetes annotation-based ownership tracking.

## Overview

**Current State**: Deterministic IDs based on cluster + resource identity  
**Target State**: Random UUID4 IDs with ownership annotations for conflict prevention

## Required Changes

### 1. Add UUID Dependency

**File**: `go.mod`
```go
require (
    // ... existing dependencies ...
    github.com/google/uuid v1.6.0
)
```

### 2. Update ID Generation Logic

**File**: `internal/k8sinline/resource/manifest/manifest.go`

#### Replace Current Functions
**Remove**:
- `generateID(obj, conn)` - deterministic hash-based
- `generateIDFromImport(obj, context)` - deterministic for imports  
- `getClusterID(conn)` - cluster identification logic

**Add**:
```go
import "github.com/google/uuid"

// generateID creates a random UUID for Terraform resource identification
func (r *manifestResource) generateID() string {
    return uuid.New().String()
}
```

#### Update Call Sites
**In Create()**: Change from:
```go
id := r.generateID(obj, conn)
```
To:
```go
id := r.generateID()
```

### 3. Implement Ownership Annotation Management

**File**: `internal/k8sinline/resource/manifest/manifest.go`

#### Add Constants
```go
const (
    OwnershipAnnotation = "k8sinline.terraform.io/terraform-id"
    CreatedAtAnnotation = "k8sinline.terraform.io/created-at"
    DefaultFieldManager = "k8sinline"
    ImportFieldManager  = "k8sinline-import"
)
```

#### Add Ownership Helper Functions
```go
// setOwnershipAnnotation marks a Kubernetes resource as managed by this Terraform resource
func (r *manifestResource) setOwnershipAnnotation(obj *unstructured.Unstructured, terraformID string) {
    annotations := obj.GetAnnotations()
    if annotations == nil {
        annotations = make(map[string]string)
    }
    annotations[OwnershipAnnotation] = terraformID
    annotations[CreatedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)
    obj.SetAnnotations(annotations)
}

// validateOwnership checks if we have permission to manage this Kubernetes resource
func (r *manifestResource) validateOwnership(liveObj *unstructured.Unstructured, expectedID string) error {
    annotations := liveObj.GetAnnotations()
    if annotations == nil {
        return fmt.Errorf("resource exists but has no ownership annotations - use 'terraform import' to adopt")
    }
    
    existingID := annotations[OwnershipAnnotation]
    if existingID == "" {
        return fmt.Errorf("resource exists but not managed by k8sinline - use 'terraform import' to adopt")
    }
    
    if existingID != expectedID {
        return fmt.Errorf("resource managed by different k8sinline resource (Terraform ID: %s)", existingID)
    }
    
    return nil
}

// getOwnershipID extracts the Terraform resource ID from Kubernetes annotations
func (r *manifestResource) getOwnershipID(obj *unstructured.Unstructured) string {
    annotations := obj.GetAnnotations()
    if annotations == nil {
        return ""
    }
    return annotations[OwnershipAnnotation]
}
```

### 4. Enhance Error Classification

**File**: `internal/k8sinline/resource/manifest/manifest.go`

**Update the existing `classifyK8sError` function** to distinguish between ownership conflicts and field management conflicts:

```go
func (r *manifestResource) classifyK8sError(err error, operation, resourceDesc string) (severity, title, detail string) {
    switch {
    case errors.IsNotFound(err):
        return "warning", fmt.Sprintf("%s: Resource Not Found", operation),
            fmt.Sprintf("The %s was not found in the cluster. It may have been deleted outside of Terraform.", resourceDesc)

    case errors.IsForbidden(err):
        return "error", fmt.Sprintf("%s: Insufficient Permissions", operation),
            fmt.Sprintf("RBAC permissions insufficient to %s %s. Check that your credentials have the required permissions for this operation. Details: %v",
                operation, resourceDesc, err)

    case errors.IsConflict(err):
        if strings.Contains(err.Error(), "k8sinline") || strings.Contains(err.Error(), DefaultFieldManager) {
            // Conflict with another k8sinline instance
            return "error", fmt.Sprintf("%s: k8sinline Field Manager Conflict", operation),
                fmt.Sprintf("Another k8sinline resource may be managing the same fields. This suggests multiple Terraform configurations are managing the same resource. Details: %v", err)
        } else {
            // Conflict with external tool
            return "error", fmt.Sprintf("%s: External Tool Field Conflict", operation),
                fmt.Sprintf("External tool (kubectl, operator, etc.) has modified fields managed by k8sinline. Consider using force=true or resolve the conflict manually. Details: %v", err)
        }

    case errors.IsTimeout(err) || errors.IsServerTimeout(err):
        return "error", fmt.Sprintf("%s: Kubernetes API Timeout", operation),
            fmt.Sprintf("Timeout while performing %s on %s. The cluster may be under heavy load or experiencing connectivity issues. Details: %v",
                operation, resourceDesc, err)

    case errors.IsUnauthorized(err):
        return "error", fmt.Sprintf("%s: Authentication Failed", operation),
            fmt.Sprintf("Authentication failed for %s %s. Check your credentials and ensure they are valid. Details: %v",
                operation, resourceDesc, err)

    case errors.IsInvalid(err):
        return "error", fmt.Sprintf("%s: Invalid Resource", operation),
            fmt.Sprintf("The %s contains invalid fields or values. Review the YAML specification and ensure all required fields are present and correctly formatted. Details: %v",
                resourceDesc, err)

    case errors.IsAlreadyExists(err):
        return "error", fmt.Sprintf("%s: Resource Already Exists", operation),
            fmt.Sprintf("The %s already exists in the cluster and cannot be created. Use import to manage existing resources with Terraform. Details: %v",
                resourceDesc, err)

    default:
        return "error", fmt.Sprintf("%s: Kubernetes API Error", operation),
            fmt.Sprintf("An unexpected error occurred while performing %s on %s. Details: %v",
                operation, resourceDesc, err)
    }
}
```

### 5. Update CRUD Operations

**File**: `internal/k8sinline/resource/manifest/manifest.go`

### 5. Update CRUD Operations

**File**: `internal/k8sinline/resource/manifest/manifest.go`

#### Create Operation
**Add ownership before apply**:
```go
func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
    // ... existing parsing and client creation ...
    
    // Generate random ID and set ownership
    id := r.generateID()
    data.ID = types.StringValue(id)
    r.setOwnershipAnnotation(obj, id)
    
    // Apply the manifest with standard field management
    err = client.Apply(ctx, obj, k8sclient.ApplyOptions{
        FieldManager: DefaultFieldManager,
        Force:        false,  // Detect conflicts with external tools
    })
    if err != nil {
        resourceDesc := fmt.Sprintf("%s %s", obj.GetKind(), obj.GetName())
        severity, title, detail := r.classifyK8sError(err, "Create", resourceDesc)
        if severity == "warning" {
            resp.Diagnostics.AddWarning(title, detail)
        } else {
            resp.Diagnostics.AddError(title, detail)
        }
        return
    }
    // ... rest of function unchanged ...
}
```

#### Read Operation
**Add existence check without ownership validation** (Read is passive):
```go
func (r *manifestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
    // ... existing logic unchanged ...
    // Read operation doesn't need ownership validation
    // Just check if resource exists and remove from state if not
}
```

#### Update Operation
**Add ownership validation before field management and handle conflicts appropriately**:
```go
func (r *manifestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
    // ... existing parsing and client creation ...
    
    // LAYER 1: Validate annotation-based ownership (our conflict prevention)
    gvr, err := r.getGVR(ctx, client, obj)
    if err != nil {
        resp.Diagnostics.AddError("Resource Discovery Failed", err.Error())
        return
    }
    
    liveObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
    if err != nil && !errors.IsNotFound(err) {
        resp.Diagnostics.AddError("Ownership Check Failed", fmt.Sprintf("Could not check resource ownership: %s", err))
        return
    }
    
    if !errors.IsNotFound(err) {
        if err := r.validateOwnership(liveObj, data.ID.ValueString()); err != nil {
            resp.Diagnostics.AddError("Resource Ownership Conflict", err.Error())
            return  // Stop before K8s field management gets involved
        }
    }
    
    // LAYER 2: Apply with K8s field management (handles external tool conflicts)
    r.setOwnershipAnnotation(obj, data.ID.ValueString())
    
    err = client.Apply(ctx, obj, k8sclient.ApplyOptions{
        FieldManager: DefaultFieldManager,
        Force:        false,  // Let K8s detect external tool conflicts
    })
    if err != nil {
        resourceDesc := fmt.Sprintf("%s %s", obj.GetKind(), obj.GetName())
        severity, title, detail := r.classifyK8sError(err, "Update", resourceDesc)
        if severity == "warning" {
            resp.Diagnostics.AddWarning(title, detail)
        } else {
            resp.Diagnostics.AddError(title, detail)
        }
        return
    }
    
    // ... rest of function unchanged ...
}
```

#### Delete Operation
**Add ownership validation before deletion**:
```go
func (r *manifestResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
    // ... existing parsing and client creation ...
    
    // Check if resource exists and validate ownership
    liveObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
    if err != nil {
        if errors.IsNotFound(err) {
            // Already gone - that's fine
            return
        }
        resp.Diagnostics.AddError("Delete Check Failed", err.Error())
        return
    }
    
    // Validate ownership before deletion
    if err := r.validateOwnership(liveObj, data.ID.ValueString()); err != nil {
        resp.Diagnostics.AddWarning("Ownership Warning", 
            fmt.Sprintf("Deleting resource with ownership issues: %s", err.Error()))
        // Continue with deletion anyway - user might be cleaning up
    }
    
    // Proceed with normal deletion
    err = client.Delete(ctx, gvr, obj.GetNamespace(), obj.GetName(), k8sclient.DeleteOptions{})
    // ... rest of function unchanged ...
}
```

### 6. Reimplement Import Strategy

**File**: `internal/k8sinline/resource/manifest/manifest.go`

**Replace entire ImportState function**:
```go
func (r *manifestResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
    // Parse import ID: "context/namespace/kind/name" or "context/kind/name"
    kubeContext, namespace, kind, name, err := r.parseImportID(req.ID)
    if err != nil {
        resp.Diagnostics.AddError("Invalid Import ID", 
            fmt.Sprintf("Expected format: <context>/<namespace>/<kind>/<name> or <context>/<kind>/<name>\n\nError: %s", err.Error()))
        return
    }
    
    // ... existing kubeconfig and client setup logic unchanged ...
    
    // Discover GVR and fetch the live object
    _, liveObj, err := client.GetGVRFromKind(ctx, kind, namespace, name)
    if err != nil {
        // ... existing error handling unchanged ...
        return
    }
    
    // Check for existing ownership
    existingTerraformID := r.getOwnershipID(liveObj)
    var terraformID string
    
    if existingTerraformID != "" {
        // Resource already managed by k8sinline - use existing ID
        terraformID = existingTerraformID
        tflog.Info(ctx, "importing resource with existing k8sinline ownership", map[string]interface{}{
            "terraform_id": terraformID,
            "kind": kind,
            "name": name,
        })
    } else {
        // Resource not managed by k8sinline - take ownership
        terraformID = r.generateID()
        r.setOwnershipAnnotation(liveObj, terraformID)
        
        // Apply the ownership annotation to the live resource
        err = client.Apply(ctx, liveObj, k8sclient.ApplyOptions{
            FieldManager: ImportFieldManager,
            Force:        true,  // Force ownership of annotations during import
        })
        if err != nil {
            resp.Diagnostics.AddError("Import Ownership Failed", 
                fmt.Sprintf("Failed to set ownership annotation during import: %s", err.Error()))
            return
        }
        
        tflog.Info(ctx, "took ownership of unmanaged resource during import", map[string]interface{}{
            "terraform_id": terraformID,
            "kind": kind,
            "name": name,
        })
    }
    
    // ... rest of import logic unchanged (convert to YAML, set state) ...
    
    importedData := manifestResourceModel{
        ID:                types.StringValue(terraformID), // Use the ownership-based ID
        YAMLBody:          types.StringValue(string(yamlBytes)),
        ClusterConnection: connObj,
        DeleteProtection:  types.BoolValue(false),
    }
    
    // ... rest unchanged ...
}
```

### 7. Update Tests

**File**: `internal/k8sinline/resource/manifest/manifest_unit_test.go`

#### Remove Deterministic ID Tests
**Remove**:
- `TestGenerateID` - no longer deterministic
- Tests that verify ID consistency with same inputs

#### Add Ownership Tests  
**Add**:
```go
func TestSetOwnershipAnnotation(t *testing.T) {
    r := &manifestResource{}
    
    obj := &unstructured.Unstructured{
        Object: map[string]interface{}{
            "apiVersion": "v1",
            "kind":       "Pod",
            "metadata": map[string]interface{}{
                "name": "test-pod",
            },
        },
    }
    
    terraformID := "test-uuid-12345"
    r.setOwnershipAnnotation(obj, terraformID)
    
    annotations := obj.GetAnnotations()
    if annotations[OwnershipAnnotation] != terraformID {
        t.Errorf("expected annotation %q, got %q", terraformID, annotations[OwnershipAnnotation])
    }
    
    if annotations[CreatedAtAnnotation] == "" {
        t.Error("expected created-at annotation to be set")
    }
}

func TestValidateOwnership(t *testing.T) {
    r := &manifestResource{}
    
    tests := []struct {
        name           string
        annotations    map[string]string
        expectedID     string
        expectError    bool
        errorContains  string
    }{
        {
            name:        "no annotations",
            annotations: nil,
            expectedID:  "test-id",
            expectError: true,
            errorContains: "no ownership annotations",
        },
        {
            name:        "missing ownership annotation", 
            annotations: map[string]string{"other": "value"},
            expectedID:  "test-id",
            expectError: true,
            errorContains: "not managed by k8sinline",
        },
        {
            name:        "wrong terraform ID",
            annotations: map[string]string{OwnershipAnnotation: "different-id"},
            expectedID:  "test-id", 
            expectError: true,
            errorContains: "managed by different k8sinline resource",
        },
        {
            name:        "correct ownership",
            annotations: map[string]string{OwnershipAnnotation: "test-id"},
            expectedID:  "test-id",
            expectError: false,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            obj := &unstructured.Unstructured{
                Object: map[string]interface{}{
                    "metadata": map[string]interface{}{
                        "annotations": tt.annotations,
                    },
                },
            }
            
            err := r.validateOwnership(obj, tt.expectedID)
            
            if tt.expectError {
                if err == nil {
                    t.Error("expected error but got none")
                }
                if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
                    t.Errorf("expected error containing %q, got %q", tt.errorContains, err.Error())
                }
            } else {
                if err != nil {
                    t.Errorf("unexpected error: %v", err)
                }
            }
        })
    }
}

func TestGenerateID_IsUUID(t *testing.T) {
    r := &manifestResource{}
    
    id1 := r.generateID()
    id2 := r.generateID()
    
    // Should be different
    if id1 == id2 {
        t.Error("expected different IDs from consecutive calls")
    }
    
    // Should be valid UUID format (basic check)
    if len(id1) != 36 {
        t.Errorf("expected UUID length 36, got %d", len(id1))
    }
    
    if !strings.Contains(id1, "-") {
        t.Error("expected UUID format with hyphens")
    }
}
```

#### Update Existing Tests
**Modify tests that assert specific ID values** - they should now just verify IDs are set and valid UUIDs.

### 8. Update Documentation

**File**: `README.md`

Add section explaining the ownership model:
```markdown
## Resource Ownership and Conflict Prevention

k8sinline uses Kubernetes annotations to track resource ownership and prevent conflicts between multiple Terraform configurations:

- `k8sinline.terraform.io/terraform-id`: Links Kubernetes resources to Terraform resources
- `k8sinline.terraform.io/created-at`: Timestamp when k8sinline first took ownership

### Multi-Cluster Safety

Unlike other providers, k8sinline safely supports managing the same Kubernetes resource (same namespace/kind/name) across different clusters because each Terraform resource gets a unique random ID.

### Two-Layer Conflict Prevention

k8sinline provides comprehensive conflict detection through:

1. **Annotation-Based Ownership** (Layer 1): Prevents multiple Terraform configurations from managing the same resource
2. **Kubernetes Field Management** (Layer 2): Prevents conflicts with external tools (kubectl, operators)

This means you get clear error messages distinguishing between:
- Cross-Terraform-state conflicts (resolved through import/state management)  
- External tool conflicts (resolved through force application or manual conflict resolution)

### Import Behavior

When importing existing resources:
- If the resource has k8sinline ownership annotations: use existing Terraform ID
- If the resource is unmanaged: take ownership with new random ID
- Resources managed by other tools can be safely imported
```

## Testing Strategy

### Unit Tests
1. ✅ Ownership annotation setting/getting
2. ✅ Ownership validation logic
3. ✅ UUID generation (format validation)
4. ✅ Error message quality

### Integration Tests  
1. ✅ Import of managed vs unmanaged resources
2. ✅ Conflict detection when two Terraform resources try to manage same K8s resource
3. ✅ Connection changes don't affect resource identity
4. ✅ Multi-cluster scenarios with identical resource names

### Acceptance Tests
1. ✅ Cross-state ownership conflicts are properly detected
2. ✅ Import workflow sets annotations correctly
3. ✅ Resource lifecycle with ownership annotations

## Rollout Plan

Since this is pre-release:
1. Implement all changes in single PR
2. Update all tests to use new model
3. Test extensively with multi-cluster scenarios
4. Update documentation and examples

## Unanswered Questions

### 1. Annotation Resilience
**Question**: What should we do if other tools strip our ownership annotations?
**Options**:
- A) Fail operations with clear error message
- B) Re-add annotations automatically (could be aggressive)
- C) Add multiple annotation keys for redundancy
- D) Store ownership in finalizers as backup

### 2. Import UX for Managed Resources
**Question**: If importing a resource already managed by k8sinline but from different state file, what should happen?
**Options**:
- A) Error: "Resource already managed by k8sinline, use terraform state mv"
- B) Allow import but warn about potential conflicts
- C) Force user to manually remove annotations first

### 3. Legacy Resource Migration
**Question**: Should we detect and provide guidance for resources created with old deterministic IDs?
**Note**: Since this is pre-release, probably not needed, but worth considering for future.

### 4. Connection Change Validation  
**Question**: When cluster connection changes, should we still validate ownership via annotations before allowing operations?
**Context**: The ADR shows this as a benefit, but we need to implement the actual validation in Update().

### 4. Field Manager Strategy
**Question**: Should we use different field managers for different operations, and how aggressive should we be?
**Current Plan**: 
- "k8sinline" for normal operations with `Force: false`
- "k8sinline-import" for imports with `Force: true` (only for annotations)
**Alternative**: Always use "k8sinline" but vary the Force parameter based on operation type

### 5. Error Message Granularity
**Question**: How specific should we be in distinguishing field manager conflicts?
**Current Plan**: Detect conflicts with other k8sinline vs external tools based on field manager name
**Risk**: Field manager names could be spoofed or changed, making detection unreliable

### 6. Annotation Cleanup
**Question**: Should Delete operations remove ownership annotations, or let Kubernetes clean them up with the resource?
**Consideration**: Removing annotations makes debugging harder if deletion fails.
