// internal/k8sconnect/resource/manifest/plan_modifier.go
package manifest

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
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

	// ADR-010: Detect resource identity changes for UPDATE operations
	// This must happen BEFORE dry-run to avoid wasting API calls when replacement is needed
	if !req.State.Raw.IsNull() {
		if requiresReplacement := r.checkResourceIdentityChanges(ctx, req, &plannedData, resp); requiresReplacement {
			// Early return - skip dry-run when resource will be replaced
			// Terraform will orchestrate delete → create
			return
		}
	}

	// Validate connection is ready for operations
	if !r.validateConnectionReady(ctx, &plannedData, resp) {
		return
	}

	// Parse the desired YAML
	yamlStr := plannedData.YAMLBody.ValueString()

	// Check if YAML is empty (can happen with unresolved interpolations during planning)
	if yamlStr == "" {
		// Mark computed fields as unknown
		plannedData.ManagedStateProjection = types.StringUnknown()
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)

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

	desiredObj, err := r.parseYAML(yamlStr)
	if err != nil {
		// Check if this might be due to unresolved interpolations
		if strings.Contains(yamlStr, "${") {
			// During plan with interpolations to computed values, we can't parse/validate
			// Mark computed fields as unknown
			plannedData.ManagedStateProjection = types.StringUnknown()
			plannedData.FieldOwnership = types.MapUnknown(types.StringType)

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

	// Save the modified plan
	diags = resp.Plan.Set(ctx, &plannedData)
	resp.Diagnostics.Append(diags...)

	// Check field ownership conflicts for updates
	if !req.State.Raw.IsNull() {
		r.checkFieldOwnershipConflicts(ctx, req, resp)
	}
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

				// Only preserve field_ownership if ignore_fields hasn't changed
				// When ignore_fields changes, field_ownership will change even if projection doesn't
				ignoreFieldsChanged := !stateData.IgnoreFields.Equal(plannedData.IgnoreFields)
				if !ignoreFieldsChanged {
					plannedData.FieldOwnership = stateData.FieldOwnership
				}
				// else: leave field_ownership as Unknown (default), Apply will compute it

				// Note: ImportedWithoutAnnotations is now in private state, not model
				// But still allow terraform-specific settings to update
				// (delete_protection, force_conflicts, etc. are not preserved)
			}
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

	// If dryRunResult is nil, it means replacement was triggered (e.g., immutable field)
	// In this case, projection is not needed
	if dryRunResult == nil {
		return true
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

// calculateProjection determines projection strategy and calculates projection
func (r *manifestResource) calculateProjection(ctx context.Context, req resource.ModifyPlanRequest, plannedData *manifestResourceModel, desiredObj, dryRunResult *unstructured.Unstructured, client k8sclient.K8sClient, resp *resource.ModifyPlanResponse) bool {
	isCreate := isCreateOperation(req)

	// CREATE operations: Set projection to Unknown (will be populated after create)
	if isCreate {
		tflog.Debug(ctx, "CREATE - setting projection to unknown")
		plannedData.ManagedStateProjection = types.StringUnknown()
		return true
	}

	// UPDATE operations: Use field ownership from dry-run result
	// Extract ownership from dry-run result (what ownership WILL BE after apply)
	paths := extractOwnedPaths(ctx, dryRunResult.GetManagedFields(), desiredObj.Object)

	// Preserve field_ownership from state for UPDATE operations
	// Only mark as unknown if ignore_fields changed or force_conflicts is set (both could affect ownership)
	var stateData manifestResourceModel
	if diags := req.State.Get(ctx, &stateData); !diags.HasError() {
		ignoreFieldsChanged := !stateData.IgnoreFields.Equal(plannedData.IgnoreFields)
		forceConflicts := !plannedData.ForceConflicts.IsNull() && plannedData.ForceConflicts.ValueBool()

		if !ignoreFieldsChanged && !forceConflicts && !stateData.FieldOwnership.IsNull() {
			plannedData.FieldOwnership = stateData.FieldOwnership
			tflog.Debug(ctx, "Preserved field_ownership from state for UPDATE")
		}
		// else: leave field_ownership as Unknown, Apply will compute it
	}

	// Apply projection
	return r.applyProjection(ctx, dryRunResult, paths, plannedData, resp)
}

// performDryRun executes the dry-run against k8s
func (r *manifestResource) performDryRun(ctx context.Context, client k8sclient.K8sClient, desiredObj *unstructured.Unstructured, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) (*unstructured.Unstructured, error) {
	// Filter ignored fields before dry-run to match what we'll actually apply
	objToApply := desiredObj.DeepCopy()
	if ignoreFields := getIgnoreFields(ctx, plannedData); ignoreFields != nil {
		objToApply = removeFieldsFromObject(objToApply, ignoreFields)
		tflog.Debug(ctx, "Filtered ignore_fields before dry-run", map[string]interface{}{
			"ignored_count": len(ignoreFields),
		})
	}

	dryRunResult, err := client.DryRunApply(ctx, objToApply, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect",
		Force:        true,
	})
	if err != nil {
		// ADR-002: Check if this is an immutable field error
		// If so, trigger automatic resource replacement instead of failing
		if r.isImmutableFieldError(err) {
			immutableFields := r.extractImmutableFields(err)
			resourceDesc := fmt.Sprintf("%s/%s %s/%s",
				desiredObj.GetAPIVersion(), desiredObj.GetKind(),
				desiredObj.GetNamespace(), desiredObj.GetName())

			tflog.Info(ctx, "Immutable field changed, triggering replacement",
				map[string]interface{}{
					"resource": resourceDesc,
					"fields":   immutableFields,
				})

			// Mark resource for replacement
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("yaml_body"))

			// Add informative warning to explain why replacement is happening
			resp.Diagnostics.AddWarning(
				"Immutable Field Changed - Replacement Required",
				fmt.Sprintf("Cannot modify immutable field(s): %v on %s\n\n"+
					"Immutable fields cannot be changed after resource creation.\n"+
					"Terraform will delete the existing resource and create a new one.\n\n"+
					"This is the correct behavior - Kubernetes does not allow these fields to be modified in-place.",
					immutableFields, resourceDesc))

			// Set projection to unknown (replacement doesn't need projection)
			plannedData.ManagedStateProjection = types.StringUnknown()

			// Return success (nil error) to allow planning to continue
			// The replacement will be shown in the plan output
			return nil, nil
		}

		// Non-immutable errors: existing behavior (fail the dry-run)
		r.setProjectionUnknown(ctx, plannedData, resp,
			fmt.Sprintf("Dry-run failed: %s", err))
		return nil, err
	}
	return dryRunResult, nil
}

