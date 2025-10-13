// internal/k8sconnect/provider.go
package k8sconnect

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/factory"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	resourceds "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/resource"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/yaml_scoped"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/yaml_split"
	manifestres "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/resource/manifest"
	patchres "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/resource/patch"
)

// version is set by ldflags during build
var version string = "dev"

// Ensure we implement the provider interface
var _ provider.Provider = (*k8sconnectProvider)(nil)

// k8sconnectProviderModel describes the provider data model.
type k8sconnectProviderModel struct {
	// Empty - no provider configuration
}

// k8sconnectProvider is our Terraform provider
type k8sconnectProvider struct {
	// Client factory only
	clientFactory factory.ClientFactory
}

// New returns a factory for k8sconnectProvider
func New() provider.Provider {
	return &k8sconnectProvider{
		clientFactory: factory.NewCachedClientFactory(),
	}
}

func (p *k8sconnectProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "k8sconnect"
	resp.Version = version
}

func (p *k8sconnectProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The k8sconnect provider enables single-apply cluster bootstrapping with inline per-resource authentication. " +
			"Deploy clusters and workloads in one terraform apply without provider dependency cycles. " +
			"Features Server-Side Apply with accurate dry-run projections, field ownership tracking for multi-controller environments, " +
			"and surgical patching of existing resources. Works seamlessly in modules and supports multi-cluster deployments.",
		Attributes: map[string]schema.Attribute{},
	}
}

func (p *k8sconnectProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config k8sconnectProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Pass client factory directly to resources and data sources
	resp.DataSourceData = p.clientFactory
	resp.ResourceData = p.clientFactory
}

func (p *k8sconnectProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource {
			// For backward compatibility, wrap the new client factory to match old interface
			return manifestres.NewManifestResourceWithClientGetter(func(conn auth.ClusterConnectionModel) (k8sclient.K8sClient, error) {
				return p.clientFactory.GetClient(conn)
			})
		},
		func() resource.Resource {
			// Patch resource using same client getter pattern
			return patchres.NewPatchResourceWithClientGetter(func(conn auth.ClusterConnectionModel) (k8sclient.K8sClient, error) {
				return p.clientFactory.GetClient(conn)
			})
		},
	}
}

func (p *k8sconnectProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		yaml_split.NewYamlSplitDataSource,
		yaml_scoped.NewYamlScopedDataSource,
		resourceds.NewResourceDataSource,
	}
}
