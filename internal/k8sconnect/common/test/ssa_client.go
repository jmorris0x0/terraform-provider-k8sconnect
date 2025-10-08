// internal/k8sconnect/common/test/ssa_client.go
package test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// SSATestClient provides minimal Server-Side Apply functionality for testing field ownership conflicts.
// This client is intentionally separate from the main application's k8sclient to avoid
// circular dependencies when testing field manager conflict detection.
type SSATestClient struct {
	config    *rest.Config
	clientset *kubernetes.Clientset
}

// NewSSATestClient creates a new test client for Server-Side Apply operations
func NewSSATestClient(t *testing.T, kubeconfigRaw string) *SSATestClient {
	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfigRaw))
	if err != nil {
		t.Fatalf("Failed to create REST config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create clientset: %v", err)
	}

	return &SSATestClient{
		config:    config,
		clientset: clientset,
	}
}

// ApplyDeploymentReplicasSSA uses Server-Side Apply to set the replicas field of a deployment
// with a specific field manager. This simulates what an HPA or operator would do when
// taking ownership of the replicas field.
func (c *SSATestClient) ApplyDeploymentReplicasSSA(ctx context.Context, namespace, name string, replicas int32, fieldManager string) error {
	// Create a minimal patch that only includes the fields we want to manage
	// This is exactly what an HPA would send - just the replicas field
	patch := map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"replicas": replicas,
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	// Use Server-Side Apply via the REST client
	// This properly transfers field ownership, unlike Update() or scale operations
	result := c.clientset.AppsV1().RESTClient().
		Patch(types.ApplyPatchType).
		Namespace(namespace).
		Resource("deployments").
		Name(name).
		Param("fieldManager", fieldManager).
		Param("force", "true").
		Body(patchData).
		Do(ctx)

	if err := result.Error(); err != nil {
		return fmt.Errorf("SSA patch failed: %w", err)
	}

	return nil
}

// ApplyDeploymentCPULimitSSA uses Server-Side Apply to modify a deployment's CPU limit with a specific field manager.
func (c *SSATestClient) ApplyDeploymentCPULimitSSA(ctx context.Context, namespace, name string, cpuLimit string, fieldManager string) error {
	patch := map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{
							"name": "nginx",
							"resources": map[string]interface{}{
								"limits": map[string]interface{}{
									"cpu": cpuLimit,
								},
							},
						},
					},
				},
			},
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	result := c.clientset.AppsV1().RESTClient().
		Patch(types.ApplyPatchType).
		Namespace(namespace).
		Resource("deployments").
		Name(name).
		Param("fieldManager", fieldManager).
		Param("force", "true").
		Body(patchData).
		Do(ctx)

	if err := result.Error(); err != nil {
		return fmt.Errorf("SSA patch failed: %w", err)
	}

	return nil
}

// ApplyDeploymentMemoryLimitSSA uses Server-Side Apply to modify a deployment's memory limit with a specific field manager.
func (c *SSATestClient) ApplyDeploymentMemoryLimitSSA(ctx context.Context, namespace, name string, memoryLimit string, fieldManager string) error {
	patch := map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{
							"name": "nginx",
							"resources": map[string]interface{}{
								"limits": map[string]interface{}{
									"memory": memoryLimit,
								},
							},
						},
					},
				},
			},
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	result := c.clientset.AppsV1().RESTClient().
		Patch(types.ApplyPatchType).
		Namespace(namespace).
		Resource("deployments").
		Name(name).
		Param("fieldManager", fieldManager).
		Param("force", "true").
		Body(patchData).
		Do(ctx)

	if err := result.Error(); err != nil {
		return fmt.Errorf("SSA patch failed: %w", err)
	}

	return nil
}

// ApplyServicePortSSA uses Server-Side Apply to modify a service port with a specific field manager.
// This can be used to test field ownership conflicts on Service resources.
func (c *SSATestClient) ApplyServicePortSSA(ctx context.Context, namespace, name string, port int32, fieldManager string) error {
	patch := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"ports": []map[string]interface{}{
				{
					"port":     port,
					"protocol": "TCP",
				},
			},
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	result := c.clientset.CoreV1().RESTClient().
		Patch(types.ApplyPatchType).
		Namespace(namespace).
		Resource("services").
		Name(name).
		Param("fieldManager", fieldManager).
		Param("force", "false").
		Body(patchData).
		Do(ctx)

	if err := result.Error(); err != nil {
		return fmt.Errorf("SSA patch failed: %w", err)
	}

	return nil
}

// ApplyConfigMapDataSSA uses Server-Side Apply to modify ConfigMap data with a specific field manager.
// This can be used to test field ownership conflicts on ConfigMap resources.
func (c *SSATestClient) ApplyConfigMapDataSSA(ctx context.Context, namespace, name string, data map[string]string, fieldManager string) error {
	patch := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"data": data,
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	result := c.clientset.CoreV1().RESTClient().
		Patch(types.ApplyPatchType).
		Namespace(namespace).
		Resource("configmaps").
		Name(name).
		Param("fieldManager", fieldManager).
		Param("force", "false").
		Body(patchData).
		Do(ctx)

	if err := result.Error(); err != nil {
		return fmt.Errorf("SSA patch failed: %w", err)
	}

	return nil
}

// ForceApplyConfigMapDataSSA is like ApplyConfigMapDataSSA but forces the ownership transfer.
// This simulates a controller that forcibly takes ownership of ConfigMap data fields.
func (c *SSATestClient) ForceApplyConfigMapDataSSA(ctx context.Context, namespace, name string, data map[string]string, fieldManager string) error {
	patch := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"data": data,
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	result := c.clientset.CoreV1().RESTClient().
		Patch(types.ApplyPatchType).
		Namespace(namespace).
		Resource("configmaps").
		Name(name).
		Param("fieldManager", fieldManager).
		Param("force", "true"). // Force ownership transfer
		Body(patchData).
		Do(ctx)

	if err := result.Error(); err != nil {
		return fmt.Errorf("SSA patch failed: %w", err)
	}

	return nil
}

// ForceApplyDeploymentReplicasSSA is like ApplyDeploymentReplicasSSA but forces the ownership transfer.
// This simulates a controller that forcibly takes ownership of a field.
func (c *SSATestClient) ForceApplyDeploymentReplicasSSA(ctx context.Context, namespace, name string, replicas int32, fieldManager string) error {
	patch := map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"replicas": replicas,
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	result := c.clientset.AppsV1().RESTClient().
		Patch(types.ApplyPatchType).
		Namespace(namespace).
		Resource("deployments").
		Name(name).
		Param("fieldManager", fieldManager).
		Param("force", "true"). // Force ownership transfer
		Body(patchData).
		Do(ctx)

	if err := result.Error(); err != nil {
		return fmt.Errorf("SSA patch failed: %w", err)
	}

	return nil
}