// getFieldOwnershipPaths gets paths based on field ownership
func (r *manifestResource) getFieldOwnershipPaths(ctx context.Context, plannedData *manifestResourceModel, desiredObj *unstructured.Unstructured, client k8sclient.K8sClient) []string {
	// Check force_conflicts setting FIRST
	forceConflicts := !plannedData.ForceConflicts.IsNull() && plannedData.ForceConflicts.ValueBool()

	if forceConflicts {
		// When force_conflicts is true, use ALL fields from YAML
		paths := extractFieldPaths(desiredObj.Object, "")
		return paths
	}

	// Try to get current object for ownership info
	gvr, err := client.GetGVR(ctx, desiredObj)
	if err != nil {
		paths := extractFieldPaths(desiredObj.Object, "")
		return paths
	}

	currentObj, err := client.Get(ctx, gvr, desiredObj.GetNamespace(), desiredObj.GetName())
	if err != nil {
		tflog.Debug(ctx, "Could not get current object, falling back to YAML paths")
		paths := extractFieldPaths(desiredObj.Object, "")
		return paths
	}

	tflog.Debug(ctx, "Using field ownership for projection")
	paths := extractOwnedPaths(ctx, currentObj.GetManagedFields(), desiredObj.Object)
	return paths
}

// applyProjection projects fields and updates plan
func (r *manifestResource) applyProjection(ctx context.Context, dryRunResult *unstructured.Unstructured, paths []string, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) bool {
	// Apply ignore_fields filtering if specified
	if ignoreFields := getIgnoreFields(ctx, plannedData); ignoreFields != nil {
		paths = filterIgnoredPaths(paths, ignoreFields)
		tflog.Debug(ctx, "Applied ignore_fields filtering in plan modifier", map[string]interface{}{
			"ignored_count":  len(ignoreFields),
			"filtered_paths": len(paths),
		})
	}

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

	// Update the plan with projection
	plannedData.ManagedStateProjection = types.StringValue(projectionJSON)
	tflog.Debug(ctx, "Dry-run projection complete", map[string]interface{}{
		"path_count":      len(paths),
		"projection_size": len(projectionJSON),
	})

	return true
}

// determineStatusField handles complex status field logic based on wait_for
func (r *manifestResource) determineStatusField(ctx context.Context, req resource.ModifyPlanRequest, plannedData *manifestResourceModel, resp *resource.ModifyPlanResponse) {
	if isCreateOperation(req) {
		r.determineCreateStatus(ctx, plannedData)
	} else {
		r.determineUpdateStatus(ctx, req, plannedData, resp)
	}
}

