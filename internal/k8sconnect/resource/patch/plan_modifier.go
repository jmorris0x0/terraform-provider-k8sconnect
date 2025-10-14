// internal/k8sconnect/resource/patch/plan_modifier.go
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
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
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
		plannedData.ManagedFields = types.StringUnknown()
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)
		plannedData.PreviousOwners = types.MapUnknown(types.StringType)
		resp.Plan.Set(ctx, &plannedData)
		return
	}

	// Validate connection is ready for operations
	if !r.isConnectionReady(plannedData.ClusterConnection) {
		tflog.Debug(ctx, "Connection has unknown values, skipping dry-run")
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

	// Get GVR and current target resource using the existing helper
	_, currentObj, err := r.getTargetResource(ctx, client, target)
	if err != nil {
		// Check if this is a CRD-not-found error
		if k8serrors.IsCRDNotFoundError(err) {
			tflog.Debug(ctx, "CRD not found during plan, will be available during apply")
			plannedData.ManagedFields = types.StringUnknown()
			plannedData.FieldOwnership = types.MapUnknown(types.StringType)
			plannedData.PreviousOwners = types.MapUnknown(types.StringType)
			resp.Plan.Set(ctx, plannedData)
			return true
		}

		// Check if target doesn't exist yet
		if errors.IsNotFound(err) {
			tflog.Debug(ctx, "Target resource not found during plan, will be created before patch applies")
			plannedData.ManagedFields = types.StringUnknown()
			plannedData.FieldOwnership = types.MapUnknown(types.StringType)
			plannedData.PreviousOwners = types.MapUnknown(types.StringType)
			resp.Plan.Set(ctx, plannedData)
			return true
		}

		// Other errors
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Get Target Resource",
			formatTarget(target))
		return false
	}

	// Surface any warnings from Get operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

	// Generate field manager name
	fieldManager := r.generateFieldManager(*plannedData)

	// Perform dry-run based on patch type
	var patchedObj *unstructured.Unstructured
	if patchType == "application/strategic-merge-patch+json" {
		// Strategic merge patch uses SSA - can do dry-run to predict field ownership
		patchedObj, err = r.dryRunStrategicMergePatch(ctx, client, currentObj, patchContent, fieldManager)

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
						"2. If the field MUST change, recreate the target resource manually or use k8sconnect_manifest\n"+
						"3. k8sconnect_manifest manages full resource lifecycle and can trigger automatic replacement",
						immutableFields, formatTarget(target)),
				)
				return false
			}

			// Other errors
			k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Dry-run Patch", formatTarget(target))
			return false
		}

		tflog.Debug(ctx, "Dry-run patch successful")

		// For CREATE operations, we can't accurately predict field ownership because
		// the ID (and thus field manager name) doesn't exist yet during plan.
		// Set computed fields to unknown so they'll be populated during apply.
		if req.State.Raw.IsNull() {
			tflog.Debug(ctx, "CREATE operation detected, setting computed fields to unknown")
			plannedData.ManagedFields = types.StringUnknown()
			plannedData.FieldOwnership = types.MapUnknown(types.StringType)
			plannedData.PreviousOwners = types.MapUnknown(types.StringType)
			return true
		}

		// For UPDATE operations with changed content, also set to unknown
		// to avoid inconsistencies between dry-run and actual apply
		if !req.State.Raw.IsNull() {
			var stateData patchResourceModel
			if diags := req.State.Get(ctx, &stateData); !diags.HasError() {
				statePatchContent := r.getPatchContent(stateData)
				planPatchContent := r.getPatchContent(*plannedData)

				if statePatchContent != planPatchContent {
					// Content changed, set to unknown (will be populated during apply)
					tflog.Debug(ctx, "Strategic merge patch content changed, setting computed fields to unknown")
					plannedData.ManagedFields = types.StringUnknown()
					plannedData.FieldOwnership = types.MapUnknown(types.StringType)
					plannedData.PreviousOwners = types.MapUnknown(types.StringType)
					return true
				}
			}
		}
	} else {
		// JSON Patch and Merge Patch don't use SSA field management,
		// so we can't predict field ownership during plan.
		tflog.Debug(ctx, "JSON/Merge patch detected, skipping dry-run (no SSA field management)")

		if !req.State.Raw.IsNull() {
			// UPDATE: Check if patch content has changed
			var stateData patchResourceModel
			if diags := req.State.Get(ctx, &stateData); !diags.HasError() {
				statePatchContent := r.getPatchContent(stateData)
				planPatchContent := r.getPatchContent(*plannedData)

				if statePatchContent == planPatchContent {
					// Patch content unchanged, preserve existing field ownership from state
					tflog.Debug(ctx, "JSON/Merge patch content unchanged, preserving state field ownership")
					plannedData.ManagedFields = stateData.ManagedFields
					plannedData.FieldOwnership = stateData.FieldOwnership
					plannedData.PreviousOwners = stateData.PreviousOwners
					return true
				}
			}
		}

		// CREATE or UPDATE with changed content: set to unknown (will be populated during apply)
		plannedData.ManagedFields = types.StringUnknown()
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)
		plannedData.PreviousOwners = types.MapUnknown(types.StringType)
		return true
	}

	// For UPDATE operations, check if patch content has changed
	// If nothing changed, skip field ownership extraction to avoid unnecessary updates
	if !req.State.Raw.IsNull() {
		var stateData patchResourceModel
		if diags := req.State.Get(ctx, &stateData); !diags.HasError() {
			// Check if patch content is the same
			statePatchContent := r.getPatchContent(stateData)
			planPatchContent := r.getPatchContent(*plannedData)

			if statePatchContent == planPatchContent {
				// Patch content hasn't changed, preserve existing field ownership from state
				tflog.Debug(ctx, "Patch content unchanged, preserving state field ownership")
				plannedData.FieldOwnership = stateData.FieldOwnership
				plannedData.ManagedFields = stateData.ManagedFields
				plannedData.PreviousOwners = stateData.PreviousOwners
				return true
			}
		}
	}

	// For UPDATE operations with changed content, extract field ownership from patched result
	return r.extractPatchedFieldOwnership(ctx, plannedData, patchedObj, currentObj, resp)
}

