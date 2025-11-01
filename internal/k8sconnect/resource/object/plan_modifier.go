package object

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/factory"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/ownership"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ModifyPlan implements resource.ResourceWithModifyPlan
func (r *objectResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	tflog.Debug(ctx, "⚠️ DEBUG: ModifyPlan - START")

	// Skip during destroy
	if req.Plan.Raw.IsNull() {
		tflog.Debug(ctx, "⚠️ DEBUG: ModifyPlan - Skipping: Plan is null (destroy operation)")
		return
	}

	// Get planned data
	var plannedData objectResourceModel
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

	// Parse the desired YAML first (we need desiredObj for yaml fallback)
	yamlStr := plannedData.YAMLBody.ValueString()

	// Check if YAML is empty (can happen with unresolved interpolations during planning)
	if yamlStr == "" {
		// Mark computed fields as unknown
		plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)
		plannedData.ManagedFields = types.MapUnknown(types.StringType)

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
			plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)
			plannedData.ManagedFields = types.MapUnknown(types.StringType)

			// Save the plan with unknown computed fields
			diags = resp.Plan.Set(ctx, &plannedData)
			resp.Diagnostics.Append(diags...)
			return
		}

		// This is a real YAML parsing error
		resp.Diagnostics.AddError("Invalid YAML", fmt.Sprintf("Failed to parse YAML: %s", err))
		return
	}

	// Validate connection is ready for operations
	connectionReady := r.isConnectionReady(plannedData.Cluster)

	// Populate object_ref from parsed YAML (prevents "(known after apply)" noise)
	// ONLY when connection is ready - during bootstrap we can't determine namespace defaults
	if connectionReady {
		if err := r.setObjectRefFromDesiredObj(ctx, desiredObj, &plannedData); err != nil {
			// Check if this is a CRD-not-found error
			if r.isCRDNotFoundError(err) {
				// CRD doesn't exist yet - this is expected during bootstrap
				// object_ref will remain as "(known after apply)" which is correct
				tflog.Debug(ctx, "CRD not found during plan - object_ref will be determined during apply", map[string]interface{}{
					"kind": desiredObj.GetKind(),
					"name": desiredObj.GetName(),
				})
			} else {
				resp.Diagnostics.AddError("Resource Type Not Found", err.Error())
				return
			}
		}
	}
	// Note: When connection not ready (bootstrap), object_ref stays as "(known after apply)"
	// This is correct - we genuinely can't determine namespace defaults without querying cluster

	if !connectionReady {
		// Connection has unknown values (bootstrap scenario) - set projection to unknown
		// User can review yaml_body in plan output
		r.setProjectionUnknown(ctx, &plannedData, resp,
			"Bootstrap scenario: connection unknown, projection will be calculated during apply")
		return
	}

	// Execute dry-run and compute projection
	if !r.executeDryRunAndProjection(ctx, req, &plannedData, desiredObj, resp) {
		return
	}

	// Check drift and preserve state if needed
	r.checkDriftAndPreserveState(ctx, req, &plannedData, resp)

	// Save the modified plan
	diags = resp.Plan.Set(ctx, &plannedData)
	resp.Diagnostics.Append(diags...)
}

// setProjectionUnknown sets projection to unknown and saves plan
//
// When we can't perform dry-run to predict the result, we set
// managed_state_projection and managed_fields to unknown.
func (r *objectResource) setProjectionUnknown(ctx context.Context, plannedData *objectResourceModel, resp *resource.ModifyPlanResponse, reason string) {
	tflog.Debug(ctx, reason)
	plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)
	plannedData.ManagedFields = types.MapUnknown(types.StringType)
	diags := resp.Plan.Set(ctx, plannedData)
	resp.Diagnostics.Append(diags...)
}

// isCreateOperation checks if this is a create vs update
func isCreateOperation(req resource.ModifyPlanRequest) bool {
	return req.State.Raw.IsNull()
}

