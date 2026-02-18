package object_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccObjectResource_ExpiredToken_PlanSucceeds is the primary ADR-023 bug reproduction.
//
// This is the exact scenario from the issue:
// "my kubeconfig is pulled from a data source and once the token expires
//
//	it seems the k8s_connect resource never uses the new one it's using
//	the cache so plan always fails"
//
// Flow:
// 1. Create two ServiceAccounts upfront: SA1 (for token1) and SA2 (for token2)
// 2. Create resource with token1 (stored in state)
// 3. Delete SA1 (invalidates token1 instantly — no waiting for expiry)
// 4. Plan with token2 from SA2 (still valid)
// 5. BEFORE ADR-023: Plan fails because Read uses invalidated token1 from state
// 6. AFTER ADR-023: Read warns, ModifyPlan uses fresh token2, plan succeeds
func TestAccObjectResource_ExpiredToken_PlanSucceeds(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	raw := os.Getenv("TF_ACC_KUBECONFIG")

	if host == "" || ca == "" || raw == "" {
		t.Skip("TF_ACC_K8S_HOST, TF_ACC_K8S_CA, and TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ctx := context.Background()

	ns := fmt.Sprintf("expired-tok-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("expired-tok-cm-%d", time.Now().UnixNano()%1000000)
	saName1 := fmt.Sprintf("expired-tok-sa1-%d", time.Now().UnixNano()%1000000)
	saName2 := fmt.Sprintf("expired-tok-sa2-%d", time.Now().UnixNano()%1000000)

	// Create two ServiceAccounts upfront — both tokens valid at struct construction time.
	// SA1's token will be invalidated by deleting SA1 in Step 2's PreConfig.
	// SA2's token remains valid for the fresh config in Step 2.
	token1 := createServiceAccountWithToken(t, k8sClient, ctx, saName1)
	token2 := createServiceAccountWithToken(t, k8sClient, ctx, saName2)
	t.Logf("Created SA1=%s (token1 len=%d), SA2=%s (token2 len=%d)", saName1, len(token1), saName2, len(token2))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create resource with token1.
			// Token1 gets stored in state.
			{
				Config: testAccExpiredTokenConfig(ns, cmName),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"token":     config.StringVariable(token1),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.expired_token_test", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Invalidate token1 by deleting SA1, then plan with token2 (from SA2).
			// State still has token1 (now invalid).
			//
			// BEFORE ADR-023: Read uses invalid token1 from state → 401 → ERROR → plan fails
			// AFTER ADR-023: Read uses invalid token1 → 401 → WARNING → stale state preserved
			//                ModifyPlan uses fresh token2 from Config → succeeds → plan works
			{
				PreConfig: func() {
					// Delete SA1 to invalidate token1 (instant — no waiting for expiry)
					deleteServiceAccount(t, k8sClient, ctx, saName1)
					t.Log("Deleted SA1 — token1 is now invalid")
				},
				Config: testAccExpiredTokenConfig(ns, cmName),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"token":     config.StringVariable(token2),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // Token changed (token1→token2), plan shows cluster update
			},
			// Step 3: Apply with token2 to update state credentials for cleanup.
			// After PlanOnly, state still has invalid token1. The framework's destroy
			// phase needs valid credentials in state to delete resources.
			{
				Config: testAccExpiredTokenConfig(ns, cmName),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"token":     config.StringVariable(token2),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
			},
		},
		CheckDestroy: func(s *terraform.State) error {
			// Cleanup both SAs
			deleteServiceAccount(t, k8sClient, ctx, saName1)
			deleteServiceAccount(t, k8sClient, ctx, saName2)
			return testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName)(s)
		},
	})
}

