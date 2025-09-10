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
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/k8sclient"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data manifestResourceModel

	diags := req.Plan.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get effective connection (resource-level or provider-level)
	var effectiveConn auth.ClusterConnectionModel

	if r.connResolver != nil {
		// Use resolver for flexible connection handling
		resourceConn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
		if err != nil && !data.ClusterConnection.IsNull() {
			resp.Diagnostics.AddError("Invalid Connection", err.Error())
			return
		}

		resolvedConn, err := r.connResolver.ResolveConnection(&resourceConn)
		if err != nil {
			resp.Diagnostics.AddError("No Connection Available",
				fmt.Sprintf("No connection configured. Either set cluster_connection on the resource or configure a provider-level connection: %s", err.Error()))
			return
		}
		effectiveConn = resolvedConn

		// IMPORTANT: If using provider connection, store it in state
		if data.ClusterConnection.IsNull() {
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

	// FIXED: Determine if we should wait (check actual conditions, not just presence of wait_for)
	shouldWait := false
	var waitConfig waitForModel

	if !data.WaitFor.IsNull() {
		diags := data.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
		if !diags.HasError() {
			// Check if ANY actual wait condition is configured
			hasWaitConditions := (!waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != "") ||
				!waitConfig.FieldValue.IsNull() ||
				(!waitConfig.Condition.IsNull() && waitConfig.Condition.ValueString() != "") ||
				(!waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool())

			if hasWaitConditions {
				shouldWait = true
				tflog.Info(ctx, "Executing wait_for conditions", map[string]interface{}{
					"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
				})

				if err := r.waitForResource(ctx, client, gvr, obj, waitConfig); err != nil {
					// Log warning but don't fail the resource creation
					resp.Diagnostics.AddWarning(
						"Wait Condition Not Met",
						fmt.Sprintf("Resource created successfully but wait condition failed: %s\n\n"+
							"The resource has been created in the cluster. You may need to manually verify its status.",
							err.Error()),
					)
				}
			}
		} else {
			resp.Diagnostics.AddWarning(
				"Invalid wait_for Configuration",
				fmt.Sprintf("Could not parse wait_for configuration: %s", diags.Errors()),
			)
		}
	}

	// Get the current state after apply (and after waiting if configured)
	currentObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read after create", fmt.Sprintf("Failed to read resource after creation: %s", err))
		return
	}

	// FIXED: Handle status based on whether we actually waited
	if shouldWait {
		if statusRaw, found, _ := unstructured.NestedMap(currentObj.Object, "status"); found && len(statusRaw) > 0 {
			statusValue, err := common.ConvertToAttrValue(ctx, statusRaw)
			if err != nil {
				tflog.Warn(ctx, "Failed to convert status", map[string]interface{}{"error": err.Error()})
				data.Status = types.DynamicNull()
			} else {
				data.Status = types.DynamicValue(statusValue)
			}
		} else {
			// No status from K8s but we waited - set empty map
			emptyStatus := map[string]interface{}{}
			statusValue, _ := common.ConvertToAttrValue(ctx, emptyStatus)
			data.Status = types.DynamicValue(statusValue)
		}
	} else {
		// Not waiting - status should be null
		data.Status = types.DynamicNull()
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

	// Key logic: preserve status if it was already set, otherwise check if it should be set
	if !data.Status.IsNull() {
		// Status was previously populated, keep it updated
		if statusRaw, found, _ := unstructured.NestedMap(currentObj.Object, "status"); found && len(statusRaw) > 0 {
			statusValue, err := common.ConvertToAttrValue(ctx, statusRaw)
			if err != nil {
				tflog.Warn(ctx, "Failed to convert status", map[string]interface{}{"error": err.Error()})
				data.Status = types.DynamicNull()
			} else {
				data.Status = types.DynamicValue(statusValue)
			}
		} else {
			// Status was tracked but K8s has no status - keep tracking with empty map
			emptyStatus := map[string]interface{}{}
			statusValue, _ := common.ConvertToAttrValue(ctx, emptyStatus)
			data.Status = types.DynamicValue(statusValue)
		}
	} else {
		// Status was not previously set - keep it null
		// We don't start tracking status in Read, only in Create/Update when waiting occurs
		data.Status = types.DynamicNull()
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

	// Parse YAML
	obj, err := r.parseYAML(plan.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Create K8s client
	client, err := r.clientFactory.GetClient(conn)
	if err != nil {
		resp.Diagnostics.AddError("Connection Failed", fmt.Sprintf("Failed to create Kubernetes client: %s", err))
		return
	}

	// Get GVR for the object
	gvr, err := client.GetGVR(ctx, obj)
	if err != nil {
		resp.Diagnostics.AddError("Resource Discovery Failed",
			fmt.Sprintf("Failed to determine resource type: %s", err))
		return
	}

	// Preserve the ID and ownership from current state
	r.setOwnershipAnnotation(obj, state.ID.ValueString())

	// Determine if we should force conflicts
	forceConflicts := false
	if !plan.ForceConflicts.IsNull() {
		forceConflicts = plan.ForceConflicts.ValueBool()
	}

	// Apply the update
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

	// Determine if we should wait (check actual conditions, not just presence of wait_for)
	shouldWait := false
	var waitConfig waitForModel

	if !plan.WaitFor.IsNull() {
		diags := plan.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
		if !diags.HasError() {
			// Check if ANY actual wait condition is configured
			hasWaitConditions := (!waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != "") ||
				!waitConfig.FieldValue.IsNull() ||
				(!waitConfig.Condition.IsNull() && waitConfig.Condition.ValueString() != "") ||
				(!waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool())

			if hasWaitConditions {
				shouldWait = true
				tflog.Info(ctx, "Executing wait_for conditions", map[string]interface{}{
					"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
				})

				if err := r.waitForResource(ctx, client, gvr, obj, waitConfig); err != nil {
					// Log warning but don't fail the resource update
					resp.Diagnostics.AddWarning(
						"Wait Condition Not Met",
						fmt.Sprintf("Resource updated successfully but wait condition failed: %s\n\n"+
							"The resource has been updated in the cluster. You may need to manually verify its status.",
							err.Error()),
					)
				}
			}
		} else {
			resp.Diagnostics.AddWarning(
				"Invalid wait_for Configuration",
				fmt.Sprintf("Could not parse wait_for configuration: %s", diags.Errors()),
			)
		}
	}

	// Get the current state after apply (and after waiting if configured)
	currentObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read after update", fmt.Sprintf("Failed to read resource after update: %s", err))
		return
	}

	// Handle status based on whether we actually waited and configuration
	if shouldWait {
		// We waited, so populate/update status from current state
		fmt.Printf("Branch: shouldWait=true, updating from currentObj\n")
		if statusRaw, found, _ := unstructured.NestedMap(currentObj.Object, "status"); found && len(statusRaw) > 0 {
			statusValue, err := common.ConvertToAttrValue(ctx, statusRaw)
			if err != nil {
				tflog.Warn(ctx, "Failed to convert status", map[string]interface{}{"error": err.Error()})
				plan.Status = types.DynamicNull()
			} else {
				plan.Status = types.DynamicValue(statusValue)
				tflog.Debug(ctx, "Status populated after waiting", map[string]interface{}{
					"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
				})
			}
		} else {
			// No status from K8s but we waited - set empty map
			emptyStatus := map[string]interface{}{}
			statusValue, _ := common.ConvertToAttrValue(ctx, emptyStatus)
			plan.Status = types.DynamicValue(statusValue)
		}
	} else if !state.Status.IsNull() {
		fmt.Printf("Branch: shouldWait=false, state has status\n")
		// Status existed before and we didn't wait - decide whether to preserve or clear

		// Re-check if wait_for has actual conditions
		var hasWaitConditions bool
		if !plan.WaitFor.IsNull() {
			var waitConfig waitForModel
			diags := plan.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
			if !diags.HasError() {
				hasWaitConditions = (!waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != "") ||
					!waitConfig.FieldValue.IsNull() ||
					(!waitConfig.Condition.IsNull() && waitConfig.Condition.ValueString() != "") ||
					(!waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool())
			}
		}

		fmt.Printf("hasWaitConditions: %v\n", hasWaitConditions)

		if hasWaitConditions {
			fmt.Printf("PRESERVING status from state\n")
			// wait_for has actual conditions - PRESERVE the existing status
			plan.Status = state.Status
			tflog.Info(ctx, "Preserving existing status - wait_for still configured with conditions", map[string]interface{}{
				"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
			})
		} else {
			fmt.Printf("CLEARING status (no wait conditions)\n")
			// wait_for was removed or has no conditions - clear the status
			plan.Status = types.DynamicNull()
			tflog.Info(ctx, "Clearing status - wait_for removed or has no conditions", map[string]interface{}{
				"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
			})
		}
	} else {
		// No previous status and no waiting - keep status null
		fmt.Printf("Branch: no previous status, keeping null\n")
		plan.Status = types.DynamicNull()
	}

	fmt.Printf("=== FINAL: plan.Status.IsNull=%v ===\n", plan.Status.IsNull())

	// Add field ownership tracking
	ownership := extractFieldOwnership(currentObj)
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
