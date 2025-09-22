// internal/k8sconnect/resource/manifest/validators.go
package manifest

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// ConfigValidators implements resource-level validation for the manifest resource
func (r *manifestResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		&clusterConnectionValidator{},
		&execAuthValidator{},
		&conflictingAttributesValidator{},
		&requiredFieldsValidator{},
	}
}

// =============================================================================
// clusterConnectionValidator ensures exactly one connection mode is specified
// =============================================================================

type clusterConnectionValidator struct{}

func (v *clusterConnectionValidator) Description(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (host + cluster_ca_certificate), kubeconfig_file, or kubeconfig_raw"
}

func (v *clusterConnectionValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (`host` + `cluster_ca_certificate`), `kubeconfig_file`, or `kubeconfig_raw`"
}

// ValidateResource ensures exactly one connection mode is specified
func (v *clusterConnectionValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// Load and validate data
	data, ok := v.loadResourceData(ctx, req, resp)
	if !ok {
		return
	}

	// Skip validation for unknown connections (during planning)
	if data.ClusterConnection.IsUnknown() {
		return
	}

	// Check connection exists
	if !v.validateConnectionExists(data.ClusterConnection, resp) {
		return
	}

	// Convert to connection model
	connModel, ok := v.getConnectionModel(ctx, data.ClusterConnection)
	if !ok {
		return // Unknown values during planning
	}

	// Validate connection modes
	v.validateConnectionModes(connModel, resp)

	// Validate inline connection if applicable
	if v.hasInlineMode(connModel) {
		v.validateInlineConnection(connModel, resp)
	}

	// Validate client certificates
	v.validateClientCertificates(connModel, resp)
}

// loadResourceData loads the manifest resource data from config
func (v *clusterConnectionValidator) loadResourceData(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) (manifestResourceModel, bool) {
	var data manifestResourceModel
	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	return data, !resp.Diagnostics.HasError()
}

// validateConnectionExists checks if connection block is present
func (v *clusterConnectionValidator) validateConnectionExists(conn types.Object, resp *resource.ValidateConfigResponse) bool {
	if conn.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Missing Cluster Connection Configuration",
			"cluster_connection block is required.",
		)
		return false
	}
	return true
}

// getConnectionModel safely converts connection object to model
func (v *clusterConnectionValidator) getConnectionModel(ctx context.Context, conn types.Object) (auth.ClusterConnectionModel, bool) {
	r := &manifestResource{}
	connModel, err := r.convertObjectToConnectionModel(ctx, conn)
	if err != nil {
		// Unknown values during planning - skip validation
		return connModel, false
	}
	return connModel, true
}

// validateConnectionModes ensures exactly one connection mode is specified
func (v *clusterConnectionValidator) validateConnectionModes(connModel auth.ClusterConnectionModel, resp *resource.ValidateConfigResponse) {
	modes := v.countActiveModes(connModel)

	if modes == 0 {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"No Connection Mode Specified",
			"Must specify exactly one connection mode:\n"+
				"• Inline: Provide 'host' and 'cluster_ca_certificate'\n"+
				"• Kubeconfig file: Provide 'kubeconfig_file' path\n"+
				"• Kubeconfig raw: Provide 'kubeconfig_raw' content",
		)
	} else if modes > 1 {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Multiple Connection Modes Specified",
			v.buildMultipleModeError(connModel),
		)
	}
}

// countActiveModes counts how many connection modes are configured
func (v *clusterConnectionValidator) countActiveModes(connModel auth.ClusterConnectionModel) int {
	modes := 0
	if v.hasInlineMode(connModel) {
		modes++
	}
	if !connModel.KubeconfigFile.IsNull() {
		modes++
	}
	if !connModel.KubeconfigRaw.IsNull() {
		modes++
	}
	return modes
}

// hasInlineMode checks if inline connection fields are present
func (v *clusterConnectionValidator) hasInlineMode(connModel auth.ClusterConnectionModel) bool {
	return !connModel.Host.IsNull() || !connModel.ClusterCACertificate.IsNull()
}

// buildMultipleModeError creates error message for multiple modes
func (v *clusterConnectionValidator) buildMultipleModeError(connModel auth.ClusterConnectionModel) string {
	conflictingModes := []string{}
	if v.hasInlineMode(connModel) {
		conflictingModes = append(conflictingModes, "inline (host + cluster_ca_certificate)")
	}
	if !connModel.KubeconfigFile.IsNull() {
		conflictingModes = append(conflictingModes, "kubeconfig_file")
	}
	if !connModel.KubeconfigRaw.IsNull() {
		conflictingModes = append(conflictingModes, "kubeconfig_raw")
	}

	return fmt.Sprintf("Only one connection mode can be specified. Found: %v\n\n"+
		"Choose ONE of:\n"+
		"• Remove 'kubeconfig_file' and 'kubeconfig_raw' to use inline mode\n"+
		"• Remove inline fields ('host', 'cluster_ca_certificate') to use kubeconfig",
		conflictingModes)
}

