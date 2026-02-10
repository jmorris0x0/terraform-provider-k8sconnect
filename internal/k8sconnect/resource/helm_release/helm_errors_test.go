package helm_release

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestClassifyHelmError(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		expected helmErrorKind
	}{
		{
			name:     "context deadline exceeded",
			err:      "resource Deployment/qa-helm/qa-timeout not ready. status: InProgress, message: Available: 0/1\ncontext deadline exceeded",
			expected: helmErrorTimeout,
		},
		{
			name:     "timed out waiting",
			err:      "timed out waiting for the condition",
			expected: helmErrorTimeout,
		},
		{
			name:     "not ready without timeout keywords still matches",
			err:      "resource Deployment/default/app not ready. status: InProgress",
			expected: helmErrorTimeout,
		},
		{
			name:     "rollback on failure",
			err:      "release qa-atomic failed, and has been uninstalled due to rollback-on-failure being set: resource Deployment/qa-helm/qa-atomic not ready. context deadline exceeded",
			expected: helmErrorRollback,
		},
		{
			name:     "rollback keyword",
			err:      "release failed, rollback initiated: context deadline exceeded",
			expected: helmErrorRollback,
		},
		{
			name:     "RollbackOnFailure keyword",
			err:      "RollbackOnFailure: release not ready, context deadline exceeded",
			expected: helmErrorRollback,
		},
		{
			name:     "namespace not found",
			err:      "create: failed to create: namespaces \"this-namespace-does-not-exist\" not found",
			expected: helmErrorNamespaceNotFound,
		},
		{
			name:     "unknown error",
			err:      "chart requires kubeVersion: >=1.21.0 which is incompatible with Kubernetes v1.20.0",
			expected: helmErrorUnknown,
		},
		{
			name:     "empty error",
			err:      "",
			expected: helmErrorUnknown,
		},
		{
			name:     "generic not found is not namespace error",
			err:      "release \"my-release\" not found",
			expected: helmErrorUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyHelmError(fmt.Errorf("%s", tt.err))
			if got != tt.expected {
				t.Errorf("classifyHelmError(%q) = %d, want %d", tt.err, got, tt.expected)
			}
		})
	}
}

func TestSuggestTimeout(t *testing.T) {
	tests := []struct {
		name     string
		current  time.Duration
		expected string
	}{
		{
			name:     "doubles 5m to 10m",
			current:  5 * time.Minute,
			expected: "10m0s",
		},
		{
			name:     "doubles 30s to 1m (minimum 60s)",
			current:  30 * time.Second,
			expected: "1m0s",
		},
		{
			name:     "very short enforces minimum 60s",
			current:  5 * time.Second,
			expected: "1m0s",
		},
		{
			name:     "doubles 1h to 2h",
			current:  1 * time.Hour,
			expected: "2h0m0s",
		},
		{
			name:     "zero enforces minimum 60s",
			current:  0,
			expected: "1m0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := suggestTimeout(tt.current)
			if got != tt.expected {
				t.Errorf("suggestTimeout(%v) = %q, want %q", tt.current, got, tt.expected)
			}
		})
	}
}

func TestFormatNamespaceNotFoundError(t *testing.T) {
	title, detail := formatNamespaceNotFoundError("my-release", "prod-ns", fmt.Errorf("namespaces \"prod-ns\" not found"))

	if title != "Namespace Not Found" {
		t.Errorf("title = %q, want %q", title, "Namespace Not Found")
	}

	// Must contain release name and namespace
	if !strings.Contains(detail, "my-release") {
		t.Error("detail should contain release name")
	}
	if !strings.Contains(detail, "prod-ns") {
		t.Error("detail should contain namespace")
	}

	// Must suggest create_namespace
	if !strings.Contains(detail, "create_namespace = true") {
		t.Error("detail should suggest create_namespace = true")
	}

	// Must suggest kubectl create
	if !strings.Contains(detail, "kubectl create namespace prod-ns") {
		t.Error("detail should suggest kubectl create namespace command")
	}
}

