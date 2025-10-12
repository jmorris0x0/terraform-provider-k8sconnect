// internal/k8sconnect/resource/patch/helpers.go
package patch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
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
	// Parse the patch content as unstructured
	var patchData map[string]interface{}

	if err := yaml.Unmarshal([]byte(patchContent), &patchData); err != nil {
		return nil, fmt.Errorf("failed to parse patch: %w", err)
	}

	// Extract all field paths from the patch
	return extractFieldPathsFromMap(patchData, ""), nil
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
	result := make(map[string]string)

	// Get all ownership info
	allOwnership := extractFieldOwnershipMap(obj)

	// Filter to only the paths we care about
	for _, path := range paths {
		if owner, exists := allOwnership[path]; exists {
			result[path] = owner
		}
	}

	return result
}

// extractFieldOwnershipMap extracts field ownership as a simple map[path]manager
func extractFieldOwnershipMap(obj *unstructured.Unstructured) map[string]string {
	result := make(map[string]string)

	for _, mf := range obj.GetManagedFields() {
		if mf.FieldsV1 == nil {
			continue
		}

		var fields map[string]interface{}
		if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
			continue
		}

		// Extract paths owned by this manager
		paths := extractPathsFromFieldsV1Simple(fields, "")
		for _, path := range paths {
			result[path] = mf.Manager
		}
	}

	return result
}

// extractPathsFromFieldsV1Simple is a simplified version that doesn't need user JSON
func extractPathsFromFieldsV1Simple(fields map[string]interface{}, prefix string) []string {
	var paths []string

	for key, value := range fields {
		if strings.HasPrefix(key, "f:") {
			// Regular field
			fieldName := strings.TrimPrefix(key, "f:")
			currentPath := fieldName
			if prefix != "" {
				currentPath = prefix + "." + fieldName
			}

			if subFields, ok := value.(map[string]interface{}); ok && len(subFields) > 0 {
				// Has sub-fields - recurse
				if _, hasDot := subFields["."]; hasDot {
					// This field itself is owned
					paths = append(paths, currentPath)
				}

				// Recurse for nested fields
				nestedPaths := extractPathsFromFieldsV1Simple(subFields, currentPath)
				paths = append(paths, nestedPaths...)
			} else {
				// Leaf field
				paths = append(paths, currentPath)
			}
		} else if strings.HasPrefix(key, "k:") {
			// Array element - we'll use simplified handling
			if subFields, ok := value.(map[string]interface{}); ok {
				// Extract array key info for path
				arrayKey := strings.TrimPrefix(key, "k:")
				arrayPath := fmt.Sprintf("%s%s", prefix, arrayKey)
				nestedPaths := extractPathsFromFieldsV1Simple(subFields, arrayPath)
				paths = append(paths, nestedPaths...)
			}
		}
	}

	return paths
}

// applyPatch applies the patch to the target resource using Server-Side Apply
func (r *patchResource) applyPatch(ctx context.Context, client k8sclient.K8sClient, targetObj *unstructured.Unstructured, data patchResourceModel, fieldManager string, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	// Get patch content
	patchContent := r.getPatchContent(data)
	if patchContent == "" {
		return nil, fmt.Errorf("no patch content provided")
	}

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
	for _, mf := range obj.GetManagedFields() {
		if mf.Manager == fieldManager {
			if mf.FieldsV1 != nil {
				// Convert to a more readable JSON format
				var fields map[string]interface{}
				if err := json.Unmarshal(mf.FieldsV1.Raw, &fields); err != nil {
					return "", fmt.Errorf("failed to parse managed fields: %w", err)
				}

				// Convert back to JSON string
				jsonBytes, err := json.Marshal(fields)
				if err != nil {
					return "", fmt.Errorf("failed to marshal managed fields: %w", err)
				}

				return string(jsonBytes), nil
			}
		}
	}

	// No fields managed by this manager
	return "{}", nil
}