// validateInlineConnection ensures inline connection has required fields
func (v *clusterConnectionValidator) validateInlineConnection(connModel auth.ClusterConnectionModel, resp *resource.ValidateConfigResponse) {
	// Check both host and CA are present
	if !v.hasCompleteInlineConfig(connModel) {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Incomplete Inline Connection",
			"Inline connections require both 'host' and 'cluster_ca_certificate'.",
		)
		return
	}

	// Validate authentication is provided
	if !v.hasAuthentication(connModel) {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Missing Authentication",
			"Inline connections require at least one authentication method:\n"+
				"• Bearer token: Set 'token'\n"+
				"• Client certificates: Set both 'client_certificate' and 'client_key'\n"+
				"• Exec auth: Configure the 'exec' block",
		)
	}
}

// hasCompleteInlineConfig checks if inline config has both required fields
func (v *clusterConnectionValidator) hasCompleteInlineConfig(connModel auth.ClusterConnectionModel) bool {
	return !connModel.Host.IsNull() && !connModel.ClusterCACertificate.IsNull()
}

// hasAuthentication checks if any authentication method is configured
func (v *clusterConnectionValidator) hasAuthentication(connModel auth.ClusterConnectionModel) bool {
	return !connModel.Token.IsNull() ||
		v.hasClientCertAuth(connModel) ||
		v.hasExecAuth(connModel)
}

// hasClientCertAuth checks if client certificate authentication is configured
func (v *clusterConnectionValidator) hasClientCertAuth(connModel auth.ClusterConnectionModel) bool {
	return !connModel.ClientCertificate.IsNull() && !connModel.ClientKey.IsNull()
}

// hasExecAuth checks if exec authentication is configured
func (v *clusterConnectionValidator) hasExecAuth(connModel auth.ClusterConnectionModel) bool {
	return connModel.Exec != nil && !connModel.Exec.APIVersion.IsNull()
}

// validateClientCertificates ensures cert and key are provided together
func (v *clusterConnectionValidator) validateClientCertificates(connModel auth.ClusterConnectionModel, resp *resource.ValidateConfigResponse) {
	hasCert := !connModel.ClientCertificate.IsNull()
	hasKey := !connModel.ClientKey.IsNull()

	if hasCert != hasKey {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Incomplete Client Certificate Configuration",
			"Both 'client_certificate' and 'client_key' must be provided together for client certificate authentication.",
		)
	}
}

// =============================================================================
// execAuthValidator ensures complete exec configuration when present
// =============================================================================

type execAuthValidator struct{}

func (v *execAuthValidator) Description(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (api_version, command, args) are provided"
}

func (v *execAuthValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (`api_version`, `command`, `args`) are provided"
}

func (v *execAuthValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data manifestResourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn := data.ClusterConnection

	// If connection is unknown or null, skip validation
	if conn.IsUnknown() || conn.IsNull() {
		return
	}

	// Convert to connection model to access exec field
	r := &manifestResource{} // Create a temporary resource instance for the helper method
	connModel, err := r.convertObjectToConnectionModel(ctx, conn)
	if err != nil {
		// If conversion fails, it might be due to unknown values during planning
		return
	}

	exec := connModel.Exec
	if exec == nil {
		return // No exec config, nothing to validate
	}

	// Only validate exec fields if they're not unknown (during planning they might be)
	// We can't meaningfully validate unknown values
	if exec.APIVersion.IsUnknown() || exec.Command.IsUnknown() {
		return // Skip validation during planning when values are unknown
	}

	// Check that all required exec fields are present
	missingFields := []string{}

	if exec.APIVersion.IsNull() {
		missingFields = append(missingFields, "api_version")
	}

	if exec.Command.IsNull() {
		missingFields = append(missingFields, "command")
	}

	if len(exec.Args) == 0 {
		missingFields = append(missingFields, "args")
	}

	if len(missingFields) > 0 {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection").AtName("exec"),
			"Incomplete Exec Authentication Configuration",
			fmt.Sprintf("When using exec authentication, all fields are required. Missing: %v\n\n"+
				"Complete exec configuration requires:\n"+
				"• **api_version**: Authentication API version (e.g., 'client.authentication.k8s.io/v1')\n"+
				"• **command**: Executable command (e.g., 'aws', 'gcloud')\n"+
				"• **args**: Command arguments (e.g., ['eks', 'get-token', '--cluster-name', 'my-cluster'])",
				missingFields),
		)
	}
}

