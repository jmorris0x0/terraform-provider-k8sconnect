// internal/k8sinline/k8sclient/client_test.go
package k8sclient

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestDiscoveryCacheInvalidation(t *testing.T) {
	// Test that our client implements CacheInvalidator
	stubClient := NewStubK8sClient()

	// Verify it implements the interface
	_, ok := stubClient.(CacheInvalidator)
	if !ok {
		t.Error("stubK8sClient should implement CacheInvalidator interface")
	}

	ctx := context.Background()

	// Test cache invalidation doesn't error
	err := stubClient.InvalidateDiscoveryCache(ctx)
	if err != nil {
		t.Errorf("InvalidateDiscoveryCache should not error for stub client, got: %v", err)
	}
}

func TestDiscoveryErrorDetection(t *testing.T) {
	// Import the function from manifest package for testing
	// This would be in the manifest package tests in practice
	tests := []struct {
		name        string
		err         error
		shouldMatch bool
	}{
		{
			name:        "nil error",
			err:         nil,
			shouldMatch: false,
		},
		{
			name:        "discovery error - no resource found",
			err:         fmt.Errorf("no resource found for apps/v1, Kind=MyCustomResource"),
			shouldMatch: true,
		},
		{
			name:        "discovery error - failed to discover",
			err:         fmt.Errorf("failed to discover resources for custom.io/v1"),
			shouldMatch: true,
		},
		{
			name:        "network error",
			err:         fmt.Errorf("connection refused"),
			shouldMatch: false,
		},
		{
			name:        "permission error",
			err:         fmt.Errorf("forbidden: access denied"),
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDiscoveryError(tt.err)
			if result != tt.shouldMatch {
				t.Errorf("isDiscoveryError(%v) = %v, want %v", tt.err, result, tt.shouldMatch)
			}
		})
	}
}

// Helper function for testing (would normally be in manifest package)
func isDiscoveryError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	discoveryErrorPatterns := []string{
		"no resource found",
		"failed to discover resources",
		"couldn't get resource list",
		"the server doesn't have a resource type",
	}

	for _, pattern := range discoveryErrorPatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return true
		}
	}
	return false
}
