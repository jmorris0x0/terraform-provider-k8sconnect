package object

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

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/validation"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/validators"
)

// ConfigValidators implements resource-level validation for the object resource
func (r *objectResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		&validators.ClusterConnection{},
		&validators.ExecAuth{},
		&conflictingAttributesValidator{},
		&requiredFieldsValidator{},
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
	var data objectResourceModel

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
	var data objectResourceModel

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
		r := &objectResource{}
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
	r := &objectResource{}
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
