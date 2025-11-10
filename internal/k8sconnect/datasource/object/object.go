package object

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/factory"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
)

type objectDataSource struct {
	clientFactory factory.ClientFactory
}

type objectDataSourceModel struct {
	APIVersion types.String `tfsdk:"api_version"`
	Kind       types.String `tfsdk:"kind"`
	Name       types.String `tfsdk:"name"`
	Namespace  types.String `tfsdk:"namespace"`
	Cluster    types.Object `tfsdk:"cluster"`

	// Outputs
	Manifest types.String  `tfsdk:"manifest"`
	YAMLBody types.String  `tfsdk:"yaml_body"`
	Object   types.Dynamic `tfsdk:"object"`
}

func NewObjectDataSource() datasource.DataSource {
	return &objectDataSource{}
}

func (d *objectDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object"
}

func (d *objectDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *objectDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
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
			"cluster": schema.SingleNestedAttribute{
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

func (d *objectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data objectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Convert connection using the auth package helper
	conn, err := auth.ObjectToConnectionModel(ctx, data.Cluster)
	if err != nil {
		resp.Diagnostics.AddError("Invalid connection", err.Error())
		return
	}

	// Get client
	client, err := d.clientFactory.GetClient(conn)
	if err != nil {
		// Client creation errors are connection-related, classify them
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Connect to Cluster", "cluster", "")
		return
	}

	// Discover the GVR using the discovery API
	apiVersion := data.APIVersion.ValueString()
	kind := data.Kind.ValueString()

	gvr, err := client.DiscoverGVR(ctx, apiVersion, kind)
	if err != nil {
		// GVR resolution errors
		resourceDesc := fmt.Sprintf("%s/%s", apiVersion, kind)
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Discover Resource Type", resourceDesc, apiVersion)
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
		k8serrors.AddClassifiedError(&resp.Diagnostics, err, "Read Resource", resourceDesc, data.APIVersion.ValueString())
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

	// Convert object to dynamic attribute for dot notation access
	objectValue, err := common.ConvertToAttrValue(ctx, obj.Object)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Convert Resource Object",
			fmt.Sprintf("Could not convert Kubernetes resource to Terraform object for field access: %s", err),
		)
		return
	}
	data.Object = types.DynamicValue(objectValue)

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
