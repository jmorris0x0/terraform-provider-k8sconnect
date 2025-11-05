package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	sigsyaml "sigs.k8s.io/yaml"
)

// Create K8s client for verification
func CreateK8sClient(t *testing.T, kubeconfigRaw string) kubernetes.Interface {
	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfigRaw))
	if err != nil {
		t.Fatalf("Failed to create kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	return clientset
}

// Check function to verify namespace exists in K8s
func CheckNamespaceExists(client kubernetes.Interface, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		_, err := client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("namespace %q does not exist in Kubernetes: %v", name, err)
		}

		fmt.Printf("✅ Verified namespace %q exists in Kubernetes\n", name)
		return nil
	}
}

// Check function to verify namespace is cleaned up
func CheckNamespaceDestroy(client kubernetes.Interface, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 10; i++ {
			_, err := client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified namespace %q was deleted from Kubernetes\n", name)
					return nil
				}
				return fmt.Errorf("unexpected error checking namespace %q: %v", name, err)
			}

			// Namespace still exists, wait a bit
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("namespace %q still exists in Kubernetes after waiting for deletion", name)
	}
}

func CheckPodExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("pod %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified pod %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

func CheckPodDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 15; i++ {
			_, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified pod %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking pod: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("pod %s/%s still exists after deletion", namespace, name)
	}
}

func CheckConfigMapExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("configmap %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified configmap %s/%s exists (inferred namespace)\n", namespace, name)
		return nil
	}
}

func CheckConfigMapDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 10; i++ {
			_, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified configmap %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking configmap: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("configmap %s/%s still exists after deletion", namespace, name)
	}
}

func CheckConfigMapData(client kubernetes.Interface, namespace, name string, expectedData map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get configmap %s/%s: %v", namespace, name, err)
		}

		for key, expectedValue := range expectedData {
			actualValue, exists := cm.Data[key]
			if !exists {
				return fmt.Errorf("configmap %s/%s missing expected key %q", namespace, name, key)
			}
			if actualValue != expectedValue {
				return fmt.Errorf("configmap %s/%s key %q: expected %q, got %q", namespace, name, key, expectedValue, actualValue)
			}
		}

		fmt.Printf("✅ Verified configmap %s/%s has expected data\n", namespace, name)
		return nil
	}
}

func CheckConfigMapAnnotation(client kubernetes.Interface, namespace, name, annotationKey, expectedValue string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get configmap %s/%s: %v", namespace, name, err)
		}

		actualValue, exists := cm.Annotations[annotationKey]
		if !exists {
			return fmt.Errorf("configmap %s/%s missing expected annotation %q", namespace, name, annotationKey)
		}
		if actualValue != expectedValue {
			return fmt.Errorf("configmap %s/%s annotation %q: expected %q, got %q", namespace, name, annotationKey, expectedValue, actualValue)
		}

		fmt.Printf("✅ Verified configmap %s/%s has expected annotation %s=%s\n", namespace, name, annotationKey, expectedValue)
		return nil
	}
}

func CheckConfigMapCount(client kubernetes.Interface, namespace string, expectedCount int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		list, err := client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list configmaps in namespace %s: %v", namespace, err)
		}

		actualCount := len(list.Items)
		if actualCount != expectedCount {
			return fmt.Errorf("expected %d configmap(s) in namespace %s, got %d", expectedCount, namespace, actualCount)
		}

		fmt.Printf("✅ Verified namespace %s has %d configmap(s)\n", namespace, actualCount)
		return nil
	}
}

func CheckPVCExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("pvc %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified PVC %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

func CheckPVCDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 20; i++ { // Longer wait for PVCs
			_, err := client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified PVC %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking PVC: %v", err)
			}
			time.Sleep(2 * time.Second)
		}
		return fmt.Errorf("PVC %s/%s still exists after deletion", namespace, name)
	}
}

