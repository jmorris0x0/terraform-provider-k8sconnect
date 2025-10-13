// internal/k8sconnect/resource/manifest/validators.go
package manifest

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/jsonpath"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/validation"
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
	return "Ensures exactly one cluster connection mode is specified: inline (host + cluster_ca_certificate or insecure) or kubeconfig"
}

func (v *clusterConnectionValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (`host` + `cluster_ca_certificate` or `insecure`), `kubeconfig`"
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

	// Use common validation logic
	err := auth.ValidateConnectionWithUnknowns(ctx, connModel)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Invalid Cluster Connection Configuration",
			err.Error(),
		)
	}
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

// =============================================================================
// execAuthValidator ensures complete exec configuration when present
// =============================================================================

type execAuthValidator struct{}

func (v *execAuthValidator) Description(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (api_version, command, args) are provided"
}

func (v *execAuthValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (`api_version`, `command`) are provided"
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
		true &&
		connModel.Kubeconfig.IsNull() &&
		connModel.Token.IsNull() &&
		connModel.ClientCertificate.IsNull() &&
		connModel.ClientKey.IsNull() &&
		connModel.ProxyURL.IsNull() &&
		connModel.Exec == nil
}

type jsonPathValidator struct{}

func (v jsonPathValidator) Description(ctx context.Context) string {
	return "validates JSONPath syntax"
}

func (v jsonPathValidator) MarkdownDescription(ctx context.Context) string {
	return "validates JSONPath syntax"
}

func (v jsonPathValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	fieldPath := req.ConfigValue.ValueString()
	jp := jsonpath.New("validator")
	if err := jp.Parse(fmt.Sprintf("{.%s}", fieldPath)); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid JSONPath Syntax",
			fmt.Sprintf("The field path '%s' is not valid JSONPath: %s", fieldPath, err),
		)
	}
}

// =============================================================================
// ignoreFieldsValidator blocks attempts to ignore provider internal annotations
// =============================================================================

type ignoreFieldsValidator struct{}

func (v ignoreFieldsValidator) Description(ctx context.Context) string {
	return "validates that ignore_fields does not include provider internal annotations"
}

func (v ignoreFieldsValidator) MarkdownDescription(ctx context.Context) string {
	return "validates that `ignore_fields` does not include provider internal annotations"
}

func (v ignoreFieldsValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	fieldPath := req.ConfigValue.ValueString()

	// Block any path under our internal annotation namespace
	annotationPrefix := "metadata.annotations." + validation.ProviderAnnotationPrefix
	if strings.HasPrefix(fieldPath, annotationPrefix) {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Cannot ignore provider internal annotations",
			fmt.Sprintf("Field path '%s' is used internally for resource tracking and cannot be ignored.\n\n"+
				"Provider internal annotations (metadata.annotations.k8sconnect.terraform.io/*) are required for:\n"+
				"• Resource deletion tracking\n"+
				"• State management\n"+
				"• Lifecycle operations\n\n"+
				"Remove this field from ignore_fields to proceed.", fieldPath),
		)
		return
	}

	// Also validate JSONPath syntax while we're here
	jp := jsonpath.New("validator")
	if err := jp.Parse(fmt.Sprintf("{.%s}", fieldPath)); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Field Path Syntax",
			fmt.Sprintf("The field path '%s' is not valid: %s\n\n"+
				"Field paths should use dot notation like:\n"+
				"• 'spec.replicas'\n"+
				"• 'metadata.annotations.example.com/key'\n"+
				"• 'data.key1'", fieldPath, err),
		)
	}
}

// =============================================================================
// serverManagedFieldsValidator blocks server-managed fields and provider internal annotations
// =============================================================================

type serverManagedFieldsValidator struct{}

func (v serverManagedFieldsValidator) Description(ctx context.Context) string {
	return "validates that yaml_body does not contain server-managed fields or provider internal annotations"
}

func (v serverManagedFieldsValidator) MarkdownDescription(ctx context.Context) string {
	return "validates that `yaml_body` does not contain server-managed fields or provider internal annotations"
}

func (v serverManagedFieldsValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	yamlStr := req.ConfigValue.ValueString()

	// Skip validation if YAML contains interpolations (will be resolved during apply)
	if validation.ContainsInterpolation(yamlStr) {
		return
	}

	// Parse the YAML to an unstructured object
	obj := &unstructured.Unstructured{}
	if err := sigsyaml.Unmarshal([]byte(yamlStr), obj); err != nil {
		// Don't validate if we can't parse - that's handled by other validators
		return
	}

	// Check for provider internal annotations
	if hasAnnotations, key := validation.HasProviderAnnotations(obj); hasAnnotations {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Provider internal annotations not allowed in yaml_body",
			fmt.Sprintf("The annotation '%s' is used internally by the provider and should not be included in yaml_body.\n\n"+
				"Provider internal annotations (k8sconnect.terraform.io/*) are automatically added by the provider for:\n"+
				"• Resource deletion tracking\n"+
				"• State management\n"+
				"• Lifecycle operations\n\n"+
				validation.CopyPasteHint+
				"Please remove all k8sconnect.terraform.io/* annotations from your YAML.", key),
		)
		return
	}

	// Check for server-managed metadata fields
	if hasFields, field := validation.HasServerManagedFields(obj); hasFields {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Server-managed fields not allowed in yaml_body",
			fmt.Sprintf("The field 'metadata.%s' is managed by the Kubernetes API server and should not be included in yaml_body.\n\n"+
				"Server-managed fields are:\n"+
				"• uid\n"+
				"• resourceVersion\n"+
				"• generation\n"+
				"• creationTimestamp\n"+
				"• managedFields\n\n"+
				validation.CopyPasteHintYAML+
				"Please remove server-managed fields from your YAML.", field),
		)
		return
	}

	// Check for status field
	if validation.HasStatusField(obj) {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Server-managed fields not allowed in yaml_body",
			"The 'status' field is a read-only subresource managed by the Kubernetes API server and should not be included in yaml_body.\n\n"+
				"The status field is automatically populated by controllers and should only be read via the 'status' computed attribute.\n\n"+
				validation.CopyPasteHintYAML+
				"Please remove the status field from your YAML.",
		)
	}
}
