package object

import (
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
)

// ADR-023: Read Fails When Cluster Auth Token Expires Between Runs
//
// These tests assert the DESIRED behavior after implementing Option E
// (resilient Read). They test classifyReadGetError which should degrade
// auth errors to warnings during Read, so terraform plan doesn't fail
// when stored tokens have expired.
//
// THESE TESTS SHOULD FAIL UNTIL OPTION E IS IMPLEMENTED.
// That's intentional — they define the target behavior.

// TestReadAuthFailure_Scenario1_ExpiredTokenOnPlan tests that an expired
// token during Read (triggered by terraform plan) should produce a WARNING,
// not a hard error. This is the primary bug reported in issue #131.
//
// Scenario: User runs terraform apply (succeeds), waits, runs terraform plan.
// The token stored in state has expired. Read calls Client.Get which returns 401.
// DESIRED: Read returns prior state with a warning.
func TestReadAuthFailure_Scenario1_ExpiredTokenOnPlan(t *testing.T) {
	r := &objectResource{}
	err := errors.NewUnauthorized("token expired")

	severity, title, _ := r.classifyReadGetError(err, "Deployment my-app", "apps/v1")

	// DESIRED behavior: warning, not error
	if severity != "warning" {
		t.Errorf("expected severity 'warning' for expired token during Read, got %q (ADR-023: Read should degrade gracefully on auth failure)", severity)
	}
	if !strings.Contains(title, "Prior State") {
		t.Errorf("expected title to mention 'Prior State', got %q (ADR-023: should indicate using cached state)", title)
	}

	// Verify IsAuthError recognizes this error (building block for the fix)
	if !k8serrors.IsAuthError(err) {
		t.Error("IsAuthError should return true for 401 Unauthorized")
	}
}

// TestReadAuthFailure_Scenario2_ExpiredTokenOnApply tests that an expired
// token during terraform apply (no saved plan) should warn during Read,
// allowing the apply to proceed with fresh config credentials.
func TestReadAuthFailure_Scenario2_ExpiredTokenOnApply(t *testing.T) {
	r := &objectResource{}
	err := errors.NewUnauthorized("token expired")

	// In terraform apply, Read runs first to refresh state.
	// If Read degrades gracefully, Apply can proceed with fresh token from config.
	severity, _, _ := r.classifyReadGetError(err, "Deployment my-app", "apps/v1")

	// DESIRED: warning so apply can continue
	if severity != "warning" {
		t.Errorf("expected Read to produce warning for expired token, got %q (ADR-023: allow apply to proceed)", severity)
	}
}

// TestReadAuthFailure_Scenario3_StalePlanToken tests that expired tokens
// during Create/Update (from a saved plan) should STILL produce hard errors.
// Option E only fixes Read — Create/Update need valid credentials.
func TestReadAuthFailure_Scenario3_StalePlanToken(t *testing.T) {
	r := &objectResource{}
	err := errors.NewUnauthorized("token expired")

	// Create and Update should still hard-fail on auth errors
	// (classifyReadGetError is only for Read, not Create/Update)
	for _, op := range []string{"Create", "Update"} {
		severity, title, _ := r.classifyK8sError(err, op, "Deployment my-app", "apps/v1")

		if severity != "error" {
			t.Errorf("%s: expected severity 'error' (auth errors during mutating ops should fail), got %q", op, severity)
		}
		if !strings.Contains(title, "Authentication Failed") {
			t.Errorf("%s: expected title to contain 'Authentication Failed', got %q", op, title)
		}
	}
}

// TestReadAuthFailure_Scenario4_MidRunTokenExpiry tests classification when
// a token expires mid-run. From Read's perspective, this looks identical to
// a cross-run expiry — Client.Get returns 401.
func TestReadAuthFailure_Scenario4_MidRunTokenExpiry(t *testing.T) {
	r := &objectResource{}
	err := errors.NewUnauthorized("token expired")

	severity, _, _ := r.classifyReadGetError(err, "ConfigMap resource-50-of-100", "v1")

	// DESIRED: warning — mid-run expiry during Read should degrade gracefully
	if severity != "warning" {
		t.Errorf("expected severity 'warning' for mid-run token expiry during Read, got %q", severity)
	}
}

