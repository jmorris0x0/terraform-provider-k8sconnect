// internal/k8sconnect/resource/manifest/plan_modifier.go
package manifest

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

	// Validate connection is ready for operations
	if !r.validateConnectionReady(ctx, &plannedData, resp) {
		return
	}

	// Parse the desired YAML
	desiredObj, err := r.parseYAML(plannedData.YAMLBody.ValueString())
	if err != nil {
		// Check if this might be due to unresolved interpolations
		yamlStr := plannedData.YAMLBody.ValueString()
		if strings.Contains(yamlStr, "${") {
			fmt.Printf("DEBUG ModifyPlan: YAML parsing failed with interpolation syntax present, deferring to apply: %v\n", err)
			// During plan with interpolations to computed values, we can't parse/validate
			// Mark computed fields as unknown
			plannedData.ManagedStateProjection = types.StringUnknown()
			plannedData.FieldOwnership = types.StringUnknown()

			// Handle status based on wait_for
			if !plannedData.WaitFor.IsNull() {
				plannedData.Status = types.DynamicUnknown()
			} else {
				plannedData.Status = types.DynamicNull()
			}

			// Save the plan with unknown computed fields
			diags = resp.Plan.Set(ctx, &plannedData)
			resp.Diagnostics.Append(diags...)
			return
		}

		// This is a real YAML parsing error
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Execute dry-run and compute projection
	if !r.executeDryRunAndProjection(ctx, req, &plannedData, desiredObj, resp) {
		return
	}

	// Check drift and preserve state if needed
	r.checkDriftAndPreserveState(ctx, req, &plannedData, resp)

	// Determine status field behavior based on wait_for
	r.determineStatusField(ctx, req, &plannedData, resp)

	// Debug: Check projection before saving
	fmt.Printf("BEFORE Plan.Set - plan projection hash: %x\n",
		md5.Sum([]byte(plannedData.ManagedStateProjection.ValueString())))

	// Save the modified plan
	diags = resp.Plan.Set(ctx, &plannedData)
	resp.Diagnostics.Append(diags...)

	// Debug: Check projection after saving
	var checkPlan manifestResourceModel
	resp.Plan.Get(ctx, &checkPlan)
	fmt.Printf("AFTER Plan.Set - plan projection hash: %x\n",
		md5.Sum([]byte(checkPlan.ManagedStateProjection.ValueString())))

	// Verify what was actually set
	var finalPlan manifestResourceModel
	resp.Plan.Get(ctx, &finalPlan)
	fmt.Printf("After Plan.Set - Status IsNull: %v, IsUnknown: %v\n", finalPlan.Status.IsNull(), finalPlan.Status.IsUnknown())

	// Check field ownership conflicts for updates
	if !req.State.Raw.IsNull() {
		r.checkFieldOwnershipConflicts(ctx, req, resp)
	}

	fmt.Printf("=== END ModifyPlan ===\n\n")
}

// setProjectionUnknown sets projection to unknown and saves plan
func (r *manifestResource) setProjectionUnknown(ctx context.Context, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse, reason string) {
	tflog.Debug(ctx, reason)
	plannedData.ManagedStateProjection = types.StringUnknown()
	diags := resp.Plan.Set(ctx, plannedData)
	resp.Diagnostics.Append(diags...)
}

// isCreateOperation checks if this is a create vs update
func isCreateOperation(req resource.ModifyPlanRequest) bool {
	return req.State.Raw.IsNull()
}

// isFieldWait checks if wait config is for a field wait
func isFieldWait(waitConfig waitForModel) bool {
	return !waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != ""
}

// hasActiveWaitConditions checks if wait config has any active conditions
func hasActiveWaitConditions(waitConfig waitForModel) bool {
	return (!waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != "") ||
		!waitConfig.FieldValue.IsNull() ||
		(!waitConfig.Condition.IsNull() && waitConfig.Condition.ValueString() != "") ||
		(!waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool())
}

