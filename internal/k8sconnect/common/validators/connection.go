package validators

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// ClusterConnection validates cluster connection configuration
// This is a generic resource-level validator that works with any resource
// that has a cluster_connection attribute
type ClusterConnection struct{}

func (v ClusterConnection) Description(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (host + cluster_ca_certificate or insecure) or kubeconfig"
}

func (v ClusterConnection) MarkdownDescription(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (`host` + `cluster_ca_certificate` or `insecure`), `kubeconfig`"
}

func (v ClusterConnection) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// Get cluster_connection attribute
	var conn types.Object
	diags := req.Config.GetAttribute(ctx, path.Root("cluster_connection"), &conn)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip validation for unknown connections (during planning)
	if conn.IsUnknown() {
		return
	}

	// Check connection exists
	if conn.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Missing Cluster Connection Configuration",
			"cluster_connection block is required.",
		)
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, conn)
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

// ExecAuth validates exec authentication configuration
// This is a generic resource-level validator that works with any resource
// that has a cluster_connection.exec attribute
type ExecAuth struct{}

func (v ExecAuth) Description(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (api_version, command) are provided"
}

func (v ExecAuth) MarkdownDescription(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (`api_version`, `command`) are provided"
}

func (v ExecAuth) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// Get cluster_connection attribute
	var conn types.Object
	diags := req.Config.GetAttribute(ctx, path.Root("cluster_connection"), &conn)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If connection is unknown or null, skip validation
	if conn.IsUnknown() || conn.IsNull() {
		return
	}

	// Convert to connection model to access exec field
	connModel, err := auth.ObjectToConnectionModel(ctx, conn)
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
				path.Root("cluster_connection").AtName("exec"),
				"Invalid Exec Authentication Configuration",
				err.Error(),
			)
		}
	}
}