func CheckPVCHasLabel(client kubernetes.Interface, namespace, name, labelKey, expectedValue string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		pvc, err := client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get pvc %s/%s: %v", namespace, name, err)
		}

		actualValue, exists := pvc.Labels[labelKey]
		if !exists {
			return fmt.Errorf("pvc %s/%s missing expected label %q", namespace, name, labelKey)
		}
		if actualValue != expectedValue {
			return fmt.Errorf("pvc %s/%s label %q: expected %q, got %q", namespace, name, labelKey, expectedValue, actualValue)
		}

		fmt.Printf("✅ Verified PVC %s/%s has label %s=%s\n", namespace, name, labelKey, expectedValue)
		return nil
	}
}

func CheckPVCStorage(client kubernetes.Interface, namespace, name, expectedStorage string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		pvc, err := client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get pvc %s/%s: %v", namespace, name, err)
		}

		storageRequest, exists := pvc.Spec.Resources.Requests["storage"]
		if !exists {
			return fmt.Errorf("pvc %s/%s has no storage request", namespace, name)
		}

		actualStorage := storageRequest.String()
		if actualStorage != expectedStorage {
			return fmt.Errorf("pvc %s/%s storage: expected %q, got %q", namespace, name, expectedStorage, actualStorage)
		}

		fmt.Printf("✅ Verified PVC %s/%s has storage %s\n", namespace, name, expectedStorage)
		return nil
	}
}

func CheckDeploymentExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("deployment %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified deployment %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

func CheckDeploymentDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 15; i++ {
			_, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified deployment %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking deployment: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("deployment %s/%s still exists after deletion", namespace, name)
	}
}

func CheckDaemonSetExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("daemonset %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified daemonset %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

func CheckDaemonSetDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 15; i++ {
			_, err := client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified daemonset %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking daemonset: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("daemonset %s/%s still exists after deletion", namespace, name)
	}
}

func CheckServiceExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("service %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified service %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

func CheckServiceDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 10; i++ {
			_, err := client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified service %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking service: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("service %s/%s still exists after deletion", namespace, name)
	}
}

func CheckResourceQuotaDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().ResourceQuotas(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				fmt.Printf("✅ Verified ResourceQuota %s/%s was deleted\n", namespace, name)
				return nil
			}
			return fmt.Errorf("unexpected error checking ResourceQuota: %v", err)
		}
		return fmt.Errorf("ResourceQuota %s/%s still exists after deletion", namespace, name)
	}
}

// Helper to check specific data value in ConfigMap
func CheckConfigMapDataValue(client kubernetes.Interface, namespace, name, key, expectedValue string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap: %v", err)
		}

		actualValue, exists := cm.Data[key]
		if !exists {
			return fmt.Errorf("ConfigMap %s/%s missing data key %s", namespace, name, key)
		}

		if actualValue != expectedValue {
			return fmt.Errorf("ConfigMap %s/%s data[%s] = %q, want %q",
				namespace, name, key, actualValue, expectedValue)
		}

		return nil
	}
}

// Helper to check ownership annotations exist
func CheckOwnershipAnnotations(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ConfigMap: %v", err)
		}

		annotations := cm.GetAnnotations()
		if annotations == nil {
			return fmt.Errorf("ConfigMap has no annotations")
		}

		if _, ok := annotations["k8sconnect.terraform.io/terraform-id"]; !ok {
			return fmt.Errorf("ConfigMap missing ownership annotation k8sconnect.terraform.io/terraform-id")
		}

		return nil
	}
}

// Helper function to check deployment replica count
func CheckDeploymentReplicaCount(client *kubernetes.Clientset, namespace, name string, expected int32) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment: %v", err)
		}

		if *deployment.Spec.Replicas != expected {
			return fmt.Errorf("expected %d replicas, got %d", expected, *deployment.Spec.Replicas)
		}

		return nil
	}
}

// Helper function to check deployment container image
func CheckDeploymentImage(client *kubernetes.Clientset, namespace, name, expectedImage string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment: %v", err)
		}

		if len(deployment.Spec.Template.Spec.Containers) == 0 {
			return fmt.Errorf("deployment %s/%s has no containers", namespace, name)
		}

		actualImage := deployment.Spec.Template.Spec.Containers[0].Image
		if actualImage != expectedImage {
			return fmt.Errorf("expected image %q, got %q", expectedImage, actualImage)
		}

		return nil
	}
}

