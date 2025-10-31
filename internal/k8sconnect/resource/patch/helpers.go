package patch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
)

// setupClient creates a Kubernetes client from the patch resource's connection configuration
func (r *patchResource) setupClient(ctx context.Context, data *patchResourceModel, diagnostics *diag.Diagnostics) (k8sclient.K8sClient, error) {
	// Convert connection object to model
	conn, err := auth.ObjectToConnectionModel(ctx, data.Cluster)
	if err != nil {
		diagnostics.AddError(
			"Invalid Connection Configuration",
			fmt.Sprintf("Failed to parse cluster: %s", err))
		return nil, err
	}

	// Use the client getter to create client
	client, err := r.clientGetter(conn)
	if err != nil {
		diagnostics.AddError(
			"Failed to Create Kubernetes Client",
			fmt.Sprintf("Could not connect to cluster: %s", err))
		return nil, err
	}

	return client, nil
}

// getTargetResource retrieves the target resource from Kubernetes
func (r *patchResource) getTargetResource(ctx context.Context, client k8sclient.K8sClient, target patchTargetModel) (schema.GroupVersionResource, *unstructured.Unstructured, error) {
	// Discover the GVR from apiVersion and kind
	apiVersion := target.APIVersion.ValueString()
	kind := target.Kind.ValueString()

	gvr, err := client.DiscoverGVR(ctx, apiVersion, kind)
	if err != nil {
		return schema.GroupVersionResource{}, nil, fmt.Errorf("failed to discover resource type: %w", err)
	}

	// Get the actual resource
	namespace := ""
	if !target.Namespace.IsNull() {
		namespace = target.Namespace.ValueString()
	}

	obj, err := client.Get(ctx, gvr, namespace, target.Name.ValueString())
	if err != nil {
		return gvr, nil, err
	}

	return gvr, obj, nil
}

// extractPatchFieldPaths extracts the field paths that will be modified by a patch
func (r *patchResource) extractPatchFieldPaths(ctx context.Context, patchContent string, patchType string) ([]string, error) {
	switch patchType {
	case "application/json-patch+json":
		// JSON Patch is an array of operations
		return extractFieldPathsFromJSONPatch(patchContent)
	case "application/merge-patch+json", "application/strategic-merge-patch+json":
		// Merge patches are maps
		var patchData map[string]interface{}
		if err := yaml.Unmarshal([]byte(patchContent), &patchData); err != nil {
			return nil, fmt.Errorf("failed to parse patch: %w", err)
		}
		return extractFieldPathsFromMap(patchData, ""), nil
	default:
		return nil, fmt.Errorf("unsupported patch type: %s", patchType)
	}
}

// extractFieldPathsFromJSONPatch extracts field paths from JSON Patch operations
func extractFieldPathsFromJSONPatch(patchContent string) ([]string, error) {
	var operations []map[string]interface{}
	if err := json.Unmarshal([]byte(patchContent), &operations); err != nil {
		return nil, fmt.Errorf("failed to parse JSON patch: %w", err)
	}

	paths := make([]string, 0)
	for _, op := range operations {
		// Each operation has a "path" field
		if pathVal, ok := op["path"]; ok {
			if pathStr, ok := pathVal.(string); ok {
				// JSON Patch paths start with "/" - remove it and convert to dot notation
				cleanPath := strings.TrimPrefix(pathStr, "/")
				cleanPath = strings.ReplaceAll(cleanPath, "/", ".")
				paths = append(paths, cleanPath)
			}
		}
		// "move" and "copy" operations also have "from" field
		if fromVal, ok := op["from"]; ok {
			if fromStr, ok := fromVal.(string); ok {
				cleanPath := strings.TrimPrefix(fromStr, "/")
				cleanPath = strings.ReplaceAll(cleanPath, "/", ".")
				paths = append(paths, cleanPath)
			}
		}
	}

	return paths, nil
}

