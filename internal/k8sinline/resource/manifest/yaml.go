// internal/k8sinline/resource/manifest/yaml.go
package manifest

import (
	"context"
	"fmt"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
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

	// Validate containers have names (critical for strategic merge)
	if err := validateContainerNames(obj); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	return obj, nil
}

// validateContainerNames ensures all containers have names for strategic merge to work correctly
func validateContainerNames(obj *unstructured.Unstructured) error {
	// Check spec.containers
	containers, found, _ := unstructured.NestedSlice(obj.Object, "spec", "containers")
	if found {
		for i, container := range containers {
			if containerMap, ok := container.(map[string]interface{}); ok {
				if name, _ := containerMap["name"].(string); name == "" {
					return fmt.Errorf("container at index %d is missing required 'name' field", i)
				}
			}
		}
	}

	// Check spec.initContainers
	initContainers, found, _ := unstructured.NestedSlice(obj.Object, "spec", "initContainers")
	if found {
		for i, container := range initContainers {
			if containerMap, ok := container.(map[string]interface{}); ok {
				if name, _ := containerMap["name"].(string); name == "" {
					return fmt.Errorf("initContainer at index %d is missing required 'name' field", i)
				}
			}
		}
	}

	// Check spec.template.spec.containers (for Deployments, DaemonSets, etc.)
	templateContainers, found, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if found {
		for i, container := range templateContainers {
			if containerMap, ok := container.(map[string]interface{}); ok {
				if name, _ := containerMap["name"].(string); name == "" {
					return fmt.Errorf("template container at index %d is missing required 'name' field", i)
				}
			}
		}
	}

	// Check spec.template.spec.initContainers
	templateInitContainers, found, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "initContainers")
	if found {
		for i, container := range templateInitContainers {
			if containerMap, ok := container.(map[string]interface{}); ok {
				if name, _ := containerMap["name"].(string); name == "" {
					return fmt.Errorf("template initContainer at index %d is missing required 'name' field", i)
				}
			}
		}
	}

	// Check spec.jobTemplate.spec.template.spec.containers (for CronJobs)
	cronJobContainers, found, _ := unstructured.NestedSlice(obj.Object, "spec", "jobTemplate", "spec", "template", "spec", "containers")
	if found {
		for i, container := range cronJobContainers {
			if containerMap, ok := container.(map[string]interface{}); ok {
				if name, _ := containerMap["name"].(string); name == "" {
					return fmt.Errorf("jobTemplate container at index %d is missing required 'name' field", i)
				}
			}
		}
	}

	return nil
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

// getGVR determines the GroupVersionResource for an object
func (r *manifestResource) getGVR(ctx context.Context, client k8sclient.K8sClient, obj *unstructured.Unstructured) (k8sschema.GroupVersionResource, error) {
	return client.GetGVR(ctx, obj)
}