// CheckDeploymentEnvVar checks that a specific environment variable in a deployment's container has the expected value
func CheckDeploymentEnvVar(client *kubernetes.Clientset, namespace, name, containerName, envVarName, expectedValue string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		deployment, err := client.AppsV1().Deployments(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment: %v", err)
		}

		// Find the container
		var container *corev1.Container
		for i := range deployment.Spec.Template.Spec.Containers {
			if deployment.Spec.Template.Spec.Containers[i].Name == containerName {
				container = &deployment.Spec.Template.Spec.Containers[i]
				break
			}
		}
		if container == nil {
			return fmt.Errorf("container %q not found in deployment %s/%s", containerName, namespace, name)
		}

		// Find the env var
		for _, env := range container.Env {
			if env.Name == envVarName {
				if env.Value != expectedValue {
					return fmt.Errorf("expected env var %q to have value %q, got %q", envVarName, expectedValue, env.Value)
				}
				return nil
			}
		}

		return fmt.Errorf("env var %q not found in container %q", envVarName, containerName)
	}
}

func CheckStatefulSetExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("statefulset %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified statefulset %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

func CheckStatefulSetDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 20; i++ { // StatefulSets can take longer to delete due to ordered pod termination
			_, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified statefulset %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking statefulset: %v", err)
			}
			time.Sleep(2 * time.Second)
		}
		return fmt.Errorf("statefulset %s/%s still exists after deletion", namespace, name)
	}
}

// CheckJobExists verifies a Job exists in the cluster
func CheckJobExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("job %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified job %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

// CheckJobDestroy verifies a Job has been deleted
func CheckJobDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 15; i++ {
			_, err := client.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified job %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking job: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("job %s/%s still exists after deletion", namespace, name)
	}
}

// CheckConfigMapFieldSet verifies that a specific field exists in a ConfigMap's data (value can be any non-empty string)
func CheckConfigMapFieldSet(client kubernetes.Interface, namespace, name, fieldPath string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get configmap %s/%s: %v", namespace, name, err)
		}

		// Parse the field path (e.g., "data.source_id")
		if !strings.HasPrefix(fieldPath, "data.") {
			return fmt.Errorf("field path must start with 'data.' for ConfigMap, got: %s", fieldPath)
		}

		key := strings.TrimPrefix(fieldPath, "data.")
		value, exists := cm.Data[key]
		if !exists {
			return fmt.Errorf("configmap %s/%s missing expected field %q", namespace, name, key)
		}

		if value == "" {
			return fmt.Errorf("configmap %s/%s field %q exists but is empty", namespace, name, key)
		}

		fmt.Printf("✅ Verified configmap %s/%s has field %s set (value: %s)\n", namespace, name, key, value)
		return nil
	}
}

// CheckServiceAccountExists verifies a ServiceAccount exists in the cluster
func CheckServiceAccountExists(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.CoreV1().ServiceAccounts(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("serviceaccount %s/%s does not exist: %v", namespace, name, err)
		}
		fmt.Printf("✅ Verified serviceaccount %s/%s exists in Kubernetes\n", namespace, name)
		return nil
	}
}

// CheckServiceAccountDestroy verifies a ServiceAccount has been deleted
func CheckServiceAccountDestroy(client kubernetes.Interface, namespace, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 10; i++ {
			_, err := client.CoreV1().ServiceAccounts(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified serviceaccount %s/%s was deleted\n", namespace, name)
					return nil
				}
				return fmt.Errorf("unexpected error checking serviceaccount: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("serviceaccount %s/%s still exists after deletion", namespace, name)
	}
}

// CheckClusterRoleBindingExists verifies a ClusterRoleBinding exists in the cluster
func CheckClusterRoleBindingExists(client kubernetes.Interface, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		_, err := client.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("clusterrolebinding %s does not exist: %v", name, err)
		}
		fmt.Printf("✅ Verified clusterrolebinding %s exists in Kubernetes\n", name)
		return nil
	}
}

