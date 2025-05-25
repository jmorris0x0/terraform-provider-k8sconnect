// internal/k8sinline/provider.go
package k8sinline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
	manifestres "github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/resource/manifest"
)

// Ensure we implement the provider interface
var _ provider.Provider = (*k8sinlineProvider)(nil)

// k8sinlineProvider is our Terraform provider with connection caching.
type k8sinlineProvider struct {
	// Connection cache - key is connection hash, value is cached client
	clientCache map[string]k8sclient.K8sClient
	cacheMutex  sync.RWMutex
}

// New returns a factory for k8sinlineProvider
func New() provider.Provider {
	return &k8sinlineProvider{
		clientCache: make(map[string]k8sclient.K8sClient),
	}
}

func (p *k8sinlineProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "k8sinline"
	resp.Version = "0.1.0"
}

func (p *k8sinlineProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	// Initialize cache if not already done
	if p.clientCache == nil {
		p.clientCache = make(map[string]k8sclient.K8sClient)
	}

	// No global configuration needed for now
	// Resources will request cached clients via the injected getter function
}

func (p *k8sinlineProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource {
			return manifestres.NewManifestResourceWithClientGetter(p.getCachedClient)
		},
	}
}

func (p *k8sinlineProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return nil // or []func() datasource.DataSource{} if you prefer
}

func (p *k8sinlineProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	// no provider‑level schema
}

// ConnectionConfig represents the essential connection details used for caching
type ConnectionConfig struct {
	Host                 string
	ClusterCACertificate string
	KubeconfigFile       string
	KubeconfigRaw        string
	Context              string
	ExecAPIVersion       string
	ExecCommand          string
	ExecArgs             []string
}

// generateCacheKey creates a stable hash from connection configuration
func (p *k8sinlineProvider) generateCacheKey(config ConnectionConfig) string {
	// Create a deterministic string from all connection parameters
	data := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%v",
		config.Host,
		config.ClusterCACertificate,
		config.KubeconfigFile,
		config.KubeconfigRaw,
		config.Context,
		config.ExecAPIVersion,
		config.ExecCommand,
		config.ExecArgs,
	)

	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// getCachedClient returns a cached client or creates a new one
func (p *k8sinlineProvider) getCachedClient(conn manifestres.ClusterConnectionModel) (k8sclient.K8sClient, error) {
	// Convert to our internal config format
	config := p.convertToConnectionConfig(conn)

	// Generate cache key
	cacheKey := p.generateCacheKey(config)

	// Try to get existing client
	p.cacheMutex.RLock()
	if client, exists := p.clientCache[cacheKey]; exists {
		p.cacheMutex.RUnlock()
		return client, nil
	}
	p.cacheMutex.RUnlock()

	// Create new client - delegate to the manifest resource's existing logic
	client, err := manifestres.CreateK8sClientFromConnection(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create cached client: %w", err)
	}

	// Cache the new client
	p.cacheMutex.Lock()
	p.clientCache[cacheKey] = client
	p.cacheMutex.Unlock()

	return client, nil
}

// convertToConnectionConfig extracts essential connection details for caching
func (p *k8sinlineProvider) convertToConnectionConfig(conn manifestres.ClusterConnectionModel) ConnectionConfig {
	config := ConnectionConfig{}

	if !conn.Host.IsNull() {
		config.Host = conn.Host.ValueString()
	}
	if !conn.ClusterCACertificate.IsNull() {
		config.ClusterCACertificate = conn.ClusterCACertificate.ValueString()
	}
	if !conn.KubeconfigFile.IsNull() {
		config.KubeconfigFile = conn.KubeconfigFile.ValueString()
	}
	if !conn.KubeconfigRaw.IsNull() {
		config.KubeconfigRaw = conn.KubeconfigRaw.ValueString()
	}
	if !conn.Context.IsNull() {
		config.Context = conn.Context.ValueString()
	}

	// Handle exec config
	if conn.Exec != nil {
		if !conn.Exec.APIVersion.IsNull() {
			config.ExecAPIVersion = conn.Exec.APIVersion.ValueString()
		}
		if !conn.Exec.Command.IsNull() {
			config.ExecCommand = conn.Exec.Command.ValueString()
		}
		if len(conn.Exec.Args) > 0 {
			config.ExecArgs = make([]string, len(conn.Exec.Args))
			for i, arg := range conn.Exec.Args {
				config.ExecArgs[i] = arg.ValueString()
			}
		}
	}

	return config
}

// GetCacheStats returns cache statistics for debugging/monitoring
func (p *k8sinlineProvider) GetCacheStats() map[string]interface{} {
	p.cacheMutex.RLock()
	defer p.cacheMutex.RUnlock()

	return map[string]interface{}{
		"cached_clients": len(p.clientCache),
		"cache_keys": func() []string {
			keys := make([]string, 0, len(p.clientCache))
			for k := range p.clientCache {
				keys = append(keys, k[:8]+"...") // Only show first 8 chars for security
			}
			return keys
		}(),
	}
}
