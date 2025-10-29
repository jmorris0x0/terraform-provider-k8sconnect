package object

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
)

// Type alias for compatibility
type FieldOwnership = fieldmanagement.FieldOwnership

// parseFieldsV1ToPathMap is a wrapper for the common implementation
func parseFieldsV1ToPathMap(managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) map[string]FieldOwnership {
	return fieldmanagement.ParseFieldsV1ToPathMap(managedFields, userJSON)
}

// extractFieldOwnership returns ownership info for ALL fields
func extractFieldOwnership(obj *unstructured.Unstructured) map[string]FieldOwnership {
	return fieldmanagement.ExtractFieldOwnership(obj)
}

// extractAllFieldOwnership extracts ownership for ALL managers (not just k8sconnect)
// This is used for ownership transition detection
func extractAllFieldOwnership(obj *unstructured.Unstructured) map[string][]string {
	return fieldmanagement.ExtractAllFieldOwnership(obj)
}
