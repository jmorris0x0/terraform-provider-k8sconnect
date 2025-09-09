// internal/k8sconnect/common/test/helpers.go
package test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
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

func WriteKubeconfigToTempFile(t *testing.T, kubeconfigContent string) string {
	tmpfile, err := os.CreateTemp("", "kubeconfig-import-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	if _, err := tmpfile.Write([]byte(kubeconfigContent)); err != nil {
		tmpfile.Close()
		os.Remove(tmpfile.Name())
		t.Fatalf("Failed to write kubeconfig: %v", err)
	}

	if err := tmpfile.Close(); err != nil {
		os.Remove(tmpfile.Name())
		t.Fatalf("Failed to close temp file: %v", err)
	}

	return tmpfile.Name()
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

// Add these functions to internal/k8sconnect/common/test/helpers.go

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
