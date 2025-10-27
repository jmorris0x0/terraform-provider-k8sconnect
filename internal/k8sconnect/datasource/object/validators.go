package object

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// ConfigValidators implements datasource.DataSourceWithConfigValidators
func (d *objectDataSource) ConfigValidators(ctx context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		&manifestDSClusterValidator{},
		&manifestDSExecAuthValidator{},
	}
}

// =============================================================================
// manifestDSClusterValidator ensures exactly one connection mode is specified
// =============================================================================

type manifestDSClusterValidator struct{}

func (v *manifestDSClusterValidator) Description(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (host + cluster_ca_certificate or insecure) or kubeconfig"
}

func (v *manifestDSClusterValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (`host` + `cluster_ca_certificate` or `insecure`), `kubeconfig`"
}

func (v *manifestDSClusterValidator) ValidateDataSource(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var data objectDataSourceModel
	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check that exactly one of cluster or cluster_connection is specified
	clusterIsSet := !data.Cluster.IsNull() && !data.Cluster.IsUnknown()
	clusterConnectionIsSet := !data.ClusterConnection.IsNull() && !data.ClusterConnection.IsUnknown()

	if !clusterIsSet && !clusterConnectionIsSet {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster"),
			"Missing Cluster Connection Configuration",
			"Either 'cluster' or 'cluster_connection' (deprecated) is required.\n\n"+
				"Specify how to connect to your Kubernetes cluster using one of these blocks:\n"+
				"• 'cluster' - recommended\n"+
				"• 'cluster_connection' - deprecated, will be removed in a future version",
		)
		return
	}

	if clusterIsSet && clusterConnectionIsSet {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster"),
			"Conflicting Configuration Blocks",
			"Cannot specify both 'cluster' and 'cluster_connection'.\n\n"+
				"Use only 'cluster' (recommended). The 'cluster_connection' attribute is deprecated and will be removed in a future version.",
		)
		return
	}

	// Use whichever is set for validation
	clusterToValidate := data.Cluster
	if clusterConnectionIsSet {
		clusterToValidate = data.ClusterConnection
	}

	// Skip validation for unknown connections (during planning)
	if clusterToValidate.IsUnknown() {
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, clusterToValidate)
	if err != nil {
		// Unknown values during planning - skip validation
		return
	}

	// Use common validation logic
	err = auth.ValidateConnectionWithUnknowns(ctx, connModel)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster"),
			"Invalid Cluster Connection Configuration",
			err.Error(),
		)
	}
}

// =============================================================================
// manifestDSExecAuthValidator ensures complete exec configuration when present
// =============================================================================

type manifestDSExecAuthValidator struct{}

func (v *manifestDSExecAuthValidator) Description(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (api_version, command) are provided"
}

func (v *manifestDSExecAuthValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (`api_version`, `command`) are provided"
}

func (v *manifestDSExecAuthValidator) ValidateDataSource(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var data objectDataSourceModel
	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Determine which cluster config to use
	clusterToValidate := data.Cluster
	if !data.ClusterConnection.IsNull() && !data.ClusterConnection.IsUnknown() {
		clusterToValidate = data.ClusterConnection
	}

	// If connection is unknown or null, skip validation
	if clusterToValidate.IsUnknown() || clusterToValidate.IsNull() {
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, clusterToValidate)
	if err != nil {
		// Unknown values during planning
		return
	}

	// Use common validation logic (which includes exec validation)
	err = auth.ValidateConnectionWithUnknowns(ctx, connModel)
	if err != nil {
		// Only report if it's an exec-related error
		if strings.Contains(err.Error(), "exec authentication") {
			resp.Diagnostics.AddAttributeError(
				path.Root("cluster").AtName("exec"),
				"Invalid Exec Authentication Configuration",
				err.Error(),
			)
		}
	}
}