// =============================================================================
// conflictingAttributesValidator prevents conflicting attribute combinations
// =============================================================================

type conflictingAttributesValidator struct{}

func (v *conflictingAttributesValidator) Description(ctx context.Context) string {
	return "Ensures conflicting attributes are not set together (e.g., delete_protection and force_destroy)"
}

func (v *conflictingAttributesValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures conflicting attributes are not set together (e.g., `delete_protection` and `force_destroy`)"
}

func (v *conflictingAttributesValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data manifestResourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip validation if values are unknown (during planning)
	if data.DeleteProtection.IsUnknown() || data.ForceDestroy.IsUnknown() {
		return
	}

	// Check delete_protection and force_destroy conflict
	deleteProtection := !data.DeleteProtection.IsNull() && data.DeleteProtection.ValueBool()
	forceDestroy := !data.ForceDestroy.IsNull() && data.ForceDestroy.ValueBool()

	if deleteProtection && forceDestroy {
		resp.Diagnostics.AddAttributeError(
			path.Root("delete_protection"),
			"Conflicting Deletion Settings",
			"'delete_protection = true' and 'force_destroy = true' cannot be set together.\n\n"+
				"These options serve opposite purposes:\n"+
				"• **delete_protection**: Prevents accidental deletion by blocking destroy operations\n"+
				"• **force_destroy**: Forces deletion by removing finalizers and bypassing safety mechanisms\n\n"+
				"Choose one approach:\n"+
				"• Set 'delete_protection = true' to protect critical resources\n"+
				"• Set 'force_destroy = true' to enable aggressive deletion for stuck resources\n"+
				"• Leave both unset (or false) for normal deletion behavior",
		)
	}
}

// =============================================================================
// requiredFieldsValidator ensures essential fields are present
// =============================================================================

type requiredFieldsValidator struct{}

func (v *requiredFieldsValidator) Description(ctx context.Context) string {
	return "Ensures required fields yaml_body and cluster_connection are specified"
}

func (v *requiredFieldsValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures required fields `yaml_body` and `cluster_connection` are specified"
}

func (v *requiredFieldsValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data manifestResourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check yaml_body is present and not empty (only if not unknown)
	if data.YAMLBody.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("yaml_body"),
			"Missing Required Field",
			"'yaml_body' is required and must contain valid Kubernetes YAML manifest content.",
		)
		return
	}

	if !data.YAMLBody.IsUnknown() && data.YAMLBody.ValueString() != "" {
		yamlStr := data.YAMLBody.ValueString()

		// If YAML contains interpolations, skip ALL validation
		// These will be resolved during apply phase
		if strings.Contains(yamlStr, "${") {
			fmt.Printf("DEBUG requiredFieldsValidator: Skipping YAML validation due to interpolation syntax\n")
			return
		}

		// No interpolations - validate the YAML (includes multi-doc check)
		r := &manifestResource{}
		_, err := r.parseYAML(yamlStr)
		if err != nil {
			resp.Diagnostics.AddAttributeError(
				path.Root("yaml_body"),
				"Invalid YAML",
				fmt.Sprintf("Failed to parse YAML: %s", err),
			)
			return
		}
	}

	// Note: cluster_connection validation is handled by clusterConnectionValidator
	// We just check that the block exists at all
	if isClusterConnectionEmpty(data.ClusterConnection) {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Missing Required Configuration Block",
			"'cluster_connection' block is required. It must specify how to connect to your Kubernetes cluster.",
		)
	}
}

// Helper function to check if cluster connection is completely empty
func isClusterConnectionEmpty(conn types.Object) bool {
	// If connection is unknown during planning, it's NOT empty - just not ready yet
	if conn.IsUnknown() {
		return false // Unknown != empty, it means values will be available later
	}

	// If connection is null, it's definitely empty
	if conn.IsNull() {
		return true
	}

	// Try to convert to model to check if all fields are null
	r := &manifestResource{}
	connModel, err := r.convertObjectToConnectionModel(context.Background(), conn)
	if err != nil {
		// If conversion fails but object is not null/unknown, assume it has values
		// This handles cases where partial unknown values prevent conversion
		return false
	}

	return connModel.Host.IsNull() &&
		connModel.ClusterCACertificate.IsNull() &&
		connModel.KubeconfigFile.IsNull() &&
		connModel.KubeconfigRaw.IsNull() &&
		connModel.Token.IsNull() &&
		connModel.ClientCertificate.IsNull() &&
		connModel.ClientKey.IsNull() &&
		connModel.ProxyURL.IsNull() &&
		connModel.Exec == nil
}