// parseWaitConfig safely parses wait_for configuration
func parseWaitConfig(ctx context.Context, waitFor types.Object) (waitForModel, bool) {
	var config waitForModel
	if waitFor.IsNull() {
		return config, false
	}
	diags := waitFor.As(ctx, &config, basetypes.ObjectAsOptions{})
	return config, !diags.HasError()
}

// validateConnectionReady checks if connection values are ready for use
func (r *manifestResource) validateConnectionReady(ctx context.Context, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) bool {
	// If connection is not ready (unknown values), skip dry-run
	if !r.isConnectionReady(plannedData.ClusterConnection) {
		tflog.Debug(ctx, "Skipping dry-run due to unknown connection values")
		plannedData.ManagedStateProjection = types.StringUnknown()
		diags := resp.Plan.Set(ctx, plannedData)
		resp.Diagnostics.Append(diags...)
		return false
	}
	return true
}

// checkDriftAndPreserveState compares projections and preserves state if no changes
func (r *manifestResource) checkDriftAndPreserveState(ctx context.Context, req resource.ModifyPlanRequest, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) {
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
				fmt.Printf("State projection: %s\n", stateData.ManagedStateProjection.ValueString())
				fmt.Printf("Plan projection: %s\n", plannedData.ManagedStateProjection.ValueString())
			}

			// ADD THIS LOGGING HERE (still inside the if block where stateData exists)
			fmt.Printf("=== checkDriftAndPreserveState END ===\n")
			fmt.Printf("Final plan projection hash: %x\n",
				md5.Sum([]byte(plannedData.ManagedStateProjection.ValueString())))
			fmt.Printf("Are projections equal at end? %v\n",
				stateData.ManagedStateProjection.Equal(plannedData.ManagedStateProjection))
		}
	}
}

// executeDryRunAndProjection performs dry-run and calculates field projection
func (r *manifestResource) executeDryRunAndProjection(ctx context.Context, req resource.ModifyPlanRequest, plannedData *manifestResourceModel, desiredObj *unstructured.Unstructured, resp *resource.ModifyPlanResponse) bool {
	// Setup client
	client, err := r.setupDryRunClient(ctx, plannedData, resp)
	if err != nil {
		return false
	}

	// Perform dry-run
	dryRunResult, err := r.performDryRun(ctx, client, desiredObj, plannedData, resp)
	if err != nil {
		return false
	}

	// Calculate and apply projection
	return r.calculateProjection(ctx, req, plannedData, desiredObj, dryRunResult, client, resp)
}

// setupDryRunClient creates the k8s client for dry-run
func (r *manifestResource) setupDryRunClient(ctx context.Context, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) (k8sclient.K8sClient, error) {
	// Convert connection
	conn, err := r.convertObjectToConnectionModel(ctx, plannedData.ClusterConnection)
	if err != nil {
		r.setProjectionUnknown(ctx, plannedData, resp,
			fmt.Sprintf("Skipping dry-run due to connection conversion error: %s", err))
		return nil, err
	}

	// Create client
	client, err := r.clientGetter(conn)
	if err != nil {
		r.setProjectionUnknown(ctx, plannedData, resp,
			fmt.Sprintf("Skipping dry-run due to client creation error: %s", err))
		return nil, err
	}

	return client, nil
}

// performDryRun executes the dry-run against k8s
func (r *manifestResource) performDryRun(ctx context.Context, client k8sclient.K8sClient, desiredObj *unstructured.Unstructured, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) (*unstructured.Unstructured, error) {
	dryRunResult, err := client.DryRunApply(ctx, desiredObj, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        true,
	})
	if err != nil {
		r.setProjectionUnknown(ctx, plannedData, resp,
			fmt.Sprintf("Dry-run failed: %s", err))
		return nil, err
	}
	return dryRunResult, nil
}

