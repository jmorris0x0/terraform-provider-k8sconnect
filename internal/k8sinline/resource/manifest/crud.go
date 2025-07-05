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

	// Get GVR for the object first
	gvr, err := r.getGVR(ctx, client, obj)
	if err != nil {
		resp.Diagnostics.AddError("Resource Discovery Failed",
			fmt.Sprintf("Failed to determine resource type: %s", err))
		return
	}

	// Check if resource already exists
	existingObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err == nil {
		// Resource exists - check ownership
		existingID := r.getOwnershipID(existingObj)
		if existingID != "" {
			// Already managed by k8sinline
			resp.Diagnostics.AddError(
				"Resource Already Managed",
				fmt.Sprintf("resource managed by different k8sinline resource (Terraform ID: %s)", existingID),
			)
			return
		}
		// Resource exists but not managed - we can take ownership
		tflog.Info(ctx, "adopting unmanaged resource", map[string]interface{}{
			"kind": obj.GetKind(),
			"name": obj.GetName(),
		})
	} else if !errors.IsNotFound(err) {
		// Real error checking if resource exists
		resp.Diagnostics.AddError("Existence Check Failed",
			fmt.Sprintf("Failed to check if resource exists: %s", err))
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

	// Check ownership - but ONLY validate ID match, not treat as conflict
	existingID := r.getOwnershipID(currentObj)
	if existingID == "" {
		// Resource exists but has no ownership annotation
		// This could happen if annotations were stripped
		// Re-add our ownership annotation during next apply
		tflog.Warn(ctx, "resource missing ownership annotation", map[string]interface{}{
			"id":   data.ID.ValueString(),
			"kind": obj.GetKind(),
			"name": obj.GetName(),
		})
		// Continue with read - don't treat as error
	} else if existingID != data.ID.ValueString() {
		// Different owner - this is a real conflict
		resp.Diagnostics.AddError(
			"Resource Ownership Conflict",
			fmt.Sprintf("Resource is managed by different k8sinline resource (Terraform ID: %s)", existingID),
		)
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
	var configData manifestResourceModel

	// Get current state
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get desired configuration
	diags = req.Config.Get(ctx, &configData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if cluster connection is ready
	if !r.isConnectionReady(configData.ClusterConnection) {
		resp.Diagnostics.AddError(
			"Cluster Connection Not Ready",
			"Cannot update resource: cluster connection contains unknown values.",
		)
		return
	}

	// Use connection from config (which may have changed)
	conn, err := r.convertObjectToConnectionModel(ctx, configData.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
		return
	}

	// Parse YAML
	obj, err := r.parseYAML(configData.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Create K8s client with new connection
	client, err := r.clientGetter(conn)
	if err != nil {
		resp.Diagnostics.AddError("Connection Failed",
			fmt.Sprintf("Failed to create Kubernetes client: %s", err))
		return
	}

	// Preserve the existing ID - it should never change
	configData.ID = data.ID

	// Check ownership before updating
	gvr, err := r.getGVR(ctx, client, obj)
	if err != nil {
		resp.Diagnostics.AddError("Resource Discovery Failed",
			fmt.Sprintf("Failed to determine resource type: %s", err))
		return
	}

	liveObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			resp.Diagnostics.AddError("Resource Not Found",
				"Resource no longer exists in Kubernetes")
			return
		}
		resp.Diagnostics.AddError("Ownership Check Failed",
			fmt.Sprintf("Could not check resource ownership: %s", err))
		return
	}

	// Validate ownership
	if err := r.validateOwnership(liveObj, data.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Resource Ownership Conflict", err.Error())
		return
	}

	// Set ownership annotation with existing ID
	r.setOwnershipAnnotation(obj, data.ID.ValueString())

	// Apply the updated manifest
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

	// Update projection
	paths := extractFieldPaths(obj.Object, "")
	currentObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read after update",
			fmt.Sprintf("Failed to read resource after update: %s", err))
		return
	}

	projection, err := projectFields(currentObj.Object, paths)
	if err != nil {
		resp.Diagnostics.AddError("Projection Failed",
			fmt.Sprintf("Failed to project managed fields: %s", err))
		return
	}

	projectionJSON, err := toJSON(projection)
	if err != nil {
		resp.Diagnostics.AddError("JSON Conversion Failed",
			fmt.Sprintf("Failed to convert projection to JSON: %s", err))
		return
	}

	// Build final state with:
	// - ID from existing state (never changes)
	// - Connection from config (may have changed)
	// - Other fields from config
	// - Computed projection
	finalData := manifestResourceModel{
		ID:                     data.ID, // Preserve existing ID
		YAMLBody:               configData.YAMLBody,
		ClusterConnection:      configData.ClusterConnection,
		DeleteProtection:       configData.DeleteProtection,
		DeleteTimeout:          configData.DeleteTimeout,
		ForceDestroy:           configData.ForceDestroy,
		ManagedStateProjection: types.StringValue(projectionJSON),
	}

	// Save the updated state
	diags = resp.State.Set(ctx, &finalData)
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
			"Cannot delete resource: cluster connection contains unknown values. This usually happens during planning when dependencies are not yet resolved.",
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

	// Get timeout configuration
	timeout := r.getDeleteTimeout(data)
	forceDestroy := false
	if !data.ForceDestroy.IsNull() {
		forceDestroy = data.ForceDestroy.ValueBool()
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