// extractFieldPathsFromMap recursively extracts field paths from a map
// Only leaf paths (final values) are returned, not intermediate map keys
func extractFieldPathsFromMap(data map[string]interface{}, prefix string) []string {
	var paths []string

	for key, value := range data {
		currentPath := key
		if prefix != "" {
			currentPath = prefix + "." + key
		}

		// If value is a map, recurse (don't add the intermediate path)
		if valueMap, ok := value.(map[string]interface{}); ok {
			nestedPaths := extractFieldPathsFromMap(valueMap, currentPath)
			paths = append(paths, nestedPaths...)
		} else if valueArray, ok := value.([]interface{}); ok {
			// Handle arrays - add the array path itself as a leaf
			paths = append(paths, currentPath)
			// Also recurse into array elements if they're maps
			for i, item := range valueArray {
				if itemMap, ok := item.(map[string]interface{}); ok {
					arrayPath := fmt.Sprintf("%s[%d]", currentPath, i)
					nestedPaths := extractFieldPathsFromMap(itemMap, arrayPath)
					paths = append(paths, nestedPaths...)
				}
			}
		} else {
			// Leaf value - add the path
			paths = append(paths, currentPath)
		}
	}

	return paths
}

// extractManagedFieldsForManager extracts field ownership for ALL managers
// (not just the specified manager) to enable proper ownership transition detection.
// This allows us to detect when external actors revert patches or take ownership.
func extractManagedFieldsForManager(obj *unstructured.Unstructured, fieldManager string) map[string][]string {
	// Extract ownership for ALL managers, not just ours
	return fieldmanagement.ExtractAllManagedFields(obj)
}

// applyPatch applies the patch to the target resource using the appropriate method based on patch type
func (r *patchResource) applyPatch(ctx context.Context, client k8sclient.K8sClient, targetObj *unstructured.Unstructured, data patchResourceModel, fieldManager string, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	// Get patch content and type
	patchContent := r.getPatchContent(data)
	if patchContent == "" {
		return nil, fmt.Errorf("no patch content provided")
	}

	patchTypeStr := r.determinePatchType(data)

	// Handle different patch types
	switch patchTypeStr {
	case "application/json-patch+json":
		return r.applyJSONOrMergePatch(ctx, client, targetObj, patchContent, types.JSONPatchType, gvr, fieldManager)
	case "application/merge-patch+json":
		return r.applyJSONOrMergePatch(ctx, client, targetObj, patchContent, types.MergePatchType, gvr, fieldManager)
	case "application/strategic-merge-patch+json":
		return r.applyStrategicMergePatch(ctx, client, targetObj, patchContent, fieldManager, gvr)
	default:
		return nil, fmt.Errorf("unsupported patch type: %s", patchTypeStr)
	}
}

// applyJSONOrMergePatch applies JSON Patch or Merge Patch using the k8s Patch API
func (r *patchResource) applyJSONOrMergePatch(ctx context.Context, client k8sclient.K8sClient, targetObj *unstructured.Unstructured, patchContent string, patchType types.PatchType, gvr schema.GroupVersionResource, fieldManager string) (*unstructured.Unstructured, error) {
	// Use Patch API with the raw patch content
	patchBytes := []byte(patchContent)

	// Create patch options with field manager
	// Note: Force field is not allowed for JSON/Merge patches (only for SSA)
	patchOptions := metav1.PatchOptions{
		FieldManager: fieldManager,
	}

	result, err := client.Patch(ctx, gvr, targetObj.GetNamespace(), targetObj.GetName(), patchType, patchBytes, patchOptions)
	if err != nil {
		// Check for immutable field errors and provide better error message
		if k8serrors.IsImmutableFieldError(err) {
			immutableFields := k8serrors.ExtractImmutableFields(err)
			return nil, fmt.Errorf("cannot patch immutable field(s): %v on %s/%s in namespace %s\n\n"+
				"The target resource has immutable fields that cannot be changed after creation.\n\n"+
				"Options:\n"+
				"1. Remove the immutable field from your patch\n"+
				"2. If the field MUST change, recreate the target resource manually or use k8sconnect_object\n"+
				"3. k8sconnect_object manages full resource lifecycle and can trigger automatic replacement\n\n"+
				"Note: JSON Patch and Merge Patch cannot detect immutable fields during plan - errors only appear during apply",
				immutableFields, targetObj.GetKind(), targetObj.GetName(), targetObj.GetNamespace())
		}
		return nil, fmt.Errorf("failed to apply patch: %w", err)
	}

	return result, nil
}

