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

	// Skip validation for unknown connections (during planning)
	if data.Cluster.IsUnknown() {
		return
	}

	// Check connection exists
	if data.Cluster.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster"),
			"Missing Cluster Connection Configuration",
			"cluster block is required.",
		)
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, data.Cluster)
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

	// If connection is unknown or null, skip validation
	if data.Cluster.IsUnknown() || data.Cluster.IsNull() {
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, data.Cluster)
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
