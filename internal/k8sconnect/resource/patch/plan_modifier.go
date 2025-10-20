package patch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/validation"
)

// ModifyPlan implements resource.ResourceWithModifyPlan for patch resource
// This enables dry-run during terraform plan to show accurate diffs
func (r *patchResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Skip during destroy
	if req.Plan.Raw.IsNull() {
		return
	}

	// Get planned data
	var plannedData patchResourceModel
	diags := req.Plan.Get(ctx, &plannedData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get target configuration
	var target patchTargetModel
	diags = plannedData.Target.As(ctx, &target, basetypes.ObjectAsOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get patch content and type
	patchContent := r.getPatchContent(plannedData)
	if patchContent == "" {
		// No patch content, set computed fields to unknown
		plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)
		plannedData.ManagedFields = types.StringUnknown()
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)
		plannedData.PreviousOwners = types.MapUnknown(types.StringType)
		resp.Plan.Set(ctx, &plannedData)
		return
	}

	// Check for interpolations - skip dry-run if patch contains unresolved values
	if validation.ContainsInterpolation(patchContent) {
		tflog.Debug(ctx, "Patch contains interpolations, skipping dry-run",
			map[string]interface{}{"patch_preview": patchContent[:min(100, len(patchContent))]})
		plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)
		plannedData.ManagedFields = types.StringUnknown()
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)
		plannedData.PreviousOwners = types.MapUnknown(types.StringType)
		resp.Plan.Set(ctx, &plannedData)
		return
	}

	// Validate connection is ready for operations
	if !r.isConnectionReady(plannedData.ClusterConnection) {
		tflog.Debug(ctx, "Connection has unknown values, skipping dry-run")
		plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)
		plannedData.ManagedFields = types.StringUnknown()
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)
		plannedData.PreviousOwners = types.MapUnknown(types.StringType)
		resp.Plan.Set(ctx, &plannedData)
		return
	}

	// Execute dry-run and extract field ownership
	if !r.executeDryRunPatch(ctx, req, &plannedData, target, patchContent, resp) {
		return
	}

	// Save the modified plan
	diags = resp.Plan.Set(ctx, &plannedData)
	resp.Diagnostics.Append(diags...)

	// Check field ownership conflicts for updates (warn about takeovers)
	if !req.State.Raw.IsNull() {
		r.checkPatchOwnershipConflicts(ctx, req, resp)
	}
}

