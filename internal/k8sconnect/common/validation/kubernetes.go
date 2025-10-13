// internal/k8sconnect/common/validation/kubernetes.go
package validation

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Provider annotation prefix for internal tracking
const ProviderAnnotationPrefix = "k8sconnect.terraform.io/"

// Standard error message hints for common user mistakes
const (
	// CopyPasteHintYAML is appended to errors when users likely copy-pasted from kubectl get -o yaml
	CopyPasteHintYAML = "This field appears to have been copy-pasted from cluster output (kubectl get -o yaml). "

	// CopyPasteHint is appended to errors when users likely copy-pasted from kubectl get
	CopyPasteHint = "This field appears to have been copy-pasted from cluster output (kubectl get). "
)

// Server-managed metadata fields that will cause apply failures or are noise
var ServerManagedMetadataFields = []string{
	"uid",
	"resourceVersion",
	"generation",
	"creationTimestamp",
	"managedFields",
}

// ValidateContainerNames ensures all containers have names for strategic merge to work correctly
func ValidateContainerNames(obj *unstructured.Unstructured) error {
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

// HasServerManagedFields checks if an object contains server-managed metadata fields
func HasServerManagedFields(obj *unstructured.Unstructured) (bool, string) {
	metadata, found := obj.Object["metadata"].(map[string]interface{})
	if !found {
		return false, ""
	}

	for _, field := range ServerManagedMetadataFields {
		if _, exists := metadata[field]; exists {
			return true, field
		}
	}

	return false, ""
}

// HasProviderAnnotations checks if an object contains provider internal annotations
func HasProviderAnnotations(obj *unstructured.Unstructured) (bool, string) {
	annotations, found, _ := unstructured.NestedStringMap(obj.Object, "metadata", "annotations")
	if !found {
		return false, ""
	}

	for key := range annotations {
		if strings.HasPrefix(key, ProviderAnnotationPrefix) {
			return true, key
		}
	}

	return false, ""
}

// HasStatusField checks if an object contains a status field
func HasStatusField(obj *unstructured.Unstructured) bool {
	_, found := obj.Object["status"]
	return found
}

// ContainsInterpolation checks if content contains Terraform interpolation syntax
func ContainsInterpolation(content string) bool {
	return strings.Contains(content, "${")
}
