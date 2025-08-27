// internal/k8sconnect/resource/manifest/plan_modifier.go
package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/k8sclient"
)

// ModifyPlan implements resource.ResourceWithModifyPlan
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

	// If connection is not ready (unknown values), skip dry-run
	if !r.isConnectionReady(plannedData.ClusterConnection) {
		tflog.Debug(ctx, "Skipping dry-run due to unknown connection values")
		plannedData.ManagedStateProjection = types.StringUnknown()
		diags = resp.Plan.Set(ctx, &plannedData)
		resp.Diagnostics.Append(diags...)
		return
	}

	// Parse the desired YAML
	desiredObj, err := r.parseYAML(plannedData.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Convert connection
	conn, err := r.convertObjectToConnectionModel(ctx, plannedData.ClusterConnection)
	if err != nil {
		tflog.Debug(ctx, "Skipping dry-run due to connection conversion error", map[string]interface{}{
			"error": err.Error(),
		})
		plannedData.ManagedStateProjection = types.StringUnknown()
		diags = resp.Plan.Set(ctx, &plannedData)
		resp.Diagnostics.Append(diags...)
		return
	}

	// Create client
	client, err := r.clientGetter(conn)
	if err != nil {
		tflog.Debug(ctx, "Skipping dry-run due to client creation error", map[string]interface{}{
			"error": err.Error(),
		})
		plannedData.ManagedStateProjection = types.StringUnknown()
		diags = resp.Plan.Set(ctx, &plannedData)
		resp.Diagnostics.Append(diags...)
		return
	}

	// Perform dry-run
	dryRunResult, err := client.DryRunApply(ctx, desiredObj, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        true,
	})
	if err != nil {
		tflog.Debug(ctx, "Dry-run failed", map[string]interface{}{
			"error": err.Error(),
		})
		// Don't fail the plan, just skip projection
		plannedData.ManagedStateProjection = types.StringUnknown()
		diags = resp.Plan.Set(ctx, &plannedData)
		resp.Diagnostics.Append(diags...)
		return
	}

	// Extract field paths from desired state
	paths := extractFieldPaths(desiredObj.Object, "")

	// Project the dry-run result
	projection, err := projectFields(dryRunResult.Object, paths)
	if err != nil {
		resp.Diagnostics.AddError("Projection Failed", fmt.Sprintf("Failed to project fields: %s", err))
		return
	}

	// Convert to JSON
	projectionJSON, err := toJSON(projection)
	if err != nil {
		resp.Diagnostics.AddError("JSON Conversion Failed", fmt.Sprintf("Failed to convert projection: %s", err))
		return
	}

	// Update the plan with projection
	plannedData.ManagedStateProjection = types.StringValue(projectionJSON)

	tflog.Debug(ctx, "Dry-run projection complete", map[string]interface{}{
		"path_count":      len(paths),
		"projection_size": len(projectionJSON),
	})

	// Check if we have state to compare against
	if !req.State.Raw.IsNull() {
		var stateData manifestResourceModel
		diags := req.State.Get(ctx, &stateData)
		resp.Diagnostics.Append(diags...)

		if !resp.Diagnostics.HasError() && !stateData.ManagedStateProjection.IsNull() {
			// If projections match, only YAML formatting changed in Kubernetes
			if stateData.ManagedStateProjection.Equal(plannedData.ManagedStateProjection) {
				tflog.Debug(ctx, "No Kubernetes resource changes detected, preserving YAML")

				// Preserve the original YAML and internal fields since no actual changes will occur
				plannedData.YAMLBody = stateData.YAMLBody
				plannedData.ManagedStateProjection = stateData.ManagedStateProjection
				plannedData.FieldOwnership = stateData.FieldOwnership
				plannedData.ImportedWithoutAnnotations = stateData.ImportedWithoutAnnotations

				// But still allow terraform-specific settings to update
				// (delete_protection, force_conflicts, etc. keep their planned values)
			} else {
				// Only check conflicts if there are actual Kubernetes changes
				r.checkFieldOwnershipConflicts(ctx, req, resp)
			}
		}
	}

	// Final plan set
	diags = resp.Plan.Set(ctx, &plannedData)
	resp.Diagnostics.Append(diags...)
}

