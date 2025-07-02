// internal/k8sinline/resource/manifest/helpers.go
package manifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
)

// parseYAML converts YAML string to unstructured.Unstructured
func (r *manifestResource) parseYAML(yamlStr string) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	err := yaml.Unmarshal([]byte(yamlStr), obj)
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

	return obj, nil
}

// objectToYAML converts an unstructured object back to clean YAML
func (r *manifestResource) objectToYAML(obj *unstructured.Unstructured) ([]byte, error) {
	// Create a clean copy without managed fields and other cluster-added metadata
	cleanObj := r.cleanObjectForExport(obj)

	// Convert to YAML
	yamlBytes, err := yaml.Marshal(cleanObj.Object)
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

	// These fields MUST be removed or kubectl apply fails
	delete(metadata, "uid")
	delete(metadata, "resourceVersion")
	delete(metadata, "generation")
	delete(metadata, "creationTimestamp")
	delete(metadata, "managedFields")

	// Remove status field entirely (never needed for apply)
	delete(cleaned.Object, "status")

	// Leave everything else - let the user clean up if they want
	// This is safer than trying to guess what's system-generated

	return cleaned
}

// generateID creates a unique identifier for the resource
func (r *manifestResource) generateID(obj *unstructured.Unstructured, conn ClusterConnectionModel) string {
	// Create a deterministic ID based on cluster + object identity
	data := fmt.Sprintf("%s/%s/%s/%s",
		r.getClusterID(conn),
		obj.GetNamespace(),
		obj.GetKind(),
		obj.GetName(),
	)

	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// classifyK8sError categorizes Kubernetes API errors for better user experience
func (r *manifestResource) classifyK8sError(err error, operation, resourceDesc string) (severity, title, detail string) {
	switch {
	case errors.IsNotFound(err):
		return "warning", fmt.Sprintf("%s: Resource Not Found", operation),
			fmt.Sprintf("The %s was not found in the cluster. It may have been deleted outside of Terraform.", resourceDesc)

	case errors.IsForbidden(err):
		return "error", fmt.Sprintf("%s: Insufficient Permissions", operation),
			fmt.Sprintf("RBAC permissions insufficient to %s %s. Check that your credentials have the required permissions for this operation. Details: %v",
				operation, resourceDesc, err)

	case errors.IsConflict(err):
		return "error", fmt.Sprintf("%s: Field Manager Conflict", operation),
			fmt.Sprintf("Server-side apply conflict detected for %s. Another tool or process may be managing the same fields. Consider using 'force=true' or resolve the conflict manually. Details: %v",
				resourceDesc, err)

	case errors.IsTimeout(err) || errors.IsServerTimeout(err):
		return "error", fmt.Sprintf("%s: Kubernetes API Timeout", operation),
			fmt.Sprintf("Timeout while performing %s on %s. The cluster may be under heavy load or experiencing connectivity issues. Details: %v",
				operation, resourceDesc, err)

	case errors.IsUnauthorized(err):
		return "error", fmt.Sprintf("%s: Authentication Failed", operation),
			fmt.Sprintf("Authentication failed for %s %s. Check your credentials and ensure they are valid. Details: %v",
				operation, resourceDesc, err)

	case errors.IsInvalid(err):
		return "error", fmt.Sprintf("%s: Invalid Resource", operation),
			fmt.Sprintf("The %s contains invalid fields or values. Review the YAML specification and ensure all required fields are present and correctly formatted. Details: %v",
				resourceDesc, err)

	case errors.IsAlreadyExists(err):
		return "error", fmt.Sprintf("%s: Resource Already Exists", operation),
			fmt.Sprintf("The %s already exists in the cluster and cannot be created. Use import to manage existing resources with Terraform. Details: %v",
				resourceDesc, err)

	default:
		return "error", fmt.Sprintf("%s: Kubernetes API Error", operation),
			fmt.Sprintf("An unexpected error occurred while performing %s on %s. Details: %v",
				operation, resourceDesc, err)
	}
}

// getGVR determines the GroupVersionResource for an object
func (r *manifestResource) getGVR(ctx context.Context, client k8sclient.K8sClient, obj *unstructured.Unstructured) (k8sschema.GroupVersionResource, error) {
	return client.GetGVR(ctx, obj)
}
