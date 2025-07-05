// internal/k8sinline/resource/manifest/crud.go
package manifest

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
	"k8s.io/apimachinery/pkg/api/errors"
)

func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data manifestResourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if cluster connection is ready (handles unknown values during planning)
	if !r.isConnectionReady(data.ClusterConnection) {
		resp.Diagnostics.AddError(
			"Cluster Connection Not Ready",
			"Cluster connection contains unknown values. This usually happens during planning when dependencies are not yet resolved.",
		)
		return
	}

	// Convert to connection model
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
		return
	}

	// Parse YAML into unstructured object
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Create K8s client from cluster connection (now with caching)
	client, err := r.clientGetter(conn)
	if err != nil {
		resp.Diagnostics.AddError("Connection Failed", fmt.Sprintf("Failed to create Kubernetes client: %s", err))
		return
	}

	id := r.generateID()
	data.ID = types.StringValue(id)
	r.setOwnershipAnnotation(obj, id)

	// Apply the manifest using server-side apply
	err = client.SetFieldManager("k8sinline").Apply(ctx, obj, k8sclient.ApplyOptions{
		FieldManager: "k8sinline",
		Force:        false,
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

	tflog.Trace(ctx, "applied manifest", map[string]interface{}{
		"id":        data.ID.ValueString(),
		"kind":      obj.GetKind(),
		"name":      obj.GetName(),
		"namespace": obj.GetNamespace(),
	})

	// Get GVR for the object
	gvr, err := r.getGVR(ctx, client, obj)
	if err != nil {
		resp.Diagnostics.AddError("Resource Discovery Failed", fmt.Sprintf("Failed to determine resource type: %s", err))
		return
	}

	// Extract paths from what we applied
	paths := extractFieldPaths(obj.Object, "")

	// Get the current state after apply
	currentObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read after create", fmt.Sprintf("Failed to read resource after creation: %s", err))
		return
	}

	// Project the current state
	projection, err := projectFields(currentObj.Object, paths)
	if err != nil {
		resp.Diagnostics.AddError("Projection Failed", fmt.Sprintf("Failed to project managed fields: %s", err))
		return
	}

	// Store the projection
	projectionJSON, err := toJSON(projection)
	if err != nil {
		resp.Diagnostics.AddError("JSON Conversion Failed", fmt.Sprintf("Failed to convert projection to JSON: %s", err))
		return
	}

	data.ManagedStateProjection = types.StringValue(projectionJSON)

	tflog.Debug(ctx, "Stored managed state projection", map[string]interface{}{
		"path_count":      len(paths),
		"projection_size": len(projectionJSON),
	})

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

	// Check if cluster connection is ready
	if !r.isConnectionReady(data.ClusterConnection) {
		tflog.Info(ctx, "Skipping Read due to unknown connection values", map[string]interface{}{
			"resource_id":        data.ID.ValueString(),
			"connection_unknown": true,
		})
		return
	}

	// Convert to connection model
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
		return
	}

	// Parse YAML to get object metadata
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Create K8s client from cluster connection (cached)
	client, err := r.clientGetter(conn)
	if err != nil {
		resp.Diagnostics.AddError("Connection Failed", fmt.Sprintf("Failed to create Kubernetes client: %s", err))
		return
	}

	// Get GVR for the object
	gvr, err := r.getGVR(ctx, client, obj)
	if err != nil {
		resp.Diagnostics.AddError("Resource Discovery Failed", fmt.Sprintf("Failed to determine resource type: %s", err))
		return
	}

	// Check if object still exists
	currentObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			// Object no longer exists - remove from state
			resp.State.RemoveResource(ctx)
			return
		}
		// Other errors should be reported
		resourceDesc := fmt.Sprintf("%s %s", obj.GetKind(), obj.GetName())
		severity, title, detail := r.classifyK8sError(err, "Read", resourceDesc)
		if severity == "warning" {
			resp.Diagnostics.AddWarning(title, detail)
		} else {
			resp.Diagnostics.AddError(title, detail)
		}
		return
	}

	// NEW: Extract paths from the yaml_body to know what we manage
	paths := extractFieldPaths(obj.Object, "")

	// NEW: Project current Kubernetes state for our managed fields
	projection, err := projectFields(currentObj.Object, paths)
	if err != nil {
		resp.Diagnostics.AddError("Projection Failed", fmt.Sprintf("Failed to project managed fields: %s", err))
		return
	}

	// NEW: Update the projection in state
	projectionJSON, err := toJSON(projection)
	if err != nil {
		resp.Diagnostics.AddError("JSON Conversion Failed", fmt.Sprintf("Failed to convert projection to JSON: %s", err))
		return
	}

	data.ManagedStateProjection = types.StringValue(projectionJSON)

	tflog.Debug(ctx, "Updated managed state projection during read", map[string]interface{}{
		"path_count":      len(paths),
		"projection_size": len(projectionJSON),
	})

	// Object exists - keep current state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data manifestResourceModel

	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if cluster connection is ready
	if !r.isConnectionReady(data.ClusterConnection) {
		resp.Diagnostics.AddError(
			"Cluster Connection Not Ready",
			"Cannot update resource: cluster connection contains unknown values. This usually happens during planning when dependencies are not yet resolved.",
		)
		return
	}

	// Convert to connection model
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
		return
	}

	// Parse YAML into unstructured object
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Create K8s client from cluster connection (cached)
	client, err := r.clientGetter(conn)
	if err != nil {
		resp.Diagnostics.AddError("Connection Failed", fmt.Sprintf("Failed to create Kubernetes client: %s", err))
		return
	}

	// Check ownership before updating
	gvr, err := r.getGVR(ctx, client, obj)
	if err != nil {
		resp.Diagnostics.AddError("Resource Discovery Failed",
			fmt.Sprintf("Failed to determine resource type: %s", err))
		return
	}

	liveObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil && !errors.IsNotFound(err) {
		resp.Diagnostics.AddError("Ownership Check Failed",
			fmt.Sprintf("Could not check resource ownership: %s", err))
		return
	}

	// If resource exists, validate we own it
	if err == nil {
		if err := r.validateOwnership(liveObj, data.ID.ValueString()); err != nil {
			resp.Diagnostics.AddError("Resource Ownership Conflict", err.Error())
			return
		}
		tflog.Trace(ctx, "ownership validated", map[string]interface{}{
			"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
			"id":       data.ID.ValueString(),
		})
	}

	// Set ownership annotation before applying
	r.setOwnershipAnnotation(obj, data.ID.ValueString())

	// Apply the updated manifest (server-side apply is idempotent)
	err = client.SetFieldManager("k8sinline").Apply(ctx, obj, k8sclient.ApplyOptions{
		FieldManager: "k8sinline",
		Force:        false,
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

	tflog.Trace(ctx, "updated manifest", map[string]interface{}{
		"id":        data.ID.ValueString(),
		"kind":      obj.GetKind(),
		"name":      obj.GetName(),
		"namespace": obj.GetNamespace(),
	})

	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data manifestResourceModel

	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check delete protection first (doesn't require connection)
	if !data.DeleteProtection.IsNull() && data.DeleteProtection.ValueBool() {
		resp.Diagnostics.AddError(
			"Resource Protected from Deletion",
			"This resource has delete_protection enabled. To delete this resource, first set delete_protection = false in your configuration, run terraform apply, then run terraform destroy.",
		)
		return
	}

	// Check if cluster connection is ready
	if !r.isConnectionReady(data.ClusterConnection) {
		resp.Diagnostics.AddError(
			"Cluster Connection Not Ready",
			"Cannot delete resource: cluster connection contains unknown values.",
		)
		return
	}

	// Convert to connection model
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
		return
	}

	// Parse YAML to get object metadata
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Create K8s client from cluster connection (cached)
	client, err := r.clientGetter(conn)
	if err != nil {
		resp.Diagnostics.AddError("Connection Failed", fmt.Sprintf("Failed to create Kubernetes client: %s", err))
		return
	}

	// Get GVR for the object
	gvr, err := r.getGVR(ctx, client, obj)
	if err != nil {
		resp.Diagnostics.AddError("Resource Discovery Failed", fmt.Sprintf("Failed to determine resource type: %s", err))
		return
	}

	// Check if resource exists before attempting deletion
	_, err = client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			// Object already gone - that's fine
			tflog.Trace(ctx, "object already deleted", map[string]interface{}{
				"id":        data.ID.ValueString(),
				"kind":      obj.GetKind(),
				"name":      obj.GetName(),
				"namespace": obj.GetNamespace(),
			})
			return
		}
		// Other errors should be reported
		resourceDesc := fmt.Sprintf("%s %s", obj.GetKind(), obj.GetName())
		severity, title, detail := r.classifyK8sError(err, "Delete", resourceDesc)
		if severity == "warning" {
			resp.Diagnostics.AddWarning(title, detail)
		} else {
			resp.Diagnostics.AddError(title, detail)
		}
		return
	}

	// Initiate deletion
	err = client.Delete(ctx, gvr, obj.GetNamespace(), obj.GetName(), k8sclient.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		resourceDesc := fmt.Sprintf("%s %s", obj.GetKind(), obj.GetName())
		severity, title, detail := r.classifyK8sError(err, "Delete", resourceDesc)
		if severity == "warning" {
			resp.Diagnostics.AddWarning(title, detail)
		} else {
			resp.Diagnostics.AddError(title, detail)
		}
		return
	}

	// Wait for normal deletion to complete
	timeout := r.getDeleteTimeout(data)
	forceDestroy := !data.ForceDestroy.IsNull() && data.ForceDestroy.ValueBool()

	tflog.Debug(ctx, "Starting deletion wait", map[string]interface{}{
		"timeout":       timeout.String(),
		"force_destroy": forceDestroy,
	})

	err = r.waitForDeletion(ctx, client, gvr, obj, timeout)
	if err == nil {
		// Successful normal deletion
		tflog.Trace(ctx, "deleted manifest normally", map[string]interface{}{
			"id":        data.ID.ValueString(),
			"kind":      obj.GetKind(),
			"name":      obj.GetName(),
			"namespace": obj.GetNamespace(),
		})
		return
	}

	// Normal deletion failed/timed out - check if we should force destroy
	if !forceDestroy {
		// Not forcing, so report the timeout error with helpful guidance
		r.handleDeletionTimeout(resp, client, gvr, obj, timeout, err)
		return
	}

	// Force destroy enabled - remove finalizers and delete
	tflog.Warn(ctx, "Normal deletion failed, attempting force destroy", map[string]interface{}{
		"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
		"timeout":  timeout.String(),
	})

	if err := r.forceDestroy(ctx, client, gvr, obj, resp); err != nil {
		resp.Diagnostics.AddError(
			"Force Destroy Failed",
			fmt.Sprintf("Failed to force destroy %s %s: %s", obj.GetKind(), obj.GetName(), err.Error()),
		)
		return
	}

	// Log successful force destroy
	tflog.Info(ctx, "Force destroyed manifest", map[string]interface{}{
		"id":        data.ID.ValueString(),
		"kind":      obj.GetKind(),
		"name":      obj.GetName(),
		"namespace": obj.GetNamespace(),
	})
}
