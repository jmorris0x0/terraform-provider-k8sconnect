// internal/k8sconnect/resource/manifest/field_ownership.go
package manifest

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

// addCoreFields adds fields that are always owned by the creator
func addCoreFields(paths []string, userJSON map[string]interface{}) []string {
	return fieldmanagement.AddCoreFields(paths, userJSON)
}
