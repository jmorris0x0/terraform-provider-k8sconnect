package object

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
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
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)

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
			plannedData.FieldOwnership = types.MapUnknown(types.StringType)

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
				resp.Diagnostics.AddError("Failed to populate object_ref",
					fmt.Sprintf("Failed to populate object_ref during plan: %s", err))
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
// managed_state_projection and field_ownership to unknown.
func (r *objectResource) setProjectionUnknown(ctx context.Context, plannedData *objectResourceModel, resp *resource.ModifyPlanResponse, reason string) {
	tflog.Debug(ctx, reason)
	plannedData.ManagedStateProjection = types.MapUnknown(types.StringType)
	plannedData.FieldOwnership = types.MapUnknown(types.StringType)
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

				// Only preserve field_ownership if ignore_fields hasn't changed
				// When ignore_fields changes, field_ownership will change even if projection doesn't
				ignoreFieldsChanged := !stateData.IgnoreFields.Equal(plannedData.IgnoreFields)
				if !ignoreFieldsChanged {
					plannedData.FieldOwnership = stateData.FieldOwnership
				}
				// else: leave field_ownership as predicted from dry-run (already set in applyProjection)

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
	// Convert connection
	conn, err := r.convertObjectToConnectionModel(ctx, plannedData.Cluster)
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
			actualOwnershipMap := extractAllFieldOwnership(currentObj)

			tflog.Debug(ctx, "PLAN PHASE - Actual current field ownership from cluster (ALL managers)", map[string]interface{}{
				"actual_ownership_map": actualOwnershipMap,
				"object_ref":           fmt.Sprintf("%s/%s %s/%s", desiredObj.GetAPIVersion(), desiredObj.GetKind(), desiredObj.GetNamespace(), desiredObj.GetName()),
			})

			// Check for ownership transitions using ACTUAL current ownership
			r.checkOwnershipTransitions(ctx, req, resp, actualOwnershipMap)
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
	surfaceK8sWarnings(ctx, client, desiredObj, &resp.Diagnostics)

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

	// Handle field_ownership based on operation type
	if isCreate {
		// For CREATE: Set field_ownership to unknown (will be populated after apply)
		// We can't accurately predict field_ownership for CREATE because k8sconnect
		// annotations haven't been added yet
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)
		tflog.Debug(ctx, "Set field_ownership to unknown for CREATE operation")
	} else {
		// For UPDATE: Compute field_ownership from dry-run result
		// This is a core feature: predicting exact field ownership using force=true dry-run

		// Extract ALL ownership from dry-run (kubectl, hpa-controller, etc.) not just k8sconnect
		// This is CRITICAL for ADR-019 override to work when k8sconnect doesn't own fields yet
		// (e.g., after import where kubectl owns everything, or ignore_fields modifications
		// where external controllers took ownership). v0.1.7 used ExtractFieldOwnershipMap
		// which iterated over ALL managers, not just k8sconnect.
		allOwnership := extractAllFieldOwnership(dryRunResult)
		ownershipMap := fieldmanagement.FlattenFieldOwnership(allOwnership)

		// ADR-019: Override predicted ownership for fields we're applying with force=true
		// Kubernetes dry-run doesn't predict force=true ownership takeover, so we must
		// explicitly recognize that fields we apply with force=true WILL be owned by k8sconnect
		// after the actual apply, regardless of what dry-run's managedFields suggest.
		//
		// Why this is needed: Dry-run shows current ownership state, not post-force ownership.
		// When we apply with force=true, we WILL take ownership, but dry-run doesn't reflect this.
		// This causes "Provider produced inconsistent result" errors when prediction doesn't match
		// actual post-apply ownership.

		// Extract fields that k8sconnect is sending (from dry-run's k8sconnect manager entry)
		fieldsWeAreSending := make(map[string]bool)
		for _, mf := range dryRunResult.GetManagedFields() {
			if mf.Manager == "k8sconnect" && mf.FieldsV1 != nil {
				// Parse k8sconnect's FieldsV1 to get the paths we're sending
				k8sconnectOwnership := parseFieldsV1ToPathMap([]metav1.ManagedFieldsEntry{mf}, dryRunResult.Object)
				for path := range k8sconnectOwnership {
					fieldsWeAreSending[path] = true
				}
				break
			}
		}

		overrideCount := 0
		for path, currentOwner := range ownershipMap {
			// Only override if we're actually sending this field
			if fieldsWeAreSending[path] && currentOwner != "k8sconnect" {
				ownershipMap[path] = "k8sconnect"
				overrideCount++
			}
		}

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

		// NOTE: We do NOT filter out ignore_fields from field_ownership
		// Users need visibility into who owns ignored fields

		// Set field_ownership to predicted value from dry-run
		predictedFieldOwnership, diags := types.MapValueFrom(ctx, types.StringType, ownershipMap)
		if !diags.HasError() {
			plannedData.FieldOwnership = predictedFieldOwnership
			tflog.Debug(ctx, "Set field_ownership from dry-run prediction", map[string]interface{}{
				"field_count": len(ownershipMap),
			})
		} else {
			// If conversion fails, mark as unknown
			plannedData.FieldOwnership = types.MapUnknown(types.StringType)
		}
	}

	return true
}