// CheckClusterRoleBindingDestroy verifies a ClusterRoleBinding has been deleted
func CheckClusterRoleBindingDestroy(client kubernetes.Interface, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		for i := 0; i < 10; i++ {
			_, err := client.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					fmt.Printf("✅ Verified clusterrolebinding %s was deleted\n", name)
					return nil
				}
				return fmt.Errorf("unexpected error checking clusterrolebinding: %v", err)
			}
			time.Sleep(1 * time.Second)
		}
		return fmt.Errorf("clusterrolebinding %s still exists after deletion", name)
	}
}

// CreateNamespaceDirectly creates a Namespace directly using the K8s client (bypassing Terraform)
// This is useful for testing scenarios where a namespace needs to exist before running tests
func CreateNamespaceDirectly(t *testing.T, client kubernetes.Interface, name string) {
	ctx := context.Background()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	_, err := client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create namespace directly: %v", err)
	}

	fmt.Printf("✅ Pre-created namespace %s directly in cluster\n", name)
}

// CreateConfigMapDirectly creates a ConfigMap directly using the K8s client (bypassing Terraform)
// This is useful for testing scenarios where a resource already exists in the cluster
func CreateConfigMapDirectly(t *testing.T, client kubernetes.Interface, namespace, name string, data map[string]string) {
	ctx := context.Background()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}

	_, err := client.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create ConfigMap directly: %v", err)
	}

	fmt.Printf("✅ Pre-created ConfigMap %s/%s directly in cluster\n", namespace, name)
}

// CleanupFinalizer removes all finalizers from a ConfigMap to allow deletion
// This is useful for cleaning up resources stuck in deletion due to test finalizers
func CleanupFinalizer(t *testing.T, client kubernetes.Interface, namespace, name string) {
	ctx := context.Background()

	cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		// If the resource is already gone, no cleanup needed
		if strings.Contains(err.Error(), "not found") {
			return
		}
		t.Logf("Warning: Failed to get ConfigMap for finalizer cleanup: %v", err)
		return
	}

	// Remove all finalizers
	cm.Finalizers = []string{}

	_, err = client.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		t.Logf("Warning: Failed to remove finalizers from ConfigMap: %v", err)
		return
	}

	// Try to delete the ConfigMap
	err = client.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !strings.Contains(err.Error(), "not found") {
		t.Logf("Warning: Failed to delete ConfigMap after removing finalizers: %v", err)
	} else {
		fmt.Printf("🧹 Cleaned up ConfigMap %s/%s (removed finalizers)\n", namespace, name)
	}
}

