// internal/k8sconnect/datasource/resource/resource.go
package resource

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/factory"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
)

type resourceDataSource struct {
	clientFactory factory.ClientFactory
}

type resourceDataSourceModel struct {
	APIVersion        types.String `tfsdk:"api_version"`
	Kind              types.String `tfsdk:"kind"`
	Name              types.String `tfsdk:"name"`
	Namespace         types.String `tfsdk:"namespace"`
	ClusterConnection types.Object `tfsdk:"cluster_connection"`

	// Outputs
	Manifest types.String  `tfsdk:"manifest"`
	YAMLBody types.String  `tfsdk:"yaml_body"`
	Object   types.Dynamic `tfsdk:"object"`
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
		Description: "Reads an existing Kubernetes resource from the cluster and makes its data available to Terraform configuration. " +
			"Use this to reference cluster resources not managed by Terraform (e.g., cloud provider defaults, operator-created resources) " +
			"or to access dynamic values like LoadBalancer IPs, Service endpoints, or ConfigMap data for use in other resources.",
		Attributes: map[string]schema.Attribute{
			"api_version": schema.StringAttribute{
				Required:    true,
				Description: "API version of the resource (e.g., 'v1', 'apps/v1')",
			},
			"kind": schema.StringAttribute{
				Required:    true,
				Description: "Kind of the resource (e.g., 'ConfigMap', 'Deployment')",
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the resource",
			},
			"namespace": schema.StringAttribute{
				Optional:    true,
				Description: "Namespace of the resource (optional for cluster-scoped resources, defaults to 'default' for namespaced resources if not specified)",
			},
			"cluster_connection": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Cluster connection configuration",
				Attributes:  auth.GetConnectionSchemaForDataSource(),
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
		// Client creation errors are connection-related, classify them
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Connect to Cluster", "cluster")
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
		// GVR resolution errors
		resourceDesc := fmt.Sprintf("%s/%s", data.APIVersion.ValueString(), data.Kind.ValueString())
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Resolve Resource Type", resourceDesc)
		return
	}

	// Get namespace from flat field
	namespace := data.Namespace.ValueString()
	name := data.Name.ValueString()

	// Get the resource
	obj, err := client.Get(ctx, gvr, namespace, name)
	if err != nil {
		resourceDesc := fmt.Sprintf("%s %s", data.Kind.ValueString(), name)
		if namespace != "" {
			resourceDesc = fmt.Sprintf("%s %s/%s", data.Kind.ValueString(), namespace, name)
		}
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Read Resource", resourceDesc)
		return
	}

	// Surface any API warnings from get operation
	surfaceK8sWarnings(ctx, client, &resp.Diagnostics)

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

// surfaceK8sWarnings checks for Kubernetes API warnings and adds them as Terraform diagnostics
func surfaceK8sWarnings(ctx context.Context, client k8sclient.K8sClient, diagnostics *diag.Diagnostics) {
	warnings := client.GetWarnings()
	for _, warning := range warnings {
		diagnostics.AddWarning(
			"Kubernetes API Warning",
			fmt.Sprintf("The Kubernetes API server returned a warning:\n\n%s", warning),
		)
		tflog.Warn(ctx, "Kubernetes API warning", map[string]interface{}{
			"warning": warning,
		})
	}
}