// checkDriftAndPreserveState compares projections and preserves state if no changes
func (r *objectResource) checkDriftAndPreserveState(ctx context.Context, req resource.ModifyPlanRequest, plannedData *objectResourceModel, resp *resource.ModifyPlanResponse) {
	// Check if we have state to compare against
	if !req.State.Raw.IsNull() {
		var stateData objectResourceModel
		diags := req.State.Get(ctx, &stateData)
		resp.Diagnostics.Append(diags...)
		if !resp.Diagnostics.HasError() && !stateData.ManagedStateProjection.IsNull() {
			// If projections match, only YAML formatting changed in Kubernetes
			if stateData.ManagedStateProjection.Equal(plannedData.ManagedStateProjection) {
				tflog.Debug(ctx, "No Kubernetes resource changes detected, preserving YAML")
				// Preserve the original YAML and internal fields since no actual changes will occur
				plannedData.YAMLBody = stateData.YAMLBody
				plannedData.ManagedStateProjection = stateData.ManagedStateProjection

				// Preserve object_ref since resource identity hasn't changed
				plannedData.ObjectRef = stateData.ObjectRef

				// Only preserve managed_fields if BOTH:
				// 1. ignore_fields hasn't changed
				// 2. managed_fields hasn't changed (no ownership transitions)
				// When either changes, we must show the predicted managed_fields from dry-run
				ignoreFieldsChanged := !stateData.IgnoreFields.Equal(plannedData.IgnoreFields)
				hasOwnershipTransition := detectOwnershipManagerTransition(ctx, stateData.ManagedFields, plannedData.ManagedFields)

				if !ignoreFieldsChanged && !hasOwnershipTransition {
					plannedData.ManagedFields = stateData.ManagedFields
				}
				// else: leave managed_fields as predicted from dry-run (already set in applyProjection)

				// Note: ImportedWithoutAnnotations is now in private state, not model
				// But still allow terraform-specific settings to update
				// (delete_protection, ignore_fields, etc. are not preserved during import)
			}
		}
	}
}

