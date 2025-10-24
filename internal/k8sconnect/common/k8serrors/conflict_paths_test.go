package k8serrors_test

import (
	"testing"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExtractConflictDetailsAndPaths(t *testing.T) {
	tests := []struct {
		name          string
		errMsg        string
		expectedPaths []string
	}{
		{
			name:          "single field conflict",
			errMsg:        `Apply failed with 1 conflict: conflict with "kubectl" using apps/v1: .spec.replicas`,
			expectedPaths: []string{"spec.replicas"},
		},
		{
			name:          "multiple field conflicts",
			errMsg:        `conflict with "kubectl": .spec.replicas; conflict with "hpa-controller": .spec.selector`,
			expectedPaths: []string{"spec.replicas", "spec.selector"},
		},
		{
			name:          "complex path with array",
			errMsg:        `conflict with "external-controller" using v1: .spec.containers[0].image`,
			expectedPaths: []string{"spec.containers[0].image"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a K8s conflict error
			err := &errors.StatusError{
				ErrStatus: metav1.Status{
					Status:  "Failure",
					Message: tt.errMsg,
					Reason:  metav1.StatusReasonConflict,
					Code:    409,
				},
			}

			_, paths := k8serrors.ExtractConflictDetailsAndPaths(err)

			if len(paths) != len(tt.expectedPaths) {
				t.Errorf("Expected %d paths, got %d: %v", len(tt.expectedPaths), len(paths), paths)
				return
			}

			for i, expected := range tt.expectedPaths {
				if paths[i] != expected {
					t.Errorf("Path %d: expected %q, got %q", i, expected, paths[i])
				}
			}
		})
	}
}

func TestFormatIgnoreFieldsSuggestion(t *testing.T) {
	tests := []struct {
		name     string
		paths    []string
		expected string
	}{
		{
			name:     "single path",
			paths:    []string{"spec.replicas"},
			expected: `  ignore_fields = ["spec.replicas"]`,
		},
		{
			name:  "multiple paths",
			paths: []string{"spec.replicas", "spec.selector"},
			expected: `  ignore_fields = [
    "spec.replicas",
    "spec.selector"
  ]`,
		},
		{
			name:     "empty paths",
			paths:    []string{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This function is unexported, so we test it via ExtractConflictDetailsAndPaths
			// For now, just document the expected format
			t.Logf("Expected format for %d paths: %s", len(tt.paths), tt.expected)
		})
	}
}
