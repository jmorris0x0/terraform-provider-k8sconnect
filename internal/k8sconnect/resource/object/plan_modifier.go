package object

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ModifyPlan implements resource.ResourceWithModifyPlan
func (r *objectResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Skip during destroy
	if req.Plan.Raw.IsNull() {
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
	if !r.isConnectionReady(plannedData.ClusterConnection) {
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

	// Check field ownership conflicts for updates
	if !req.State.Raw.IsNull() {
		r.checkFieldOwnershipConflicts(ctx, req, resp)
	}
}

// setProjectionUnknown sets projection and field_ownership to unknown and saves plan
//
// IMPORTANT: When we can't perform dry-run to predict the result, we must set BOTH
// managed_state_projection AND field_ownership to unknown. If we only set projection
// to unknown but leave field_ownership as-is (from imported state), Terraform will
// detect an inconsistency when apply adds k8sconnect annotations to field_ownership.
//
// BUG FIX: This was causing "Provider produced inconsistent result after apply" errors
// after importing kubectl-created resources, because:
// 1. Import sets field_ownership to kubectl's ownership (no k8sconnect annotations)
// 2. Dry-run fails for some reason (CRD not found, client error, etc.)
// 3. setProjectionUnknown was called but only set projection, not field_ownership
// 4. Apply added k8sconnect annotations to the resource
// 5. Terraform saw field_ownership changed from plan (kubectl only) to apply result (kubectl + k8sconnect)
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

				// Only preserve field_ownership if ignore_fields hasn't changed
				// When ignore_fields changes, field_ownership will change even if projection doesn't
				ignoreFieldsChanged := !stateData.IgnoreFields.Equal(plannedData.IgnoreFields)
				if !ignoreFieldsChanged {
					plannedData.FieldOwnership = stateData.FieldOwnership
				}
				// else: leave field_ownership as Unknown (default), Apply will compute it

				// Preserve object_ref since resource identity hasn't changed
				plannedData.ObjectRef = stateData.ObjectRef

				// Note: ImportedWithoutAnnotations is now in private state, not model
				// But still allow terraform-specific settings to update
				// (delete_protection, ignore_fields, etc. are not preserved during import)
			}
		}
	}
}

