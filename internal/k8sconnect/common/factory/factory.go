package factory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

// ClientFactory handles creation and caching of K8s clients
type ClientFactory interface {
	GetClient(conn auth.ClusterConnectionModel) (k8sclient.K8sClient, error)
}

// CachedClientFactory implements ClientFactory with connection caching
type CachedClientFactory struct {
	cache map[string]k8sclient.K8sClient
	mu    sync.RWMutex
}

// NewCachedClientFactory creates a new factory with caching
func NewCachedClientFactory() *CachedClientFactory {
	return &CachedClientFactory{
		cache: make(map[string]k8sclient.K8sClient),
	}
}

// GetClient returns a cached client or creates a new one
func (f *CachedClientFactory) GetClient(conn auth.ClusterConnectionModel) (k8sclient.K8sClient, error) {
	cacheKey := f.generateCacheKey(conn)

	// First check with read lock
	f.mu.RLock()
	if client, exists := f.cache[cacheKey]; exists {
		f.mu.RUnlock()
		return client, nil
	}
	f.mu.RUnlock()

	// Acquire write lock for creation
	f.mu.Lock()
	defer f.mu.Unlock()

	// Double-check - another goroutine might have created it
	if client, exists := f.cache[cacheKey]; exists {
		return client, nil
	}

	// Now safe to create
	config, err := auth.CreateRESTConfig(context.Background(), conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config: %w", err)
	}

	client, err := k8sclient.NewDynamicK8sClient(config)
	if err != nil {
		return nil, err
	}

	f.cache[cacheKey] = client
	return client, nil
}

// generateCacheKey creates a unique key for caching clients based on connection config
func (f *CachedClientFactory) generateCacheKey(conn auth.ClusterConnectionModel) string {
	h := sha256.New()

	// Hash all connection fields that affect the client
	f.hashStringField(h, conn.Host)
	f.hashStringField(h, conn.ClusterCACertificate)
	f.hashStringField(h, conn.Kubeconfig)
	f.hashStringField(h, conn.Context)
	f.hashStringField(h, conn.Token)
	f.hashStringField(h, conn.ClientCertificate)
	f.hashStringField(h, conn.ClientKey)
	f.hashBoolField(h, conn.Insecure)
	f.hashStringField(h, conn.ProxyURL)

	// Hash exec config if present
	if conn.Exec != nil {
		f.hashStringField(h, conn.Exec.APIVersion)
		f.hashStringField(h, conn.Exec.Command)
		for _, arg := range conn.Exec.Args {
			f.hashStringField(h, arg)
		}
		for k, v := range conn.Exec.Env {
			h.Write([]byte(k))
			f.hashStringField(h, v)
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}

// hashStringField safely hashes a types.String field
func (f *CachedClientFactory) hashStringField(h hash.Hash, field types.String) {
	if !field.IsNull() && !field.IsUnknown() {
		h.Write([]byte(field.ValueString()))
	}
}

// hashBoolField safely hashes a types.Bool field
func (f *CachedClientFactory) hashBoolField(h hash.Hash, field types.Bool) {
	if !field.IsNull() && !field.IsUnknown() {
		h.Write([]byte(fmt.Sprintf("%v", field.ValueBool())))
	}
}

// ClearCache removes all cached clients
// Useful for testing or when provider is reconfigured
func (f *CachedClientFactory) ClearCache() {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Clear the map
	f.cache = make(map[string]k8sclient.K8sClient)
}

// GetCacheSize returns the number of cached clients
// Useful for monitoring and debugging
func (f *CachedClientFactory) GetCacheSize() int {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return len(f.cache)
}