// extractFieldOwnershipMap extracts field ownership from object and flattens to map[string]string
func extractFieldOwnershipMap(ctx context.Context, obj *unstructured.Unstructured) map[string]string {
	// Extract all field ownership
	ownership := extractAllFieldOwnership(obj)

	// Flatten using the common logic
	return fieldmanagement.FlattenFieldOwnership(ownership)
}

// checkOwnershipTransitions compares previous vs current field ownership and warns about transitions
func (r *objectResource) checkOwnershipTransitions(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse, currentOwnership map[string][]string) {
	tflog.Debug(ctx, "⚠️ DEBUG: checkOwnershipTransitions - START", map[string]interface{}{
		"current_ownership_map": currentOwnership,
		"current_field_count":   len(currentOwnership),
	})

	// Skip if no state (first apply)
	if req.State.Raw.IsNull() {
		tflog.Debug(ctx, "⚠️ DEBUG: checkOwnershipTransitions - No state yet - skipping transition check")
		return
	}

	// Get previous ownership from state attribute
	var stateData objectResourceModel
	diags := req.State.Get(ctx, &stateData)
	if diags.HasError() {
		// Error reading state
		tflog.Warn(ctx, "⚠️ DEBUG: Failed to read state - skipping transition check", map[string]interface{}{
			"diagnostics": diags,
		})
		return
	}

	// Extract previous ownership map from state
	var previousOwnershipFlat map[string]string
	if !stateData.FieldOwnership.IsNull() && !stateData.FieldOwnership.IsUnknown() {
		d := stateData.FieldOwnership.ElementsAs(ctx, &previousOwnershipFlat, false)
		if d.HasError() {
			tflog.Warn(ctx, "Failed to extract previous field ownership from state", map[string]interface{}{
				"diagnostics": d,
			})
			return
		}
	}

	if previousOwnershipFlat == nil || len(previousOwnershipFlat) == 0 {
		// No previous ownership tracked - first apply or imported resource
		tflog.Debug(ctx, "⚠️ DEBUG: checkOwnershipTransitions - No previous ownership in state - skipping transition check")
		return
	}

	// Flatten current ownership for comparison
	currentOwnershipFlat := fieldmanagement.FlattenFieldOwnership(currentOwnership)

	tflog.Debug(ctx, "⚠️ DEBUG: checkOwnershipTransitions - Comparing ownership", map[string]interface{}{
		"previous_ownership_map": previousOwnershipFlat,
		"previous_field_count":   len(previousOwnershipFlat),
		"current_ownership_map":  currentOwnershipFlat,
		"current_field_count":    len(currentOwnershipFlat),
	})

	// Find ownership transitions (fields that changed owner)
	var transitions []ownershipTransition

	// Check all paths in current ownership
	for path, currentOwner := range currentOwnershipFlat {
		previousOwner, existed := previousOwnershipFlat[path]

		// If ownership changed, record transition
		if !existed || previousOwner != currentOwner {
			tflog.Debug(ctx, "⚠️ DEBUG: checkOwnershipTransitions - TRANSITION DETECTED", map[string]interface{}{
				"path":            path,
				"previous_owner":  previousOwner,
				"current_owner":   currentOwner,
				"prev_owned_by_us": existed && previousOwner == "k8sconnect",
				"now_owned_by_us":  currentOwner == "k8sconnect",
			})

			// Build owner lists for the transition message (backward compatibility)
			previousOwners := []string{}
			if existed {
				previousOwners = append(previousOwners, previousOwner)
			}
			currentOwners := []string{currentOwner}

			transitions = append(transitions, ownershipTransition{
				Path:           path,
				PreviousOwners: previousOwners,
				CurrentOwners:  currentOwners,
			})
		}
	}

	// Check for fields that were removed (in previousOwnershipFlat but not in currentOwnershipFlat)
	// This can happen when ignore_fields changes or fields are removed from YAML
	for path, previousOwner := range previousOwnershipFlat {
		if _, exists := currentOwnershipFlat[path]; !exists {
			// Field was previously owned but is no longer tracked
			// Only report if it was previously owned by k8sconnect
			if previousOwner == "k8sconnect" {
				tflog.Debug(ctx, "Field ownership released", map[string]interface{}{
					"path":           path,
					"previous_owner": previousOwner,
				})
			}
		}
	}

	// Emit warnings for ownership transitions
	if len(transitions) > 0 {
		tflog.Debug(ctx, "⚠️ DEBUG: checkOwnershipTransitions - Calling addOwnershipTransitionWarning", map[string]interface{}{
			"transition_count": len(transitions),
			"transitions":      transitions,
		})
		addOwnershipTransitionWarning(resp, transitions)
		tflog.Debug(ctx, "⚠️ DEBUG: checkOwnershipTransitions - Warning added to diagnostics")
	} else {
		tflog.Debug(ctx, "⚠️ DEBUG: checkOwnershipTransitions - NO TRANSITIONS FOUND", map[string]interface{}{
			"previous_ownership_map": previousOwnershipFlat,
			"current_ownership_map":  currentOwnershipFlat,
		})
	}
}

