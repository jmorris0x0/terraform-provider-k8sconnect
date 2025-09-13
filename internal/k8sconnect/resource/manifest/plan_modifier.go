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

		// This is an UPDATE operation
		fmt.Printf("\n=== ModifyPlan STATUS DECISION (UPDATE) ===\n")
		if !resp.Diagnostics.HasError() {
			// Handle status field planning based on wait_for configuration
			if !req.State.Raw.IsNull() {
				// UPDATE operation
				var stateData manifestResourceModel
				diags := req.State.Get(ctx, &stateData)
				resp.Diagnostics.Append(diags...)

				if !resp.Diagnostics.HasError() {
					if !stateData.Status.IsNull() {
						fmt.Printf("State has existing status\n")
						// We have existing status
						if plannedData.WaitFor.IsNull() {
							// wait_for was removed - clear status
							plannedData.Status = types.DynamicNull()
							fmt.Printf("Setting status to: DynamicNull (wait_for removed)\n")
							tflog.Debug(ctx, "UPDATE: wait_for removed, status will be cleared")
						} else {
							// Parse wait config to check type
							var waitConfig waitForModel
							diags := plannedData.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})

							if diags.HasError() {
								// Can't parse wait_for, clear status to be safe
								plannedData.Status = types.DynamicNull()
								fmt.Printf("Setting status to: DynamicNull (cannot parse wait_for)\n")
								tflog.Debug(ctx, "UPDATE: Cannot parse wait_for, clearing status")
							} else {
								// Only field waits maintain status
								isFieldWait := !waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != ""
								fmt.Printf("wait_for.field = '%v', isFieldWait = %v\n", waitConfig.Field.ValueString(), isFieldWait)

								if isFieldWait {
									// Preserve status - will be updated after wait
									plannedData.Status = stateData.Status
									fmt.Printf("Setting status to: preserved from state\n")
									tflog.Debug(ctx, "UPDATE: field wait configured, preserving status for update")
								} else {
									// Non-field wait - clear status
									plannedData.Status = types.DynamicNull()
									fmt.Printf("Setting status to: DynamicNull (non-field wait)\n")
									tflog.Debug(ctx, "UPDATE: non-field wait, status will be cleared")
								}
							}
						}
					} else {
						fmt.Printf("State has no existing status\n")
						// No existing status
						if !plannedData.WaitFor.IsNull() && !stateData.WaitFor.IsNull() {
							// Both have wait_for - compare them
							var planWait, stateWait waitForModel
							planDiags := plannedData.WaitFor.As(ctx, &planWait, basetypes.ObjectAsOptions{})
							stateDiags := stateData.WaitFor.As(ctx, &stateWait, basetypes.ObjectAsOptions{})

							if !planDiags.HasError() && !stateDiags.HasError() {
								planIsFieldWait := !planWait.Field.IsNull() && planWait.Field.ValueString() != ""
								fmt.Printf("wait_for.field = '%v', isFieldWait = %v\n", planWait.Field.ValueString(), planIsFieldWait)

								if planIsFieldWait {
									// Check if the field path changed
									fmt.Printf("Comparing fields - plan: '%v', state: '%v'\n",
										planWait.Field.ValueString(), stateWait.Field.ValueString())

									if planWait.Field.ValueString() != stateWait.Field.ValueString() {
										// Field path changed - try to populate status
										plannedData.Status = types.DynamicUnknown()
										fmt.Printf("Setting status to: DynamicUnknown (field changed)\n")
										tflog.Debug(ctx, "UPDATE: wait_for.field changed, will try to populate status",
											map[string]interface{}{
												"old_field": stateWait.Field.ValueString(),
												"new_field": planWait.Field.ValueString(),
											})
									} else {
										// Same field path - we already tried, keep null
										plannedData.Status = types.DynamicNull()
										fmt.Printf("Setting status to: DynamicNull (field unchanged, already tried)\n")
										tflog.Debug(ctx, "UPDATE: wait_for.field unchanged, keeping status null",
											map[string]interface{}{
												"field": planWait.Field.ValueString(),
											})
									}
								} else {
									// Non-field wait
									plannedData.Status = types.DynamicNull()
									fmt.Printf("Setting status to: DynamicNull (non-field wait)\n")
									tflog.Debug(ctx, "UPDATE: non-field wait, status stays null")
								}
							} else {
								// Parse error - keep null
								plannedData.Status = types.DynamicNull()
								fmt.Printf("Setting status to: DynamicNull (parse error)\n")
								tflog.Debug(ctx, "UPDATE: Cannot parse wait_for, keeping status null")
							}
						} else if !plannedData.WaitFor.IsNull() && stateData.WaitFor.IsNull() {
							// wait_for added for first time
							fmt.Printf("wait_for added (was null in state)\n")
							var waitConfig waitForModel
							diags := plannedData.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})

							if !diags.HasError() {
								isFieldWait := !waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != ""
								fmt.Printf("wait_for.field = '%v', isFieldWait = %v\n", waitConfig.Field.ValueString(), isFieldWait)
								if isFieldWait {
									// New field wait - try to populate
									plannedData.Status = types.DynamicUnknown()
									fmt.Printf("Setting status to: DynamicUnknown (new field wait)\n")
									tflog.Debug(ctx, "UPDATE: wait_for.field added, will populate status")
								} else {
									// Non-field wait
									plannedData.Status = types.DynamicNull()
									fmt.Printf("Setting status to: DynamicNull (non-field wait)\n")
									tflog.Debug(ctx, "UPDATE: non-field wait added, no status")
								}
							} else {
								plannedData.Status = types.DynamicNull()
								fmt.Printf("Setting status to: DynamicNull (parse error)\n")
							}
						} else {
							// No wait_for at all or wait_for removed
							plannedData.Status = types.DynamicNull()
							fmt.Printf("Setting status to: DynamicNull (no wait_for or removed)\n")
							tflog.Debug(ctx, "UPDATE: No wait_for configured, status will be null")
						}
					}
				}
			} else {
				// CREATE operation
				fmt.Printf("\n=== ModifyPlan STATUS DECISION (CREATE) ===\n")
				if !plannedData.WaitFor.IsNull() {
					var waitConfig waitForModel
					diags := plannedData.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})

					if !diags.HasError() {
						// Check if this is a field wait
						isFieldWait := !waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != ""
						fmt.Printf("wait_for.field = '%v', isFieldWait = %v\n", waitConfig.Field.ValueString(), isFieldWait)

						if isFieldWait {
							// Only field waits populate status
							plannedData.Status = types.DynamicUnknown()
							fmt.Printf("Setting status to: DynamicUnknown (field wait will populate)\n")
							tflog.Debug(ctx, "CREATE: field wait will populate status")
						} else {
							// All other wait types don't populate status
							plannedData.Status = types.DynamicNull()
							fmt.Printf("Setting status to: DynamicNull (non-field wait)\n")
							tflog.Debug(ctx, "CREATE: non-field wait, no status")
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
			}
		}
	} else {
		// This is a CREATE operation
		fmt.Printf("\n=== ModifyPlan STATUS DECISION (CREATE - alternate path) ===\n")
		if !plannedData.WaitFor.IsNull() {
			var waitConfig waitForModel
			diags := plannedData.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})

			if diags.HasError() {
				plannedData.Status = types.DynamicUnknown()
				fmt.Printf("Setting status to: DynamicUnknown (parse error)\n")
				tflog.Debug(ctx, "CREATE: Cannot parse wait_for, marking status as unknown")
			} else {
				// Check if ANY actual wait condition is configured
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
			}
		} else {
			plannedData.Status = types.DynamicNull()
			fmt.Printf("Setting status to: DynamicNull (no wait_for)\n")
			tflog.Debug(ctx, "CREATE: No wait_for configured, status will be null")
		}
	}

	// Final plan set
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
	// Group conflicts by field manager
	conflictsByManager := make(map[string][]FieldConflict)
	for _, c := range conflicts {
		conflictsByManager[c.Owner] = append(conflictsByManager[c.Owner], c)
	}

	var details []string
	for manager, managerConflicts := range conflictsByManager {
		details = append(details, fmt.Sprintf("Managed by '%s':", manager))
		for _, c := range managerConflicts {
			details = append(details, fmt.Sprintf("  - %s: %v → %v",
				c.Path, formatValue(c.CurrentValue), formatValue(c.DesiredValue)))
		}
		details = append(details, "") // Empty line between managers
	}

	message := fmt.Sprintf(
		"The following fields are managed by other controllers:\n\n%s",
		strings.Join(details, "\n"),
	)

	if !forceConflicts.IsNull() && forceConflicts.ValueBool() {
		message += "\nThese fields will be forcibly updated because 'force_conflicts = true'."
		resp.Diagnostics.AddWarning("Field Ownership Conflicts - Will Force Update", message)
	} else {
		message += "\nTo resolve this conflict do one of the following:\n" +
			"1. Remove the conflicting fields from your Terraform configuration\n" +
			"2. Set 'force_conflicts = true' to override (may cause persistent conflicts)\n" +
			"3. Use a different field_manager name to take ownership"
		resp.Diagnostics.AddWarning("Field Ownership Conflicts Detected", message)
	}
}

// formatValue formats a value for display in conflict messages
func formatValue(val interface{}) string {
	if val == nil {
		return "<removed>"
	}
	// Truncate long values
	str := fmt.Sprintf("%v", val)
	if len(str) > 50 {
		return str[:47] + "..."
	}
	return str
}
