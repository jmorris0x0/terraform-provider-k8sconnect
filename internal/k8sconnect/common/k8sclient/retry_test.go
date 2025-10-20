package k8sclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestWithRetry_Success(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	config := RetryConfig{
		MaxRetries:   3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		TotalTimeout: 5 * time.Second,
	}

	err := withRetry(ctx, config, func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestWithRetry_SuccessAfterRetries(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	config := RetryConfig{
		MaxRetries:   5,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		TotalTimeout: 5 * time.Second,
	}

	err := withRetry(ctx, config, func() error {
		callCount++
		if callCount < 3 {
			return &url.Error{Op: "Get", URL: "https://api", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}
		}
		return nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestWithRetry_MaxRetriesReached(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	config := RetryConfig{
		MaxRetries:   3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		TotalTimeout: 5 * time.Second,
	}

	retryableErr := &url.Error{Op: "Get", URL: "https://api", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}

	err := withRetry(ctx, config, func() error {
		callCount++
		return retryableErr
	})

	if err == nil {
		t.Error("expected error, got nil")
	}

	if callCount != 4 { // Initial + 3 retries
		t.Errorf("expected 4 calls, got %d", callCount)
	}

	if !strings.Contains(err.Error(), "after 3 retries") {
		t.Errorf("expected retry message in error, got: %v", err)
	}
}

func TestWithRetry_NonRetryableError(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	config := RetryConfig{
		MaxRetries:   5,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		TotalTimeout: 5 * time.Second,
	}

	// 400 Bad Request is not retryable
	nonRetryableErr := apierrors.NewBadRequest("invalid request")

	err := withRetry(ctx, config, func() error {
		callCount++
		return nonRetryableErr
	})

	if err == nil {
		t.Error("expected error, got nil")
	}

	if callCount != 1 {
		t.Errorf("expected 1 call (no retries), got %d", callCount)
	}
}

func TestWithRetry_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	callCount := 0

	config := RetryConfig{
		MaxRetries:   10,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		TotalTimeout: 5 * time.Second,
	}

	retryableErr := &url.Error{Op: "Get", URL: "https://api", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}

	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	err := withRetry(ctx, config, func() error {
		callCount++
		return retryableErr
	})

	if err == nil {
		t.Error("expected context cancellation error, got nil")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// Should have made 1-2 attempts before cancellation
	if callCount < 1 || callCount > 3 {
		t.Errorf("expected 1-3 calls before cancellation, got %d", callCount)
	}
}

func TestWithRetry_TotalTimeout(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	config := RetryConfig{
		MaxRetries:   100, // High number
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		TotalTimeout: 200 * time.Millisecond, // Low timeout
	}

	retryableErr := &url.Error{Op: "Get", URL: "https://api", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}

	err := withRetry(ctx, config, func() error {
		callCount++
		return retryableErr
	})

	if err == nil {
		t.Error("expected timeout error, got nil")
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout message in error, got: %v", err)
	}

	// Should make a few attempts before timeout
	if callCount < 1 {
		t.Errorf("expected at least 1 call, got %d", callCount)
	}
}

func TestCalculateBackoff(t *testing.T) {
	config := RetryConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
	}

	tests := []struct {
		attempt     int
		minExpected time.Duration
		maxExpected time.Duration
	}{
		{0, 90 * time.Millisecond, 110 * time.Millisecond},    // ~100ms ± 10%
		{1, 180 * time.Millisecond, 220 * time.Millisecond},   // ~200ms ± 10%
		{2, 360 * time.Millisecond, 440 * time.Millisecond},   // ~400ms ± 10%
		{3, 720 * time.Millisecond, 880 * time.Millisecond},   // ~800ms ± 10%
		{4, 1440 * time.Millisecond, 1760 * time.Millisecond}, // ~1.6s ± 10%
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			// Test multiple times since jitter is random
			for i := 0; i < 10; i++ {
				delay := calculateBackoff(tt.attempt, config)
				if delay < tt.minExpected || delay > tt.maxExpected {
					t.Errorf("attempt %d: delay %v out of range [%v, %v]",
						tt.attempt, delay, tt.minExpected, tt.maxExpected)
				}
			}
		})
	}
}

func TestCalculateBackoff_MaxDelayCap(t *testing.T) {
	config := RetryConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     2 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
	}

	// High attempt number should be capped at MaxDelay
	delay := calculateBackoff(10, config)

	// Should be around 2s ± 10% jitter
	minExpected := 1800 * time.Millisecond
	maxExpected := 2200 * time.Millisecond

	if delay < minExpected || delay > maxExpected {
		t.Errorf("delay %v should be capped near MaxDelay %v (± jitter)", delay, config.MaxDelay)
	}
}

func TestIsRetryableError_NetworkErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "connection refused",
			err:       &url.Error{Op: "Get", URL: "https://api", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}},
			retryable: true,
		},
		{
			name:      "connection reset",
			err:       &url.Error{Op: "Get", URL: "https://api", Err: &net.OpError{Op: "read", Err: syscall.ECONNRESET}},
			retryable: true,
		},
		{
			name:      "connection timeout",
			err:       &url.Error{Op: "Get", URL: "https://api", Err: &net.OpError{Op: "dial", Err: syscall.ETIMEDOUT}},
			retryable: true,
		},
		{
			name:      "network unreachable",
			err:       &url.Error{Op: "Get", URL: "https://api", Err: &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}},
			retryable: true,
		},
		{
			name:      "DNS error",
			err:       &net.DNSError{Err: "no such host", Name: "invalid.example.com", IsNotFound: true},
			retryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			if result != tt.retryable {
				t.Errorf("expected retryable=%v, got %v for error: %v", tt.retryable, result, tt.err)
			}
		})
	}
}