// applyStrategicMergePatch applies a strategic merge patch using Server-Side Apply
func (r *patchResource) applyStrategicMergePatch(ctx context.Context, client k8sclient.K8sClient, targetObj *unstructured.Unstructured, patchContent string, fieldManager string, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	// Parse patch content into unstructured format
	var patchData map[string]interface{}
	if err := yaml.Unmarshal([]byte(patchContent), &patchData); err != nil {
		return nil, fmt.Errorf("failed to parse patch content: %w", err)
	}

	// Create a new object that combines target metadata with patch data
	patchObj := &unstructured.Unstructured{Object: make(map[string]interface{})}
	patchObj.SetAPIVersion(targetObj.GetAPIVersion())
	patchObj.SetKind(targetObj.GetKind())
	patchObj.SetName(targetObj.GetName())
	patchObj.SetNamespace(targetObj.GetNamespace())

	// Merge patch data into the object
	// This is a deep merge - patch fields override/extend existing fields
	mergeMaps(patchObj.Object, patchData)

	// Apply using Server-Side Apply with our unique field manager
	err := client.Apply(ctx, patchObj, k8sclient.ApplyOptions{
		FieldManager: fieldManager,
		Force:        true, // Required because we're taking ownership
	})
	if err != nil {
		// Check for immutable field errors and provide better error message
		if k8serrors.IsImmutableFieldError(err) {
			immutableFields := k8serrors.ExtractImmutableFields(err)
			return nil, fmt.Errorf("cannot patch immutable field(s): %v on %s/%s in namespace %s\n\n"+
				"The target resource has immutable fields that cannot be changed after creation.\n\n"+
				"Options:\n"+
				"1. Remove the immutable field from your patch\n"+
				"2. If the field MUST change, recreate the target resource manually or use k8sconnect_object\n"+
				"3. k8sconnect_object manages full resource lifecycle and can trigger automatic replacement\n\n"+
				"Note: This error was caught during apply. When connections are ready, Strategic Merge patches can detect this during plan.",
				immutableFields, targetObj.GetKind(), targetObj.GetName(), targetObj.GetNamespace())
		}
		return nil, fmt.Errorf("failed to apply patch: %w", err)
	}

	// Read back the patched resource
	result, err := client.Get(ctx, gvr, targetObj.GetNamespace(), targetObj.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to read patched resource: %w", err)
	}

	return result, nil
}

// mergeMaps performs a deep merge of src into dst
func mergeMaps(dst, src map[string]interface{}) {
	for key, srcVal := range src {
		if dstVal, exists := dst[key]; exists {
			// Key exists in both
			if dstMap, dstIsMap := dstVal.(map[string]interface{}); dstIsMap {
				if srcMap, srcIsMap := srcVal.(map[string]interface{}); srcIsMap {
					// Both are maps - recurse
					mergeMaps(dstMap, srcMap)
					continue
				}
			}
		}
		// Either key doesn't exist in dst, or one of the values isn't a map
		// Override with src value
		dst[key] = srcVal
	}
}

// detectValueDrift compares the desired patch values with actual current values
// Returns (hasDrift bool, driftedFields []string, error)
func (r *patchResource) detectValueDrift(ctx context.Context, currentObj *unstructured.Unstructured, data patchResourceModel) (bool, []string, error) {
	patchContent := r.getPatchContent(data)
	if patchContent == "" {
		return false, nil, nil
	}

	patchType := r.determinePatchType(data)

	switch patchType {
	case "application/strategic-merge-patch+json":
		return r.detectStrategicMergeDrift(currentObj, patchContent)
	case "application/json-patch+json":
		return r.detectJSONPatchDrift(currentObj, patchContent)
	case "application/merge-patch+json":
		return r.detectMergePatchDrift(currentObj, patchContent)
	default:
		return false, nil, fmt.Errorf("unsupported patch type: %s", patchType)
	}
}