// executeDryRunAndProjection performs dry-run and calculates field projection
func (r *objectResource) executeDryRunAndProjection(ctx context.Context, req resource.ModifyPlanRequest, plannedData *objectResourceModel, desiredObj *unstructured.Unstructured, resp *resource.ModifyPlanResponse) bool {
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
func (r *objectResource) calculateProjection(ctx context.Context, req resource.ModifyPlanRequest, plannedData *objectResourceModel, desiredObj, dryRunResult *unstructured.Unstructured, client k8sclient.K8sClient, resp *resource.ModifyPlanResponse) bool {
	isCreate := isCreateOperation(req)

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

		// Set field_ownership to unknown for CREATE (will be populated after apply)
		plannedData.FieldOwnership = types.MapUnknown(types.StringType)

		// Project the dry-run result to show what will be created
		return r.applyProjection(ctx, dryRunResult, paths, plannedData, resp)
	}

	// UPDATE operations: Use field ownership from dry-run result
	// Extract ownership from dry-run result (what ownership WILL BE after apply)
	paths := extractOwnedPaths(ctx, dryRunResult.GetManagedFields(), desiredObj.Object)

	// Extract predicted field ownership from dry-run for plan
	predictedOwnership := parseFieldsV1ToPathMap(dryRunResult.GetManagedFields(), desiredObj.Object)

	// Convert to types.Map for Terraform
	ownershipMap := make(map[string]string)
	for path, ownership := range predictedOwnership {
		ownershipMap[path] = ownership.Manager
	}

	// FIX: Override prediction for fields we'll forcibly take ownership of
	// Dry-run with force=true doesn't predict ownership takeover in managedFields.
	// We use force=true during apply, so we WILL take ownership of all fields we're applying.
	// Get the list of fields we're actually applying (desiredObj with ignored fields removed)
	objToApply := desiredObj.DeepCopy()
	if ignoreFields := getIgnoreFields(ctx, plannedData); ignoreFields != nil {
		objToApply = removeFieldsFromObject(objToApply, ignoreFields)
	}
	appliedFieldsList := extractAllFieldsFromYAML(objToApply.Object, "")
	// Normalize paths to match ownershipMap format (merge keys -> array indexes)
	appliedFields := make(map[string]bool)
	for _, path := range appliedFieldsList {
		normalized := normalizePathForComparison(path, objToApply.Object)
		appliedFields[normalized] = true
	}

	// Override ownership prediction for fields we're applying
	// Only override fields that:
	// 1. Exist in ownershipMap (dry-run knows about them)
	// 2. Have a non-k8sconnect owner (need correction)
	// 3. Are in appliedFields (terraform is actually applying them, not controller-managed/ignored)
	for path, currentOwner := range ownershipMap {
		if currentOwner != "k8sconnect" && appliedFields[path] {
			ownershipMap[path] = "k8sconnect"
		}
	}

	// Internal annotations (k8sconnect.terraform.io/*) are intentionally NOT tracked in field_ownership
	// They exist in the cluster but are filtered from state to avoid unnecessary drift detection
	// These are implementation details, not user-managed fields

	// Filter out status fields - they are not preserved during Apply operations
	// Status is managed by controllers after apply, not during apply
	for path := range ownershipMap {
		if strings.HasPrefix(path, "status.") || path == "status" {
			delete(ownershipMap, path)
		}
	}

	// Note: We do NOT filter out ignored fields from field_ownership
	// Users need to see who owns ignored fields for visibility

	// Set field_ownership to predicted value from dry-run
	predictedFieldOwnership, diags := types.MapValueFrom(ctx, types.StringType, ownershipMap)
	if !diags.HasError() {
		plannedData.FieldOwnership = predictedFieldOwnership
		tflog.Debug(ctx, "Set field_ownership from dry-run prediction", map[string]interface{}{
			"field_count": len(ownershipMap),
		})
	}

	// Apply projection
	return r.applyProjection(ctx, dryRunResult, paths, plannedData, resp)
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
func (r *objectResource) applyProjection(ctx context.Context, dryRunResult *unstructured.Unstructured, paths []string, plannedData *objectResourceModel, resp *resource.ModifyPlanResponse) bool {
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

	return true
}

// checkFieldOwnershipConflicts detects when fields managed by other controllers are being changed
func (r *objectResource) checkFieldOwnershipConflicts(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Get state and plan projections
	var stateData, planData objectResourceModel
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
		userWantsPaths = filterIgnoredPaths(userWantsPaths, ignoreFields, desiredObj.Object)
	}

	// Normalize user paths to match ownership map format (merge keys -> array indexes)
	normalizedUserPaths := make(map[string]string) // normalized -> original
	for _, userPath := range userWantsPaths {
		normalized := normalizePathForComparison(userPath, desiredObj.Object)
		normalizedUserPaths[normalized] = userPath
	}

	// Check each path the user wants against ownership
	var conflicts []FieldConflict
	for normalizedPath, originalPath := range normalizedUserPaths {
		// Skip metadata fields that are always owned by us
		if strings.HasPrefix(normalizedPath, "metadata.annotations.k8sconnect.terraform.io/") {
			continue
		}
		// Skip core fields that don't have ownership
		if normalizedPath == "apiVersion" || normalizedPath == "kind" || normalizedPath == "metadata.name" || normalizedPath == "metadata.namespace" {
			continue
		}

		if owner, exists := ownership[normalizedPath]; exists {
			if owner.Manager != "k8sconnect" {
				conflicts = append(conflicts, FieldConflict{
					Path:  originalPath,
					Owner: owner.Manager,
				})
			}
		}
	}

	if len(conflicts) > 0 {
		// field_ownership is already set from dry-run prediction in calculateProjection
		// Just add the warning about conflicts
		addConflictWarning(resp, conflicts)
	}
}

// FieldConflict represents a field that user wants but is owned by another controller
type FieldConflict struct {
	Path  string
	Owner string
}