// setupDryRunClient creates the k8s client for dry-run (reused from manifest pattern)
func (r *patchResource) setupDryRunClient(ctx context.Context, plannedData *patchResourceModel, resp *resource.ModifyPlanResponse) (k8sclient.K8sClient, error) {
	// Convert connection
	conn, err := auth.ObjectToConnectionModel(ctx, plannedData.ClusterConnection)
	if err != nil {
		tflog.Debug(ctx, "Skipping dry-run due to connection conversion error", map[string]interface{}{"error": err.Error()})
		plannedData.ManagedFields = types.StringUnknown()
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)
		plannedData.PreviousOwners = types.MapUnknown(types.StringType)
		return nil, err
	}

	// Create client
	client, err := r.clientGetter(conn)
	if err != nil {
		tflog.Debug(ctx, "Skipping dry-run due to client creation error", map[string]interface{}{"error": err.Error()})
		plannedData.ManagedFields = types.StringUnknown()
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)
		plannedData.PreviousOwners = types.MapUnknown(types.StringType)
		return nil, err
	}

	return client, nil
}

// extractPatchedFieldOwnership extracts field ownership for fields modified by this patch
func (r *patchResource) extractPatchedFieldOwnership(ctx context.Context, plannedData *patchResourceModel, patchedObj, currentObj *unstructured.Unstructured, resp *resource.ModifyPlanResponse) bool {
	// Extract managed fields from the patched object
	managedFields := patchedObj.GetManagedFields()
	if len(managedFields) == 0 {
		tflog.Warn(ctx, "No managed fields in dry-run result")
		plannedData.ManagedFields = types.StringNull()
		plannedData.FieldOwnership = types.MapNull(types.StringType)
		plannedData.PreviousOwners = types.MapNull(types.StringType)
		return true
	}

	// Extract field ownership map (field path -> manager name)
	// Only extract fields owned by OUR field manager
	fieldOwnership := make(map[string]string)
	fieldManagerName := r.generateFieldManager(*plannedData)
	var ourFieldsV1 []byte

	// Parse managed fields to find our manager's fields
	for _, mf := range managedFields {
		manager := mf.Manager
		if mf.FieldsV1 == nil {
			continue
		}

		// Only process fields owned by our field manager
		if manager != fieldManagerName {
			continue
		}

		// Store our manager's FieldsV1 for managed_fields attribute
		ourFieldsV1 = mf.FieldsV1.Raw

		// Parse FieldsV1 to extract field paths
		var fieldsV1 map[string]interface{}
		if err := json.Unmarshal(mf.FieldsV1.Raw, &fieldsV1); err != nil {
			tflog.Warn(ctx, "Failed to parse FieldsV1", map[string]interface{}{"manager": manager, "error": err.Error()})
			continue
		}

		// Extract paths from FieldsV1 structure
		paths := extractFieldPaths(fieldsV1, "")
		for _, path := range paths {
			fieldOwnership[path] = manager
		}
	}

	// Store only our manager's FieldsV1 in managed_fields
	if ourFieldsV1 != nil {
		plannedData.ManagedFields = types.StringValue(string(ourFieldsV1))
	} else {
		plannedData.ManagedFields = types.StringNull()
	}

	// Store field ownership
	ownershipMap, diags := types.MapValueFrom(ctx, types.StringType, fieldOwnership)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return false
	}
	plannedData.FieldOwnership = ownershipMap

	// For CREATE operations, also track previous owners
	if currentObj != nil && len(currentObj.GetManagedFields()) > 0 {
		previousOwners := make(map[string]string)

		for _, mf := range currentObj.GetManagedFields() {
			manager := mf.Manager
			if mf.FieldsV1 == nil {
				continue
			}

			var fieldsV1 map[string]interface{}
			if err := json.Unmarshal(mf.FieldsV1.Raw, &fieldsV1); err != nil {
				continue
			}

			paths := extractFieldPaths(fieldsV1, "")
			for _, path := range paths {
				// Only track if this path will be taken over
				if newOwner, willBePatched := fieldOwnership[path]; willBePatched {
					if newOwner != manager {
						previousOwners[path] = manager
					}
				}
			}
		}

		if len(previousOwners) > 0 {
			prevOwnersMap, diags := types.MapValueFrom(ctx, types.StringType, previousOwners)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return false
			}
			plannedData.PreviousOwners = prevOwnersMap
		} else {
			plannedData.PreviousOwners = types.MapNull(types.StringType)
		}
	} else {
		plannedData.PreviousOwners = types.MapNull(types.StringType)
	}

	tflog.Debug(ctx, "Extracted field ownership from dry-run",
		map[string]interface{}{
			"owned_fields": len(fieldOwnership),
		})

	return true
}

