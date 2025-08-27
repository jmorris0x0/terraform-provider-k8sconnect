// internal/k8sconnect/resource/manifest/crud.go
package manifest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/k8sclient"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data manifestResourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if cluster connection is ready (handles unknown values during planning)
	// Note: Now we need to check if we have ANY connection (resource OR provider level)
	if !data.ClusterConnection.IsNull() && !r.isConnectionReady(data.ClusterConnection) {
		resp.Diagnostics.AddError(
			"Cluster Connection Not Ready",
			"Cluster connection contains unknown values. This usually happens during planning when dependencies are not yet resolved.",
		)
		return
	}

	// Try to resolve connection (resource level or provider level)
	var effectiveConn auth.ClusterConnectionModel

	if r.connResolver != nil {
		// We have a resolver, use it to get effective connection
		var resourceConn *auth.ClusterConnectionModel

		// Convert resource-level connection if present
		if !data.ClusterConnection.IsNull() && !data.ClusterConnection.IsUnknown() {
			conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
			if err != nil {
				resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
				return
			}
			resourceConn = &conn
		}

		// Resolve effective connection
		resolvedConn, err := r.connResolver.ResolveConnection(resourceConn)
		if err != nil {
			resp.Diagnostics.AddError("No Cluster Connection Available",
				fmt.Sprintf("%s\n\nYou can configure a connection either at the provider level or in the resource's cluster_connection block.", err.Error()))
			return
		}
		effectiveConn = resolvedConn

		// IMPORTANT: If using provider connection, store it in state
		if resourceConn == nil {
			// Resource is using provider-level connection
			// We need to store it to prevent accidental cluster switches
			connObj, err := r.convertConnectionToObject(ctx, effectiveConn)
			if err != nil {
				resp.Diagnostics.AddError("Failed to store connection", err.Error())
				return
			}
			data.ClusterConnection = connObj

			tflog.Info(ctx, "using provider-level connection, storing in resource state")
		}
	} else {
		// Fallback for old behavior (no resolver)
		if data.ClusterConnection.IsNull() {
			resp.Diagnostics.AddError("No Cluster Connection",
				"cluster_connection is required when provider-level connection is not configured")
			return
		}

		conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
		if err != nil {
			resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
			return
		}
		effectiveConn = conn
	}

	// Parse YAML into unstructured object
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Create K8s client using the resolved connection
	client, err := r.clientGetter(effectiveConn)
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
			// Check if this is our resource (imported or previously created)
			if existingID == data.ID.ValueString() {
				// This is our resource - treat as update
				tflog.Info(ctx, "resource already owned by this state, treating as update", map[string]interface{}{
					"kind": obj.GetKind(),
					"name": obj.GetName(),
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
				"kind": obj.GetKind(),
				"name": obj.GetName(),
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
	r.setOwnershipAnnotation(obj, id)

	// Apply the manifest using server-side apply
	err = client.Apply(ctx, obj, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
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

	// Add field ownership tracking
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

	// Clear the import flag now that annotations are set
	data.ImportedWithoutAnnotations = types.BoolNull()

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
		tflog.Info(ctx, "Skipping Read due to unknown connection values")
		return
	}

	// Get connection from state (this already has the effective connection stored)
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
		return
	}

	// Parse the stored YAML to get resource info
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("YAML Parse Failed", err.Error())
		return
	}

	// Create K8s client using the proper factory method
	client, err := r.clientFactory.GetClient(conn)
	if err != nil {
		resp.Diagnostics.AddError("Client Creation Failed", err.Error())
		return
	}

	// Get the GVR for this resource
	gvr, err := client.GetGVR(ctx, obj)
	if err != nil {
		resp.Diagnostics.AddError("GVR Resolution Failed", err.Error())
		return
	}

	// Check if resource exists
	currentObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			// Resource was deleted outside of Terraform
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Failed to read resource",
			fmt.Sprintf("Failed to read %s %s: %s", obj.GetKind(), obj.GetName(), err))
		return
	}

	// Populate field ownership
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

	// Verify ownership
	currentID := r.getOwnershipID(currentObj)
	if currentID == "" {
		resp.Diagnostics.AddError(
			"Resource Not Managed",
			fmt.Sprintf("The %s '%s' exists but is not managed by Terraform. "+
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

	tflog.Debug(ctx, "Updated managed state projection", map[string]interface{}{
		"id":              data.ID.ValueString(),
		"path_count":      len(paths),
		"projection_size": len(projectionJSON),
	})

	// Set the refreshed state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

func (r *manifestResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state manifestResourceModel

	// Get both plan and current state
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	diags = req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if connection is changing
	if r.connResolver != nil && !plan.ClusterConnection.Equal(state.ClusterConnection) {
		// Connection is changing - verify it's safe
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

		// Use resolver to check if this targets a different cluster
		if err := r.connResolver.ValidateConnectionChange(oldConn, newConn); err != nil {
			resp.Diagnostics.AddError("Connection Change Blocked", err.Error())
			return
		}
	}

	// Get connection from plan (uses the stored effective connection)
	conn, err := r.convertObjectToConnectionModel(ctx, plan.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
		return
	}

	// Parse the YAML
	obj, err := r.parseYAML(plan.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Ensure the resource has our ownership annotation with the correct ID
	r.setOwnershipAnnotation(obj, state.ID.ValueString())

	// Create K8s client
	client, err := r.clientGetter(conn)
	if err != nil {
		resp.Diagnostics.AddError("Connection Failed", fmt.Sprintf("Failed to create Kubernetes client: %s", err))
		return
	}

	// Get GVR for the object
	gvr, err := r.getGVR(ctx, client, obj)
	if err != nil {
		resp.Diagnostics.AddError("Resource Discovery Failed",
			fmt.Sprintf("Failed to determine resource type: %s", err))
		return
	}

	// Verify the resource still exists and we own it
	currentObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			resp.Diagnostics.AddError(
				"Resource Not Found",
				fmt.Sprintf("The %s '%s' no longer exists in the cluster. It may have been deleted outside of Terraform.",
					obj.GetKind(), obj.GetName()),
			)
			return
		}
		resp.Diagnostics.AddError("Failed to check resource", fmt.Sprintf("Failed to get current resource state: %s", err))
		return
	}

	// Verify ownership
	currentID := r.getOwnershipID(currentObj)
	if currentID != state.ID.ValueString() {
		if currentID == "" {
			resp.Diagnostics.AddError(
				"Resource Ownership Lost",
				fmt.Sprintf("The %s '%s' is no longer managed by Terraform. The ownership annotations have been removed.",
					obj.GetKind(), obj.GetName()),
			)
		} else {
			resp.Diagnostics.AddError(
				"Resource Ownership Conflict",
				fmt.Sprintf("The %s '%s' is now managed by a different Terraform resource (ID: %s).",
					obj.GetKind(), obj.GetName(), currentID),
			)
		}
		return
	}

	// Get force_conflicts setting
	forceConflicts := false
	if !plan.ForceConflicts.IsNull() {
		forceConflicts = plan.ForceConflicts.ValueBool()
	}

	// Handle imported resources that need ownership annotation
	if !state.ImportedWithoutAnnotations.IsNull() && state.ImportedWithoutAnnotations.ValueBool() {
		// For imported resources, we need to add our ownership annotation using patch
		annotationPatch := map[string]interface{}{
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					OwnershipAnnotation: state.ID.ValueString(),
				},
			},
		}

		patchData, err := json.Marshal(annotationPatch)
		if err != nil {
			resp.Diagnostics.AddError("Failed to create patch", err.Error())
			return
		}

		_, err = client.Patch(ctx, gvr, obj.GetNamespace(), obj.GetName(),
			k8stypes.StrategicMergePatchType, patchData, metav1.PatchOptions{
				FieldManager: "k8sconnect",
			})
		if err != nil {
			resp.Diagnostics.AddError("Failed to add ownership annotation",
				fmt.Sprintf("Could not add ownership annotation to imported resource: %s", err))
			return
		}

		tflog.Info(ctx, "Added ownership annotation to imported resource using patch", map[string]interface{}{
			"kind": obj.GetKind(),
			"name": obj.GetName(),
			"id":   state.ID.ValueString(),
		})
	}

	// Apply the updated manifest
	err = client.Apply(ctx, obj, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        forceConflicts,
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
		"id":        state.ID.ValueString(),
		"kind":      obj.GetKind(),
		"name":      obj.GetName(),
		"namespace": obj.GetNamespace(),
	})

	// Extract paths from what we applied
	paths := extractFieldPaths(obj.Object, "")

	// Get the current state after apply
	updatedObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read after update", fmt.Sprintf("Failed to read resource after update: %s", err))
		return
	}

	// Add field ownership tracking
	ownership := extractFieldOwnership(updatedObj)
	ownershipJSON, err := json.Marshal(ownership)
	if err != nil {
		tflog.Warn(ctx, "Failed to marshal field ownership", map[string]interface{}{
			"error": err.Error(),
		})
		plan.FieldOwnership = types.StringValue("{}")
	} else {
		plan.FieldOwnership = types.StringValue(string(ownershipJSON))
	}

	// Project the current state
	projection, err := projectFields(updatedObj.Object, paths)
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

	// Update the plan with the new projection and preserve the ID
	plan.ID = state.ID
	plan.ManagedStateProjection = types.StringValue(projectionJSON)

	// Preserve the import flag from state
	plan.ImportedWithoutAnnotations = state.ImportedWithoutAnnotations

	// Save updated data to state
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

	// Check delete protection first
	if !data.DeleteProtection.IsNull() && data.DeleteProtection.ValueBool() {
		resp.Diagnostics.AddError(
			"Delete Protection Enabled",
			"This resource has delete protection enabled. Set delete_protection = false to allow deletion.",
		)
		return
	}

	// Get connection from state
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Connection Conversion Failed", err.Error())
		return
	}

	// Parse the YAML to get resource info
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML in State", fmt.Sprintf("Failed to parse stored YAML: %s", err))
		return
	}

	// Create K8s client
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