func TestIsRetryableError_K8sAPIErrors(t *testing.T) {
	// Create a GroupResource for testing
	podsGR := schema.GroupResource{Group: "", Resource: "pods"}
	testGK := schema.GroupKind{Group: "", Kind: "Test"}

	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "500 internal server error",
			err:       apierrors.NewInternalError(errors.New("internal error")),
			retryable: true,
		},
		{
			name:      "503 service unavailable",
			err:       apierrors.NewServiceUnavailable("service unavailable"),
			retryable: true,
		},
		{
			name:      "504 gateway timeout",
			err:       apierrors.NewTimeoutError("gateway timeout", 1),
			retryable: true,
		},
		{
			name: "429 rate limit",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Status: metav1.StatusFailure,
					Code:   429,
					Reason: metav1.StatusReasonTooManyRequests,
				},
			},
			retryable: true,
		},
		{
			name:      "400 bad request",
			err:       apierrors.NewBadRequest("bad request"),
			retryable: false,
		},
		{
			name:      "401 unauthorized",
			err:       apierrors.NewUnauthorized("unauthorized"),
			retryable: false,
		},
		{
			name:      "403 forbidden",
			err:       apierrors.NewForbidden(podsGR, "test", errors.New("forbidden")),
			retryable: false,
		},
		{
			name:      "404 not found",
			err:       apierrors.NewNotFound(podsGR, "test"),
			retryable: false,
		},
		{
			name:      "409 conflict",
			err:       apierrors.NewConflict(podsGR, "test", errors.New("conflict")),
			retryable: false,
		},
		{
			name:      "422 invalid",
			err:       apierrors.NewInvalid(testGK, "test", nil),
			retryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			if result != tt.retryable {
				t.Errorf("expected retryable=%v, got %v for error: %v", tt.retryable, result, tt.err)
			}
		})
	}
}

func TestIsRetryableError_MessagePatterns(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "etcd leader changed",
			err:       errors.New("etcdserver: leader changed"),
			retryable: true,
		},
		{
			name:      "connection refused message",
			err:       errors.New("dial tcp: connection refused"),
			retryable: true,
		},
		{
			name:      "connection reset message",
			err:       errors.New("read tcp: connection reset by peer"),
			retryable: true,
		},
		{
			name:      "broken pipe",
			err:       errors.New("write tcp: broken pipe"),
			retryable: true,
		},
		{
			name:      "tls handshake timeout",
			err:       errors.New("net/http: TLS handshake timeout"),
			retryable: true,
		},
		{
			name:      "no such host",
			err:       errors.New("dial tcp: lookup api.example.com: no such host"),
			retryable: true,
		},
		{
			name:      "temporary failure",
			err:       errors.New("temporary failure in name resolution"),
			retryable: true,
		},
		{
			name:      "generic error",
			err:       errors.New("something went wrong"),
			retryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			if result != tt.retryable {
				t.Errorf("expected retryable=%v, got %v for error: %v", tt.retryable, result, tt.err)
			}
		})
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "none",
		},
		{
			name:     "network error",
			err:      &url.Error{Op: "Get", URL: "https://api", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}},
			expected: "network",
		},
		{
			name:     "server error",
			err:      apierrors.NewInternalError(errors.New("internal")),
			expected: "server_error",
		},
		{
			name: "rate limit",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{Code: 429},
			},
			expected: "rate_limit",
		},
		{
			name:     "client error",
			err:      apierrors.NewBadRequest("bad"),
			expected: "client_error",
		},
		{
			name:     "etcd error",
			err:      errors.New("etcdserver: leader changed"),
			expected: "etcd",
		},
		{
			name:     "tls error",
			err:      errors.New("tls handshake failed"),
			expected: "tls",
		},
		{
			name:     "timeout error message",
			err:      errors.New("operation timeout"),
			expected: "timeout",
		},
		{
			name:     "unknown error",
			err:      errors.New("something unexpected"),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyError(tt.err)
			if result != tt.expected {
				t.Errorf("expected classification %q, got %q for error: %v", tt.expected, result, tt.err)
			}
		})
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	// Verify default config has sensible values
	if DefaultRetryConfig.MaxRetries != 5 {
		t.Errorf("expected MaxRetries=5, got %d", DefaultRetryConfig.MaxRetries)
	}

	if DefaultRetryConfig.InitialDelay != 100*time.Millisecond {
		t.Errorf("expected InitialDelay=100ms, got %v", DefaultRetryConfig.InitialDelay)
	}

	if DefaultRetryConfig.MaxDelay != 30*time.Second {
		t.Errorf("expected MaxDelay=30s, got %v", DefaultRetryConfig.MaxDelay)
	}

	if DefaultRetryConfig.Multiplier != 2.0 {
		t.Errorf("expected Multiplier=2.0, got %v", DefaultRetryConfig.Multiplier)
	}

	if DefaultRetryConfig.Jitter != 0.1 {
		t.Errorf("expected Jitter=0.1, got %v", DefaultRetryConfig.Jitter)
	}

	if DefaultRetryConfig.TotalTimeout != 2*time.Minute {
		t.Errorf("expected TotalTimeout=2m, got %v", DefaultRetryConfig.TotalTimeout)
	}
}
