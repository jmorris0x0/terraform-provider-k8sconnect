package test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// CreateServiceAccountWithToken creates a ServiceAccount with cluster-admin
// and returns a bearer token for it.
func CreateServiceAccountWithToken(t *testing.T, client kubernetes.Interface, ctx context.Context, saName string) string {
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

// DeleteServiceAccount removes the SA and its ClusterRoleBinding, invalidating any tokens.
func DeleteServiceAccount(t *testing.T, client kubernetes.Interface, ctx context.Context, saName string) {
	t.Helper()

	_ = client.CoreV1().ServiceAccounts("default").Delete(ctx, saName, metav1.DeleteOptions{})
	_ = client.RbacV1().ClusterRoleBindings().Delete(ctx, saName+"-admin", metav1.DeleteOptions{})
}

// WaitForTokenRejection polls the K8s API with the given token until it is rejected
// (401/403) or the deadline expires. This makes ADR-023 tests deterministic across
// environments: on fast networks (CI Linux), SA-deletion propagation may lag the next
// API call enough that the token still validates; this loop guarantees we don't proceed
// until the token is genuinely rejected.
func WaitForTokenRejection(t *testing.T, host, caBase64, token string) {
	t.Helper()

	caBytes, err := base64.StdEncoding.DecodeString(caBase64)
	if err != nil {
		t.Fatalf("failed to decode CA: %v", err)
	}

	config := &rest.Config{
		Host:        host,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caBytes,
		},
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("failed to build polling client: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, err := client.Discovery().ServerResourcesForGroupVersion("v1")
		if err != nil {
			if apierrors.IsUnauthorized(err) || apierrors.IsForbidden(err) {
				t.Log("Confirmed token rejected by API server")
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("Token still accepted by API server after 30s; SA deletion did not propagate. " +
		"ADR-023 tests cannot reliably exercise the auth-error path until this resolves.")
}