// ownershipTransition represents a field whose ownership changed
type ownershipTransition struct {
	Path           string
	PreviousOwners []string
	CurrentOwners  []string
}

// addOwnershipTransitionWarning emits a warning about field ownership transitions
func addOwnershipTransitionWarning(resp *resource.ModifyPlanResponse, transitions []ownershipTransition) {
	var details []string
	for _, t := range transitions {
		// Format the transition showing all managers
		prevStr := strings.Join(t.PreviousOwners, ", ")
		if prevStr == "" {
			prevStr = "(none)"
		}
		currStr := strings.Join(t.CurrentOwners, ", ")

		// Show the future transition
		// k8sconnect will take ownership using force=true during apply
		details = append(details, fmt.Sprintf("  • %s: [%s] → [%s]", t.Path, prevStr, currStr))
	}

	warningMessage := fmt.Sprintf("Field ownership will change if you apply:\n%s\n\n"+
		"k8sconnect will take or maintain ownership using force=true. "+
		"Other controllers may attempt to reclaim these fields, causing drift.",
		strings.Join(details, "\n"))

	tflog.Warn(context.Background(), "⚠️ DEBUG: addOwnershipTransitionWarning - ADDING WARNING TO DIAGNOSTICS", map[string]interface{}{
		"warning_summary":  "Field Ownership Transition",
		"warning_detail":   warningMessage,
		"transition_count": len(transitions),
	})

	resp.Diagnostics.AddWarning(
		"Field Ownership Transition",
		warningMessage,
	)

	tflog.Warn(context.Background(), "⚠️ DEBUG: addOwnershipTransitionWarning - WARNING ADDED", map[string]interface{}{
		"diagnostics_has_error":   resp.Diagnostics.HasError(),
		"diagnostics_error_count": len(resp.Diagnostics.Errors()),
		"diagnostics_warn_count":  len(resp.Diagnostics.Warnings()),
	})
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
