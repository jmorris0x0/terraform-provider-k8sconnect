package k8serrors

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestIsFieldValidationError tests detection of field validation errors
func TestIsFieldValidationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "unknown field error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    400,
					Message: `unknown field "spec.replica"`,
				},
			},
			expected: true,
		},
		{
			name: "duplicate field error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    400,
					Message: `duplicate field "spec.replicas"`,
				},
			},
			expected: true,
		},
		{
			name: "strict decoding error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    400,
					Message: `strict decoding error: unknown field "spec.replica"`,
				},
			},
			expected: true,
		},
		{
			name: "field not declared in schema",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    400,
					Message: `.spec.replica: field not declared in schema`,
				},
			},
			expected: true,
		},
		{
			name: "field not declared in schema - wrapped",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    400,
					Message: `failed to create typed patch object (ns/name; apps/v1, Kind=Deployment): .spec.replica: field not declared in schema`,
				},
			},
			expected: true,
		},
		{
			name: "multiple unknown fields",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    400,
					Message: `strict decoding error: unknown field "spec.replica", unknown field "spec.container"`,
				},
			},
			expected: true,
		},
		{
			name: "wrong status code - 422 (CEL/immutable)",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `unknown field "spec.replica"`,
				},
			},
			expected: false,
		},
		{
			name: "wrong status code - 404",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    404,
					Message: `unknown field "spec.replica"`,
				},
			},
			expected: false,
		},
		{
			name: "status 400 but different error message",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    400,
					Message: `Bad Request`,
				},
			},
			expected: false,
		},
		{
			name:     "non-StatusError",
			err:      errors.NewBadRequest("some error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsFieldValidationError(tt.err)
			if result != tt.expected {
				t.Errorf("IsFieldValidationError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestExtractFieldValidationDetails tests parsing of field validation errors
func TestExtractFieldValidationDetails(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedFields []string // Field paths we expect to find in the output
		expectCount    bool     // Should output contain error count?
	}{
		{
			name: "single unknown field",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `unknown field "spec.replica"`,
				},
			},
			expectedFields: []string{"spec.replica", "unknown field"},
			expectCount:    false,
		},
		{
			name: "single duplicate field",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `duplicate field "spec.replicas"`,
				},
			},
			expectedFields: []string{"spec.replicas", "duplicate field"},
			expectCount:    false,
		},
		{
			name: "multiple fields - comma separated",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `strict decoding error: unknown field "spec.replica", unknown field "spec.container"`,
				},
			},
			expectedFields: []string{"spec.replica", "spec.container", "Found 2 field validation errors"},
			expectCount:    true,
		},
		{
			name: "multiple fields - three errors",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `strict decoding error: unknown field "spec.replica", unknown field "spec.container", duplicate field "metadata.name"`,
				},
			},
			expectedFields: []string{"spec.replica", "spec.container", "metadata.name", "Found 3 field validation errors"},
			expectCount:    true,
		},
		{
			name: "nested field path",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `unknown field "spec.template.spec.containers[0].imagePullPolice"`,
				},
			},
			expectedFields: []string{"spec.template.spec.containers[0].imagePullPolice", "unknown field"},
			expectCount:    false,
		},
		{
			name: "field not declared in schema format",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `.spec.replica: field not declared in schema`,
				},
			},
			expectedFields: []string{".spec.replica", "field not declared in schema"},
			expectCount:    false,
		},
		{
			name: "field not declared - wrapped message",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `failed to create typed patch object (ns/name; apps/v1, Kind=Deployment): .spec.replica: field not declared in schema`,
				},
			},
			expectedFields: []string{".spec.replica", "field not declared in schema"},
			expectCount:    false,
		},
		{
			name: "bracketed list format",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `[unknown field "spec.replica", unknown field "spec.container"]`,
				},
			},
			expectedFields: []string{"spec.replica", "spec.container", "Found 2 field validation errors"},
			expectCount:    true,
		},
		{
			name:           "non-StatusError",
			err:            errors.NewBadRequest("some error"),
			expectedFields: []string{"Field validation failed"},
			expectCount:    false,
		},
		{
			name: "error with no parseable fields",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `validation error occurred`,
				},
			},
			expectedFields: []string{"Field validation failed", "validation error occurred"},
			expectCount:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractFieldValidationDetails(tt.err)

			// Check that all expected fields are present
			for _, expected := range tt.expectedFields {
				if !strings.Contains(result, expected) {
					t.Errorf("ExtractFieldValidationDetails() output missing expected string %q.\nGot: %s", expected, result)
				}
			}

			// Verify count appears only when expected
			if tt.expectCount {
				if !strings.Contains(result, "Found") {
					t.Errorf("ExtractFieldValidationDetails() should include error count but doesn't.\nGot: %s", result)
				}
			}
		})
	}
}