// extractFieldPaths recursively extracts field paths from FieldsV1 structure
// This is adapted from manifest's field ownership parsing
func extractFieldPaths(obj map[string]interface{}, prefix string) []string {
	var paths []string

	for key, value := range obj {
		// Strip the "f:" prefix that Kubernetes uses in FieldsV1
		fieldName := strings.TrimPrefix(key, "f:")

		// Build the full path
		var fullPath string
		if prefix == "" {
			fullPath = fieldName
		} else {
			fullPath = prefix + "." + fieldName
		}

		// Check if this is a nested object
		if valueMap, ok := value.(map[string]interface{}); ok {
			// Recursively extract paths from nested object
			nestedPaths := extractFieldPaths(valueMap, fullPath)
			paths = append(paths, nestedPaths...)
		} else {
			// This is a leaf field
			paths = append(paths, fullPath)
		}
	}

	return paths
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
			"• Consider if k8sconnect_manifest with ignore_fields would be better for full lifecycle management",
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
		FieldManager: fieldManager,
		Force:        true, // Required for taking ownership
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

// convertPatchType converts string patch type to k8s PatchType
func (r *patchResource) convertPatchType(patchType string) k8stypes.PatchType {
	switch patchType {
	case "strategic":
		return k8stypes.StrategicMergePatchType
	case "json":
		return k8stypes.JSONPatchType
	case "merge":
		return k8stypes.MergePatchType
	default:
		return k8stypes.StrategicMergePatchType
	}
}

// boolPtr returns a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
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
