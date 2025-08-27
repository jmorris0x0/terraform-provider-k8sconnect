// internal/k8sconnect/resource/manifest/validators.go
package manifest

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

func (v *clusterConnectionValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data manifestResourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn := data.ClusterConnection

	// If connection is unknown (during planning), skip ALL validation
	// The connection will be validated again during apply when values are known
	if conn.IsUnknown() {
		return
	}

	// If connection is null, that's an error
	if conn.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Missing Cluster Connection Configuration",
			"cluster_connection block is required.",
		)
		return
	}

	// Convert to connection model to access fields
	r := &manifestResource{} // Create a temporary resource instance for the helper method
	connModel, err := r.convertObjectToConnectionModel(ctx, conn)
	if err != nil {
		// If conversion fails, it might be due to unknown values, which is okay during planning
		// Don't report validation errors for unknown values - they'll be validated at apply time
		return
	}

	// Check for inline mode (host-based)
	hasInline := !connModel.Host.IsNull()

	// Check for kubeconfig modes
	hasFile := !connModel.KubeconfigFile.IsNull()
	hasRaw := !connModel.KubeconfigRaw.IsNull()

	// Count active modes
	modeCount := 0
	activeModes := []string{}

	if hasInline {
		modeCount++
		activeModes = append(activeModes, "inline")
	}

	if hasFile {
		modeCount++
		activeModes = append(activeModes, "kubeconfig_file")
	}

	if hasRaw {
		modeCount++
		activeModes = append(activeModes, "kubeconfig_raw")
	}

	// Only validate mode count if we have enough information
	// If any fields are unknown, we can't make definitive statements about mode count
	hasUnknownFields := connModel.Host.IsUnknown() ||
		connModel.ClusterCACertificate.IsUnknown() ||
		connModel.KubeconfigFile.IsUnknown() ||
		connModel.KubeconfigRaw.IsUnknown()

	if hasUnknownFields {
		// Skip mode validation when we have unknown values
		return
	}

	// Validate exactly one mode is specified (only when all values are known)
	if modeCount == 0 {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Missing Cluster Connection Configuration",
			"Exactly one cluster connection mode must be specified:\n\n"+
				"• **Inline mode**: Set 'host' with authentication\n"+
				"• **Kubeconfig file**: Set 'kubeconfig_file'\n"+
				"• **Kubeconfig raw**: Set 'kubeconfig_raw'",
		)
	} else if modeCount > 1 {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Multiple Cluster Connection Modes Specified",
			fmt.Sprintf("Only one cluster connection mode can be specified, but found %d: %v\n\n"+
				"Choose exactly one:\n"+
				"• **Inline mode**: Set 'host' with authentication (remove kubeconfig settings)\n"+
				"• **Kubeconfig file**: Set 'kubeconfig_file' (remove inline and raw kubeconfig settings)\n"+
				"• **Kubeconfig raw**: Set 'kubeconfig_raw' (remove inline and file kubeconfig settings)",
				modeCount, activeModes),
		)
	}

	// Additional validation for inline mode
	if hasInline && modeCount == 1 {
		// Validate CA cert or insecure for inline mode
		if connModel.ClusterCACertificate.IsNull() && (connModel.Insecure.IsNull() || !connModel.Insecure.ValueBool()) {
			resp.Diagnostics.AddAttributeError(
				path.Root("cluster_connection"),
				"Missing TLS Configuration",
				"Inline connections require either 'cluster_ca_certificate' or 'insecure = true'.",
			)
		}

		// Validate authentication is provided
		hasAuth := !connModel.Token.IsNull() ||
			(!connModel.ClientCertificate.IsNull() && !connModel.ClientKey.IsNull()) ||
			(connModel.Exec != nil && !connModel.Exec.APIVersion.IsNull())

		if !hasAuth {
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

	// Validate client certificate and key are provided together
	if (!connModel.ClientCertificate.IsNull() && connModel.ClientKey.IsNull()) ||
		(connModel.ClientCertificate.IsNull() && !connModel.ClientKey.IsNull()) {
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
	} else if !data.YAMLBody.IsUnknown() && data.YAMLBody.ValueString() == "" {
		// Only check for empty string if the value is known
		resp.Diagnostics.AddAttributeError(
			path.Root("yaml_body"),
			"Empty YAML Content",
			"'yaml_body' cannot be empty. It must contain a valid single-document Kubernetes YAML manifest.",
		)
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