// detectStrategicMergeDrift checks if strategic merge patch values have drifted
func (r *patchResource) detectStrategicMergeDrift(currentObj *unstructured.Unstructured, patchContent string) (bool, []string, error) {
	// Parse the patch to get desired values
	var patchData map[string]interface{}
	if err := yaml.Unmarshal([]byte(patchContent), &patchData); err != nil {
		return false, nil, fmt.Errorf("failed to parse patch: %w", err)
	}

	// Recursively compare desired values with current values and collect drifted paths
	driftedPaths := collectValueDrift(currentObj.Object, patchData, "")
	return len(driftedPaths) > 0, driftedPaths, nil
}

// detectMergePatchDrift checks if merge patch values have drifted
func (r *patchResource) detectMergePatchDrift(currentObj *unstructured.Unstructured, patchContent string) (bool, []string, error) {
	// Merge patch has same semantics as strategic merge for value comparison
	return r.detectStrategicMergeDrift(currentObj, patchContent)
}

// detectJSONPatchDrift checks if JSON patch values have drifted
func (r *patchResource) detectJSONPatchDrift(currentObj *unstructured.Unstructured, patchContent string) (bool, []string, error) {
	// Parse JSON patch operations
	var operations []map[string]interface{}
	if err := json.Unmarshal([]byte(patchContent), &operations); err != nil {
		return false, nil, fmt.Errorf("failed to parse JSON patch: %w", err)
	}

	var driftedPaths []string

	// Check each operation
	for _, op := range operations {
		opType, _ := op["op"].(string)
		pathStr, _ := op["path"].(string)
		expectedValue := op["value"]

		// Only check "add" and "replace" operations that set values
		if opType != "add" && opType != "replace" {
			continue
		}

		// Convert JSON Patch path to nested map lookup
		// "/data/key" -> ["data", "key"]
		path := strings.TrimPrefix(pathStr, "/")
		pathParts := strings.Split(path, "/")

		// Get current value at this path
		currentValue := getValueAtPath(currentObj.Object, pathParts)

		// Compare values
		if !valuesEqual(currentValue, expectedValue) {
			// Use the original path format from JSON Patch
			driftedPaths = append(driftedPaths, strings.ReplaceAll(path, "/", "."))
		}
	}

	return len(driftedPaths) > 0, driftedPaths, nil
}

// collectValueDrift recursively checks if any values in patchData differ from currentData
// Returns a list of paths that have drifted
func collectValueDrift(currentData, patchData map[string]interface{}, prefix string) []string {
	var driftedPaths []string

	for key, patchValue := range patchData {
		// Build the full path
		var fullPath string
		if prefix == "" {
			fullPath = key
		} else {
			fullPath = prefix + "." + key
		}

		currentValue, exists := currentData[key]

		if !exists {
			// Field missing in current object
			driftedPaths = append(driftedPaths, fullPath)
			continue
		}

		// Recursively compare if both are maps
		if patchMap, ok := patchValue.(map[string]interface{}); ok {
			if currentMap, ok := currentValue.(map[string]interface{}); ok {
				nestedDrift := collectValueDrift(currentMap, patchMap, fullPath)
				driftedPaths = append(driftedPaths, nestedDrift...)
				continue
			}
		}

		// Direct value comparison
		if !valuesEqual(currentValue, patchValue) {
			driftedPaths = append(driftedPaths, fullPath)
		}
	}

	return driftedPaths
}

// getValueAtPath retrieves a value at a given path in a nested map
func getValueAtPath(obj map[string]interface{}, pathParts []string) interface{} {
	if len(pathParts) == 0 {
		return obj
	}

	current := interface{}(obj)
	for _, part := range pathParts {
		switch v := current.(type) {
		case map[string]interface{}:
			current = v[part]
		case []interface{}:
			// Handle array index
			var idx int
			if _, err := fmt.Sscanf(part, "%d", &idx); err == nil {
				if idx >= 0 && idx < len(v) {
					current = v[idx]
				} else {
					return nil
				}
			} else {
				return nil
			}
		default:
			return nil
		}
	}

	return current
}

// valuesEqual compares two values for equality, handling type conversions
func valuesEqual(a, b interface{}) bool {
	// Handle nil cases
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Convert to comparable types
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)

	return string(aJSON) == string(bJSON)
}
