// internal/k8sconnect/resource/manifest/crud.go
package manifest

import (
	"context"
	"encoding/json"
	"fmt"

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

	// Use pipeline for common setup
	pipeline := NewOperationPipeline(r)
	rc, err := pipeline.PrepareContext(ctx, &data, true)
	if err != nil {
		resp.Diagnostics.AddError("Preparation Failed", err.Error())
		return
	}

	// Check if resource already exists
	existingObj, err := rc.Client.Get(ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err == nil {
		// Resource exists - check ownership
		existingID := r.getOwnershipID(existingObj)
		if existingID != "" {
			// Check if this is our resource (imported or previously created)
			if existingID == data.ID.ValueString() {
				// This is our resource - treat as update
				tflog.Info(ctx, "resource already owned by this state, treating as update", map[string]interface{}{
					"kind": rc.Object.GetKind(),
					"name": rc.Object.GetName(),
					"id":   existingID,
				})
				// Continue with the create logic which will act as an update
			} else {
				// Different ID - owned by another state
				resp.Diagnostics.AddError(
					"Resource Already Managed",
					fmt.Sprintf("resource managed by different k8sconnect resource (Terraform ID: %s)", existingID),
				)
				return
			}
		} else {
			// Resource exists but not managed - we can take ownership
			tflog.Info(ctx, "adopting unmanaged resource", map[string]interface{}{
				"kind": rc.Object.GetKind(),
				"name": rc.Object.GetName(),
			})
		}
	} else if !errors.IsNotFound(err) {
		// Real error checking if resource exists
		resp.Diagnostics.AddError("Existence Check Failed",
			fmt.Sprintf("Failed to check if resource exists: %s", err))
		return
	}

	// Only generate new ID if we don't already have one
	var id string
	if data.ID.IsNull() || data.ID.ValueString() == "" {
		id = r.generateID()
	} else {
		// Use existing ID (from import or previous apply)
		id = data.ID.ValueString()
	}

	data.ID = types.StringValue(id)
	r.setOwnershipAnnotation(rc.Object, data.ID.ValueString())

	// Apply with Force=false
	err = rc.Client.Apply(ctx, rc.Object, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        false,
	})
	if err != nil {
		resp.Diagnostics.AddError("Creation Failed",
			fmt.Sprintf("Failed to create %s: %s", rc.Object.GetKind(), err))
		return
	}

	tflog.Info(ctx, "Resource created", map[string]interface{}{
		"id":        data.ID.ValueString(),
		"kind":      rc.Object.GetKind(),
		"name":      rc.Object.GetName(),
		"namespace": rc.Object.GetNamespace(),
	})

	// Handle wait conditions
	waited := false
	if err := pipeline.ExecuteWait(rc); err != nil {
		resp.Diagnostics.AddWarning("Wait Failed",
			fmt.Sprintf("Resource created but wait failed: %s", err))
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

	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data manifestResourceModel

	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if connection is ready (same as original)
	if !r.isConnectionReady(data.ClusterConnection) {
		tflog.Info(ctx, "Skipping Read due to unknown connection values")
		return
	}

	// Get connection from state (same as original)
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
		return
	}

	// Parse the stored YAML to get resource info (same as original)
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("YAML Parse Failed", err.Error())
		return
	}

	// Create K8s client
	var client k8sclient.K8sClient
	if r.clientFactory != nil {
		client, err = r.clientFactory.GetClient(conn)
	} else if r.clientGetter != nil {
		client, err = r.clientGetter(conn)
	} else {
		resp.Diagnostics.AddError("No Client Factory", "No client factory or getter configured")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Client Creation Failed", err.Error())
		return
	}

	// Get the GVR for this resource (same as original)
	gvr, err := client.GetGVR(ctx, obj)
	if err != nil {
		resp.Diagnostics.AddError("GVR Resolution Failed", err.Error())
		return
	}

	// Check if resource exists
	currentObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			tflog.Info(ctx, "Resource not found, removing from state",
				map[string]interface{}{
					"id":        data.ID.ValueString(),
					"kind":      obj.GetKind(),
					"name":      obj.GetName(),
					"namespace": obj.GetNamespace(),
				})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to Get Resource",
			fmt.Sprintf("Failed to read %s %s: %s", obj.GetKind(), obj.GetName(), err))
		return
	}

	// Check ownership
	currentID := r.getOwnershipID(currentObj)
	if currentID == "" {
		resp.Diagnostics.AddWarning(
			"Resource Ownership Lost",
			fmt.Sprintf("The %s '%s' is no longer marked as managed by Terraform.\n"+
				"The ownership annotations have been removed.",
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

	// Extract paths from the stored YAML (what we're managing)
	paths := extractFieldPaths(obj.Object, "")

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

	// Validate connection change
	if r.connResolver != nil && !state.ClusterConnection.Equal(plan.ClusterConnection) {
		oldConn, err := r.convertObjectToConnectionModel(ctx, state.ClusterConnection)
		if err != nil {
			resp.Diagnostics.AddError("Failed to parse old connection", err.Error())
			return
		}

		newConn, err := r.convertObjectToConnectionModel(ctx, plan.ClusterConnection)
		if err != nil {
			resp.Diagnostics.AddError("Failed to parse new connection", err.Error())
			return
		}

		if err := r.connResolver.ValidateConnectionChange(oldConn, newConn); err != nil {
			resp.Diagnostics.AddError("Connection Change Blocked", err.Error())
			return
		}
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
