package wait_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccWaitResource_PollsForResourceToExist verifies that when a wait resource
// references a Kubernetes object that doesn't exist yet, the wait polls for
// existence (bounded by the configured timeout) instead of failing immediately.
//
// This is the use case from issue #171: operators (Stackgres, cert-manager,
// ALB controller, Karpenter, Crossplane, etc.) create downstream resources
// lazily after their CR is reconciled. Users need to wait on those downstream
// resources without knowing exactly when they will appear.
//
// Timing strategy:
//
//	terraform-plugin-testing setup typically takes ~60s before the apply phase
//	starts and the wait resource Create runs. We delay the goroutine for 90s
//	so the Service appears AFTER wait Create has started polling, deterministically
//	exercising the polling code path regardless of machine speed.
func TestAccWaitResource_PollsForResourceToExist(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-exist-%d", time.Now().UnixNano()%1000000)
	svcName := fmt.Sprintf("delayed-svc-%d", time.Now().UnixNano()%1000000)

	// Pre-create namespace directly so the wait resource has somewhere to find the Service
	testhelpers.CreateNamespaceDirectly(t, k8sClient, nsName)
	t.Cleanup(func() {
		_ = k8sClient.CoreV1().Namespaces().Delete(context.Background(), nsName, metav1.DeleteOptions{})
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					// Simulate an operator creating the Service ~90 seconds into the test.
					// This is well past the typical ~60s of terraform-plugin-testing setup,
					// ensuring wait Create has already started polling when this fires.
					go func() {
						time.Sleep(90 * time.Second)
						if err := createServiceDirectly(k8sClient, nsName, svcName); err != nil {
							fmt.Printf("Background Service creation failed: %v\n", err)
						} else {
							fmt.Printf("✅ Background goroutine created Service %s/%s\n", nsName, svcName)
						}
					}()
				},
				Config: testAccWaitConfigPollExistence(nsName, svcName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckServiceExists(k8sClient, nsName, svcName),
					resource.TestCheckResourceAttrSet("k8sconnect_wait.delayed", "id"),
				),
			},
		},
	})
}

// TestAccWaitResource_TimesOutWhenResourceNeverAppears verifies that when a wait
// resource references an object that never gets created, the wait times out
// cleanly with a clear error message that distinguishes "object never appeared"
// from "object was deleted" or "wait condition not met."
//
// The error message regex is intentionally strict: it matches only the NEW
// post-polling error message ("did not appear" / "never appeared within").
// This causes the test to FAIL on the pre-fix codebase, which emits the
// fast-fail "was not found" error instead.
func TestAccWaitResource_TimesOutWhenResourceNeverAppears(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-noexist-%d", time.Now().UnixNano()%1000000)
	svcName := fmt.Sprintf("never-svc-%d", time.Now().UnixNano()%1000000)

	testhelpers.CreateNamespaceDirectly(t, k8sClient, nsName)
	t.Cleanup(func() {
		_ = k8sClient.CoreV1().Namespaces().Delete(context.Background(), nsName, metav1.DeleteOptions{})
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigPollExistenceShortTimeout(nsName, svcName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile(`(?s)Wait Operation Failed.*(did not appear|never appeared)`),
			},
		},
	})
}

func testAccWaitConfigPollExistence(namespace, svcName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_wait" "delayed" {
  object_ref = {
    api_version = "v1"
    kind        = "Service"
    name        = %q
    namespace   = %q
  }

  cluster = {
    kubeconfig = var.raw
  }

  wait_for = {
    field   = "spec.clusterIP"
    timeout = "180s"
  }
}
`, svcName, namespace)
}

func testAccWaitConfigPollExistenceShortTimeout(namespace, svcName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_wait" "missing" {
  object_ref = {
    api_version = "v1"
    kind        = "Service"
    name        = %q
    namespace   = %q
  }

  cluster = {
    kubeconfig = var.raw
  }

  wait_for = {
    field   = "spec.clusterIP"
    timeout = "5s"
  }
}
`, svcName, namespace)
}

// TestAccWaitResource_PollsForClusterScopedResource verifies polling works for
// cluster-scoped resources (no namespace), exercising a different code path in
// the Get call than namespaced resources.
func TestAccWaitResource_PollsForClusterScopedResource(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-cluster-poll-%d", time.Now().UnixNano()%1000000)

	t.Cleanup(func() {
		_ = k8sClient.CoreV1().Namespaces().Delete(context.Background(), nsName, metav1.DeleteOptions{})
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					go func() {
						time.Sleep(90 * time.Second)
						if err := createNamespaceDirectly(k8sClient, nsName); err != nil {
							fmt.Printf("Background Namespace creation failed: %v\n", err)
						} else {
							fmt.Printf("✅ Background goroutine created Namespace %s\n", nsName)
						}
					}()
				},
				Config: testAccWaitConfigClusterScopedPoll(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, nsName),
					resource.TestCheckResourceAttrSet("k8sconnect_wait.cluster_scoped", "id"),
				),
			},
		},
	})
}

// TestAccWaitResource_UnknownKindFailsFast verifies that polling does NOT engage
// when the resource kind itself is invalid (CRD not installed, typo in kind).
// GVR discovery fails before polling, so the error message must be the discovery
// error, not the polling-timeout error.
//
// The strict regex is what protects against regression: if polling were
// engaged on an unknown kind, the resulting error would say "did not appear
// within" which would not match this regex, failing the test.
func TestAccWaitResource_UnknownKindFailsFast(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	nsName := fmt.Sprintf("wait-unknown-kind-%d", time.Now().UnixNano()%1000000)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, nsName)
	t.Cleanup(func() {
		_ = k8sClient.CoreV1().Namespaces().Delete(context.Background(), nsName, metav1.DeleteOptions{})
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccWaitConfigUnknownKind(nsName),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile(`(?s)(Failed to Discover GVR|not found in apiVersion)`),
			},
		},
	})
}

func testAccWaitConfigClusterScopedPoll(nsName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_wait" "cluster_scoped" {
  object_ref = {
    api_version = "v1"
    kind        = "Namespace"
    name        = %q
  }

  cluster = {
    kubeconfig = var.raw
  }

  wait_for = {
    field   = "metadata.uid"
    timeout = "180s"
  }
}
`, nsName)
}

func testAccWaitConfigUnknownKind(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_wait" "unknown" {
  object_ref = {
    api_version = "fake.example.com/v1"
    kind        = "NotARealResource"
    name        = "anything"
    namespace   = %q
  }

  cluster = {
    kubeconfig = var.raw
  }

  wait_for = {
    field   = "spec.foo"
    timeout = "30s"
  }
}
`, namespace)
}

func createNamespaceDirectly(client kubernetes.Interface, name string) error {
	ctx := context.Background()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	_, err := client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	return err
}

// createServiceDirectly creates a minimal Service directly via the K8s client.
// Used to simulate an operator creating a downstream resource lazily.
func createServiceDirectly(client kubernetes.Interface, namespace, name string) error {
	ctx := context.Background()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80}},
		},
	}
	_, err := client.CoreV1().Services(namespace).Create(ctx, svc, metav1.CreateOptions{})
	return err
}