// calculateProjection determines projection strategy and calculates projection
func (r *manifestResource) calculateProjection(ctx context.Context, req resource.ModifyPlanRequest, plannedData *manifestResourceModel, desiredObj, dryRunResult *unstructured.Unstructured, client k8sclient.K8sClient, resp *resource.ModifyPlanResponse) bool {
	useFieldOwnership := !plannedData.UseFieldOwnership.IsNull() && plannedData.UseFieldOwnership.ValueBool()
	isCreate := isCreateOperation(req)

	// Enhanced logging
	fmt.Printf("\n=== ModifyPlan PROJECTION LOGIC ===\n")
	fmt.Printf("useFieldOwnership: %v\n", useFieldOwnership)
	fmt.Printf("isCreate: %v\n", isCreate)
	fmt.Printf("State.Raw.IsNull: %v\n", req.State.Raw.IsNull())

	// Determine projection strategy
	paths := r.determineProjectionPaths(ctx, plannedData, desiredObj, client, useFieldOwnership, isCreate)

	// Special case: CREATE with field ownership
	if useFieldOwnership && isCreate && len(paths) == 0 {
		fmt.Printf("Decision: CREATE with field ownership - setting projection to Unknown\n")
		tflog.Debug(ctx, "CREATE with field ownership - setting projection to unknown")
		plannedData.ManagedStateProjection = types.StringUnknown()
		fmt.Printf("=== END ModifyPlan PROJECTION LOGIC ===\n\n")
		return true
	}

	// Apply projection
	return r.applyProjection(ctx, dryRunResult, paths, plannedData, resp)
}

// determineProjectionPaths decides which paths to project based on strategy
func (r *manifestResource) determineProjectionPaths(ctx context.Context, plannedData *manifestResourceModel, desiredObj *unstructured.Unstructured, client k8sclient.K8sClient, useFieldOwnership, isCreate bool) []string {
	// CREATE with field ownership - special case
	if useFieldOwnership && isCreate {
		return nil // Signal to set projection to Unknown
	}

	// UPDATE with field ownership
	if useFieldOwnership && !isCreate {
		fmt.Printf("Decision: UPDATE with field ownership - attempting to get current object\n")
		return r.getFieldOwnershipPaths(ctx, plannedData, desiredObj, client)
	}

	// Standard extraction (CREATE without ownership or feature disabled)
	fmt.Printf("Decision: Standard extraction (CREATE without ownership or feature disabled)\n")
	paths := extractFieldPaths(desiredObj.Object, "")
	fmt.Printf("extractFieldPaths returned %d paths\n", len(paths))
	return paths
}

// getFieldOwnershipPaths gets paths based on field ownership
func (r *manifestResource) getFieldOwnershipPaths(ctx context.Context, plannedData *manifestResourceModel, desiredObj *unstructured.Unstructured, client k8sclient.K8sClient) []string {
	// Check force_conflicts setting FIRST
	forceConflicts := !plannedData.ForceConflicts.IsNull() && plannedData.ForceConflicts.ValueBool()
	fmt.Printf("force_conflicts setting: %v\n", forceConflicts)

	if forceConflicts {
		// When force_conflicts is true, use ALL fields from YAML
		fmt.Printf("force_conflicts=true: Using ALL fields from YAML (not checking ownership)\n")
		paths := extractFieldPaths(desiredObj.Object, "")
		fmt.Printf("extractFieldPaths returned %d paths (forcing ownership of all)\n", len(paths))
		return paths
	}

	// Try to get current object for ownership info
	gvr, err := client.GetGVR(ctx, desiredObj)
	if err != nil {
		fmt.Printf("Could not get GVR: %v\n", err)
		paths := extractFieldPaths(desiredObj.Object, "")
		fmt.Printf("extractFieldPaths (GVR error) returned %d paths\n", len(paths))
		return paths
	}

	currentObj, err := client.Get(ctx, gvr, desiredObj.GetNamespace(), desiredObj.GetName())
	if err != nil {
		fmt.Printf("Could not get current object: %v\n", err)
		tflog.Debug(ctx, "Could not get current object, falling back to YAML paths")
		paths := extractFieldPaths(desiredObj.Object, "")
		fmt.Printf("extractFieldPaths (fallback) returned %d paths\n", len(paths))
		return paths
	}

	fmt.Printf("Got current object with %d managedFields entries\n", len(currentObj.GetManagedFields()))
	tflog.Debug(ctx, "Using field ownership for projection")
	paths := extractOwnedPaths(ctx, currentObj.GetManagedFields(), desiredObj.Object)
	fmt.Printf("extractOwnedPaths returned %d paths\n", len(paths))
	return paths
}

