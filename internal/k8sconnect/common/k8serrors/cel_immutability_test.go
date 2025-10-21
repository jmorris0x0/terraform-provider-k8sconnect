package k8serrors

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestIsImmutableFieldError_CEL tests detection of CEL-based immutability errors
// These tests currently FAIL because IsImmutableFieldError doesn't check for "may not change"
//
// Bug: Kubernetes 1.25+ uses CEL for immutability with message "may not change once set"
// But IsImmutableFieldError only checks: "immutable", "forbidden", "cannot be changed", "may not be modified"
// Missing: "may not change"
//
// This causes CEL immutability errors to get weak generic guidance instead of actionable fix steps
func TestIsImmutableFieldError_CEL(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "CEL immutability: may not change once set (Service clusterIP)",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `Service "test-service" is invalid: spec.clusterIPs[0]: Invalid value: []string{"10.96.100.200"}: may not change once set`,
				},
			},
			expected: true, // BUG: Currently returns false!
		},
		{
			name: "CEL immutability: may not change once set (PVC storageClassName)",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `PersistentVolumeClaim "test-pvc" is invalid: spec.storageClassName: Invalid value: "fast": may not change once set`,
				},
			},
			expected: true, // BUG: Currently returns false!
		},
		{
			name: "CEL immutability: may not change (generic)",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `spec.selector: Invalid value: map[string]string{"app":"v2"}: may not change`,
				},
			},
			expected: true, // BUG: Currently returns false!
		},
		{
			name: "Existing detection: immutable keyword (should still work)",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `spec.storageClassName: Forbidden: field is immutable`,
				},
			},
			expected: true,
		},
		{
			name: "Existing detection: cannot be changed (should still work)",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `spec.clusterIP: Forbidden: cannot be changed`,
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsImmutableFieldError(tt.err)
			if result != tt.expected {
				t.Errorf("IsImmutableFieldError() = %v, want %v", result, tt.expected)
				if statusErr, ok := tt.err.(*errors.StatusError); ok {
					t.Logf("Error message: %s", statusErr.ErrStatus.Message)
				}
			}
		})
	}
}

// TestClassifyError_CELImmutability tests that CEL immutability errors get proper guidance
// This test currently FAILS because "may not change" errors are classified as generic CEL errors
func TestClassifyError_CELImmutability(t *testing.T) {
	tests := []struct {
		name             string
		err              error
		operation        string
		resourceDesc     string
		expectedSeverity string
		expectedInTitle  string
		expectedInDetail string
		notInDetail      string // Should NOT appear in detail
	}{
		{
			name: "CEL immutability should get actionable guidance",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `Service "test-service" is invalid: spec.clusterIPs[0]: Invalid value: []string{"10.96.100.200"}: may not change once set`,
				},
			},
			operation:        "Update",
			resourceDesc:     "Service test-service",
			expectedSeverity: "error",
			expectedInTitle:  "Immutable Field Changed",        // BUG: Currently says "CEL Validation Failed"
			expectedInDetail: "terraform apply -replace",       // BUG: Currently not present
			notInDetail:      "Fix the field value to satisfy", // Weak guidance we DON'T want
		},
		{
			name: "Traditional immutability should still work",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `spec.storageClassName: Forbidden: field is immutable`,
				},
			},
			operation:        "Update",
			resourceDesc:     "PVC test-pvc",
			expectedSeverity: "error",
			expectedInTitle:  "Immutable Field Changed",
			expectedInDetail: "terraform apply -replace",
			notInDetail:      "Fix the field value to satisfy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, title, detail := ClassifyError(tt.err, tt.operation, tt.resourceDesc)

			if severity != tt.expectedSeverity {
				t.Errorf("severity = %v, want %v", severity, tt.expectedSeverity)
			}

			if !strings.Contains(title, tt.expectedInTitle) {
				t.Errorf("title should contain %q, got: %s", tt.expectedInTitle, title)
			}

			if !strings.Contains(detail, tt.expectedInDetail) {
				t.Errorf("detail should contain %q, got: %s", tt.expectedInDetail, detail)
			}

			if tt.notInDetail != "" && strings.Contains(detail, tt.notInDetail) {
				t.Errorf("detail should NOT contain %q, got: %s", tt.notInDetail, detail)
			}
		})
	}
}

// TestPermissionErrorsNotMisclassified verifies that RBAC permission errors
// are NOT misclassified as immutable field errors
func TestPermissionErrorsNotMisclassified(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "403 Forbidden (RBAC permission error) - should NOT be immutable",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    403, // HTTP Forbidden - permission error
					Message: `pods is forbidden: User "system:anonymous" cannot create resource "pods" in API group ""`,
				},
			},
			expected: false, // NOT an immutable field error
		},
		{
			name: "422 with 'Forbidden' in message (immutable field) - should BE immutable",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422, // Unprocessable Entity - validation error
					Message: `spec.clusterIP: Forbidden: cannot be changed`,
				},
			},
			expected: true, // IS an immutable field error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsImmutableFieldError(tt.err)
			if result != tt.expected {
				t.Errorf("IsImmutableFieldError() = %v, want %v", result, tt.expected)
				if statusErr, ok := tt.err.(*errors.StatusError); ok {
					t.Logf("Status Code: %d", statusErr.ErrStatus.Code)
					t.Logf("Message: %s", statusErr.ErrStatus.Message)
				}
			}
		})
	}
}
