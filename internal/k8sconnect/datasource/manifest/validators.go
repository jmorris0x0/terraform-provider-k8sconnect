// internal/k8sconnect/datasource/manifest/validators.go
package manifest

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// ConfigValidators implements datasource.DataSourceWithConfigValidators
func (d *manifestDataSource) ConfigValidators(ctx context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		&manifestDSClusterConnectionValidator{},
		&manifestDSExecAuthValidator{},
	}
}

// =============================================================================
// manifestDSClusterConnectionValidator ensures exactly one connection mode is specified
// =============================================================================

type manifestDSClusterConnectionValidator struct{}

func (v *manifestDSClusterConnectionValidator) Description(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (host + cluster_ca_certificate or insecure) or kubeconfig"
}

func (v *manifestDSClusterConnectionValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (`host` + `cluster_ca_certificate` or `insecure`), `kubeconfig`"
}

func (v *manifestDSClusterConnectionValidator) ValidateDataSource(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var data manifestDataSourceModel
	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip validation for unknown connections (during planning)
	if data.ClusterConnection.IsUnknown() {
		return
	}

	// Check connection exists
	if data.ClusterConnection.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Missing Cluster Connection Configuration",
			"cluster_connection block is required.",
		)
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		// Unknown values during planning - skip validation
		return
	}

	// Use common validation logic
	err = auth.ValidateConnectionWithUnknowns(ctx, connModel)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
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
	var data manifestDataSourceModel
	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If connection is unknown or null, skip validation
	if data.ClusterConnection.IsUnknown() || data.ClusterConnection.IsNull() {
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, data.ClusterConnection)
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
				path.Root("cluster_connection").AtName("exec"),
				"Invalid Exec Authentication Configuration",
				err.Error(),
			)
		}
	}
}