// applyProjection projects fields and updates plan
func (r *manifestResource) applyProjection(ctx context.Context, dryRunResult *unstructured.Unstructured, paths []string, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) bool {
	// Project the dry-run result
	projection, err := projectFields(dryRunResult.Object, paths)
	if err != nil {
		resp.Diagnostics.AddError("Projection Failed", fmt.Sprintf("Failed to project fields: %s", err))
		return false
	}

	// Convert to JSON
	projectionJSON, err := toJSON(projection)
	if err != nil {
		resp.Diagnostics.AddError("JSON Conversion Failed", fmt.Sprintf("Failed to convert projection: %s", err))
		return false
	}

	// Log the projection content for debugging
	fmt.Printf("Projection includes nodePort: %v\n", strings.Contains(projectionJSON, "nodePort"))

	// Update the plan with projection
	plannedData.ManagedStateProjection = types.StringValue(projectionJSON)
	tflog.Debug(ctx, "Dry-run projection complete", map[string]interface{}{
		"path_count":      len(paths),
		"projection_size": len(projectionJSON),
	})

	fmt.Printf("=== END ModifyPlan PROJECTION LOGIC ===\n\n")
	return true
}

// determineStatusField handles complex status field logic based on wait_for
func (r *manifestResource) determineStatusField(ctx context.Context, req resource.ModifyPlanRequest, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) {
	fmt.Printf("\n=== ModifyPlan STATUS DECISION ")

	if isCreateOperation(req) {
		fmt.Printf("(CREATE - alternate path) ===\n")
		r.determineCreateStatus(ctx, plannedData)
	} else {
		fmt.Printf("(UPDATE) ===\n")
		r.determineUpdateStatus(ctx, req, plannedData, resp)
	}

	fmt.Printf("\n=== ModifyPlan FINAL STATUS ===\n")
	fmt.Printf("Status IsNull: %v, IsUnknown: %v\n", plannedData.Status.IsNull(), plannedData.Status.IsUnknown())
}

// determineCreateStatus handles status for CREATE operations
func (r *manifestResource) determineCreateStatus(ctx context.Context, plannedData *manifestResourceModel) {
	waitConfig, ok := parseWaitConfig(ctx, plannedData.WaitFor)
	if !ok {
		plannedData.Status = types.DynamicNull()
		fmt.Printf("Setting status to: DynamicNull (no wait_for)\n")
		tflog.Debug(ctx, "CREATE: No wait_for configured, status will be null")
		return
	}

	// Check and log wait conditions
	hasConditions := hasActiveWaitConditions(waitConfig)
	r.logWaitConditions(waitConfig, hasConditions)

	if hasConditions && isFieldWait(waitConfig) {
		plannedData.Status = types.DynamicUnknown()
		fmt.Printf("Setting status to: DynamicUnknown (has field wait)\n")
		tflog.Debug(ctx, "CREATE: wait_for.field configured, marking status as unknown")
	} else {
		plannedData.Status = types.DynamicNull()
		if hasConditions {
			fmt.Printf("Setting status to: DynamicNull (no field wait)\n")
			tflog.Debug(ctx, "CREATE: non-field wait type, status will be null")
		} else {
			fmt.Printf("Setting status to: DynamicNull (no actual conditions)\n")
			tflog.Debug(ctx, "CREATE: wait_for has no actual conditions, status will be null")
		}
	}
}

