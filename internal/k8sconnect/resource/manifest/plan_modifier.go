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
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
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

	// Extract field paths - use experimental field ownership for projection if enabled
	var paths []string
	useFieldOwnership := !plannedData.UseFieldOwnership.IsNull() && plannedData.UseFieldOwnership.ValueBool()
	isCreate := req.State.Raw.IsNull()

	// Enhanced logging
	fmt.Printf("\n=== ModifyPlan PROJECTION LOGIC ===\n")
	fmt.Printf("useFieldOwnership: %v\n", useFieldOwnership)
	fmt.Printf("isCreate: %v\n", isCreate)
	fmt.Printf("State.Raw.IsNull: %v\n", req.State.Raw.IsNull())

	if useFieldOwnership && isCreate {
		// CREATE with field ownership - set projection to Unknown to avoid mismatch
		fmt.Printf("Decision: CREATE with field ownership - setting projection to Unknown\n")
		tflog.Debug(ctx, "CREATE with field ownership - setting projection to unknown")
		plannedData.ManagedStateProjection = types.StringUnknown()
	} else {
		// Either UPDATE with field ownership, or any operation without field ownership
		if useFieldOwnership && !isCreate {
			// UPDATE - try to use field ownership
			fmt.Printf("Decision: UPDATE with field ownership - attempting to get current object\n")
			gvr, err := client.GetGVR(ctx, desiredObj)
			if err == nil {
				currentObj, err := client.Get(ctx, gvr, desiredObj.GetNamespace(), desiredObj.GetName())
				if err == nil {
					fmt.Printf("Got current object with %d managedFields entries\n", len(currentObj.GetManagedFields()))
					tflog.Debug(ctx, "Using field ownership for projection")
					paths = extractOwnedPaths(ctx, currentObj.GetManagedFields(), desiredObj.Object)
					fmt.Printf("extractOwnedPaths returned %d paths\n", len(paths))
				} else {
					fmt.Printf("Could not get current object: %v\n", err)
					tflog.Debug(ctx, "Could not get current object, falling back to YAML paths")
					paths = extractFieldPaths(desiredObj.Object, "")
					fmt.Printf("extractFieldPaths (fallback) returned %d paths\n", len(paths))
				}
			} else {
				fmt.Printf("Could not get GVR: %v\n", err)
				paths = extractFieldPaths(desiredObj.Object, "")
				fmt.Printf("extractFieldPaths (GVR error) returned %d paths\n", len(paths))
			}
		} else {
			// CREATE or feature disabled - use standard extraction
			fmt.Printf("Decision: Standard extraction (CREATE without ownership or feature disabled)\n")
			paths = extractFieldPaths(desiredObj.Object, "")
			fmt.Printf("extractFieldPaths returned %d paths\n", len(paths))
		}

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

		// Log the projection content for debugging
		fmt.Printf("Projection includes nodePort: %v\n", strings.Contains(projectionJSON, "nodePort"))

		// Update the plan with projection
		plannedData.ManagedStateProjection = types.StringValue(projectionJSON)
		tflog.Debug(ctx, "Dry-run projection complete", map[string]interface{}{
			"path_count":      len(paths),
			"projection_size": len(projectionJSON),
		})
	}
	fmt.Printf("=== END ModifyPlan PROJECTION LOGIC ===\n\n")

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
				// (delete_protection, force_conflicts, etc. are not preserved)
			} else {
				// Log what's different
				fmt.Printf("=== PROJECTION MISMATCH ===\n")
				fmt.Printf("State projection includes nodePort: %v\n", strings.Contains(stateData.ManagedStateProjection.ValueString(), "nodePort"))
				fmt.Printf("Plan projection includes nodePort: %v\n", strings.Contains(plannedData.ManagedStateProjection.ValueString(), "nodePort"))
			}
		}
	}

	// Handle status field based on wait_for configuration
	fmt.Printf("\n=== ModifyPlan STATUS DECISION ")
	isCreate = req.State.Raw.IsNull()
	if isCreate {
		fmt.Printf("(CREATE - alternate path) ===\n")
		// For CREATE, check if wait_for has actual wait conditions
		if !plannedData.WaitFor.IsNull() {
			var waitConfig waitForModel
			diags := plannedData.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})

			if !diags.HasError() {
				// Check if wait_for has actual conditions (not just an empty object)
				hasWaitConditions := (!waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != "") ||
					!waitConfig.FieldValue.IsNull() ||
					(!waitConfig.Condition.IsNull() && waitConfig.Condition.ValueString() != "") ||
					(!waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool())

				fmt.Printf("hasWaitConditions = %v\n", hasWaitConditions)
				fmt.Printf("  Field: '%v'\n", waitConfig.Field.ValueString())
				fmt.Printf("  FieldValue IsNull: %v\n", waitConfig.FieldValue.IsNull())
				fmt.Printf("  Condition: '%v'\n", waitConfig.Condition.ValueString())
				fmt.Printf("  Rollout: %v\n", waitConfig.Rollout.ValueBool())

				if hasWaitConditions {
					plannedData.Status = types.DynamicUnknown()
					fmt.Printf("Setting status to: DynamicUnknown (has wait conditions)\n")
					tflog.Debug(ctx, "CREATE: wait_for has actual conditions, marking status as unknown")
				} else {
					plannedData.Status = types.DynamicNull()
					fmt.Printf("Setting status to: DynamicNull (no actual conditions)\n")
					tflog.Debug(ctx, "CREATE: wait_for has no actual conditions, status will be null")
				}
			} else {
				plannedData.Status = types.DynamicNull()
				fmt.Printf("Setting status to: DynamicNull (parse error)\n")
				tflog.Debug(ctx, "CREATE: Cannot parse wait_for, no status")
			}
		} else {
			plannedData.Status = types.DynamicNull()
			fmt.Printf("Setting status to: DynamicNull (no wait_for)\n")
			tflog.Debug(ctx, "CREATE: No wait_for configured, status will be null")
		}
	} else {
		// This is an UPDATE operation
		fmt.Printf("(UPDATE) ===\n")
		var stateData manifestResourceModel
		diags := req.State.Get(ctx, &stateData)
		resp.Diagnostics.Append(diags...)

		if !resp.Diagnostics.HasError() {
			if !stateData.Status.IsNull() {
				fmt.Printf("State has existing status\n")
			} else {
				fmt.Printf("State has no existing status\n")
			}

			if plannedData.WaitFor.IsNull() {
				// wait_for was removed - clear status
				plannedData.Status = types.DynamicNull()
				fmt.Printf("Setting status to: DynamicNull (no wait_for or removed)\n")
				tflog.Debug(ctx, "UPDATE: wait_for removed or not configured, status will be null")
			} else {
				// Parse wait config to check type
				var waitConfig waitForModel
				diags := plannedData.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})

				if !diags.HasError() {
					// Check if this is a field wait
					isFieldWait := !waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != ""
					fmt.Printf("wait_for.field = '%v', isFieldWait = %v\n", waitConfig.Field.ValueString(), isFieldWait)

					// Compare with state's wait_for
					if !stateData.WaitFor.IsNull() {
						var stateWaitConfig waitForModel
						stateDiags := stateData.WaitFor.As(ctx, &stateWaitConfig, basetypes.ObjectAsOptions{})
						if !stateDiags.HasError() {
							stateFieldWait := !stateWaitConfig.Field.IsNull() && stateWaitConfig.Field.ValueString() != ""
							fmt.Printf("Comparing fields - plan: '%v', state: '%v'\n",
								waitConfig.Field.ValueString(), stateWaitConfig.Field.ValueString())

							if isFieldWait && stateFieldWait && waitConfig.Field.Equal(stateWaitConfig.Field) {
								// Same field wait - check if we already tried it
								if !stateData.Status.IsNull() {
									// We have status from before - keep it
									plannedData.Status = stateData.Status
									fmt.Printf("Setting status to: preserved from state (field unchanged)\n")
								} else {
									// Field wait but no status yet - null
									plannedData.Status = types.DynamicNull()
									fmt.Printf("Setting status to: DynamicNull (field unchanged, already tried)\n")
								}
							} else if isFieldWait {
								// Different field wait or new field wait
								plannedData.Status = types.DynamicUnknown()
								fmt.Printf("Setting status to: DynamicUnknown (field changed or new)\n")
							} else {
								// Not a field wait
								plannedData.Status = types.DynamicNull()
								fmt.Printf("Setting status to: DynamicNull (not a field wait)\n")
							}
						} else {
							// Can't parse state wait_for
							if isFieldWait {
								plannedData.Status = types.DynamicUnknown()
								fmt.Printf("Setting status to: DynamicUnknown (new field wait)\n")
							} else {
								plannedData.Status = types.DynamicNull()
								fmt.Printf("Setting status to: DynamicNull (non-field wait)\n")
							}
						}
					} else {
						// wait_for added for first time
						if isFieldWait {
							plannedData.Status = types.DynamicUnknown()
							fmt.Printf("Setting status to: DynamicUnknown (new field wait)\n")
						} else {
							plannedData.Status = types.DynamicNull()
							fmt.Printf("Setting status to: DynamicNull (non-field wait)\n")
						}
					}
				} else {
					// Can't parse wait_for
					plannedData.Status = types.DynamicNull()
					fmt.Printf("Setting status to: DynamicNull (parse error)\n")
					tflog.Debug(ctx, "UPDATE: Cannot parse wait_for, clearing status")
				}
			}
		}
	}

	fmt.Printf("\n=== ModifyPlan FINAL STATUS ===\n")
	fmt.Printf("Status IsNull: %v, IsUnknown: %v\n", plannedData.Status.IsNull(), plannedData.Status.IsUnknown())

	diags = resp.Plan.Set(ctx, &plannedData)
	resp.Diagnostics.Append(diags...)

	// Verify what was actually set
	var finalPlan manifestResourceModel
	resp.Plan.Get(ctx, &finalPlan)
	fmt.Printf("After Plan.Set - Status IsNull: %v, IsUnknown: %v\n", finalPlan.Status.IsNull(), finalPlan.Status.IsUnknown())
	fmt.Printf("=== END ModifyPlan ===\n\n")
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
					// For now, treat array changes as single change
					if !reflect.DeepEqual(currentSlice, desiredSlice) {
						changes = append(changes, FieldChange{
							Path:         path,
							CurrentValue: currentSlice,
							DesiredValue: desiredSlice,
						})
					}
					continue
				}
			}

			// Value changed
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
			// Field is being removed
			changes = append(changes, FieldChange{
				Path:         path,
				CurrentValue: currentVal,
				DesiredValue: nil,
			})
		}
	}

	return changes
}

// addConflictWarning adds a warning about field ownership conflicts
func addConflictWarning(resp *resource.ModifyPlanResponse, conflicts []FieldConflict, forceConflicts types.Bool) {
	if forceConflicts.ValueBool() {
		// Just warn when forcing
		var conflictDetails []string
		for _, c := range conflicts {
			conflictDetails = append(conflictDetails, fmt.Sprintf("  - %s (owned by %s)", c.Path, c.Owner))
		}
		resp.Diagnostics.AddWarning(
			"Field Ownership Override",
			fmt.Sprintf("Forcing ownership of fields managed by other controllers:\n%s\n\n"+
				"These fields will be forcibly taken over. The other controllers may fight back.",
				strings.Join(conflictDetails, "\n")),
		)
	} else {
		// Error when not forcing
		var conflictDetails []string
		for _, c := range conflicts {
			conflictDetails = append(conflictDetails, fmt.Sprintf("  - %s (owned by %s)", c.Path, c.Owner))
		}
		resp.Diagnostics.AddError(
			"Field Ownership Conflict",
			fmt.Sprintf("Cannot modify fields owned by other controllers:\n%s\n\n"+
				"To force ownership, set force_conflicts = true",
				strings.Join(conflictDetails, "\n")),
		)
	}
}
