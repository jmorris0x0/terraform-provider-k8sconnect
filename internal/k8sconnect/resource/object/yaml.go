// internal/k8sconnect/resource/object/yaml.go
package object

import (
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/validation"
)

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
func (r *objectResource) parseYAML(yamlStr string) (*unstructured.Unstructured, error) {
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
	if err := validation.ValidateContainerNames(obj); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	return obj, nil
}

// objectToYAML converts an unstructured object back to clean YAML
func (r *objectResource) objectToYAML(obj *unstructured.Unstructured) ([]byte, error) {
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
func (r *objectResource) cleanObjectForExport(obj *unstructured.Unstructured) *unstructured.Unstructured {
	// Create a deep copy
	cleaned := obj.DeepCopy()

	// Remove only the fields that will definitely cause problems on re-apply
	metadata := cleaned.Object["metadata"].(map[string]interface{})

	// Remove server-managed metadata fields using shared constant list
	for _, field := range validation.ServerManagedMetadataFields {
		delete(metadata, field)
	}

	// Remove status field entirely (never needed for apply)
	delete(cleaned.Object, "status")

	// Leave everything else - let the user clean up if they want
	// This is safer than trying to guess what's system-generated

	return cleaned
}