// determineUpdateStatus handles status for UPDATE operations
func (r *manifestResource) determineUpdateStatus(ctx context.Context, req resource.ModifyPlanRequest, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) {
	// Get state data
	var stateData manifestResourceModel
	diags := req.State.Get(ctx, &stateData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Log current state
	if !stateData.Status.IsNull() {
		fmt.Printf("State has existing status\n")
	} else {
		fmt.Printf("State has no existing status\n")
	}

	// Parse configurations
	planWaitConfig, hasPlanWait := parseWaitConfig(ctx, plannedData.WaitFor)
	stateWaitConfig, hasStateWait := parseWaitConfig(ctx, stateData.WaitFor)

	// Determine status based on wait configurations
	status := r.calculateUpdateStatus(
		hasPlanWait, hasStateWait,
		planWaitConfig, stateWaitConfig,
		!stateData.Status.IsNull(),
	)

	// Apply the status decision
	if status.preserve {
		plannedData.Status = stateData.Status
		fmt.Printf("Setting status to: preserved from state (%s)\n", status.reason)
	} else if status.unknown {
		plannedData.Status = types.DynamicUnknown()
		fmt.Printf("Setting status to: DynamicUnknown (%s)\n", status.reason)
	} else {
		plannedData.Status = types.DynamicNull()
		fmt.Printf("Setting status to: DynamicNull (%s)\n", status.reason)
	}

	tflog.Debug(ctx, fmt.Sprintf("UPDATE: %s", status.reason))
}

// statusDecision represents the outcome of status calculation
type statusDecision struct {
	preserve bool
	unknown  bool
	reason   string
}

// calculateUpdateStatus determines what status should be for updates
func (r *manifestResource) calculateUpdateStatus(hasPlanWait, hasStateWait bool, planConfig, stateConfig waitForModel, stateHasStatus bool) statusDecision {
	// No wait_for configured
	if !hasPlanWait {
		return statusDecision{reason: "wait_for removed or not configured"}
	}

	planIsFieldWait := isFieldWait(planConfig)
	fmt.Printf("wait_for.field = '%v', isFieldWait = %v\n", planConfig.Field.ValueString(), planIsFieldWait)

	// Not a field wait
	if !planIsFieldWait {
		return statusDecision{reason: "not a field wait"}
	}

	// New field wait (no previous wait_for)
	if !hasStateWait {
		return statusDecision{unknown: true, reason: "new field wait"}
	}

	// Compare with previous wait_for
	stateIsFieldWait := isFieldWait(stateConfig)
	fmt.Printf("Comparing fields - plan: '%v', state: '%v'\n",
		planConfig.Field.ValueString(), stateConfig.Field.ValueString())

	// Check if field unchanged
	if stateIsFieldWait && planConfig.Field.Equal(stateConfig.Field) {
		if stateHasStatus {
			return statusDecision{preserve: true, reason: "field unchanged"}
		}
		return statusDecision{reason: "field unchanged, already tried"}
	}

	// Field changed or different wait type
	return statusDecision{unknown: true, reason: "field changed or new"}
}

// logWaitConditions logs detailed wait condition info
func (r *manifestResource) logWaitConditions(waitConfig waitForModel, hasConditions bool) {
	fmt.Printf("hasWaitConditions = %v\n", hasConditions)
	fmt.Printf("  Field: '%v'\n", waitConfig.Field.ValueString())
	fmt.Printf("  FieldValue IsNull: %v\n", waitConfig.FieldValue.IsNull())
	fmt.Printf("  Condition: '%v'\n", waitConfig.Condition.ValueString())
	fmt.Printf("  Rollout: %v\n", waitConfig.Rollout.ValueBool())
}

// checkFieldOwnershipConflicts detects when fields managed by other controllers are being changed
func (r *manifestResource) checkFieldOwnershipConflicts(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	fmt.Printf("=== checkFieldOwnershipConflicts START ===\n")
	// Get state and plan projections
	var stateData, planData manifestResourceModel
	diags := req.State.Get(ctx, &stateData)
	resp.Diagnostics.Append(diags...)
	diags = req.Plan.Get(ctx, &planData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		fmt.Printf("Has diagnostics error, returning\n")
		return
	}
	fmt.Printf("Projections - state hash: %x, plan hash: %x\n",
		md5.Sum([]byte(stateData.ManagedStateProjection.ValueString())),
		md5.Sum([]byte(planData.ManagedStateProjection.ValueString())))
	// Skip if projections are not available
	if stateData.ManagedStateProjection.IsNull() || planData.ManagedStateProjection.IsNull() {
		fmt.Printf("Projections not available (state null: %v, plan null: %v), returning\n",
			stateData.ManagedStateProjection.IsNull(), planData.ManagedStateProjection.IsNull())
		return
	}
	// Skip if projections are the same
	if stateData.ManagedStateProjection.Equal(planData.ManagedStateProjection) {
		fmt.Printf("Projections are equal, returning\n")
		return
	}
	fmt.Printf("Projections differ - checking for conflicts\n")
	// Get field ownership from state
	if stateData.FieldOwnership.IsNull() {
		fmt.Printf("field_ownership is null, returning\n")
		return
	}
	fmt.Printf("field_ownership value: %s\n", stateData.FieldOwnership.ValueString())
	var ownership map[string]FieldOwnership
	if err := json.Unmarshal([]byte(stateData.FieldOwnership.ValueString()), &ownership); err != nil {
		tflog.Warn(ctx, "Failed to unmarshal field ownership", map[string]interface{}{
			"error": err.Error(),
		})
		fmt.Printf("Failed to unmarshal field ownership: %v\n", err)
		return
	}
	fmt.Printf("Parsed ownership map with %d entries\n", len(ownership))
	// Parse the user's desired YAML to see what fields they want
	desiredObj, err := r.parseYAML(planData.YAMLBody.ValueString())
	if err != nil {
		fmt.Printf("Failed to parse user's YAML: %v\n", err)
		return
	}
	// Extract all paths the user wants to manage
	userWantsPaths := extractFieldPaths(desiredObj.Object, "")
	fmt.Printf("User wants to manage %d paths\n", len(userWantsPaths))
	// Check each path the user wants against ownership
	var conflicts []FieldConflict
	for _, path := range userWantsPaths {
		// Skip metadata fields that are always owned by us
		if strings.HasPrefix(path, "metadata.annotations.k8sconnect.terraform.io/") {
			continue
		}
		// Skip core fields that don't have ownership
		if path == "apiVersion" || path == "kind" || path == "metadata.name" || path == "metadata.namespace" {
			continue
		}

		// ADD DETAILED LOGGING HERE
		if owner, exists := ownership[path]; exists {
			fmt.Printf("  Path %s: owned by '%s', checking against 'k8sconnect', equal? %v\n",
				path, owner.Manager, owner.Manager == "k8sconnect")
			if owner.Manager != "k8sconnect" {
				fmt.Printf("  CONFLICT DETECTED: Field %s owned by %s (not k8sconnect)\n", path, owner.Manager)
				conflicts = append(conflicts, FieldConflict{
					Path:  path,
					Owner: owner.Manager,
				})
			} else {
				fmt.Printf("  OK: Field %s correctly owned by k8sconnect\n", path)
			}
		} else {
			// Field not in ownership map - might be a new field or not tracked
			fmt.Printf("  NO OWNERSHIP DATA: Field %s not in ownership map (likely ok - new or untracked)\n", path)
		}
	}
	fmt.Printf("Total conflicts: %d\n", len(conflicts))
	if len(conflicts) > 0 {
		fmt.Printf("Calling addConflictWarning with force_conflicts=%v\n", planData.ForceConflicts.ValueBool())
		addConflictWarning(resp, conflicts, planData.ForceConflicts)
	}
	fmt.Printf("=== checkFieldOwnershipConflicts END ===\n")
}

// FieldConflict represents a field that user wants but is owned by another controller
type FieldConflict struct {
	Path  string
	Owner string
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
