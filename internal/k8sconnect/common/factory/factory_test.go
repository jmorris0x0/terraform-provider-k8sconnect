package factory

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

func TestCachedClientFactory_CacheKey(t *testing.T) {
	factory := NewCachedClientFactory()

	// Test that same connection generates same key
	conn1 := auth.ClusterModel{
		Host:  types.StringValue("https://k8s.example.com"),
		Token: types.StringValue("test-token"),
	}

	conn2 := auth.ClusterModel{
		Host:  types.StringValue("https://k8s.example.com"),
		Token: types.StringValue("test-token"),
	}

	key1 := factory.generateCacheKey(conn1)
	key2 := factory.generateCacheKey(conn2)

	assert.Equal(t, key1, key2, "Same connections should generate same cache key")

	// Test that different connections generate different keys
	conn3 := auth.ClusterModel{
		Host:  types.StringValue("https://k8s.example.com"),
		Token: types.StringValue("different-token"),
	}

	key3 := factory.generateCacheKey(conn3)
	assert.NotEqual(t, key1, key3, "Different connections should generate different cache keys")
}

func TestCachedClientFactory_CacheKeyWithExec(t *testing.T) {
	factory := NewCachedClientFactory()

	conn1 := auth.ClusterModel{
		Host: types.StringValue("https://k8s.example.com"),
		Exec: &auth.ExecAuthModel{
			APIVersion: types.StringValue("client.authentication.k8s.io/v1beta1"),
			Command:    types.StringValue("aws"),
			Args: []types.String{
				types.StringValue("eks"),
				types.StringValue("get-token"),
			},
		},
	}

	conn2 := auth.ClusterModel{
		Host: types.StringValue("https://k8s.example.com"),
		Exec: &auth.ExecAuthModel{
			APIVersion: types.StringValue("client.authentication.k8s.io/v1beta1"),
			Command:    types.StringValue("aws"),
			Args: []types.String{
				types.StringValue("eks"),
				types.StringValue("get-token"),
				types.StringValue("--cluster-name"),
				types.StringValue("prod"),
			},
		},
	}

	key1 := factory.generateCacheKey(conn1)
	key2 := factory.generateCacheKey(conn2)

	assert.NotEqual(t, key1, key2, "Different exec args should generate different cache keys")
}

func TestCachedClientFactory_ClearCache(t *testing.T) {
	factory := NewCachedClientFactory()

	// Simulate adding to cache by checking size
	assert.Equal(t, 0, factory.GetCacheSize(), "Cache should start empty")

	factory.ClearCache()
	assert.Equal(t, 0, factory.GetCacheSize(), "Cache should be empty after clear")
}

func TestCachedClientFactory_GetClient_InvalidConnection(t *testing.T) {
	factory := NewCachedClientFactory()

	// Connection with no valid auth method
	conn := auth.ClusterModel{
		// Empty - should fail
	}

	_, err := factory.GetClient(conn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create REST config")
}

// Note: Testing actual client creation would require a valid Kubernetes
// configuration, which we don't have in unit tests. The integration
// with real clusters is tested in the acceptance tests.