// CleanupNamespace forcefully deletes a namespace
// This is useful for cleaning up test namespaces
func CleanupNamespace(t *testing.T, client kubernetes.Interface, namespace string) {
	ctx := context.Background()

	err := client.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	if err != nil {
		if !strings.Contains(err.Error(), "not found") {
			t.Logf("Warning: Failed to delete namespace %s: %v", namespace, err)
		}
		return
	}

	// Wait for namespace to be deleted (up to 30 seconds)
	for i := 0; i < 30; i++ {
		_, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if err != nil && strings.Contains(err.Error(), "not found") {
			fmt.Printf("🧹 Cleaned up namespace %s\n", namespace)
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Logf("Warning: Namespace %s still exists after cleanup attempt", namespace)
}

// CreateConfigMapWithKubectl creates a ConfigMap using kubectl apply command
// This simulates an external tool creating a resource, which will have a different field manager
func CreateConfigMapWithKubectl(t *testing.T, namespace, name string, data map[string]string) {
	// Create YAML for the ConfigMap
	yaml := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
`, name, namespace)

	// Add data if provided
	if len(data) > 0 {
		yaml += "data:\n"
		for k, v := range data {
			yaml += fmt.Sprintf("  %s: %s\n", k, v)
		}
	}

	// Write to temp file
	tmpfile := fmt.Sprintf("/tmp/kubectl-cm-%s.yaml", name)
	if err := os.WriteFile(tmpfile, []byte(yaml), 0644); err != nil {
		t.Fatalf("Failed to write temp file for kubectl: %v", err)
	}
	defer os.Remove(tmpfile)

	// Apply with kubectl
	cmd := exec.Command("kubectl", "apply", "-f", tmpfile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl apply failed: %v\nOutput: %s", err, output)
	}

	fmt.Printf("✅ Created ConfigMap %s/%s with kubectl (field manager: kubectl-create)\n", namespace, name)
}

// DeleteResourceWithKubectl deletes a resource using kubectl delete command
// This simulates an out-of-band deletion (e.g., CRD deletion cascading to CRs)
func DeleteResourceWithKubectl(t *testing.T, kubeconfigRaw, resourceType, name, namespace string) {
	// Write kubeconfig to temp file
	tmpKubeconfig := fmt.Sprintf("/tmp/kubectl-kubeconfig-%d.yaml", time.Now().UnixNano())
	if err := os.WriteFile(tmpKubeconfig, []byte(kubeconfigRaw), 0600); err != nil {
		t.Fatalf("Failed to write kubeconfig for kubectl: %v", err)
	}
	defer os.Remove(tmpKubeconfig)

	// Build kubectl delete command
	args := []string{"--kubeconfig", tmpKubeconfig, "delete", resourceType, name}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "--ignore-not-found=true")

	cmd := exec.Command("kubectl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl delete failed: %v\nOutput: %s", err, output)
	}

	if namespace != "" {
		fmt.Printf("✅ Deleted %s %s/%s with kubectl\n", resourceType, namespace, name)
	} else {
		fmt.Printf("✅ Deleted %s %s with kubectl\n", resourceType, name)
	}
}

// CheckFieldManager verifies that a resource has the expected field manager
func CheckFieldManager(client kubernetes.Interface, namespace, kind, name, expectedManager string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Get the resource based on kind
		var managedFields []metav1.ManagedFieldsEntry
		var err error

		switch kind {
		case "ConfigMap":
			var cm *corev1.ConfigMap
			cm, err = client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
			if err == nil {
				managedFields = cm.ManagedFields
			}
		default:
			return fmt.Errorf("unsupported kind for field manager check: %s", kind)
		}

		if err != nil {
			return fmt.Errorf("failed to get %s %s/%s: %v", kind, namespace, name, err)
		}

		// Check if the expected manager owns any fields
		found := false
		for _, mf := range managedFields {
			if mf.Manager == expectedManager {
				found = true
				break
			}
		}

		if !found {
			var managers []string
			for _, mf := range managedFields {
				managers = append(managers, mf.Manager)
			}
			return fmt.Errorf("%s %s/%s does not have field manager %q. Found managers: %v",
				kind, namespace, name, expectedManager, managers)
		}

		fmt.Printf("✅ Verified %s %s/%s has field manager %q\n", kind, namespace, name, expectedManager)
		return nil
	}
}

// CheckHasAnnotation verifies that a resource has a specific annotation
func CheckHasAnnotation(client kubernetes.Interface, namespace, kind, name, annotationKey string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		// Get the resource based on kind
		var annotations map[string]string
		var err error

		switch kind {
		case "ConfigMap":
			var cm *corev1.ConfigMap
			cm, err = client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
			if err == nil {
				annotations = cm.Annotations
			}
		default:
			return fmt.Errorf("unsupported kind for annotation check: %s", kind)
		}

		if err != nil {
			return fmt.Errorf("failed to get %s %s/%s: %v", kind, namespace, name, err)
		}

		// Check if the annotation exists
		if _, found := annotations[annotationKey]; !found {
			return fmt.Errorf("%s %s/%s does not have annotation %q. Found annotations: %v",
				kind, namespace, name, annotationKey, annotations)
		}

		fmt.Printf("✅ Verified %s %s/%s has annotation %q\n", kind, namespace, name, annotationKey)
		return nil
	}
}

// ScaleDeploymentWithKubectl scales a deployment using kubectl scale command
// This simulates external drift (kubectl changes replicas with client-side apply manager)
func ScaleDeploymentWithKubectl(t *testing.T, namespace, name string, replicas int) {
	cmd := exec.Command("kubectl", "scale", "deployment", name, "-n", namespace, fmt.Sprintf("--replicas=%d", replicas))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl scale failed: %v\nOutput: %s", err, output)
	}

	fmt.Printf("✅ Scaled deployment %s/%s to %d replicas with kubectl (creates drift)\n", namespace, name, replicas)
}

// CheckDeploymentReplicas verifies that a deployment has the expected replica count
func CheckDeploymentReplicas(client kubernetes.Interface, namespace, name string, expectedReplicas int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()
		deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment %s/%s: %v", namespace, name, err)
		}

		actualReplicas := int32(expectedReplicas)
		if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != actualReplicas {
			return fmt.Errorf("deployment %s/%s expected %d replicas, got %v",
				namespace, name, expectedReplicas, deployment.Spec.Replicas)
		}

		fmt.Printf("✅ Verified deployment %s/%s has %d replicas\n", namespace, name, expectedReplicas)
		return nil
	}
}

// CheckYAMLSemanticEquality returns a TestCheckFunc that compares two YAML strings semantically,
// ignoring field ordering and whitespace differences. This is used to verify yaml_body attribute
// after import, where YAML field ordering may differ from user config due to Go map iteration order.
func CheckYAMLSemanticEquality(resourceName, attributeName, expectedYAML string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource %q not found in state", resourceName)
		}

		actualYAML, ok := rs.Primary.Attributes[attributeName]
		if !ok {
			return fmt.Errorf("attribute %q not found on resource %q", attributeName, resourceName)
		}

		// Parse both YAMLs into maps
		var expectedMap, actualMap map[string]interface{}

		if err := sigsyaml.Unmarshal([]byte(expectedYAML), &expectedMap); err != nil {
			return fmt.Errorf("failed to parse expected YAML: %w", err)
		}

		if err := sigsyaml.Unmarshal([]byte(actualYAML), &actualMap); err != nil {
			return fmt.Errorf("failed to parse actual YAML from state: %w", err)
		}

		// Deep compare the maps
		if !deepEqualMaps(expectedMap, actualMap) {
			return fmt.Errorf("YAML semantic mismatch:\n\nExpected:\n%s\n\nActual:\n%s",
				expectedYAML, actualYAML)
		}

		return nil
	}
}

// deepEqualMaps recursively compares two maps for semantic equality
func deepEqualMaps(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}

	for key, aVal := range a {
		bVal, ok := b[key]
		if !ok {
			return false
		}

		if !deepEqualValues(aVal, bVal) {
			return false
		}
	}

	return true
}

// deepEqualValues recursively compares two values for semantic equality
func deepEqualValues(a, b interface{}) bool {
	// Handle nil cases
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Handle maps
	aMap, aIsMap := a.(map[string]interface{})
	bMap, bIsMap := b.(map[string]interface{})
	if aIsMap && bIsMap {
		return deepEqualMaps(aMap, bMap)
	}
	if aIsMap != bIsMap {
		return false
	}

	// Handle slices
	aSlice, aIsSlice := a.([]interface{})
	bSlice, bIsSlice := b.([]interface{})
	if aIsSlice && bIsSlice {
		if len(aSlice) != len(bSlice) {
			return false
		}
		for i := range aSlice {
			if !deepEqualValues(aSlice[i], bSlice[i]) {
				return false
			}
		}
		return true
	}
	if aIsSlice != bIsSlice {
		return false
	}

	// For primitives, use direct comparison
	return a == b
}

// RemoveAnnotation removes an annotation from a Kubernetes resource
// This is used in tests to simulate annotation loss scenarios
func RemoveAnnotation(t *testing.T, client kubernetes.Interface, namespace, kind, name, annotationKey string) {
	ctx := context.Background()

	switch kind {
	case "ConfigMap":
		cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Failed to get ConfigMap %s/%s: %v", namespace, name, err)
		}

		if cm.Annotations == nil {
			fmt.Printf("⚠️  ConfigMap %s/%s has no annotations to remove\n", namespace, name)
			return
		}

		delete(cm.Annotations, annotationKey)

		_, err = client.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("Failed to update ConfigMap %s/%s: %v", namespace, name, err)
		}

		fmt.Printf("✅ Removed annotation %q from ConfigMap %s/%s\n", annotationKey, namespace, name)

	default:
		t.Fatalf("Unsupported kind for RemoveAnnotation: %s", kind)
	}
}
