// internal/k8sconnect/resource/manifest/crud.go
package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data manifestResourceModel

	diags := req.Plan.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Generate ID for new resource
	data.ID = types.StringValue(r.generateID())

	// Use pipeline for setup
	pipeline := NewOperationPipeline(r)
	rc, err := pipeline.PrepareContext(ctx, &data, false)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// Set ownership annotation
	r.setOwnershipAnnotation(rc.Object, data.ID.ValueString())

	// Check if resource already exists and verify ownership
	existingObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err == nil {
		// Resource exists - check ownership
		existingID := r.getOwnershipID(existingObj)
		if existingID != "" && existingID != data.ID.ValueString() {
			// Different ID - owned by another state
			resp.Diagnostics.AddError(
				"Resource Already Managed",
				fmt.Sprintf("resource managed by different k8sconnect resource (Terraform ID: %s)", existingID),
			)
			return
		}
		// If existingID is empty (unowned) or matches our ID, we can proceed
	} else if !errors.IsNotFound(err) {
		// Real error checking if resource exists
		resp.Diagnostics.AddError("Existence Check Failed",
			fmt.Sprintf("Failed to check if resource exists: %s", err))
		return
	}

	// Check force conflicts
	forceConflicts := false
	if !data.ForceConflicts.IsNull() {
		forceConflicts = data.ForceConflicts.ValueBool()
	}

	// Apply the resource
	err = rc.Client.Apply(ctx, rc.Object, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        forceConflicts,
	})
	if err != nil {
		if isFieldConflictError(err) {
			resp.Diagnostics.AddError("Field Manager Conflict",
				"Another controller owns fields you're trying to set. "+
					"Set force_conflicts = true to override.")
		} else {
			resp.Diagnostics.AddError("Create Failed", err.Error())
		}
		return
	}

	// Phase 2 - Only read back if using field ownership
	if !data.UseFieldOwnership.IsNull() && data.UseFieldOwnership.ValueBool() {
		createdObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
		if err != nil {
			tflog.Warn(ctx, "Failed to read resource after create", map[string]interface{}{
				"error": err.Error(),
				"kind":  rc.Object.GetKind(),
				"name":  rc.Object.GetName(),
			})
		} else {
			rc.Object = createdObj
			tflog.Debug(ctx, "Read resource after create for managedFields", map[string]interface{}{
				"kind":          rc.Object.GetKind(),
				"name":          rc.Object.GetName(),
				"has_managed":   len(rc.Object.GetManagedFields()) > 0,
				"field_manager": "k8sconnect",
			})
		}
	}

	tflog.Info(ctx, "Resource created", map[string]interface{}{
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})

	// Handle wait conditions
	fmt.Printf("=== Create BEFORE WAIT ===\n")
	fmt.Printf("wait_for IsNull: %v\n", rc.Data.WaitFor.IsNull())

	fmt.Printf("=== Create AFTER WAIT ===\n")
	waited := false
	if err := pipeline.ExecuteWait(rc); err != nil {
		fmt.Printf("Wait error: %v\n", err)
		resp.Diagnostics.AddWarning("Wait Failed",
			fmt.Sprintf("Resource created but wait failed: %s", err))
		waited = true
	} else if !rc.Data.WaitFor.IsNull() {
		var waitConfig waitForModel
		diags := rc.Data.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
		if resp.Diagnostics.Append(diags...); !resp.Diagnostics.HasError() {
			waited = true
			fmt.Printf("Parsed wait_for - Field: %v\n", waitConfig.Field)
		}
	}

	fmt.Printf("Waited flag: %v\n", waited)

	// Update status field
	fmt.Printf("Status BEFORE UpdateStatus - IsNull: %v, IsUnknown: %v\n",
		rc.Data.Status.IsNull(), rc.Data.Status.IsUnknown())

	if err := pipeline.UpdateStatus(rc, waited); err != nil {
		tflog.Warn(ctx, "Failed to update status", map[string]interface{}{"error": err.Error()})
	}

	fmt.Printf("Status AFTER UpdateStatus - IsNull: %v, IsUnknown: %v\n",
		rc.Data.Status.IsNull(), rc.Data.Status.IsUnknown())

	// Update projection
	if err := pipeline.UpdateProjection(rc); err != nil {
		resp.Diagnostics.AddWarning("Projection Update Failed",
			fmt.Sprintf("Resource created but projection update failed: %s", err))
	}

	// Save state
	fmt.Printf("FINAL Status before State.Set - IsNull: %v, IsUnknown: %v\n",
		rc.Data.Status.IsNull(), rc.Data.Status.IsUnknown())
	fmt.Printf("=== END Create ===\n\n")

	diags = resp.State.Set(ctx, rc.Data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data manifestResourceModel

	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Parse connection from state
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		// Connection error - likely removed resource
		resp.State.RemoveResource(ctx)
		return
	}

	// Create client
	client, err := r.clientGetter(conn)
	if err != nil {
		// Can't connect - resource might be gone
		resp.State.RemoveResource(ctx)
		return
	}

	// Parse YAML to get the object identity
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML",
			fmt.Sprintf("Failed to parse YAML from state: %s", err))
		return
	}

	// Get GVR
	gvr, err := client.GetGVR(ctx, obj)
	if err != nil {
		resp.Diagnostics.AddError("Resource Discovery Failed",
			fmt.Sprintf("Failed to get resource type: %s", err))
		return
	}

	// Read current state from Kubernetes
	currentObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			// Resource was deleted outside Terraform
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read Failed",
			fmt.Sprintf("Failed to read %s: %s", obj.GetKind(), err))
		return
	}

	// Check if this resource is owned by this Terraform resource
	annotations := currentObj.GetAnnotations()
	currentID := ""
	if annotations != nil {
		currentID = annotations["k8sconnect.terraform.io/terraform-id"]
	}

	if currentID == "" {
		resp.Diagnostics.AddError(
			"Resource Not Managed",
			fmt.Sprintf("The %s '%s' exists but is not managed by Terraform. Use 'terraform import' to manage it.",
				obj.GetKind(), obj.GetName()),
		)
		return
	}

	if currentID != data.ID.ValueString() {
		resp.Diagnostics.AddError(
			"Resource Ownership Conflict",
			fmt.Sprintf("The %s '%s' is now managed by a different Terraform resource (ID: %s).",
				obj.GetKind(), obj.GetName(), currentID),
		)
		return
	}

	// Extract paths - use field ownership if flag is enabled
	var paths []string
	useFieldOwnership := !data.UseFieldOwnership.IsNull() && data.UseFieldOwnership.ValueBool()

	if useFieldOwnership && len(currentObj.GetManagedFields()) > 0 {
		tflog.Debug(ctx, "Using field ownership for projection during Read", map[string]interface{}{
			"managers": len(currentObj.GetManagedFields()),
		})
		// Use the desired YAML to determine paths, but project from actual K8s object
		paths = extractOwnedPaths(ctx, currentObj.GetManagedFields(), obj.Object)
	} else {
		if useFieldOwnership {
			tflog.Warn(ctx, "Field ownership requested but no managedFields available during Read")
		}
		// Fall back to standard extraction from YAML
		paths = extractFieldPaths(obj.Object, "")
	}

	// Project the current state to only include fields we manage
	projection, err := projectFields(currentObj.Object, paths)
	if err != nil {
		resp.Diagnostics.AddError("Projection Failed",
			fmt.Sprintf("Failed to project managed fields: %s", err))
		return
	}

	// Convert projection to JSON for storage
	projectionJSON, err := toJSON(projection)
	if err != nil {
		resp.Diagnostics.AddError("JSON Conversion Failed",
			fmt.Sprintf("Failed to convert projection to JSON: %s", err))
		return
	}

	fmt.Printf("=== READ: Projection for %s/%s ===\n", currentObj.GetKind(), currentObj.GetName())
	fmt.Printf("Projection: %s\n", projectionJSON[:min(500, len(projectionJSON))])
	fmt.Printf("=== END READ ===\n")

	// Update the projection in state - this is what enables drift detection
	data.ManagedStateProjection = types.StringValue(projectionJSON)

	// The original Read ALSO updates field_ownership - needed for imports
	ownership := extractFieldOwnership(currentObj)
	ownershipJSON, err := json.Marshal(ownership)
	if err != nil {
		tflog.Warn(ctx, "Failed to marshal field ownership", map[string]interface{}{
			"error": err.Error(),
		})
		data.FieldOwnership = types.StringValue("{}")
	} else {
		data.FieldOwnership = types.StringValue(string(ownershipJSON))
	}

	tflog.Debug(ctx, "Updated managed state projection", map[string]interface{}{
		"id":              data.ID.ValueString(),
		"use_ownership":   useFieldOwnership,
		"path_count":      len(paths),
		"projection_size": len(projectionJSON),
	})

	// Set the refreshed state - only projection was updated
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var state, plan manifestResourceModel

	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	diags = req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Use pipeline for setup
	pipeline := NewOperationPipeline(r)
	rc, err := pipeline.PrepareContext(ctx, &plan, true)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// Preserve ID and ownership
	plan.ID = state.ID
	r.setOwnershipAnnotation(rc.Object, plan.ID.ValueString())

	// Check force conflicts
	forceConflicts := false
	if !plan.ForceConflicts.IsNull() {
		forceConflicts = plan.ForceConflicts.ValueBool()
	}

	// Apply the update
	err = rc.Client.Apply(ctx, rc.Object, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        forceConflicts,
	})
	if err != nil {
		if isFieldConflictError(err) {
			resp.Diagnostics.AddError("Field Manager Conflict",
				"Another controller owns fields you're trying to modify. "+
					"Set force_conflicts = true to override.")
		} else {
			resp.Diagnostics.AddError("Update Failed", err.Error())
		}
		return
	}

	tflog.Info(ctx, "Resource updated", map[string]interface{}{
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})

	// Handle wait conditions
	waited := false
	if err := pipeline.ExecuteWait(rc); err != nil {
		resp.Diagnostics.AddWarning("Wait Failed",
			fmt.Sprintf("Resource updated but wait failed: %s", err))
		waited = true
	} else if !rc.Data.WaitFor.IsNull() {
		var waitConfig waitForModel
		diags := rc.Data.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
		if !diags.HasError() && pipeline.hasActiveWaitConditions(waitConfig) {
			waited = true
		}
	}

	pipeline.UpdateStatus(rc, waited)

	if err := pipeline.UpdateProjection(rc); err != nil {
		tflog.Warn(ctx, "Failed to update projection", map[string]interface{}{"error": err.Error()})
	}

	// Handle status transitions
	if !state.Status.IsNull() && plan.WaitFor.IsNull() {
		plan.Status = types.DynamicNull()
		tflog.Info(ctx, "Clearing status - wait_for was removed")
	}

	diags = resp.State.Set(ctx, &plan)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data manifestResourceModel

	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check delete protection
	if !data.DeleteProtection.IsNull() && data.DeleteProtection.ValueBool() {
		resp.Diagnostics.AddError(
			"Delete Protection Enabled",
			"This resource has delete protection enabled. Set delete_protection = false to allow deletion.",
		)
		return
	}

	// Use pipeline for setup
	pipeline := NewOperationPipeline(r)
	rc, err := pipeline.PrepareContext(ctx, &data, true)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// Get delete options
	timeout := r.getDeleteTimeout(data)
	forceDestroy := false
	if !data.ForceDestroy.IsNull() {
		forceDestroy = data.ForceDestroy.ValueBool()
	}

	// Check if resource exists
	_, err = rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			tflog.Info(ctx, "Resource already deleted")
			return
		}
		resp.Diagnostics.AddError("Failed to check resource", err.Error())
		return
	}

	// Delete with empty options (original signature requires it)
	err = rc.Client.Delete(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName(), k8sclient.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		resp.Diagnostics.AddError("Deletion Failed", err.Error())
		return
	}

	// Handle force destroy and timeout AFTER delete
	if !forceDestroy {
		err = r.waitForDeletion(ctx, rc.Client, rc.GVR, rc.Object, timeout)
		if err != nil {
			r.handleDeletionTimeout(resp, rc.Client, rc.GVR, rc.Object, timeout, err)
			return
		}
	} else {
		// Force destroy - try to remove finalizers if deletion is stuck
		if err := r.forceDestroy(ctx, rc.Client, rc.GVR, rc.Object, resp); err != nil {
			tflog.Warn(ctx, "Force destroy encountered issues", map[string]interface{}{"error": err.Error()})
			// Don't fail - resource might already be gone
		}
	}

	tflog.Info(ctx, "Resource deleted", map[string]interface{}{
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})
}
