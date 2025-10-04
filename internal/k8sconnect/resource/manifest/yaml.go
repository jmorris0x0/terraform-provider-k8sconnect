// internal/k8sconnect/resource/manifest/yaml.go
package manifest

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"
)

// Provider annotation prefix for internal tracking
const providerAnnotationPrefix = "k8sconnect.terraform.io/"

// Server-managed metadata fields that will cause apply failures or are noise
var serverManagedMetadataFields = []string{
	"uid",
	"resourceVersion",
	"generation",
	"creationTimestamp",
	"managedFields",
}

// isMultiDocumentYAML checks if the YAML content contains multiple documents
func isMultiDocumentYAML(yamlStr string) bool {
	// Use yaml decoder to properly detect multiple documents
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(yamlStr), 4096)

	documentCount := 0
	for {
		var obj interface{}
		err := decoder.Decode(&obj)
		if err != nil {
			if err == io.EOF {
				// End of stream, no more documents
				break
			}
			// Invalid YAML, but that will be caught by parseYAML later
			break
		}
		documentCount++
		if documentCount > 1 {
			return true
		}
	}

	return false
}

// parseYAML converts YAML string to unstructured.Unstructured
func (r *manifestResource) parseYAML(yamlStr string) (*unstructured.Unstructured, error) {
	// Check for multi-document YAML
	if isMultiDocumentYAML(yamlStr) {
		return nil, fmt.Errorf("multi-document YAML detected (contains '---' separator). Use the k8sconnect_yaml_split data source to split the documents first")
	}

	obj := &unstructured.Unstructured{}
	err := sigsyaml.Unmarshal([]byte(yamlStr), obj)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	// Validate required fields
	if obj.GetAPIVersion() == "" {
		return nil, fmt.Errorf("apiVersion is required")
	}
	if obj.GetKind() == "" {
		return nil, fmt.Errorf("kind is required")
	}
	if obj.GetName() == "" {
		return nil, fmt.Errorf("metadata.name is required")
	}

	// Validate containers have names (critical for strategic merge)
	if err := validateContainerNames(obj); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	return obj, nil
}

// validateContainerNames ensures all containers have names for strategic merge to work correctly
func validateContainerNames(obj *unstructured.Unstructured) error {
	// Define all container paths that need validation
	containerPaths := []struct {
		path          []string
		containerType string
	}{
		{[]string{"spec", "containers"}, "container"},
		{[]string{"spec", "initContainers"}, "initContainer"},
		{[]string{"spec", "template", "spec", "containers"}, "template container"},
		{[]string{"spec", "template", "spec", "initContainers"}, "template initContainer"},
	}

	// Check each path
	for _, cp := range containerPaths {
		if err := validateContainersAtPath(obj.Object, cp.path, cp.containerType); err != nil {
			return err
		}
	}

	return nil
}

// validateContainersAtPath validates containers at a specific path in the object
func validateContainersAtPath(obj map[string]interface{}, path []string, containerType string) error {
	containers, found, _ := unstructured.NestedSlice(obj, path...)
	if !found {
		// No containers at this path, that's fine
		return nil
	}

	for i, container := range containers {
		containerMap, ok := container.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s at index %d is not a valid object", containerType, i)
		}

		name, _ := containerMap["name"].(string)
		if name == "" {
			return fmt.Errorf("%s at index %d is missing required 'name' field", containerType, i)
		}
	}

	return nil
}

// objectToYAML converts an unstructured object back to clean YAML
func (r *manifestResource) objectToYAML(obj *unstructured.Unstructured) ([]byte, error) {
	// Create a clean copy without managed fields and other cluster-added metadata
	cleanObj := r.cleanObjectForExport(obj)

	// Convert to YAML
	yamlBytes, err := sigsyaml.Marshal(cleanObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal object to YAML: %w", err)
	}

	return yamlBytes, nil
}

// cleanObjectForExport removes server-generated fields that would cause apply failures
func (r *manifestResource) cleanObjectForExport(obj *unstructured.Unstructured) *unstructured.Unstructured {
	// Create a deep copy
	cleaned := obj.DeepCopy()

	// Remove only the fields that will definitely cause problems on re-apply
	metadata := cleaned.Object["metadata"].(map[string]interface{})

	// Remove server-managed metadata fields using shared constant list
	for _, field := range serverManagedMetadataFields {
		delete(metadata, field)
	}

	// Remove status field entirely (never needed for apply)
	delete(cleaned.Object, "status")

	// Leave everything else - let the user clean up if they want
	// This is safer than trying to guess what's system-generated

	return cleaned
}

// getGVR determines the GroupVersionResource for an object
func (r *manifestResource) getGVR(ctx context.Context, client k8sclient.K8sClient, obj *unstructured.Unstructured) (k8sschema.GroupVersionResource, error) {
	return client.GetGVR(ctx, obj)
}

// ContainsInterpolation checks if YAML content contains Terraform interpolation syntax
func ContainsInterpolation(content string) bool {
	return strings.Contains(content, "${")
}