func TestFormatTimeoutError(t *testing.T) {
	ctx := context.Background()
	// Pass nil rcg to skip pod diagnostics
	title, detail := formatTimeoutError(ctx, "Install", "my-release", "default", 30*time.Second, fmt.Errorf("context deadline exceeded"), nil)

	if !strings.Contains(title, "Timed Out") {
		t.Errorf("title should contain 'Timed Out', got %q", title)
	}

	// Must contain human-readable timeout instead of raw Go error
	if !strings.Contains(detail, "30s") {
		t.Error("detail should contain human-readable timeout duration")
	}
	if strings.Contains(detail, "context deadline exceeded") {
		t.Error("detail should NOT contain raw 'context deadline exceeded'")
	}

	// Must contain release name and namespace
	if !strings.Contains(detail, "my-release") {
		t.Error("detail should contain release name")
	}
	if !strings.Contains(detail, "default") {
		t.Error("detail should contain namespace")
	}

	// Must have Options section
	if !strings.Contains(detail, "Options:") {
		t.Error("detail should contain Options section")
	}

	// Must suggest increasing timeout
	if !strings.Contains(detail, "timeout =") {
		t.Error("detail should suggest increasing timeout")
	}

	// Must suggest kubectl command
	if !strings.Contains(detail, "kubectl get pods") {
		t.Error("detail should suggest kubectl get pods command")
	}

	// Must suggest wait = false
	if !strings.Contains(detail, "wait = false") {
		t.Error("detail should suggest wait = false")
	}
}

func TestFormatRollbackError(t *testing.T) {
	ctx := context.Background()
	title, detail := formatRollbackError(ctx, "Install", "my-release", "qa-ns", 5*time.Minute, fmt.Errorf("rollback on failure"), nil)

	if !strings.Contains(title, "Rolled Back") {
		t.Errorf("title should contain 'Rolled Back', got %q", title)
	}

	// Must mention automatic rollback
	if !strings.Contains(detail, "automatically rolled back") {
		t.Error("detail should mention automatic rollback")
	}

	// Must suggest atomic = false
	if !strings.Contains(detail, "atomic = false") {
		t.Error("detail should suggest atomic = false")
	}

	// Must contain namespace in kubectl command
	if !strings.Contains(detail, "kubectl get pods -n qa-ns") {
		t.Error("detail should contain namespace-specific kubectl command")
	}
}

func TestFormatHelmOperationError_Dispatch(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		err           string
		expectInTitle string
	}{
		{
			name:          "timeout dispatches to timeout formatter",
			err:           "context deadline exceeded",
			expectInTitle: "Timed Out",
		},
		{
			name:          "rollback dispatches to rollback formatter",
			err:           "uninstalled due to rollback-on-failure: context deadline exceeded",
			expectInTitle: "Rolled Back",
		},
		{
			name:          "namespace not found dispatches to namespace formatter",
			err:           "namespaces \"test\" not found",
			expectInTitle: "Namespace Not Found",
		},
		{
			name:          "unknown falls through to default",
			err:           "some random helm error",
			expectInTitle: "Failed to Install Helm Release",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, _ := formatHelmOperationError(ctx, "Install", "test", "default", 5*time.Minute, fmt.Errorf("%s", tt.err), nil)
			if !strings.Contains(title, tt.expectInTitle) {
				t.Errorf("title = %q, want to contain %q", title, tt.expectInTitle)
			}
		})
	}
}

func TestFormatHelmOperationError_UnknownPreservesRawError(t *testing.T) {
	ctx := context.Background()
	rawErr := "chart requires kubeVersion: >=1.28.0"
	_, detail := formatHelmOperationError(ctx, "Upgrade", "my-rel", "default", 5*time.Minute, fmt.Errorf("%s", rawErr), nil)

	if !strings.Contains(detail, rawErr) {
		t.Errorf("unknown errors should preserve raw error string, got: %s", detail)
	}
}

func TestGetPodDiagnostics_NilRCG(t *testing.T) {
	ctx := context.Background()
	result := getPodDiagnostics(ctx, "default", nil)
	if result != "" {
		t.Errorf("nil rcg should return empty string, got: %q", result)
	}
}