// TestIsCELValidationError tests detection of CEL validation errors
func TestIsCELValidationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "explicit failed rule",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `field.path: failed rule: self.replicas <= 10: replicas cannot exceed 10`,
				},
			},
			expected: true,
		},
		{
			name: "x-kubernetes-validations reference",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `x-kubernetes-validations rule failed`,
				},
			},
			expected: true,
		},
		{
			name: "CRD with invalid value pattern",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `Widget.example.com "test" is invalid: spec.replicas: Invalid value: 15: replicas cannot exceed 10`,
				},
			},
			expected: true,
		},
		{
			name: "multiple CEL errors in bracketed list",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `[spec.replicas: Invalid value: 15: replicas cannot exceed 10, spec.replicas: Invalid value: 15: replicas must be <= maxReplicas]`,
				},
			},
			expected: true,
		},
		{
			name: "built-in required value error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `spec.replicas: Required value`,
				},
			},
			expected: false,
		},
		{
			name: "built-in must be error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `spec.replicas: Invalid value: must be greater than 0`,
				},
			},
			expected: false,
		},
		{
			name: "non-CRD error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `Deployment is invalid: spec.replicas: Required value`,
				},
			},
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCELValidationError(tt.err)
			if result != tt.expected {
				t.Errorf("IsCELValidationError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestExtractCELValidationDetails tests parsing of CEL validation errors
func TestExtractCELValidationDetails(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedFields []string // Strings we expect to find in the output
	}{
		{
			name: "single CEL error with failed rule",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `spec.replicas: failed rule: self.replicas <= 10: replicas cannot exceed 10`,
				},
			},
			expectedFields: []string{"spec.replicas", "self.replicas <= 10", "replicas cannot exceed 10"},
		},
		{
			name: "single CEL error with invalid value",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `spec.replicas: Invalid value: "15": replicas cannot exceed 10`,
				},
			},
			expectedFields: []string{"spec.replicas", "replicas cannot exceed 10"},
		},
		{
			name: "multiple CEL errors in bracketed list",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `[spec.replicas: Invalid value: "15": replicas cannot exceed 10, spec.replicas: Invalid value: "15": replicas must be <= maxReplicas]`,
				},
			},
			expectedFields: []string{"Found 2 CEL validation errors", "spec.replicas", "replicas cannot exceed 10", "replicas must be <= maxReplicas"},
		},
		{
			name:           "non-StatusError",
			err:            errors.NewBadRequest("some error"),
			expectedFields: []string{"CEL validation rule failed"},
		},
		{
			name: "error with no parseable CEL details",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `some validation error`,
				},
			},
			expectedFields: []string{"CEL validation rule failed", "Full error: some validation error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractCELValidationDetails(tt.err)

			// Check that all expected fields are present
			for _, expected := range tt.expectedFields {
				if !strings.Contains(result, expected) {
					t.Errorf("ExtractCELValidationDetails() output missing expected string %q.\nGot: %s", expected, result)
				}
			}
		})
	}
}