// checkFieldOwnershipConflicts detects when fields managed by other controllers are being changed
func (r *manifestResource) checkFieldOwnershipConflicts(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Get state and plan projections
	var stateData, planData manifestResourceModel

	diags := req.State.Get(ctx, &stateData)
	resp.Diagnostics.Append(diags...)
	diags = req.Plan.Get(ctx, &planData)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Skip if projections are not available
	if stateData.ManagedStateProjection.IsNull() || planData.ManagedStateProjection.IsNull() {
		return
	}

	// Skip if projections are the same
	if stateData.ManagedStateProjection.Equal(planData.ManagedStateProjection) {
		return
	}

	// Get field ownership from state
	if stateData.FieldOwnership.IsNull() {
		return
	}

	var ownership map[string]FieldOwnership
	if err := json.Unmarshal([]byte(stateData.FieldOwnership.ValueString()), &ownership); err != nil {
		tflog.Warn(ctx, "Failed to unmarshal field ownership", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	// Parse projections
	var stateProj, planProj map[string]interface{}
	if err := json.Unmarshal([]byte(stateData.ManagedStateProjection.ValueString()), &stateProj); err != nil {
		return
	}
	if err := json.Unmarshal([]byte(planData.ManagedStateProjection.ValueString()), &planProj); err != nil {
		return
	}

	// Find changed fields
	changes := findChangedFields(stateProj, planProj, "")
	if len(changes) == 0 {
		return
	}

	// Check ownership for each change
	var conflicts []FieldConflict
	for _, change := range changes {
		if owner, exists := ownership[change.Path]; exists && owner.Manager != "k8sconnect" {
			conflicts = append(conflicts, FieldConflict{
				Path:         change.Path,
				CurrentValue: change.CurrentValue,
				DesiredValue: change.DesiredValue,
				Owner:        owner.Manager,
			})
		}
	}

	if len(conflicts) > 0 {
		addConflictWarning(resp, conflicts, planData.ForceConflicts)
	}
}

// FieldConflict represents a field that is changing but owned by another controller
type FieldConflict struct {
	Path         string
	CurrentValue interface{}
	DesiredValue interface{}
	Owner        string
}

// FieldChange represents any field that is changing
type FieldChange struct {
	Path         string
	CurrentValue interface{}
	DesiredValue interface{}
}

// findChangedFields recursively finds all fields that differ between current and desired state
func findChangedFields(current, desired map[string]interface{}, prefix string) []FieldChange {
	var changes []FieldChange

	// Check all desired fields
	for key, desiredVal := range desired {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		currentVal, exists := current[key]
		if !exists {
			// Field is being added
			changes = append(changes, FieldChange{
				Path:         path,
				CurrentValue: nil,
				DesiredValue: desiredVal,
			})
			continue
		}

		// Check if values differ
		if !reflect.DeepEqual(currentVal, desiredVal) {
			// Check if both are maps - recurse
			if currentMap, ok := currentVal.(map[string]interface{}); ok {
				if desiredMap, ok := desiredVal.(map[string]interface{}); ok {
					// Recurse into nested objects
					nestedChanges := findChangedFields(currentMap, desiredMap, path)
					changes = append(changes, nestedChanges...)
					continue
				}
			}

			// Check if both are slices - compare as whole
			if currentSlice, ok := currentVal.([]interface{}); ok {
				if desiredSlice, ok := desiredVal.([]interface{}); ok {
					if !reflect.DeepEqual(currentSlice, desiredSlice) {
						changes = append(changes, FieldChange{
							Path:         path,
							CurrentValue: currentVal,
							DesiredValue: desiredVal,
						})
					}
					continue
				}
			}

			// Values are different
			changes = append(changes, FieldChange{
				Path:         path,
				CurrentValue: currentVal,
				DesiredValue: desiredVal,
			})
		}
	}

	// Check for removed fields
	for key, currentVal := range current {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		if _, exists := desired[key]; !exists {
			changes = append(changes, FieldChange{
				Path:         path,
				CurrentValue: currentVal,
				DesiredValue: nil,
			})
		}
	}

	return changes
}

// addConflictWarning adds a warning diagnostic about field ownership conflicts
func addConflictWarning(resp *resource.ModifyPlanResponse, conflicts []FieldConflict, forceConflicts types.Bool) {
	var details []string
	for _, c := range conflicts {
		details = append(details, fmt.Sprintf(
			"  ~ %s: %v -> %v (managed by '%s')",
			c.Path, formatValue(c.CurrentValue), formatValue(c.DesiredValue), c.Owner,
		))
	}

	message := fmt.Sprintf(
		"The following fields will be changed but are currently managed by other controllers:\n\n%s\n\n",
		strings.Join(details, "\n"),
	)

	if !forceConflicts.IsNull() && forceConflicts.ValueBool() {
		message += "These fields will be forcibly updated because 'force_conflicts = true'."
	} else {
		message += "These fields may not be updated unless 'force_conflicts = true' is set.\n" +
			"The update might be rejected or immediately reverted by the other controller."
	}

	resp.Diagnostics.AddWarning("Field Ownership Conflicts Detected", message)
}

// formatValue formats a value for display in conflict messages
func formatValue(v interface{}) string {
	if v == nil {
		return "<removed>"
	}
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case []interface{}:
		return fmt.Sprintf("[%d items]", len(val))
	case map[string]interface{}:
		return fmt.Sprintf("{%d fields}", len(val))
	default:
		return fmt.Sprint(v)
	}
}