// executeDryRunAndProjection performs dry-run and calculates field projection
func (r *objectResource) executeDryRunAndProjection(ctx context.Context, req resource.ModifyPlanRequest, plannedData *objectResourceModel, desiredObj *unstructured.Unstructured, resp *resource.ModifyPlanResponse) bool {
	tflog.Debug(ctx, "⚠️ DEBUG: executeDryRunAndProjection - START", map[string]interface{}{
		"object_ref": fmt.Sprintf("%s/%s %s/%s", desiredObj.GetAPIVersion(), desiredObj.GetKind(), desiredObj.GetNamespace(), desiredObj.GetName()),
	})

	// Setup client
	client, err := r.setupDryRunClient(ctx, plannedData, resp)
	if err != nil {
		// Check if this is a CRD-not-found error during plan phase
		if r.isCRDNotFoundError(err) {
			// CRD doesn't exist yet (will be created during apply) - set projection to unknown
			// User can review yaml_body in plan output
			r.setProjectionUnknown(ctx, plannedData, resp,
				"CRD not found during plan: projection will be calculated during apply")
			return true
		}
		return false
	}

	// Perform dry-run
	dryRunResult, err := r.performDryRun(ctx, client, desiredObj, plannedData, resp)
	if err != nil {
		// Check if this is a CRD-not-found error during plan phase
		if r.isCRDNotFoundError(err) {
			// CRD doesn't exist yet (will be created during apply) - set projection to unknown
			// User can review yaml_body in plan output
			r.setProjectionUnknown(ctx, plannedData, resp,
				"CRD not found during dry-run: projection will be calculated during apply")
			return true
		}
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
func (r *objectResource) setupDryRunClient(ctx context.Context, plannedData *objectResourceModel, resp *resource.ModifyPlanResponse) (k8sclient.K8sClient, error) {
	client, err := factory.SetupClient(ctx, plannedData.Cluster, r.clientGetter)
	if err != nil {
		r.setProjectionUnknown(ctx, plannedData, resp,
			fmt.Sprintf("Skipping dry-run due to client setup error: %s", err))
		return nil, err
	}

	return client, nil
}

// calculateProjection determines projection strategy and calculates projection
func (r *objectResource) calculateProjection(ctx context.Context, req resource.ModifyPlanRequest, plannedData *objectResourceModel, desiredObj, dryRunResult *unstructured.Unstructured, client k8sclient.K8sClient, resp *resource.ModifyPlanResponse) bool {
	tflog.Debug(ctx, "⚠️ DEBUG: calculateProjection - START", map[string]interface{}{
		"object_ref": fmt.Sprintf("%s/%s %s/%s", desiredObj.GetAPIVersion(), desiredObj.GetKind(), desiredObj.GetNamespace(), desiredObj.GetName()),
	})

	isCreate := isCreateOperation(req)

	tflog.Debug(ctx, "⚠️ DEBUG: calculateProjection - Operation type determined", map[string]interface{}{
		"object_ref": fmt.Sprintf("%s/%s %s/%s", desiredObj.GetAPIVersion(), desiredObj.GetKind(), desiredObj.GetNamespace(), desiredObj.GetName()),
		"is_create":  isCreate,
	})

	// CREATE operations: Use dry-run result to show accurate preview with K8s defaults
	// This replaces the old behavior of setting projection to unknown
	if isCreate {
		tflog.Debug(ctx, "CREATE - using dry-run result for projection")

		// For CREATE, project all fields from dry-run result (no existing ownership to filter by)
		// The dry-run result contains all the fields we're setting plus K8s defaults
		paths := extractOwnedPaths(ctx, dryRunResult.GetManagedFields(), desiredObj.Object)

		// Apply ignore_fields filtering if specified
		if ignoreFields := getIgnoreFields(ctx, plannedData); ignoreFields != nil {
			paths = filterIgnoredPaths(paths, ignoreFields, desiredObj.Object)
		}

		// Project the dry-run result to show what will be created
		return r.applyProjection(ctx, dryRunResult, paths, plannedData, isCreate, resp)
	}

	// UPDATE operations: Check for ownership transitions BEFORE dry-run
	// We need to compare previous ownership (from private state) vs ACTUAL current ownership (from cluster)
	// NOT predicted ownership (from dry-run with force=true)

	tflog.Debug(ctx, "⚠️ DEBUG: calculateProjection - UPDATE operation, checking ownership transitions", map[string]interface{}{
		"object_ref": fmt.Sprintf("%s/%s %s/%s", desiredObj.GetAPIVersion(), desiredObj.GetKind(), desiredObj.GetNamespace(), desiredObj.GetName()),
	})

	// Fetch actual current state from cluster to detect ownership transitions
	gvr, err := client.DiscoverGVR(ctx, desiredObj.GetAPIVersion(), desiredObj.GetKind())
	if err != nil {
		tflog.Debug(ctx, "Could not discover GVR for ownership transition check", map[string]interface{}{
			"error": err.Error(),
		})
		// Continue without ownership transition check - not critical for plan
	} else {
		currentObj, err := client.Get(ctx, gvr, desiredObj.GetNamespace(), desiredObj.GetName())
		if err != nil {
			tflog.Debug(ctx, "Could not fetch current object for ownership transition check", map[string]interface{}{
				"error": err.Error(),
			})
			// Continue without ownership transition check - not critical for plan
		} else {
			// Extract ACTUAL current ownership from cluster for ALL managers
			// This is critical: we need to see ownership by external-operator, kubectl, etc.
			// to detect transitions, not just k8sconnect-owned fields
			actualOwnershipMap := fieldmanagement.ExtractAllManagedFields(currentObj)

			tflog.Debug(ctx, "PLAN PHASE - Actual current field ownership from cluster (ALL managers)", map[string]interface{}{
				"actual_ownership_map": actualOwnershipMap,
				"object_ref":           fmt.Sprintf("%s/%s %s/%s", desiredObj.GetAPIVersion(), desiredObj.GetKind(), desiredObj.GetNamespace(), desiredObj.GetName()),
			})

			// Check for ownership conflicts using ADR-021 classification
			// Pass nil for stateObj - function will parse from state
			r.detectOwnershipConflicts(ctx, req, resp, actualOwnershipMap, nil, currentObj, desiredObj)
		}
	}

	// Now continue with projection calculation using dry-run result
	// Extract ownership from dry-run result (what ownership WILL BE after apply)
	paths := extractOwnedPaths(ctx, dryRunResult.GetManagedFields(), desiredObj.Object)

	// Apply projection
	return r.applyProjection(ctx, dryRunResult, paths, plannedData, isCreate, resp)
}

// performDryRun executes the dry-run against k8s
func (r *objectResource) performDryRun(ctx context.Context, client k8sclient.K8sClient, desiredObj *unstructured.Unstructured, plannedData *objectResourceModel, resp *resource.ModifyPlanResponse) (*unstructured.Unstructured, error) {
	// Filter ignored fields before dry-run to match what we'll actually apply
	objToApply := desiredObj.DeepCopy()
	if ignoreFields := getIgnoreFields(ctx, plannedData); ignoreFields != nil {
		objToApply = removeFieldsFromObject(objToApply, ignoreFields)
		tflog.Debug(ctx, "Filtered ignore_fields before dry-run", map[string]interface{}{
			"ignored_count": len(ignoreFields),
		})
	}

	dryRunResult, err := client.DryRunApply(ctx, objToApply, k8sclient.ApplyOptions{
		FieldManager:    "k8sconnect",
		Force:           true,
		FieldValidation: "Strict", // ADR-017: Validate fields against OpenAPI schema during plan
	})

	// Surface any API warnings from dry-run operation
	k8sclient.SurfaceK8sWarningsWithIdentity(ctx, client, desiredObj, &resp.Diagnostics)

	if err != nil {
		// ADR-017: Check if this is a field validation error (typos, unknown fields, etc.)
		// Field validation errors should fail immediately with clear error message
		// These are USER errors, not cluster state issues, so don't retry
		if r.isFieldValidationError(err) {
			resourceDesc := fmt.Sprintf("%s/%s %s/%s",
				desiredObj.GetAPIVersion(), desiredObj.GetKind(),
				desiredObj.GetNamespace(), desiredObj.GetName())

			// Use classified error formatting for clear user feedback
			r.addClassifiedError(&resp.Diagnostics, err, "Plan", resourceDesc)

			// Set projection to unknown (can't project invalid resource)
			plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)

			// Return error to stop planning
			return nil, err
		}

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
			plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)

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

// applyProjection projects fields and updates plan
func (r *objectResource) applyProjection(ctx context.Context, dryRunResult *unstructured.Unstructured, paths []string, plannedData *objectResourceModel, isCreate bool, resp *resource.ModifyPlanResponse) bool {
	// Apply ignore_fields filtering if specified
	if ignoreFields := getIgnoreFields(ctx, plannedData); ignoreFields != nil {
		paths = filterIgnoredPaths(paths, ignoreFields, dryRunResult.Object)
		tflog.Debug(ctx, "Applied ignore_fields filtering in plan modifier", map[string]interface{}{
			"ignored_count":  len(ignoreFields),
			"filtered_paths": len(paths),
		})
	}

	// Project the dry-run result
	projection, err := projectFields(dryRunResult.Object, paths)
	if err != nil {
		resp.Diagnostics.AddError("Projection Failed",
			fmt.Sprintf("Failed to project fields for %s: %s", formatResource(dryRunResult), err))
		return false
	}

	// Convert projection to flat map for clean diff display
	projectionMap := flattenProjectionToMap(projection, paths)

	// Convert to types.Map
	mapValue, diags := types.MapValueFrom(ctx, types.StringType, projectionMap)
	if diags.HasError() {
		resp.Diagnostics.AddError("Map Conversion Failed",
			fmt.Sprintf("Failed to convert projection to map for %s: %s", formatResource(dryRunResult), diags.Errors()))
		return false
	}

	// Update the plan with projection
	plannedData.ManagedStateProjection = mapValue

	tflog.Debug(ctx, "Dry-run projection complete", map[string]interface{}{
		"path_count": len(paths),
		"map_size":   len(projectionMap),
	})

	// Handle managed_fields based on operation type
	if isCreate {
		// For CREATE: Set managed_fields to unknown (will be populated after apply)
		// We can't accurately predict managed_fields for CREATE because k8sconnect
		// annotations haven't been added yet
		plannedData.ManagedFields = types.MapUnknown(types.StringType)
		tflog.Debug(ctx, "Set managed_fields to unknown for CREATE operation")
	} else {
		// For UPDATE: Compute managed_fields from dry-run result
		// This is a core feature: predicting exact field ownership using force=true dry-run

		// Extract ALL ownership from dry-run (kubectl, hpa-controller, etc.) not just k8sconnect
		// This is CRITICAL for ADR-019 override to work when k8sconnect doesn't own fields yet
		// (e.g., after import where kubectl owns everything, or ignore_fields modifications
		// where external controllers took ownership). v0.1.7 used ExtractManagedFieldsMap
		// which iterated over ALL managers, not just k8sconnect.
		allOwnership := fieldmanagement.ExtractAllManagedFields(dryRunResult)
		ownershipMap := fieldmanagement.FlattenManagedFields(allOwnership)

		// ADR-019: Override predicted ownership for fields we're applying with force=true
		// Kubernetes dry-run doesn't predict force=true ownership takeover, so we must
		// explicitly recognize that fields we apply with force=true WILL be owned by k8sconnect
		// after the actual apply, regardless of what dry-run's managedFields suggest.
		//
		// Why this is needed: Dry-run shows current ownership state, not post-force ownership.
		// When we apply with force=true, we WILL take ownership, but dry-run doesn't reflect this.
		// This causes "Provider produced inconsistent result" errors when prediction doesn't match
		// actual post-apply ownership.

		// Convert paths to map for efficient lookup
		// The 'paths' variable contains ALL fields we're managing from yaml_body (after ignore_fields filtering)
		// This works for both:
		// - First apply after import (k8sconnect not in managedFields yet)
		// - Drift reclaim (k8sconnect already in managedFields)
		fieldsWeAreSending := make(map[string]bool)
		for _, path := range paths {
			fieldsWeAreSending[path] = true
		}

		// DEBUG: Log what we're working with
		pathsList := make([]string, 0, len(fieldsWeAreSending))
		for p := range fieldsWeAreSending {
			pathsList = append(pathsList, p)
		}
		tflog.Debug(ctx, "Fields we're sending (paths)", map[string]interface{}{
			"paths": pathsList,
		})

		ownershipList := make([]string, 0, len(ownershipMap))
		for p, owner := range ownershipMap {
			ownershipList = append(ownershipList, fmt.Sprintf("%s=%s", p, owner))
		}
		tflog.Debug(ctx, "Current ownership from dry-run", map[string]interface{}{
			"ownership": ownershipList,
		})

		overrideCount := 0
		for path, currentOwner := range ownershipMap {
			// Only override if we're actually sending this field
			if fieldsWeAreSending[path] && currentOwner != "k8sconnect" {
				tflog.Debug(ctx, "Overriding ownership", map[string]interface{}{
					"path": path,
					"from": currentOwner,
					"to":   "k8sconnect",
				})
				ownershipMap[path] = "k8sconnect"
				overrideCount++
			}
		}

		// Note: Parent field removal happens automatically in extractManagedFieldsMap()
		// No need to do it again here

		if overrideCount > 0 {
			tflog.Info(ctx, "Applied force=true ownership prediction overrides", map[string]interface{}{
				"override_count": overrideCount,
			})
		}

		// Filter out status fields - they are not preserved during Apply operations
		// Also filter out K8s system annotations that appear/change unpredictably
		for path := range ownershipMap {
			if strings.HasPrefix(path, "status.") || path == "status" {
				delete(ownershipMap, path)
			}
			// Filter K8s system annotations to avoid plan/apply inconsistencies
			if fieldmanagement.IsKubernetesSystemAnnotation(path) {
				delete(ownershipMap, path)
			}
		}

		// NOTE: We do NOT filter out ignore_fields from managed_fields
		// Users need visibility into who owns ignored fields

		// Set managed_fields to predicted value from dry-run
		predictedManagedFields, diags := types.MapValueFrom(ctx, types.StringType, ownershipMap)
		if !diags.HasError() {
			plannedData.ManagedFields = predictedManagedFields
			tflog.Debug(ctx, "Set managed_fields from dry-run prediction", map[string]interface{}{
				"field_count": len(ownershipMap),
			})
		} else {
			// If conversion fails, mark as unknown
			plannedData.ManagedFields = types.MapUnknown(types.StringType)
		}
	}

	return true
}

// extractManagedFieldsMap extracts field ownership from object and flattens to map[string]string
func extractManagedFieldsMap(ctx context.Context, obj *unstructured.Unstructured) map[string]string {
	// Extract all field ownership
	ownership := fieldmanagement.ExtractAllManagedFields(obj)

	// Flatten using the common logic
	ownershipFlat := fieldmanagement.FlattenManagedFields(ownership)

	// Remove parent field entries when child fields exist
	// Example: If "data.owner" exists, remove "data" from ownership map
	// This prevents parent ownership from overriding child ownership in the final map
	ownershipFlat = removeParentFieldsFromOwnership(ownershipFlat)

	return ownershipFlat
}

// detectOwnershipManagerTransition returns true if any field that exists in BOTH
// state and plan has a different manager (ownership transition).
// This is different from just new fields being added - we only care about manager
// changes on existing fields (e.g., kubectl → k8sconnect).
func detectOwnershipManagerTransition(ctx context.Context, stateOwnership, planOwnership types.Map) bool {
	// If either is null/unknown, no transition to detect
	if stateOwnership.IsNull() || stateOwnership.IsUnknown() ||
		planOwnership.IsNull() || planOwnership.IsUnknown() {
		return false
	}

	// Extract to Go maps
	var stateMap map[string]string
	var planMap map[string]string

	diagsState := stateOwnership.ElementsAs(ctx, &stateMap, false)
	if diagsState.HasError() {
		return false
	}

	diagsPlan := planOwnership.ElementsAs(ctx, &planMap, false)
	if diagsPlan.HasError() {
		return false
	}

	// Check if any field that exists in BOTH has a different manager
	for field, stateManager := range stateMap {
		if planManager, existsInPlan := planMap[field]; existsInPlan {
			if stateManager != planManager {
				// Manager changed on existing field - this is a transition
				tflog.Debug(ctx, "Detected ownership manager transition", map[string]interface{}{
					"field":       field,
					"old_manager": stateManager,
					"new_manager": planManager,
				})
				return true
			}
		}
	}

	// No transitions detected
	return false
}

// removeParentFieldsFromOwnership removes parent field entries when child fields are present
// Example: If ownership has both "data" and "data.owner", remove "data"
func removeParentFieldsFromOwnership(ownership map[string]string) map[string]string {
	result := make(map[string]string)

	// First, copy all entries
	for path, owner := range ownership {
		result[path] = owner
	}

	// For each field, check if there are child fields
	// If so, remove the parent field
	for path := range ownership {
		parts := strings.Split(path, ".")
		// Check all parent paths
		for i := 1; i < len(parts); i++ {
			parentPath := strings.Join(parts[:i], ".")
			// If parent exists in map, remove it (child takes precedence)
			if _, hasParent := result[parentPath]; hasParent {
				delete(result, parentPath)
			}
		}
	}

	return result
}

// detectOwnershipConflicts uses ADR-021's 16-row classification to detect ownership conflicts.
// Only warns on actual conflicts (controller fights), stays silent on normal operations.
// Accepts optional stateObj, currentObj, and desiredObj for extracting field values in warnings.
func (r *objectResource) detectOwnershipConflicts(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse, currentOwnership map[string][]string, stateObj, currentObj, desiredObj *unstructured.Unstructured) {
	// Skip if no state (first apply)
	if req.State.Raw.IsNull() {
		return
	}

	// Get state and planned data
	var stateData, plannedData objectResourceModel
	if diags := req.State.Get(ctx, &stateData); diags.HasError() {
		return
	}
	if diags := req.Plan.Get(ctx, &plannedData); diags.HasError() {
		return
	}

	// Detect config_changed dimension
	configChanged := !stateData.YAMLBody.Equal(plannedData.YAMLBody) ||
		!stateData.IgnoreFields.Equal(plannedData.IgnoreFields)

	tflog.Debug(ctx, "🔍 CONFIG CHANGED", map[string]interface{}{
		"configChanged":       configChanged,
		"yamlBodyChanged":     !stateData.YAMLBody.Equal(plannedData.YAMLBody),
		"ignoreFieldsChanged": !stateData.IgnoreFields.Equal(plannedData.IgnoreFields),
	})

	// Extract baseline ownership from private state
	baselineOwnership, ok := r.extractBaselineOwnership(ctx, req)
	if !ok {
		return
	}

	// Parse stateObj from state yaml_body if not provided
	if stateObj == nil {
		stateObj = r.parseStateObject(ctx, stateData.YAMLBody.ValueString())
	}

	// Build map of fields we're sending in this apply
	fieldsSendingMap := r.buildFieldsSendingMap(ctx, &plannedData)

	// Flatten current ownership for comparison
	currentOwnershipFlat := fieldmanagement.FlattenManagedFields(currentOwnership)

	// Classify all fields and collect conflicts
	conflicts := r.classifyFieldConflicts(ctx, currentOwnershipFlat, baselineOwnership, fieldsSendingMap,
		configChanged, stateObj, currentObj, desiredObj)

	// Emit warnings (resource-level aggregation) with resource identity to prevent collapsing
	if conflicts.HasConflicts() {
		// Extract resource identity from desiredObj (or fallback to currentObj/stateObj)
		var kind, namespace, name string
		if desiredObj != nil {
			kind = desiredObj.GetKind()
			namespace = desiredObj.GetNamespace()
			name = desiredObj.GetName()
		} else if currentObj != nil {
			kind = currentObj.GetKind()
			namespace = currentObj.GetNamespace()
			name = currentObj.GetName()
		} else if stateObj != nil {
			kind = stateObj.GetKind()
			namespace = stateObj.GetNamespace()
			name = stateObj.GetName()
		}

		for _, warning := range conflicts.FormatWarnings(kind, namespace, name) {
			resp.Diagnostics.AddWarning(warning.Summary, warning.Detail)
		}
	}
}

// extractBaselineOwnership retrieves and parses the ownership baseline from private state
func (r *objectResource) extractBaselineOwnership(ctx context.Context, req resource.ModifyPlanRequest) (map[string]string, bool) {
	ownershipBaselineJSON, diags := req.Private.GetKey(ctx, "ownership_baseline")
	if diags.HasError() || ownershipBaselineJSON == nil {
		tflog.Debug(ctx, "No ownership baseline in private state, skipping conflict detection")
		return nil, false
	}

	var baselineOwnership map[string]string
	if err := json.Unmarshal(ownershipBaselineJSON, &baselineOwnership); err != nil {
		tflog.Debug(ctx, "Failed to parse ownership baseline from private state", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, false
	}

	if len(baselineOwnership) == 0 {
		return nil, false
	}

	return baselineOwnership, true
}

// parseStateObject parses the state YAML body into an unstructured object
func (r *objectResource) parseStateObject(ctx context.Context, stateYAML string) *unstructured.Unstructured {
	if stateYAML == "" {
		return nil
	}

	obj, err := r.parseYAML(stateYAML)
	if err != nil {
		tflog.Debug(ctx, "Failed to parse state yaml_body for value extraction", map[string]interface{}{
			"error": err.Error(),
		})
		return nil
	}

	return obj
}

// buildFieldsSendingMap creates a map of fields we're sending in this apply
func (r *objectResource) buildFieldsSendingMap(ctx context.Context, plannedData *objectResourceModel) map[string]bool {
	fieldsSendingMap := make(map[string]bool)

	yamlStr := plannedData.YAMLBody.ValueString()
	if yamlStr == "" {
		return fieldsSendingMap
	}

	desiredObj, err := r.parseYAML(yamlStr)
	if err != nil {
		return fieldsSendingMap
	}

	// Get all field paths from desired object
	allPaths := extractOwnedPaths(ctx, []metav1.ManagedFieldsEntry{}, desiredObj.Object)

	// Filter out ignore_fields
	ignoreFields := getIgnoreFields(ctx, plannedData)
	var pathsToSend []string
	if ignoreFields != nil {
		pathsToSend = filterIgnoredPaths(allPaths, ignoreFields, desiredObj.Object)
	} else {
		pathsToSend = allPaths
	}

	// Build map for fast lookup
	for _, path := range pathsToSend {
		fieldsSendingMap[path] = true
	}

	tflog.Debug(ctx, "Detected fields we're sending in yaml_body", map[string]interface{}{
		"field_count": len(fieldsSendingMap),
	})

	return fieldsSendingMap
}

// classifyFieldConflicts classifies all fields and builds conflict detector
func (r *objectResource) classifyFieldConflicts(ctx context.Context,
	currentOwnershipFlat, baselineOwnership map[string]string,
	fieldsSendingMap map[string]bool, configChanged bool,
	stateObj, currentObj, desiredObj *unstructured.Unstructured) *ownership.ConflictDetection {

	conflicts := ownership.NewConflictDetection()

	for fieldPath, currentManager := range currentOwnershipFlat {
		baselineManager, existedInBaseline := baselineOwnership[fieldPath]

		// Calculate the 4 boolean dimensions
		prevOwned := existedInBaseline && stringSliceContains([]string{baselineManager}, "k8sconnect")
		nowOwned := fieldsSendingMap[fieldPath] || stringSliceContains([]string{currentManager}, "k8sconnect")
		externalChanged := r.detectExternalChange(existedInBaseline, baselineManager, currentManager)

		// Classify conflict type
		conflictType := ownership.ClassifyConflict(prevOwned, nowOwned, configChanged, externalChanged)

		// DEBUG: Log classification for ALL fields
		tflog.Debug(ctx, "🔍 CLASSIFY FIELD", map[string]interface{}{
			"field":           fieldPath,
			"prevOwned":       prevOwned,
			"nowOwned":        nowOwned,
			"configChanged":   configChanged,
			"externalChanged": externalChanged,
			"conflictType":    conflictType.String(),
			"baselineManager": baselineManager,
			"currentManager":  currentManager,
			"inBaseline":      existedInBaseline,
			"sendingField":    fieldsSendingMap[fieldPath],
		})

		// Add to conflict detector if not NoConflict
		if conflictType != ownership.NoConflict {
			fieldChange := r.createFieldChange(fieldPath, baselineManager, currentManager,
				stateObj, currentObj, desiredObj)
			conflicts.AddField(conflictType, fieldChange)
		}
	}

	return conflicts
}

// detectExternalChange determines if an external manager modified/owns a field
func (r *objectResource) detectExternalChange(existedInBaseline bool, baselineManager, currentManager string) bool {
	// Case 1: Field was in baseline and manager changed to someone else
	if existedInBaseline && baselineManager != currentManager && currentManager != "k8sconnect" {
		return true
	}
	// Case 2: Field is NEW to us (not in baseline) but external already owns it
	if !existedInBaseline && currentManager != "k8sconnect" && currentManager != "" {
		return true
	}
	return false
}

// createFieldChange creates a FieldChange with values extracted from objects
func (r *objectResource) createFieldChange(fieldPath, baselineManager, currentManager string,
	stateObj, currentObj, desiredObj *unstructured.Unstructured) ownership.FieldChange {

	fieldChange := ownership.FieldChange{
		Path:            fieldPath,
		PreviousManager: baselineManager,
		CurrentManager:  currentManager,
		PlannedManager:  "k8sconnect",
	}

	// Extract field values if objects are available
	if stateObj != nil {
		fieldChange.PreviousValue = getFieldValue(stateObj, fieldPath)
	}
	if currentObj != nil {
		fieldChange.CurrentValue = getFieldValue(currentObj, fieldPath)
	}
	if desiredObj != nil {
		fieldChange.PlannedValue = getFieldValue(desiredObj, fieldPath)
	}

	return fieldChange
}

// stringSliceContains checks if a string slice contains a value
func stringSliceContains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}

// getFieldValue extracts a field value from an unstructured object by JSON path
func getFieldValue(obj *unstructured.Unstructured, fieldPath string) interface{} {
	if obj == nil {
		return nil
	}

	pathParts := strings.Split(fieldPath, ".")
	var current interface{} = obj.Object

	for _, part := range pathParts {
		// Handle map access
		if m, ok := current.(map[string]interface{}); ok {
			current = m[part]
		} else {
			return nil
		}
	}

	return current
}

// setObjectRefFromDesiredObj populates object_ref from the parsed resource during plan phase
// This prevents object_ref from showing as "(known after apply)" when only non-identity fields change
// IMPORTANT: Only call this when connection is ready - we need to query cluster for namespace scoping
func (r *objectResource) setObjectRefFromDesiredObj(ctx context.Context, obj *unstructured.Unstructured, data *objectResourceModel) error {
	objRef := objectRefModel{
		APIVersion: types.StringValue(obj.GetAPIVersion()),
		Kind:       types.StringValue(obj.GetKind()),
		Name:       types.StringValue(obj.GetName()),
	}

	// Namespace handling:
	// 1. Check hardcoded list of common cluster-scoped resources (fast path, covers 95% of cases)
	// 2. For unknown resources: Query cluster to determine scope (handles custom resources)
	// 3. For namespace-scoped resources:
	//    - If namespace explicitly set in YAML, use it
	//    - If empty, default to "default" (matches K8s/k8sclient behavior)
	// 4. For cluster-scoped resources:
	//    - Strip namespace from object (K8s ignores it anyway)
	//    - Set object_ref.namespace to null (matches what K8s returns)

	var isNamespaced bool

	// Fast path: Use hardcoded list for common cluster-scoped resources
	// This avoids discovery queries for standard Kubernetes resources and works during bootstrap
	if k8sclient.IsClusterScopedResource(obj.GetAPIVersion(), obj.GetKind()) {
		isNamespaced = false
	} else {
		// Unknown resource type - query the cluster
		conn, err := r.convertObjectToConnectionModel(ctx, data.Cluster)
		if err != nil {
			return fmt.Errorf("failed to convert connection for namespace detection: %w", err)
		}

		client, err := r.clientGetter(conn)
		if err != nil {
			return fmt.Errorf("failed to create client for namespace detection: %w", err)
		}

		isNamespaced, err = client.IsResourceNamespaced(ctx, obj.GetAPIVersion(), obj.GetKind())
		if err != nil {
			// Return error as-is so caller can check if it's CRD-not-found
			// This handles the case where a Custom Resource's CRD doesn't exist yet
			return err
		}
	}

	if isNamespaced {
		// Namespace-scoped resource
		ns := obj.GetNamespace()
		if ns == "" {
			// No explicit namespace -> K8s defaults to "default"
			ns = "default"
			obj.SetNamespace(ns)
		}
		objRef.Namespace = types.StringValue(ns)
	} else {
		// Cluster-scoped resource
		originalNs := obj.GetNamespace()
		if originalNs != "" {
			// User specified namespace on cluster-scoped resource - this is invalid
			// Kubernetes will ignore it, so we need to strip it and warn the user
			tflog.Warn(ctx, "Namespace specified for cluster-scoped resource will be ignored by Kubernetes", map[string]interface{}{
				"kind":      obj.GetKind(),
				"name":      obj.GetName(),
				"namespace": originalNs,
			})
		}
		// Strip namespace field to match what K8s returns
		obj.SetNamespace("")
		objRef.Namespace = types.StringNull()
	}

	// Convert to types.Object
	objRefValue, diags := types.ObjectValueFrom(ctx, map[string]attr.Type{
		"api_version": types.StringType,
		"kind":        types.StringType,
		"name":        types.StringType,
		"namespace":   types.StringType,
	}, objRef)

	if diags.HasError() {
		return fmt.Errorf("failed to convert object_ref to types.Object: %v", diags)
	}

	data.ObjectRef = objRefValue
	return nil
}
