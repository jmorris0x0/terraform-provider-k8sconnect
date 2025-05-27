// internal/k8sinline/resource/manifest/validators.go
package manifest

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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

	// Check for inline mode (host + cluster_ca_certificate)
	// A field is "specified" if it's not null (even if unknown during planning)
	hasInline := !conn.Host.IsNull() || !conn.ClusterCACertificate.IsNull()

	// Check for kubeconfig modes
	hasFile := !conn.KubeconfigFile.IsNull()
	hasRaw := !conn.KubeconfigRaw.IsNull()

	// Count active modes
	modeCount := 0
	activeModes := []string{}

	if hasInline {
		modeCount++
		activeModes = append(activeModes, "inline")

		// For inline mode, both host AND cluster_ca_certificate are required
		// Only validate this if we can actually check the values (not unknown)
		if !conn.Host.IsUnknown() && !conn.ClusterCACertificate.IsUnknown() {
			if conn.Host.IsNull() && !conn.ClusterCACertificate.IsNull() {
				resp.Diagnostics.AddAttributeError(
					path.Root("cluster_connection").AtName("host"),
					"Missing Required Field for Inline Connection",
					"When using inline connection mode, both 'host' and 'cluster_ca_certificate' are required.",
				)
			}

			if conn.ClusterCACertificate.IsNull() && !conn.Host.IsNull() {
				resp.Diagnostics.AddAttributeError(
					path.Root("cluster_connection").AtName("cluster_ca_certificate"),
					"Missing Required Field for Inline Connection",
					"When using inline connection mode, both 'host' and 'cluster_ca_certificate' are required.",
				)
			}
		}
	}

	if hasFile {
		modeCount++
		activeModes = append(activeModes, "kubeconfig_file")
	}

	if hasRaw {
		modeCount++
		activeModes = append(activeModes, "kubeconfig_raw")
	}

	// Validate exactly one mode is specified
	if modeCount == 0 {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Missing Cluster Connection Configuration",
			"Exactly one cluster connection mode must be specified:\n\n"+
				"• **Inline mode**: Set both 'host' and 'cluster_ca_certificate'\n"+
				"• **Kubeconfig file**: Set 'kubeconfig_file'\n"+
				"• **Kubeconfig raw**: Set 'kubeconfig_raw'",
		)
	} else if modeCount > 1 {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Multiple Cluster Connection Modes Specified",
			fmt.Sprintf("Only one cluster connection mode can be specified, but found %d: %v\n\n"+
				"Choose exactly one:\n"+
				"• **Inline mode**: Set both 'host' and 'cluster_ca_certificate' (remove kubeconfig settings)\n"+
				"• **Kubeconfig file**: Set 'kubeconfig_file' (remove inline and raw kubeconfig settings)\n"+
				"• **Kubeconfig raw**: Set 'kubeconfig_raw' (remove inline and file kubeconfig settings)",
				modeCount, activeModes),
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

	exec := data.ClusterConnection.Exec
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
func isClusterConnectionEmpty(conn ClusterConnectionModel) bool {
	return conn.Host.IsNull() &&
		conn.ClusterCACertificate.IsNull() &&
		conn.KubeconfigFile.IsNull() &&
		conn.KubeconfigRaw.IsNull() &&
		conn.Exec == nil
}
