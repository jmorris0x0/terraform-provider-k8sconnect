// internal/k8sconnect/common/k8sclient/retry.go
package k8sclient

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// RetryConfig defines the retry behavior for Kubernetes API operations
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts
	MaxRetries int

	// InitialDelay is the initial backoff delay
	InitialDelay time.Duration

	// MaxDelay is the maximum backoff delay (cap)
	MaxDelay time.Duration

	// Multiplier is the backoff multiplier (exponential)
	Multiplier float64

	// Jitter is the jitter factor (0.0 to 1.0) to add randomness
	Jitter float64

	// TotalTimeout is the maximum time to spend on all retries
	TotalTimeout time.Duration
}

// DefaultRetryConfig returns the default retry configuration based on
// Kubernetes and Terraform ecosystem best practices
var DefaultRetryConfig = RetryConfig{
	MaxRetries:   5,
	InitialDelay: 100 * time.Millisecond,
	MaxDelay:     30 * time.Second,
	Multiplier:   2.0,
	Jitter:       0.1, // 10% jitter
	TotalTimeout: 2 * time.Minute,
}

// withRetry executes the given operation with exponential backoff retry logic
func withRetry(ctx context.Context, config RetryConfig, operation func() error) error {
	startTime := time.Now()
	var lastErr error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		// Check if total timeout exceeded
		if time.Since(startTime) > config.TotalTimeout {
			tflog.Warn(ctx, "Retry total timeout exceeded", map[string]interface{}{
				"timeout":  config.TotalTimeout,
				"elapsed":  time.Since(startTime),
				"attempts": attempt,
			})
			if lastErr != nil {
				return fmt.Errorf("operation failed after timeout (%v): %w", config.TotalTimeout, lastErr)
			}
			return fmt.Errorf("operation timed out after %v", config.TotalTimeout)
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Execute the operation
		err := operation()
		if err == nil {
			// Success!
			if attempt > 0 {
				tflog.Info(ctx, "Operation succeeded after retry", map[string]interface{}{
					"attempts": attempt + 1,
					"elapsed":  time.Since(startTime),
				})
			}
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			tflog.Debug(ctx, "Error is not retryable", map[string]interface{}{
				"error": err.Error(),
			})
			return err
		}

		// Don't sleep after the last attempt
		if attempt == config.MaxRetries {
			tflog.Warn(ctx, "Max retries reached", map[string]interface{}{
				"max_retries": config.MaxRetries,
				"last_error":  err.Error(),
			})
			return fmt.Errorf("operation failed after %d retries: %w", config.MaxRetries, err)
		}

		// Calculate backoff delay with jitter
		delay := calculateBackoff(attempt, config)

		tflog.Debug(ctx, "Retrying operation after backoff", map[string]interface{}{
			"attempt":    attempt + 1,
			"delay":      delay,
			"error":      err.Error(),
			"error_type": classifyError(err),
		})

		// Sleep with context awareness
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return lastErr
}

// calculateBackoff calculates the backoff delay with exponential backoff and jitter
func calculateBackoff(attempt int, config RetryConfig) time.Duration {
	// Exponential backoff: initialDelay * (multiplier ^ attempt)
	exponential := float64(config.InitialDelay) * math.Pow(config.Multiplier, float64(attempt))

	// Cap at max delay
	if exponential > float64(config.MaxDelay) {
		exponential = float64(config.MaxDelay)
	}

	// Add jitter: random value between (1-jitter) and (1+jitter)
	jitterRange := exponential * config.Jitter
	jitterValue := exponential - jitterRange + (rand.Float64() * 2 * jitterRange)

	delay := time.Duration(jitterValue)

	// Ensure delay is not negative or zero
	if delay <= 0 {
		delay = config.InitialDelay
	}

	return delay
}

// isRetryableError determines if an error should trigger a retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Network errors - always retry
	if isNetworkError(err) {
		return true
	}

	// Check for Kubernetes API errors
	if statusErr, ok := err.(*apierrors.StatusError); ok {
		code := statusErr.ErrStatus.Code

		// Retry on server errors
		if code >= 500 && code < 600 {
			return true
		}

		// Retry on rate limiting
		if code == 429 {
			return true
		}

		// Don't retry client errors
		if code >= 400 && code < 500 {
			// Special case: 408 Request Timeout is retryable
			if code == 408 {
				return true
			}
			return false
		}
	}

	// Check error message for common retryable patterns
	errMsg := strings.ToLower(err.Error())

	// etcd leader changes are transient
	if strings.Contains(errMsg, "etcdserver: leader changed") {
		return true
	}

	// Connection errors
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "connection timed out") {
		return true
	}

	// Temporary failures
	if strings.Contains(errMsg, "temporary failure") ||
		strings.Contains(errMsg, "try again") {
		return true
	}

	// DNS resolution failures
	if strings.Contains(errMsg, "no such host") ||
		strings.Contains(errMsg, "name resolution") {
		return true
	}

	// TLS errors (may be transient during cert rotation)
	if strings.Contains(errMsg, "tls handshake timeout") ||
		strings.Contains(errMsg, "tls: bad certificate") {
		return true
	}

	// Discovery errors - CRD/CR timing issues (API server discovery cache not updated yet)
	if strings.Contains(errMsg, "could not find the requested resource") ||
		strings.Contains(errMsg, "no matches for kind") {
		return true
	}

	// Default: don't retry unknown errors
	return false
}

// isNetworkError checks if an error is a network-related error
func isNetworkError(err error) bool {
	// Check for standard network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// Check for DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Check for URL errors
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isNetworkError(urlErr.Err)
	}

	// Check for syscall errors
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Temporary() || opErr.Timeout() {
			return true
		}

		// Check underlying syscall error
		var syscallErr syscall.Errno
		if errors.As(opErr.Err, &syscallErr) {
			switch syscallErr {
			case syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ECONNABORTED,
				syscall.ETIMEDOUT, syscall.ENETUNREACH, syscall.EHOSTUNREACH:
				return true
			}
		}
	}

	return false
}

// classifyError returns a human-readable classification of the error type
// This is used for logging purposes
func classifyError(err error) string {
	if err == nil {
		return "none"
	}

	if isNetworkError(err) {
		return "network"
	}

	if statusErr, ok := err.(*apierrors.StatusError); ok {
		code := statusErr.ErrStatus.Code
		switch {
		case code >= 500:
			return "server_error"
		case code == 429:
			return "rate_limit"
		case code == 408:
			return "timeout"
		case code >= 400:
			return "client_error"
		}
	}

	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "etcdserver") {
		return "etcd"
	}
	if strings.Contains(errMsg, "tls") || strings.Contains(errMsg, "certificate") {
		return "tls"
	}
	if strings.Contains(errMsg, "timeout") {
		return "timeout"
	}

	return "unknown"
}