// determineCreateStatus handles status for CREATE operations
func (r *manifestResource) determineCreateStatus(ctx context.Context, plannedData *manifestResourceModel) {
	waitConfig, ok := parseWaitConfig(ctx, plannedData.WaitFor)
	if !ok {
		plannedData.Status = types.DynamicNull()
		tflog.Debug(ctx, "CREATE: No wait_for configured, status will be null")
		return
	}

	// Check wait conditions
	hasConditions := hasActiveWaitConditions(waitConfig)

	if hasConditions && isFieldWait(waitConfig) {
		plannedData.Status = types.DynamicUnknown()
		tflog.Debug(ctx, "CREATE: wait_for.field configured, marking status as unknown")
	} else {
		plannedData.Status = types.DynamicNull()
		if hasConditions {
			tflog.Debug(ctx, "CREATE: non-field wait type, status will be null")
		} else {
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
	} else if status.unknown {
		plannedData.Status = types.DynamicUnknown()
	} else {
		plannedData.Status = types.DynamicNull()
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

// checkFieldOwnershipConflicts detects when fields managed by other controllers are being changed
func (r *manifestResource) checkFieldOwnershipConflicts(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Get state and plan projections
	var stateData, planData manifestResourceModel
	diags := req.State.Get(ctx, &stateData)
	resp.Diagnostics.Append(diags...)
	diags = resp.Plan.Get(ctx, &planData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip if projections are not available
	if stateData.ManagedStateProjection.IsNull() || planData.ManagedStateProjection.IsNull() {
		return
	}

	// NOTE: We do NOT skip when projections are equal!
	// Even if there's no drift, we need to check if user's YAML contains fields
	// owned by other controllers and warn/error appropriately.

	// Get field ownership from state
	if stateData.FieldOwnership.IsNull() {
		return
	}

	// Convert types.Map to map[string]string
	var ownershipMap map[string]string
	diags = stateData.FieldOwnership.ElementsAs(ctx, &ownershipMap, false)
	if diags.HasError() {
		tflog.Warn(ctx, "Failed to extract field ownership map", map[string]interface{}{
			"diagnostics": diags,
		})
		return
	}

	// Convert map[string]string (manager names) to map[string]FieldOwnership for compatibility
	ownership := make(map[string]FieldOwnership, len(ownershipMap))
	for path, manager := range ownershipMap {
		ownership[path] = FieldOwnership{Manager: manager}
	}

	// Parse the user's desired YAML to see what fields they want
	desiredObj, err := r.parseYAML(planData.YAMLBody.ValueString())
	if err != nil {
		return
	}

	// Extract all paths the user wants to manage
	userWantsPaths := extractAllFieldsFromYAML(desiredObj.Object, "")

	// Filter out ignored fields - we don't check ownership for fields we're explicitly ignoring
	if ignoreFields := getIgnoreFields(ctx, &planData); ignoreFields != nil {
		userWantsPaths = filterIgnoredPaths(userWantsPaths, ignoreFields)
	}

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

		if owner, exists := ownership[path]; exists {
			if owner.Manager != "k8sconnect" {
				conflicts = append(conflicts, FieldConflict{
					Path:  path,
					Owner: owner.Manager,
				})
			}
		}
	}

	if len(conflicts) > 0 {
		addConflictWarning(resp, conflicts, planData.ForceConflicts)
	}
}

// filterFieldOwnership filters a field_ownership value to remove ignored fields
func (r *manifestResource) filterFieldOwnership(ctx context.Context, ownershipValue types.Map, data *manifestResourceModel) types.Map {
	if ownershipValue.IsNull() || ownershipValue.IsUnknown() {
		return ownershipValue
	}

	// Convert to map[string]string
	var ownershipMap map[string]string
	diags := ownershipValue.ElementsAs(ctx, &ownershipMap, false)
	if diags.HasError() {
		// If we can't parse, just return the original value
		return ownershipValue
	}

	// Filter out ignored fields
	if ignoreFields := getIgnoreFields(ctx, data); ignoreFields != nil {
		for _, ignorePath := range ignoreFields {
			delete(ownershipMap, ignorePath)
		}
	}

	// Convert back to types.Map
	filteredMap, diags := types.MapValueFrom(ctx, types.StringType, ownershipMap)
	if diags.HasError() {
		return ownershipValue
	}

	return filteredMap
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
				"These fields will be forcibly taken over. The other controllers may fight back.\n"+
				"Consider adding these paths to ignore_fields to release ownership instead.",
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
				"To resolve: add conflicting paths to ignore_fields to release ownership, or set force_conflicts = true to override.",
				strings.Join(conflictDetails, "\n")),
		)
	}
}
