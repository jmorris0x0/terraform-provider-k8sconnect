// internal/k8sconnect/datasource/resource/resource.go
package resource

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/factory"
)

type resourceDataSource struct {
	clientFactory factory.ClientFactory
}

type resourceDataSourceModel struct {
	APIVersion        types.String `tfsdk:"api_version"`
	Kind              types.String `tfsdk:"kind"`
	Metadata          types.Object `tfsdk:"metadata"`
	ClusterConnection types.Object `tfsdk:"cluster_connection"`

	// Outputs
	Manifest types.String  `tfsdk:"manifest"`
	YAMLBody types.String  `tfsdk:"yaml_body"`
	Object   types.Dynamic `tfsdk:"object"`
}

type metadataModel struct {
	Name      types.String `tfsdk:"name"`
	Namespace types.String `tfsdk:"namespace"`
}

func NewResourceDataSource() datasource.DataSource {
	return &resourceDataSource{}
}

func (d *resourceDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource"
}

func (d *resourceDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	clientFactory, ok := req.ProviderData.(factory.ClientFactory)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Provider Data Type",
			"Expected factory.ClientFactory",
		)
		return
	}

	d.clientFactory = clientFactory
}

func (d *resourceDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads an existing Kubernetes resource from the cluster",
		Attributes: map[string]schema.Attribute{
			"api_version": schema.StringAttribute{
				Required:    true,
				Description: "API version of the resource (e.g., 'v1', 'apps/v1')",
			},
			"kind": schema.StringAttribute{
				Required:    true,
				Description: "Kind of the resource (e.g., 'ConfigMap', 'Deployment')",
			},
			"metadata": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Metadata to identify the resource",
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required:    true,
						Description: "Name of the resource",
					},
					"namespace": schema.StringAttribute{
						Optional:    true,
						Description: "Namespace of the resource (defaults to 'default' for namespaced resources)",
					},
				},
			},
			"cluster_connection": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Cluster connection configuration",
				Attributes: map[string]schema.Attribute{
					"host": schema.StringAttribute{
						Optional:    true,
						Description: "The hostname (in form of URI) of the Kubernetes API server.",
					},
					"cluster_ca_certificate": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Root certificate bundle for TLS authentication. Accepts PEM format or base64-encoded PEM - automatically detected.",
					},
					"kubeconfig_file": schema.StringAttribute{
						Optional:    true,
						Description: "Path to the kubeconfig file.",
					},
					"kubeconfig_raw": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Raw kubeconfig file content.",
					},
					"context": schema.StringAttribute{
						Optional:    true,
						Description: "Context to use from the kubeconfig.",
					},
					"token": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Token to authenticate to the Kubernetes API server.",
					},
					"client_certificate": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Client certificate for TLS authentication. Accepts PEM format or base64-encoded PEM - automatically detected.",
					},
					"client_key": schema.StringAttribute{
						Optional:    true,
						Sensitive:   true,
						Description: "Client certificate key for TLS authentication. Accepts PEM format or base64-encoded PEM - automatically detected.",
					},
					"insecure": schema.BoolAttribute{
						Optional:    true,
						Description: "Whether server should be accessed without verifying the TLS certificate.",
					},
					"proxy_url": schema.StringAttribute{
						Optional:    true,
						Description: "URL of the proxy to use for requests.",
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
								Description: "Command to execute.",
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

			// Outputs
			"manifest": schema.StringAttribute{
				Computed:    true,
				Description: "JSON representation of the complete resource",
			},
			"yaml_body": schema.StringAttribute{
				Computed:    true,
				Description: "YAML representation of the complete resource",
			},
			"object": schema.DynamicAttribute{
				Computed:    true,
				Description: "The resource object for accessing individual fields",
			},
		},
	}
}

func (d *resourceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data resourceDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Convert connection using the auth package helper
	conn, err := auth.ObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		resp.Diagnostics.AddError("Invalid connection", err.Error())
		return
	}

	// Get client
	client, err := d.clientFactory.GetClient(conn)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create client", err.Error())
		return
	}

	// Parse metadata
	var metadata metadataModel
	diags := data.Metadata.As(ctx, &metadata, basetypes.ObjectAsOptions{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Use the client's discovery-based GVR resolution instead of naive pluralization
	// Create a minimal unstructured object for GVR discovery
	tempObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": data.APIVersion.ValueString(),
			"kind":       data.Kind.ValueString(),
		},
	}

	// Use the client's proper discovery-based resolution
	gvr, err := client.GetGVR(ctx, tempObj)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to resolve resource type",
			fmt.Sprintf("Unable to determine resource type for %s/%s: %v. "+
				"This may indicate the CRD is not installed or the API version is incorrect.",
				data.APIVersion.ValueString(),
				data.Kind.ValueString(),
				err))
		return
	}

	// Get namespace (default to "default" for namespaced resources)
	namespace := metadata.Namespace.ValueString()

	// Get the resource
	obj, err := client.Get(ctx, gvr, namespace, metadata.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Resource not found",
			fmt.Sprintf("Failed to get %s/%s in namespace %s: %v",
				data.Kind.ValueString(),
				metadata.Name.ValueString(),
				namespace,
				err),
		)
		return
	}

	// Set outputs
	jsonBytes, err := json.MarshalIndent(obj.Object, "", "  ")
	if err != nil {
		resp.Diagnostics.AddError("Failed to marshal JSON", err.Error())
		return
	}
	data.Manifest = types.StringValue(string(jsonBytes))

	yamlBytes, err := yaml.Marshal(obj.Object)
	if err != nil {
		resp.Diagnostics.AddError("Failed to marshal YAML", err.Error())
		return
	}
	data.YAMLBody = types.StringValue(string(yamlBytes))

	// For now, don't set the object field - it's causing type conversion issues
	// The manifest and yaml_body fields already provide the data in usable formats
	data.Object = types.DynamicNull()

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
