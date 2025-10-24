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
	cleanObj := r.cleanObjectForImport(obj)

	// Convert to YAML
	yamlBytes, err := sigsyaml.Marshal(cleanObj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal object to YAML: %w", err)
	}

	return yamlBytes, nil
}

// cleanObjectForImport removes server-generated fields from imported resources
// to produce clean yaml_body that looks like user-written configuration.
// This is used during import to ensure state contains plausible user config,
// not raw cluster state with server-added metadata.
func (r *objectResource) cleanObjectForImport(obj *unstructured.Unstructured) *unstructured.Unstructured {
	// Create a deep copy
	cleaned := obj.DeepCopy()

	metadata := cleaned.Object["metadata"].(map[string]interface{})

	// Remove server-managed metadata fields (uid, resourceVersion, etc.)
	for _, field := range validation.ServerManagedMetadataFields {
		delete(metadata, field)
	}

	// Remove status block entirely (never needed for apply)
	delete(cleaned.Object, "status")

	// Remove server-added annotations
	if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
		// kubectl's last-applied-configuration annotation
		delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		// Deployment revision annotation (added by kube-controller-manager)
		delete(annotations, "deployment.kubernetes.io/revision")
		// StatefulSet revision annotation
		delete(annotations, "statefulset.kubernetes.io/revision")
		// DaemonSet revision annotation
		delete(annotations, "deprecated.daemonset.template.generation")

		// If annotations is now empty, remove it entirely
		if len(annotations) == 0 {
			delete(metadata, "annotations")
		}
	}

	// Clean nested pod template metadata (for Deployments, StatefulSets, DaemonSets, Jobs, CronJobs)
	if spec, ok := cleaned.Object["spec"].(map[string]interface{}); ok {
		r.cleanPodTemplateMetadata(spec)
	}

	return cleaned
}

// cleanPodTemplateMetadata removes server-added fields from pod template metadata
func (r *objectResource) cleanPodTemplateMetadata(spec map[string]interface{}) {
	// Handle Deployment, StatefulSet, DaemonSet, ReplicaSet
	if template, ok := spec["template"].(map[string]interface{}); ok {
		if templateMeta, ok := template["metadata"].(map[string]interface{}); ok {
			delete(templateMeta, "creationTimestamp")
		}
	}

	// Handle Job
	if jobTemplate, ok := spec["jobTemplate"].(map[string]interface{}); ok {
		if jobSpec, ok := jobTemplate["spec"].(map[string]interface{}); ok {
			if template, ok := jobSpec["template"].(map[string]interface{}); ok {
				if templateMeta, ok := template["metadata"].(map[string]interface{}); ok {
					delete(templateMeta, "creationTimestamp")
				}
			}
		}
	}
}