// TestReadAuthFailure_Scenario5_WrongToken tests that a genuinely wrong token
// during Read also degrades to a warning. We CANNOT distinguish expired from
// wrong via the 401 status code alone. The warning message tells users to
// check their config if the issue persists.
func TestReadAuthFailure_Scenario5_WrongToken(t *testing.T) {
	r := &objectResource{}
	err := errors.NewUnauthorized("invalid bearer token")

	severity, _, detail := r.classifyReadGetError(err, "Deployment my-app", "apps/v1")

	// DESIRED: warning (can't distinguish expired from wrong, warning is safer)
	if severity != "warning" {
		t.Errorf("expected severity 'warning' for wrong token during Read, got %q (ADR-023: degrade all 401s in Read)", severity)
	}

	// The warning message should tell users this might be a config issue
	if !strings.Contains(strings.ToLower(detail), "expired") && !strings.Contains(strings.ToLower(detail), "authentication") {
		t.Errorf("expected warning detail to mention authentication/expiry, got %q", detail)
	}
}

// TestReadAuthFailure_Scenario6_ForbiddenRBAC tests that a 403 Forbidden during
// Read also degrades to a warning. A 403 could mean the token was valid but the
// associated role was removed, which is a similar "stale credentials" problem.
func TestReadAuthFailure_Scenario6_ForbiddenRBAC(t *testing.T) {
	r := &objectResource{}
	err := errors.NewForbidden(
		schema.GroupResource{Group: "apps", Resource: "deployments"},
		"my-app",
		fmt.Errorf("user lacks get permission"),
	)

	severity, _, _ := r.classifyReadGetError(err, "Deployment my-app", "apps/v1")

	// DESIRED: warning — 403 during Read should also degrade gracefully
	if severity != "warning" {
		t.Errorf("expected severity 'warning' for 403 during Read, got %q (ADR-023: degrade auth errors in Read)", severity)
	}

	// But 403 during Create/Update/Delete should still be a hard error
	for _, op := range []string{"Create", "Update", "Delete"} {
		severity, _, _ := r.classifyK8sError(err, op, "Deployment my-app", "apps/v1")
		if severity != "error" {
			t.Errorf("%s: expected 403 to remain hard error for mutating ops, got %q", op, severity)
		}
	}
}

// TestReadNonAuthErrors_ShouldNotChange verifies that non-auth errors during
// Read are completely unaffected by the resilient Read change. Connection errors,
// server errors, etc. should still produce hard errors.
func TestReadNonAuthErrors_ShouldNotChange(t *testing.T) {
	tests := []struct {
		name             string
		err              error
		expectedSeverity string
		expectedInTitle  string
		isAuthError      bool
	}{
		{
			name:             "404 Not Found — should remain warning",
			err:              errors.NewNotFound(schema.GroupResource{Resource: "deployments"}, "my-app"),
			expectedSeverity: "warning",
			expectedInTitle:  "Resource Not Found",
			isAuthError:      false,
		},
		{
			name: "500 Internal Server Error — should remain error",
			err: &errors.StatusError{
				ErrStatus: errors.NewInternalError(fmt.Errorf("database crashed")).ErrStatus,
			},
			expectedSeverity: "error",
			expectedInTitle:  "Kubernetes API Error",
			isAuthError:      false,
		},
		{
			name:             "connection refused — should remain error",
			err:              fmt.Errorf("dial tcp 10.0.0.1:6443: connection refused"),
			expectedSeverity: "error",
			expectedInTitle:  "Cluster Connection Failed",
			isAuthError:      false,
		},
		{
			name:             "DNS lookup failure — should remain error",
			err:              fmt.Errorf("dial tcp: lookup cluster.example.com: no such host"),
			expectedSeverity: "error",
			expectedInTitle:  "Cluster Connection Failed",
			isAuthError:      false,
		},
		{
			name:             "timeout — should remain error",
			err:              fmt.Errorf("dial tcp 10.0.0.1:6443: i/o timeout"),
			expectedSeverity: "error",
			expectedInTitle:  "Cluster Connection Failed",
			isAuthError:      false,
		},
	}

	r := &objectResource{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, title, _ := r.classifyReadGetError(tt.err, "Deployment my-app", "apps/v1")

			if severity != tt.expectedSeverity {
				t.Errorf("severity = %q, want %q", severity, tt.expectedSeverity)
			}

			if !strings.Contains(title, tt.expectedInTitle) {
				t.Errorf("title %q does not contain %q", title, tt.expectedInTitle)
			}

			if k8serrors.IsAuthError(tt.err) != tt.isAuthError {
				t.Errorf("IsAuthError() = %v, want %v", k8serrors.IsAuthError(tt.err), tt.isAuthError)
			}
		})
	}
}