// isConnectionReady checks if all connection fields are known (not computed)
// Reused from manifest pattern
func (r *patchResource) isConnectionReady(conn types.Object) bool {
	if conn.IsNull() || conn.IsUnknown() {
		return false
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(context.Background(), conn)
	if err != nil {
		// Conversion failed due to unknown values
		return false
	}

	// Check if required fields are known
	if connModel.Host.IsUnknown() {
		return false
	}

	// Check auth fields
	if !connModel.Token.IsNull() && connModel.Token.IsUnknown() {
		return false
	}

	if !connModel.Kubeconfig.IsNull() && connModel.Kubeconfig.IsUnknown() {
		return false
	}

	if !connModel.ClusterCACertificate.IsNull() && connModel.ClusterCACertificate.IsUnknown() {
		return false
	}

	// Connection is ready
	return true
}

// executeDryRunPatch performs the dry-run patch operation
func (r *patchResource) executeDryRunPatch(ctx context.Context, req resource.ModifyPlanRequest, plannedData *patchResourceModel, target patchTargetModel, patchContent string, resp *resource.ModifyPlanResponse) bool {
	// Setup client
	client, err := r.setupDryRunClient(ctx, plannedData, resp)
	if err != nil {
		return false
	}

	// Determine patch type
	patchType := r.determinePatchType(*plannedData)

	// Get target resource identity
	apiVersion := target.APIVersion.ValueString()
	kind := target.Kind.ValueString()
	name := target.Name.ValueString()
	namespace := target.Namespace.ValueString()

	tflog.Debug(ctx, "Executing dry-run patch",
		map[string]interface{}{
			"api_version": apiVersion,
			"kind":        kind,
			"name":        name,
			"namespace":   namespace,
			"patch_type":  patchType,
		})

	// Validate target resource and check for conflicts
	currentObj, ok := r.validatePatchTarget(ctx, client, target, plannedData, patchContent, resp)
	if !ok {
		// validatePatchTarget sets resp.Plan if needed and adds diagnostics
		// Return true if it was a "soft" failure (CRD not found, target doesn't exist)
		// where we set projection to unknown and want plan to succeed
		return !resp.Diagnostics.HasError()
	}

	// Generate our field manager name
	fieldManager := r.generateFieldManager(*plannedData)

	// Execute dry-run patch
	patchedObj, ok := r.executePatchDryRun(ctx, client, currentObj, plannedData, target, patchContent, fieldManager, resp)
	if !ok {
		return false
	}

	// Calculate projection and manage state based on patch type and operation
	return r.calculatePatchProjection(ctx, req, plannedData, patchedObj, currentObj, fieldManager, resp)
}

// setupDryRunClient creates the k8s client for dry-run (reused from manifest pattern)
func (r *patchResource) setupDryRunClient(ctx context.Context, plannedData *patchResourceModel, resp *resource.ModifyPlanResponse) (k8sclient.K8sClient, error) {
	// Convert connection
	conn, err := auth.ObjectToConnectionModel(ctx, plannedData.ClusterConnection)
	if err != nil {
		tflog.Debug(ctx, "Skipping dry-run due to connection conversion error", map[string]interface{}{"error": err.Error()})
		setProjectionUnknown(plannedData)
		return nil, err
	}

	// Create client
	client, err := r.clientGetter(conn)
	if err != nil {
		tflog.Debug(ctx, "Skipping dry-run due to client creation error", map[string]interface{}{"error": err.Error()})
		setProjectionUnknown(plannedData)
		return nil, err
	}

	return client, nil
}

// checkPatchOwnershipConflicts detects when fields managed by other controllers are being taken over
// Adapted from manifest's ownership conflict detection
func (r *patchResource) checkPatchOwnershipConflicts(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Get state and plan data
	var stateData, planData patchResourceModel
	diags := req.State.Get(ctx, &stateData)
	resp.Diagnostics.Append(diags...)
	diags = resp.Plan.Get(ctx, &planData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip if we don't have field ownership in state
	if stateData.FieldOwnership.IsNull() || planData.FieldOwnership.IsNull() {
		return
	}

	// Convert ownership maps
	var stateOwnership, planOwnership map[string]string
	diags = stateData.FieldOwnership.ElementsAs(ctx, &stateOwnership, false)
	if diags.HasError() {
		return
	}
	diags = planData.FieldOwnership.ElementsAs(ctx, &planOwnership, false)
	if diags.HasError() {
		return
	}

	// Check for ownership changes (takeovers from other controllers)
	var conflicts []fieldConflict
	for path, planOwner := range planOwnership {
		if stateOwner, existed := stateOwnership[path]; existed {
			// Field existed before
			if stateOwner != planOwner && stateOwner != r.generateFieldManager(planData) {
				// Ownership changed from another controller
				conflicts = append(conflicts, fieldConflict{
					Path:         path,
					CurrentOwner: stateOwner,
					NewOwner:     planOwner,
				})
			}
		}
	}

	// If we have previous owners from the plan (first patch application), add those to conflicts
	if !planData.PreviousOwners.IsNull() {
		var previousOwners map[string]string
		diags = planData.PreviousOwners.ElementsAs(ctx, &previousOwners, false)
		if !diags.HasError() {
			for path, prevOwner := range previousOwners {
				// Check if not already in conflicts
				found := false
				for _, c := range conflicts {
					if c.Path == path {
						found = true
						break
					}
				}
				if !found {
					conflicts = append(conflicts, fieldConflict{
						Path:         path,
						CurrentOwner: prevOwner,
						NewOwner:     r.generateFieldManager(planData),
					})
				}
			}
		}
	}

	if len(conflicts) > 0 {
		r.addConflictWarning(resp, conflicts)
	}
}

// fieldConflict represents a field ownership takeover
type fieldConflict struct {
	Path         string
	CurrentOwner string
	NewOwner     string
}

// addConflictWarning adds a warning about field ownership takeovers
func (r *patchResource) addConflictWarning(resp *resource.ModifyPlanResponse, conflicts []fieldConflict) {
	var conflictDetails []string
	for _, c := range conflicts {
		conflictDetails = append(conflictDetails, fmt.Sprintf("  - %s (currently owned by %s)", c.Path, c.CurrentOwner))
	}

	resp.Diagnostics.AddWarning(
		"Field Ownership Takeover",
		fmt.Sprintf("This patch will forcefully take ownership of fields managed by other controllers:\n%s\n\n"+
			"These fields will be taken over with force=true. The other controllers may fight back for control.\n\n"+
			"This is expected behavior for patches (force=true is always used), but be aware that:\n"+
			"• External controllers may revert your changes\n"+
			"• You may need to disable or reconfigure those controllers\n"+
			"• Consider if k8sconnect_object with ignore_fields would be better for full lifecycle management",
			strings.Join(conflictDetails, "\n")),
	)
}

// dryRunStrategicMergePatch performs a dry-run of a strategic merge patch using SSA
func (r *patchResource) dryRunStrategicMergePatch(ctx context.Context, client k8sclient.K8sClient, currentObj *unstructured.Unstructured, patchContent string, fieldManager string) (*unstructured.Unstructured, error) {
	// Parse patch content
	var patchData map[string]interface{}
	if err := json.Unmarshal([]byte(patchContent), &patchData); err != nil {
		// Try YAML
		if err := yaml.Unmarshal([]byte(patchContent), &patchData); err != nil {
			return nil, fmt.Errorf("failed to parse patch content: %w", err)
		}
	}

	// Create a new object that combines target metadata with patch data
	patchObj := &unstructured.Unstructured{Object: make(map[string]interface{})}
	patchObj.SetAPIVersion(currentObj.GetAPIVersion())
	patchObj.SetKind(currentObj.GetKind())
	patchObj.SetName(currentObj.GetName())
	patchObj.SetNamespace(currentObj.GetNamespace())

	// Merge patch data into the object
	r.mergeMaps(patchObj.Object, patchData)

	// Perform dry-run using SSA
	dryRunResult, err := client.DryRunApply(ctx, patchObj, k8sclient.ApplyOptions{
		FieldManager:    fieldManager,
		Force:           true,     // Required for taking ownership
		FieldValidation: "Strict", // ADR-017: Validate fields against OpenAPI schema during plan
	})

	if err != nil {
		return nil, err
	}

	return dryRunResult, nil
}

// mergeMaps performs a deep merge of src into dst (adapted from helpers.go)
func (r *patchResource) mergeMaps(dst, src map[string]interface{}) {
	for key, srcVal := range src {
		if dstVal, exists := dst[key]; exists {
			// Key exists in both
			if dstMap, dstIsMap := dstVal.(map[string]interface{}); dstIsMap {
				if srcMap, srcIsMap := srcVal.(map[string]interface{}); srcIsMap {
					// Both are maps - recurse
					r.mergeMaps(dstMap, srcMap)
					continue
				}
			}
		}
		// Either key doesn't exist in dst, or one of the values isn't a map
		// Override with src value
		dst[key] = srcVal
	}
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// generateFieldManager returns the field manager name for this patch
// During CREATE (plan phase), ID doesn't exist yet, so we use a placeholder
// During UPDATE, we use the actual ID from state
func (r *patchResource) generateFieldManager(data patchResourceModel) string {
	if data.ID.IsNull() || data.ID.IsUnknown() {
		// Plan phase for CREATE - use placeholder
		// The actual ID will be different, but this is just for dry-run prediction
		return "k8sconnect-patch-temp"
	}
	// UPDATE or after CREATE - use actual ID
	return fmt.Sprintf("k8sconnect-patch-%s", data.ID.ValueString())
}

// =============================================================================
// Helper Functions for executeDryRunPatch refactoring
// =============================================================================

// setProjectionUnknown sets all projection-related fields to unknown
func setProjectionUnknown(data *patchResourceModel) {
	data.ManagedStateProjection = types.MapUnknown(types.StringType)
	data.ManagedFields = types.StringUnknown()
	data.FieldOwnership = types.MapUnknown(types.StringType)
	data.PreviousOwners = types.MapUnknown(types.StringType)
}

// hasPatchContentChanged checks if patch content has changed between state and plan
func (r *patchResource) hasPatchContentChanged(ctx context.Context, req resource.ModifyPlanRequest, plannedData patchResourceModel) bool {
	if req.State.Raw.IsNull() {
		// CREATE operation - no state to compare
		return true
	}

	var stateData patchResourceModel
	if diags := req.State.Get(ctx, &stateData); diags.HasError() {
		// Can't compare, assume changed
		return true
	}

	statePatchContent := r.getPatchContent(stateData)
	planPatchContent := r.getPatchContent(plannedData)

	return statePatchContent != planPatchContent
}

// validatePatchTarget gets and validates the target resource for patching
// Returns currentObj and true if valid, or nil and false if invalid (errors added to resp)
func (r *patchResource) validatePatchTarget(
	ctx context.Context,
	client k8sclient.K8sClient,
	target patchTargetModel,
	plannedData *patchResourceModel,
	patchContent string,
	resp *resource.ModifyPlanResponse,
) (*unstructured.Unstructured, bool) {
	// Get GVR and current target resource
	_, currentObj, err := r.getTargetResource(ctx, client, target)
	if err != nil {
		// Check if this is a CRD-not-found error
		if k8serrors.IsCRDNotFoundError(err) {
			tflog.Debug(ctx, "CRD not found during plan, will be available during apply")
			setProjectionUnknown(plannedData)
			resp.Plan.Set(ctx, plannedData)
			return nil, false
		}

		// Check if target doesn't exist yet
		if errors.IsNotFound(err) {
			tflog.Debug(ctx, "Target resource not found during plan, will be created before patch applies")
			setProjectionUnknown(plannedData)
			resp.Plan.Set(ctx, plannedData)
			return nil, false
		}

		// Other errors
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Get Target Resource",
			formatTarget(target))
		return nil, false
	}

	// Surface any warnings from Get operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

	// CRITICAL VALIDATION: Prevent self-patching
	if r.isManagedByThisState(ctx, currentObj) {
		resp.Diagnostics.AddError(
			"Cannot Patch Own Resource",
			fmt.Sprintf("This resource is already managed by k8sconnect_object "+
				"in this Terraform state.\n\n"+
				"You cannot patch resources you already own. Instead:\n"+
				"1. Modify the k8sconnect_object directly, or\n"+
				"2. Use ignore_fields to allow external controllers to manage specific fields\n\n"+
				"Target: %s",
				formatTarget(target)),
		)
		return nil, false
	}

	// CRITICAL VALIDATION: Prevent multiple patches on the same fields
	patchType := r.determinePatchType(*plannedData)
	patchedFieldPaths, err := r.extractPatchFieldPaths(ctx, patchContent, patchType)
	if err != nil {
		resp.Diagnostics.AddError("Failed to Parse Patch", err.Error())
		return nil, false
	}

	// Get current field ownership
	currentOwnership := fieldmanagement.ExtractFieldOwnershipMap(currentObj)

	// Generate our field manager name
	fieldManager := r.generateFieldManager(*plannedData)

	// Check for conflicts with other k8sconnect_patch resources
	var conflicts []string
	for _, path := range patchedFieldPaths {
		if owner, exists := currentOwnership[path]; exists {
			// Check if owned by another k8sconnect-patch-* manager (not us)
			if strings.HasPrefix(owner, "k8sconnect-patch-") && owner != fieldManager {
				conflicts = append(conflicts, fmt.Sprintf("  - %s (currently owned by %s)", path, owner))
			}
		}
	}

	if len(conflicts) > 0 {
		resp.Diagnostics.AddError(
			"Patch Conflicts with Existing Patch",
			fmt.Sprintf("This patch attempts to modify fields already managed by another k8sconnect_patch resource:\n%s\n\n"+
				"Multiple patches cannot manage the same fields - they will fight for control and cause drift.\n\n"+
				"Options:\n"+
				"1. Remove the conflicting fields from one of the patches\n"+
				"2. Consolidate both patches into a single k8sconnect_patch resource\n"+
				"3. Use different fields that don't overlap\n\n"+
				"Target: %s",
				strings.Join(conflicts, "\n"),
				formatTarget(target)),
		)
		return nil, false
	}

	return currentObj, true
}

// executePatchDryRun executes a dry-run patch for strategic merge patches
// Returns patchedObj and true if successful, or nil and false on error
// For non-SSA patches (JSON/Merge), returns nil and true (no dry-run available)
func (r *patchResource) executePatchDryRun(
	ctx context.Context,
	client k8sclient.K8sClient,
	currentObj *unstructured.Unstructured,
	plannedData *patchResourceModel,
	target patchTargetModel,
	patchContent string,
	fieldManager string,
	resp *resource.ModifyPlanResponse,
) (*unstructured.Unstructured, bool) {
	patchType := r.determinePatchType(*plannedData)

	// JSON Patch and Merge Patch don't use SSA field management
	if patchType != "application/strategic-merge-patch+json" {
		tflog.Debug(ctx, "JSON/Merge patch detected, skipping dry-run (no SSA field management)")
		return nil, true // No patchedObj, but not an error
	}

	// Strategic merge patch uses SSA - can do dry-run to predict field ownership
	patchedObj, err := r.dryRunStrategicMergePatch(ctx, client, currentObj, patchContent, fieldManager)

	// Surface any warnings from Patch operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

	if err != nil {
		// Check for immutable field errors
		if k8serrors.IsImmutableFieldError(err) {
			immutableFields := k8serrors.ExtractImmutableFields(err)
			resp.Diagnostics.AddError(
				"Immutable Field in Patch",
				fmt.Sprintf("Cannot patch immutable field(s): %v on %s\n\n"+
					"The target resource has immutable fields that cannot be changed after creation.\n\n"+
					"Options:\n"+
					"1. Remove the immutable field from your patch\n"+
					"2. If the field MUST change, recreate the target resource manually or use k8sconnect_object\n"+
					"3. k8sconnect_object manages full resource lifecycle and can trigger automatic replacement",
					immutableFields, formatTarget(target)),
			)
			return nil, false
		}

		// Other errors
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Dry-run Patch", formatTarget(target))
		return nil, false
	}

	tflog.Debug(ctx, "Dry-run patch successful")
	return patchedObj, true
}

// calculatePatchProjection handles projection calculation and state management
// based on whether it's a strategic merge patch or JSON/Merge patch
func (r *patchResource) calculatePatchProjection(
	ctx context.Context,
	req resource.ModifyPlanRequest,
	plannedData *patchResourceModel,
	patchedObj *unstructured.Unstructured,
	currentObj *unstructured.Unstructured,
	fieldManager string,
	resp *resource.ModifyPlanResponse,
) bool {
	// Strategic merge patch with dry-run result
	if patchedObj != nil {
		return r.handleStrategicMergeProjection(ctx, req, plannedData, patchedObj, currentObj, fieldManager, resp)
	}

	// JSON/Merge patch - no dry-run available
	return r.handleNonSSAPatchState(ctx, req, plannedData, resp)
}

// handleStrategicMergeProjection calculates projection for strategic merge patches
func (r *patchResource) handleStrategicMergeProjection(
	ctx context.Context,
	req resource.ModifyPlanRequest,
	plannedData *patchResourceModel,
	patchedObj *unstructured.Unstructured,
	currentObj *unstructured.Unstructured,
	fieldManager string,
	resp *resource.ModifyPlanResponse,
) bool {
	// For CREATE operations, calculate projection
	if req.State.Raw.IsNull() {
		return r.calculateCreateProjection(ctx, plannedData, patchedObj, fieldManager, resp)
	}

	// For UPDATE: check if content changed
	if r.hasPatchContentChanged(ctx, req, *plannedData) {
		// Content changed, calculate new projection
		return r.calculateUpdateProjection(ctx, plannedData, patchedObj, fieldManager, resp)
	}

	// Content unchanged, preserve state
	return r.preserveState(ctx, req, plannedData)
}

// handleNonSSAPatchState manages state for JSON/Merge patches (no SSA)
func (r *patchResource) handleNonSSAPatchState(
	ctx context.Context,
	req resource.ModifyPlanRequest,
	plannedData *patchResourceModel,
	resp *resource.ModifyPlanResponse,
) bool {
	// For UPDATE, check if content unchanged
	if !req.State.Raw.IsNull() && !r.hasPatchContentChanged(ctx, req, *plannedData) {
		// Content unchanged, preserve state
		return r.preserveState(ctx, req, plannedData)
	}

	// CREATE or UPDATE with changed content
	// Non-SSA patches don't support projection
	plannedData.ManagedStateProjection = types.MapNull(types.StringType) // Null for non-SSA
	plannedData.ManagedFields = types.StringUnknown()
	plannedData.FieldOwnership = types.MapUnknown(types.StringType)
	plannedData.PreviousOwners = types.MapUnknown(types.StringType)
	return true
}

// calculateProjectionFromDryRun calculates projection for CREATE or UPDATE operations
func (r *patchResource) calculateProjectionFromDryRun(
	ctx context.Context,
	plannedData *patchResourceModel,
	patchedObj *unstructured.Unstructured,
	fieldManager string,
	operationType string,
	resp *resource.ModifyPlanResponse,
) bool {
	tflog.Debug(ctx, fmt.Sprintf("%s operation - calculating projection from dry-run result", operationType))

	paths := extractPatchedPaths(ctx, patchedObj.GetManagedFields(), fieldManager)
	projection, err := projectPatchedFields(patchedObj.Object, paths)
	if err != nil {
		tflog.Warn(ctx, "Failed to project patched fields", map[string]interface{}{"error": err.Error()})
		setProjectionUnknown(plannedData)
		return true
	}

	projectionMap := flattenPatchProjectionToMap(projection, paths)
	mapValue, diags := types.MapValueFrom(ctx, types.StringType, projectionMap)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return false
	}

	plannedData.ManagedStateProjection = mapValue
	tflog.Debug(ctx, fmt.Sprintf("Projection calculated for %s", operationType), map[string]interface{}{
		"field_count": len(projectionMap),
	})

	// Field ownership populated during apply
	plannedData.ManagedFields = types.StringUnknown()
	plannedData.FieldOwnership = types.MapUnknown(types.StringType)
	plannedData.PreviousOwners = types.MapUnknown(types.StringType)
	return true
}

// calculateCreateProjection calculates projection for CREATE operations
func (r *patchResource) calculateCreateProjection(
	ctx context.Context,
	plannedData *patchResourceModel,
	patchedObj *unstructured.Unstructured,
	fieldManager string,
	resp *resource.ModifyPlanResponse,
) bool {
	return r.calculateProjectionFromDryRun(ctx, plannedData, patchedObj, fieldManager, "CREATE", resp)
}

// calculateUpdateProjection calculates projection for UPDATE operations with changed content
func (r *patchResource) calculateUpdateProjection(
	ctx context.Context,
	plannedData *patchResourceModel,
	patchedObj *unstructured.Unstructured,
	fieldManager string,
	resp *resource.ModifyPlanResponse,
) bool {
	return r.calculateProjectionFromDryRun(ctx, plannedData, patchedObj, fieldManager, "UPDATE", resp)
}

// preserveState preserves existing state when content hasn't changed
func (r *patchResource) preserveState(
	ctx context.Context,
	req resource.ModifyPlanRequest,
	plannedData *patchResourceModel,
) bool {
	var stateData patchResourceModel
	if diags := req.State.Get(ctx, &stateData); diags.HasError() {
		return false
	}

	tflog.Debug(ctx, "Patch content unchanged, preserving state")
	plannedData.ManagedStateProjection = stateData.ManagedStateProjection
	plannedData.ManagedFields = stateData.ManagedFields
	plannedData.FieldOwnership = stateData.FieldOwnership
	plannedData.PreviousOwners = stateData.PreviousOwners
	return true
}