// normalizePathForComparison converts paths with merge keys like "containers[name=nginx]"
// to array index format like "containers[0]" by resolving the merge key in the actual object.
// This allows comparing paths from extractFieldPaths (which uses merge keys) with paths
// from field ownership (which uses array indexes).
func normalizePathForComparison(path string, obj map[string]interface{}) string {
	// Split path into segments
	segments := strings.Split(path, ".")
	var normalizedSegments []string
	currentObj := interface{}(obj)

	for _, segment := range segments {
		// Check if this segment contains a merge key pattern like "containers[name=nginx]"
		if strings.Contains(segment, "[") && strings.Contains(segment, "=") {
			// Extract field name and merge key
			parts := strings.SplitN(segment, "[", 2)
			fieldName := parts[0]
			mergeKeyPart := strings.TrimSuffix(parts[1], "]")

			// Parse merge key (e.g., "name=nginx" -> key="name", value="nginx")
			mergeKeyParts := strings.SplitN(mergeKeyPart, "=", 2)
			if len(mergeKeyParts) != 2 {
				// Malformed merge key, use as-is
				normalizedSegments = append(normalizedSegments, segment)
				continue
			}
			mergeKey := mergeKeyParts[0]
			mergeValue := mergeKeyParts[1]

			// Navigate to the array field in the current object
			if objMap, ok := currentObj.(map[string]interface{}); ok {
				if arrayVal, ok := objMap[fieldName]; ok {
					if array, ok := arrayVal.([]interface{}); ok {
						// Find the index of the array element with matching merge key
						foundIndex := -1
						for i, item := range array {
							if itemMap, ok := item.(map[string]interface{}); ok {
								if val, ok := itemMap[mergeKey]; ok {
									if fmt.Sprintf("%v", val) == mergeValue {
										foundIndex = i
										currentObj = item
										break
									}
								}
							}
						}
						if foundIndex >= 0 {
							normalizedSegments = append(normalizedSegments, fmt.Sprintf("%s[%d]", fieldName, foundIndex))
							continue
						}
					}
				}
			}

			// Couldn't resolve, use as-is
			normalizedSegments = append(normalizedSegments, segment)
		} else if strings.Contains(segment, "[") {
			// Already has array index like "containers[0]", use as-is
			normalizedSegments = append(normalizedSegments, segment)
			// Navigate to that array element
			parts := strings.SplitN(segment, "[", 2)
			fieldName := parts[0]
			indexStr := strings.TrimSuffix(parts[1], "]")
			var idx int
			if _, err := fmt.Sscanf(indexStr, "%d", &idx); err == nil && idx >= 0 {
				if objMap, ok := currentObj.(map[string]interface{}); ok {
					if arrayVal, ok := objMap[fieldName]; ok {
						if array, ok := arrayVal.([]interface{}); ok {
							if idx < len(array) {
								currentObj = array[idx]
							}
						}
					}
				}
			}
		} else {
			// Regular field, navigate deeper
			normalizedSegments = append(normalizedSegments, segment)
			if objMap, ok := currentObj.(map[string]interface{}); ok {
				currentObj = objMap[segment]
			}
		}
	}

	return strings.Join(normalizedSegments, ".")
}

func addConflictWarning(resp *resource.ModifyPlanResponse, conflicts []FieldConflict) {
	// Always warn about conflicts - we will force ownership during apply
	var conflictDetails []string
	var conflictPaths []string
	for _, c := range conflicts {
		conflictDetails = append(conflictDetails, fmt.Sprintf("  - %s (managed by \"%s\")", c.Path, c.Owner))
		conflictPaths = append(conflictPaths, c.Path)
	}

	// Format ignore_fields suggestion
	ignoreFieldsSuggestion := formatIgnoreFieldsSuggestion(conflictPaths)

	resp.Diagnostics.AddWarning(
		"Field Ownership Override",
		fmt.Sprintf("Forcing ownership of fields managed by other controllers:\n%s\n\n"+
			"These fields will be forcibly taken over. The other controllers may fight back.\n\n"+
			"To release ownership and allow other controllers to manage these fields, add:\n\n%s",
			strings.Join(conflictDetails, "\n"), ignoreFieldsSuggestion),
	)
}

// formatIgnoreFieldsSuggestion creates a ready-to-use ignore_fields configuration from conflict paths
func formatIgnoreFieldsSuggestion(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	if len(paths) == 1 {
		return fmt.Sprintf("  ignore_fields = [\"%s\"]", paths[0])
	}

	// Multiple paths - format as multi-line for readability
	var lines []string
	lines = append(lines, "  ignore_fields = [")
	for i, path := range paths {
		if i < len(paths)-1 {
			lines = append(lines, fmt.Sprintf("    \"%s\",", path))
		} else {
			lines = append(lines, fmt.Sprintf("    \"%s\"", path))
		}
	}
	lines = append(lines, "  ]")
	return strings.Join(lines, "\n")
}
