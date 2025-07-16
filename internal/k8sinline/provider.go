// internal/k8sinline/provider.go
package k8sinline

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/common"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/common/client"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/datasource/yaml_split"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
	manifestres "github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/resource/manifest"
)

// version is set by ldflags during build
var version string = "dev"

// Ensure we implement the provider interface
var _ provider.Provider = (*k8sinlineProvider)(nil)

// k8sinlineProviderModel describes the provider data model.
type k8sinlineProviderModel struct {
	ClusterConnection types.Object `tfsdk:"cluster_connection"`
}

// k8sinlineProvider is our Terraform provider
type k8sinlineProvider struct {
	// Connection resolver and client factory
	connectionResolver *auth.ConnectionResolver
	clientFactory      client.ClientFactory
}

// New returns a factory for k8sinlineProvider
func New() provider.Provider {
	return &k8sinlineProvider{
		connectionResolver: auth.NewConnectionResolver(),
		clientFactory:      client.NewCachedClientFactory(),
	}
}

func (p *k8sinlineProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "k8sinline"
	resp.Version = version
}

func (p *k8sinlineProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The k8sinline provider enables management of Kubernetes resources with inline or provider-level authentication.",
		Attributes: map[string]schema.Attribute{
			"cluster_connection": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Default cluster connection configuration. Resources will use this connection unless they specify their own.",
				Attributes: map[string]schema.Attribute{
					"host": schema.StringAttribute{
						Optional:    true,
						Description: "The hostname (in form of URI) of the Kubernetes API server.",
					},
					"cluster_ca_certificate": schema.StringAttribute{
						Optional:    true,
						Description: "PEM-encoded root certificate bundle for TLS authentication.",
					},
					"kubeconfig_file": schema.StringAttribute{
						Optional:    true,
						Description: "Path to the kubeconfig file. Defaults to KUBECONFIG environment variable or ~/.kube/config.",
					},
					"kubeconfig_raw": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Raw kubeconfig file content.",
					},
					"context": schema.StringAttribute{
						Optional:    true,
						Description: "Context to use from the kubeconfig file.",
					},
					"token": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Token to authenticate to the Kubernetes API server.",
					},
					"client_certificate": schema.StringAttribute{
						Optional:    true,
						Description: "PEM-encoded client certificate for TLS authentication.",
					},
					"client_key": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "PEM-encoded client certificate key for TLS authentication.",
					},
					"insecure": schema.BoolAttribute{
						Optional:    true,
						Description: "Whether server should be accessed without verifying the TLS certificate.",
					},
					"proxy_url": schema.StringAttribute{
						Optional:    true,
						Description: "URL of the proxy to use for requests to the Kubernetes API.",
					},
					"exec": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "Configuration for exec-based authentication.",
						Attributes: map[string]schema.Attribute{
							"api_version": schema.StringAttribute{
								Required:    true,
								Description: "API version to use when encoding the ExecCredentials resource.",
							},
							"command": schema.StringAttribute{
								Required:    true,
								Description: "Command to execute for credential plugin.",
							},
							"args": schema.ListAttribute{
								Optional:    true,
								ElementType: types.StringType,
								Description: "Arguments to pass when executing the plugin.",
							},
							"env": schema.MapAttribute{
								Optional:    true,
								ElementType: types.StringType,
								Description: "Environment variables to set when executing the plugin.",
							},
						},
					},
				},
			},
		},
	}
}

func (p *k8sinlineProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config k8sinlineProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if provider has connection configuration
	if !config.ClusterConnection.IsNull() && !config.ClusterConnection.IsUnknown() {
		// Convert to connection model - reuse the same conversion logic from manifest resource
		var conn auth.ClusterConnectionModel

		attrs := config.ClusterConnection.Attributes()

		// Basic fields
		conn.Host = attrs["host"].(types.String)
		conn.ClusterCACertificate = attrs["cluster_ca_certificate"].(types.String)
		conn.KubeconfigFile = attrs["kubeconfig_file"].(types.String)
		conn.KubeconfigRaw = attrs["kubeconfig_raw"].(types.String)
		conn.Context = attrs["context"].(types.String)
		conn.Token = attrs["token"].(types.String)
		conn.ClientCertificate = attrs["client_certificate"].(types.String)
		conn.ClientKey = attrs["client_key"].(types.String)
		conn.Insecure = attrs["insecure"].(types.Bool)
		conn.ProxyURL = attrs["proxy_url"].(types.String)

		// Handle exec if present
		if execObj, ok := attrs["exec"].(types.Object); ok && !execObj.IsNull() {
			execAttrs := execObj.Attributes()
			conn.Exec = &auth.ExecAuthModel{
				APIVersion: execAttrs["api_version"].(types.String),
				Command:    execAttrs["command"].(types.String),
			}

			// Handle args list
			if argsList, ok := execAttrs["args"].(types.List); ok && !argsList.IsNull() {
				args := make([]types.String, 0, len(argsList.Elements()))
				for _, elem := range argsList.Elements() {
					args = append(args, elem.(types.String))
				}
				conn.Exec.Args = args
			}

			// Handle env map
			if envMap, ok := execAttrs["env"].(types.Map); ok && !envMap.IsNull() {
				env := make(map[string]types.String)
				for k, v := range envMap.Elements() {
					env[k] = v.(types.String)
				}
				conn.Exec.Env = env
			}
		}

		p.connectionResolver.SetProviderConnection(&conn)
	}

	// Create connection config to pass to resources
	connectionConfig := &common.ConnectionConfig{
		ConnectionResolver: p.connectionResolver,
		ClientFactory:      p.clientFactory,
	}

	// Make connection config available to resources and data sources
	resp.DataSourceData = connectionConfig
	resp.ResourceData = connectionConfig
}

func (p *k8sinlineProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource {
			// For backward compatibility, wrap the new client factory to match old interface
			return manifestres.NewManifestResourceWithClientGetter(func(conn auth.ClusterConnectionModel) (k8sclient.K8sClient, error) {
				return p.clientFactory.GetClient(conn)
			})
		},
	}
}

func (p *k8sinlineProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		yaml_split.NewYamlSplitDataSource,
	}
}