// TestAccObjectResource_ExpiredToken_DriftDetected verifies that drift detection
// still works correctly when the stored token has been invalidated.
//
// This is the critical follow-up: plan shouldn't crash AND should still catch drift.
func TestAccObjectResource_ExpiredToken_DriftDetected(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	raw := os.Getenv("TF_ACC_KUBECONFIG")

	if host == "" || ca == "" || raw == "" {
		t.Skip("TF_ACC_K8S_HOST, TF_ACC_K8S_CA, and TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ctx := context.Background()

	ns := fmt.Sprintf("expired-drift-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("expired-drift-cm-%d", time.Now().UnixNano()%1000000)
	saName1 := fmt.Sprintf("expired-drift-sa1-%d", time.Now().UnixNano()%1000000)
	saName2 := fmt.Sprintf("expired-drift-sa2-%d", time.Now().UnixNano()%1000000)

	// Create two SAs upfront
	token1 := createServiceAccountWithToken(t, k8sClient, ctx, saName1)
	token2 := createServiceAccountWithToken(t, k8sClient, ctx, saName2)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create with token1
			{
				Config: testAccExpiredTokenConfig(ns, cmName),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"token":     config.StringVariable(token1),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.expired_token_test", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Invalidate token1 + externally modify resource → drift MUST be detected
			{
				PreConfig: func() {
					// Invalidate token1
					deleteServiceAccount(t, k8sClient, ctx, saName1)
					t.Log("Deleted SA1 — token1 is now invalid")

					// Externally modify the ConfigMap
					cm, err := k8sClient.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
					if err != nil {
						t.Fatalf("Failed to get ConfigMap: %v", err)
					}
					cm.Data["key1"] = "externally-modified"
					_, err = k8sClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{
						FieldManager: "kubectl-patch",
					})
					if err != nil {
						t.Fatalf("Failed to modify ConfigMap externally: %v", err)
					}
					t.Log("Token1 invalidated AND ConfigMap modified externally")
				},
				Config: testAccExpiredTokenConfig(ns, cmName),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"token":     config.StringVariable(token2),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // MUST detect drift even with invalidated token in state
			},
			// Step 3: Apply to correct drift (uses token2 which is still valid)
			{
				Config: testAccExpiredTokenConfig(ns, cmName),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"token":     config.StringVariable(token2),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapData(k8sClient, ns, cmName, map[string]string{
						"key1": "value1",
						"key2": "value2",
					}),
				),
			},
		},
		CheckDestroy: func(s *terraform.State) error {
			deleteServiceAccount(t, k8sClient, ctx, saName1)
			deleteServiceAccount(t, k8sClient, ctx, saName2)
			return testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName)(s)
		},
	})
}

// createServiceAccountWithToken creates a ServiceAccount with cluster-admin
// and returns a bearer token for it.
func createServiceAccountWithToken(t *testing.T, client kubernetes.Interface, ctx context.Context, saName string) string {
	t.Helper()

	// Create ServiceAccount
	_, err := client.CoreV1().ServiceAccounts("default").Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: saName},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create ServiceAccount %s: %v", saName, err)
	}

	// Create ClusterRoleBinding
	_, err = client.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: saName + "-admin"},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: "default",
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create ClusterRoleBinding for %s: %v", saName, err)
	}

	// Create token (1 hour — we invalidate by deleting the SA, not by waiting)
	expiry := int64(3600)
	tokenReq, err := client.CoreV1().ServiceAccounts("default").CreateToken(ctx, saName, &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			ExpirationSeconds: &expiry,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create token for %s: %v", saName, err)
	}

	return tokenReq.Status.Token
}

// deleteServiceAccount removes the SA and its ClusterRoleBinding, invalidating any tokens.
func deleteServiceAccount(t *testing.T, client kubernetes.Interface, ctx context.Context, saName string) {
	t.Helper()

	_ = client.CoreV1().ServiceAccounts("default").Delete(ctx, saName, metav1.DeleteOptions{})
	_ = client.RbacV1().ClusterRoleBindings().Delete(ctx, saName+"-admin", metav1.DeleteOptions{})
}

func testAccExpiredTokenConfig(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "host" { type = string }
variable "ca" { type = string }
variable "token" { type = string }
variable "namespace" { type = string }
variable "cm_name" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "expired_token_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    token                  = var.token
  }
}

resource "k8sconnect_object" "expired_token_test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key1: value1
  key2: value2
YAML

  cluster = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    token                  = var.token
  }
  depends_on = [k8sconnect_object.expired_token_namespace]
}
`, namespace, cmName, namespace)
}
