// internal/k8sinline/provider.go
package k8sinline

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Ensure we implement the provider interface
var _ provider.Provider = (*k8sinlineProvider)(nil)

// k8sinlineProvider is our Terraform provider.
type k8sinlineProvider struct{}

// New returns a factory for k8sinlineProvider
func New() provider.Provider {
	return &k8sinlineProvider{}
}

func (p *k8sinlineProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "k8sinline"
	resp.Version = "0.1.0"
}

func (p *k8sinlineProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	// no global configuration for now
}

func (p *k8sinlineProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewManifestResource,
	}
}

func (p *k8sinlineProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return nil // or []func() datasource.DataSource{} if you prefer
}

func (p *k8sinlineProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	// no provider‑level schema
}
