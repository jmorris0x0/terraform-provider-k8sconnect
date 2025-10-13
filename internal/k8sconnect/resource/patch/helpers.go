// internal/k8sconnect/resource/patch/helpers.go
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
	conn, err := auth.ObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		diagnostics.AddError(
			"Invalid Connection Configuration",
			fmt.Sprintf("Failed to parse cluster_connection: %s", err))
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
	// Create a dummy object to get the GVR
	dummyObj := &unstructured.Unstructured{}
	dummyObj.SetAPIVersion(target.APIVersion.ValueString())
	dummyObj.SetKind(target.Kind.ValueString())
	dummyObj.SetName(target.Name.ValueString())
	if !target.Namespace.IsNull() {
		dummyObj.SetNamespace(target.Namespace.ValueString())
	}

	// Get the GVR
	gvr, err := client.GetGVR(ctx, dummyObj)
	if err != nil {
		return schema.GroupVersionResource{}, nil, fmt.Errorf("failed to determine resource type: %w", err)
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
func extractFieldPathsFromMap(data map[string]interface{}, prefix string) []string {
	var paths []string

	for key, value := range data {
		currentPath := key
		if prefix != "" {
			currentPath = prefix + "." + key
		}

		// Add this path
		paths = append(paths, currentPath)

		// If value is a map, recurse
		if valueMap, ok := value.(map[string]interface{}); ok {
			nestedPaths := extractFieldPathsFromMap(valueMap, currentPath)
			paths = append(paths, nestedPaths...)
		} else if valueArray, ok := value.([]interface{}); ok {
			// Handle arrays
			for i, item := range valueArray {
				if itemMap, ok := item.(map[string]interface{}); ok {
					arrayPath := fmt.Sprintf("%s[%d]", currentPath, i)
					nestedPaths := extractFieldPathsFromMap(itemMap, arrayPath)
					paths = append(paths, nestedPaths...)
				}
			}
		}
	}

	return paths
}

// extractFieldOwnershipForPaths extracts ownership info for specific field paths
func extractFieldOwnershipForPaths(obj *unstructured.Unstructured, paths []string) map[string]string {
	return fieldmanagement.ExtractFieldOwnershipForPaths(obj, paths)
}

// extractFieldOwnershipMap extracts field ownership as a simple map[path]manager
func extractFieldOwnershipMap(obj *unstructured.Unstructured) map[string]string {
	return fieldmanagement.ExtractFieldOwnershipMap(obj)
}

// extractFieldOwnershipForManager extracts field ownership for a specific field manager
func extractFieldOwnershipForManager(obj *unstructured.Unstructured, fieldManager string) map[string]string {
	allOwnership := fieldmanagement.ExtractFieldOwnershipMap(obj)
	ourOwnership := make(map[string]string)

	for path, manager := range allOwnership {
		if manager == fieldManager {
			ourOwnership[path] = manager
		}
	}

	return ourOwnership
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
				"2. If the field MUST change, recreate the target resource manually or use k8sconnect_manifest\n"+
				"3. k8sconnect_manifest manages full resource lifecycle and can trigger automatic replacement\n\n"+
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
				"2. If the field MUST change, recreate the target resource manually or use k8sconnect_manifest\n"+
				"3. k8sconnect_manifest manages full resource lifecycle and can trigger automatic replacement\n\n"+
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

// extractManagedFieldsForManager extracts the managed fields JSON for a specific field manager
func extractManagedFieldsForManager(obj *unstructured.Unstructured, fieldManager string) (string, error) {
	return fieldmanagement.ExtractManagedFieldsForManager(obj, fieldManager)
}