// TestIsImmutableFieldError tests detection of immutable field errors
func TestIsImmutableFieldError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "immutable keyword",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `spec.storageClassName: Forbidden: field is immutable`,
				},
			},
			expected: true,
		},
		{
			name: "forbidden keyword",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `spec.clusterIP: Forbidden: cannot be changed`,
				},
			},
			expected: true,
		},
		{
			name: "cannot be changed",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `spec.type: cannot be changed`,
				},
			},
			expected: true,
		},
		{
			name: "may not be modified",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `spec.selector: may not be modified`,
				},
			},
			expected: true,
		},
		{
			name: "wrong status code - 400",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    400,
					Message: `field is immutable`,
				},
			},
			expected: false,
		},
		{
			name: "status 422 but different error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422,
					Message: `validation failed`,
				},
			},
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsImmutableFieldError(tt.err)
			if result != tt.expected {
				t.Errorf("IsImmutableFieldError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestClassifyError tests the main error classification function
func TestClassifyError(t *testing.T) {
	tests := []struct {
		name             string
		err              error
		operation        string
		resourceDesc     string
		expectedSeverity string
		expectedInTitle  string
		expectedInDetail string
	}{
		{
			name: "field validation error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    400,
					Message: `unknown field "spec.replica"`,
				},
			},
			operation:        "Plan",
			resourceDesc:     "Deployment test-deployment",
			expectedSeverity: "error",
			expectedInTitle:  "Field Validation Failed",
			expectedInDetail: "spec.replica",
		},
		{
			name: "CEL validation error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    422, // CEL errors are status 422 (Unprocessable Entity)
					Message: `spec.replicas: Invalid value: "15": replicas cannot exceed 10`,
				},
			},
			operation:        "Create",
			resourceDesc:     "Widget test-widget",
			expectedSeverity: "error",
			expectedInTitle:  "CEL Validation Failed",
			expectedInDetail: "replicas cannot exceed 10",
		},
		{
			name: "immutable field error",
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
		},
		{
			name:             "not found error",
			err:              errors.NewNotFound(schema.GroupResource{}, "test"),
			operation:        "Read",
			resourceDesc:     "ConfigMap test-cm",
			expectedSeverity: "warning",
			expectedInTitle:  "Resource Not Found",
			expectedInDetail: "deleted outside of Terraform",
		},
		{
			name:             "forbidden error",
			err:              errors.NewForbidden(schema.GroupResource{}, "test", nil),
			operation:        "Create",
			resourceDesc:     "Secret test-secret",
			expectedSeverity: "error",
			expectedInTitle:  "Insufficient Permissions",
			expectedInDetail: "RBAC permissions insufficient",
		},
		{
			name: "CRD not found error",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Message: `no matches for kind "Widget" in version "example.com/v1"`,
				},
			},
			operation:        "Create",
			resourceDesc:     "Widget test-widget",
			expectedSeverity: "error",
			expectedInTitle:  "Custom Resource Definition Not Found",
			expectedInDetail: "Custom Resource Definition (CRD) for Widget test-widget does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, title, detail := ClassifyError(tt.err, tt.operation, tt.resourceDesc)

			if severity != tt.expectedSeverity {
				t.Errorf("ClassifyError() severity = %v, want %v", severity, tt.expectedSeverity)
			}

			if !strings.Contains(title, tt.expectedInTitle) {
				t.Errorf("ClassifyError() title %q does not contain %q", title, tt.expectedInTitle)
			}

			if !strings.Contains(detail, tt.expectedInDetail) {
				t.Errorf("ClassifyError() detail does not contain %q.\nGot: %s", tt.expectedInDetail, detail)
			}
		})
	}
}

// TestFieldValidationErrorPriority tests that field validation errors are checked before CEL
func TestFieldValidationErrorPriority(t *testing.T) {
	// This error could match both field validation (status 400) and has "Invalid value" (CEL pattern)
	// Field validation should take precedence
	err := &errors.StatusError{
		ErrStatus: metav1.Status{
			Code:    400,
			Message: `unknown field "spec.replica"`,
		},
	}

	severity, title, detail := ClassifyError(err, "Plan", "Deployment test")

	// Should be classified as field validation, not CEL
	if !strings.Contains(title, "Field Validation Failed") {
		t.Errorf("Expected field validation error, got title: %s", title)
	}

	if strings.Contains(title, "CEL") {
		t.Errorf("Should not be classified as CEL error, got title: %s", title)
	}

	if !strings.Contains(detail, "spec.replica") {
		t.Errorf("Expected field path in detail, got: %s", detail)
	}

	if severity != "error" {
		t.Errorf("Expected error severity, got: %s", severity)
	}
}

// TestExtractConflictDetails tests parsing of SSA conflict error messages
// Note: SSA conflicts are intentionally prevented by using Force=true (ADR-005)
// This test exists for defensive programming in case Force is ever disabled
func TestExtractConflictDetails(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedSubstr string
	}{
		{
			name: "conflict with kubectl",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    409,
					Message: `Apply failed with 1 conflict: conflict with "kubectl" using v1: .spec.replicas`,
				},
			},
			expectedSubstr: "kubectl",
		},
		{
			name: "conflict with hpa-controller",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    409,
					Message: `conflict with "hpa-controller" with subresource "scale" using autoscaling/v1: .spec.replicas`,
				},
			},
			expectedSubstr: "hpa-controller",
		},
		{
			name: "multiple conflicts",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    409,
					Message: `Apply failed with 2 conflicts: conflict with "kubectl" using v1: .metadata.annotations conflict with "helm" using v1: .metadata.labels`,
				},
			},
			expectedSubstr: "kubectl",
		},
		{
			name: "unparseable conflict message",
			err: &errors.StatusError{
				ErrStatus: metav1.Status{
					Code:    409,
					Message: `some generic conflict error`,
				},
			},
			expectedSubstr: "field ownership conflicts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := ExtractConflictDetails(tt.err)
			if !strings.Contains(details, tt.expectedSubstr) {
				t.Errorf("ExtractConflictDetails() = %q, expected to contain %q", details, tt.expectedSubstr)
			}
		})
	}
}
