package validators

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// Cluster validates cluster connection configuration
// This is a generic resource-level validator that works with any resource
// that has a cluster attribute
type Cluster struct{}

func (v Cluster) Description(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (host + cluster_ca_certificate or insecure) or kubeconfig"
}

func (v Cluster) MarkdownDescription(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (`host` + `cluster_ca_certificate` or `insecure`), `kubeconfig`"
}

func (v Cluster) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// Get cluster attribute
	var conn types.Object
	diags := req.Config.GetAttribute(ctx, path.Root("cluster"), &conn)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Also check for deprecated cluster_connection
	var connDeprecated types.Object
	diagsDeprecated := req.Config.GetAttribute(ctx, path.Root("cluster_connection"), &connDeprecated)
	// Ignore errors here - cluster_connection might not exist in all resources

	// Determine which connection to validate
	clusterIsSet := !conn.IsNull() && !conn.IsUnknown()
	clusterConnectionIsSet := diagsDeprecated == nil && !connDeprecated.IsNull() && !connDeprecated.IsUnknown()

	// Check that at least one is specified
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

	// Check that both aren't specified
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
	connToValidate := conn
	if clusterConnectionIsSet {
		connToValidate = connDeprecated
	}

	// Skip validation for unknown connections (during planning)
	if connToValidate.IsUnknown() {
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, connToValidate)
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

// ExecAuth validates exec authentication configuration
// This is a generic resource-level validator that works with any resource
// that has a cluster.exec attribute
type ExecAuth struct{}

func (v ExecAuth) Description(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (api_version, command) are provided"
}

func (v ExecAuth) MarkdownDescription(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (`api_version`, `command`) are provided"
}

func (v ExecAuth) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// Get cluster attribute
	var conn types.Object
	diags := req.Config.GetAttribute(ctx, path.Root("cluster"), &conn)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Also check for deprecated cluster_connection
	var connDeprecated types.Object
	diagsDeprecated := req.Config.GetAttribute(ctx, path.Root("cluster_connection"), &connDeprecated)

	// Determine which connection to use
	connToValidate := conn
	if diagsDeprecated == nil && !connDeprecated.IsNull() && !connDeprecated.IsUnknown() {
		connToValidate = connDeprecated
	}

	// If connection is unknown or null, skip validation
	if connToValidate.IsUnknown() || connToValidate.IsNull() {
		return
	}

	// Convert to connection model to access exec field
	connModel, err := auth.ObjectToConnectionModel(ctx, connToValidate)
	if err != nil {
		// If conversion fails, it might be due to unknown values during planning
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
