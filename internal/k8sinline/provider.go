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

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/datasource/yaml_split"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
	manifestres "github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/resource/manifest"
)

// version is set by ldflags during build
var version string = "dev"

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
	resp.Version = version
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
	return []func() datasource.DataSource{
		yaml_split.NewYamlSplitDataSource,
	}
}

func (p *k8sinlineProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	// no provider‑level schema yet
}

// getCachedClient returns a cached Kubernetes client for the given connection.
// This now uses the common auth package.
func (p *k8sinlineProvider) getCachedClient(conn auth.ClusterConnectionModel) (k8sclient.K8sClient, error) {
	// Generate cache key based on connection config
	cacheKey := p.generateCacheKey(conn)

	// Check cache first
	p.cacheMutex.RLock()
	if client, exists := p.clientCache[cacheKey]; exists {
		p.cacheMutex.RUnlock()
		return client, nil
	}
	p.cacheMutex.RUnlock()

	// Create new client using common auth
	config, err := auth.CreateRESTConfig(context.Background(), conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config: %w", err)
	}

	client, err := k8sclient.NewDynamicK8sClient(config)
	if err != nil {
		return nil, err
	}

	// Cache the client
	p.cacheMutex.Lock()
	p.clientCache[cacheKey] = client
	p.cacheMutex.Unlock()

	return client, nil
}

// generateCacheKey creates a unique key for caching clients based on connection config.
// Updated to use the common auth model.
func (p *k8sinlineProvider) generateCacheKey(conn auth.ClusterConnectionModel) string {
	h := sha256.New()

	// Hash all connection fields
	h.Write([]byte(conn.Host.ValueString()))
	h.Write([]byte(conn.ClusterCACertificate.ValueString()))
	h.Write([]byte(conn.KubeconfigFile.ValueString()))
	h.Write([]byte(conn.KubeconfigRaw.ValueString()))
	h.Write([]byte(conn.Context.ValueString()))
	h.Write([]byte(conn.Token.ValueString()))
	h.Write([]byte(conn.ClientCertificate.ValueString()))
	h.Write([]byte(conn.ClientKey.ValueString()))
	h.Write([]byte(fmt.Sprintf("%v", conn.Insecure.ValueBool())))
	h.Write([]byte(conn.ProxyURL.ValueString()))

	// Hash exec config if present
	if conn.Exec != nil {
		h.Write([]byte(conn.Exec.APIVersion.ValueString()))
		h.Write([]byte(conn.Exec.Command.ValueString()))
		for _, arg := range conn.Exec.Args {
			h.Write([]byte(arg.ValueString()))
		}
		for k, v := range conn.Exec.Env {
			h.Write([]byte(k))
			h.Write([]byte(v.ValueString()))
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}
