package validators

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/validation"
)

// StrategicMergePatch validates strategic merge patch content for common issues
type StrategicMergePatch struct{}

func (v StrategicMergePatch) Description(ctx context.Context) string {
	return "validates strategic merge patch content for container names, server-managed fields, and provider annotations"
}

func (v StrategicMergePatch) MarkdownDescription(ctx context.Context) string {
	return "validates strategic merge patch content for container names, server-managed fields, and provider annotations"
}

func (v StrategicMergePatch) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	patchContent := req.ConfigValue.ValueString()

	// Skip validation if patch contains interpolations (will be resolved during apply)
	if validation.ContainsInterpolation(patchContent) {
		return
	}

	// Parse patch as unstructured (accepts both YAML and JSON)
	obj := &unstructured.Unstructured{}
	if err := sigsyaml.Unmarshal([]byte(patchContent), obj); err != nil {
		// Don't validate if we can't parse - let Kubernetes handle parse errors
		return
	}

	// Validate container names (critical for strategic merge)
	if err := validation.ValidateContainerNames(obj); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Strategic Merge Patch",
			fmt.Sprintf("Container names are required for strategic merge patches.\n\n"+
				"Error: %s\n\n"+
				"Strategic merge patches use container names as merge keys. "+
				"Without names, Kubernetes cannot determine which container to update, "+
				"and the patch may fail silently or produce unexpected results.\n\n"+
				"Example of correct container specification:\n"+
				"spec:\n"+
				"  template:\n"+
				"    spec:\n"+
				"      containers:\n"+
				"      - name: nginx  # <-- name is required\n"+
				"        image: nginx:1.21", err),
		)
		return
	}

	// Check for server-managed metadata fields
	if hasFields, field := validation.HasServerManagedFields(obj); hasFields {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Server-Managed Field in Patch",
			fmt.Sprintf("The field 'metadata.%s' is managed by the Kubernetes API server and cannot be patched.\n\n"+
				"Server-managed fields are:\n"+
				"• uid\n"+
				"• resourceVersion\n"+
				"• generation\n"+
				"• creationTimestamp\n"+
				"• managedFields\n\n"+
				"These fields are automatically set by Kubernetes and attempting to patch them will result in an error.\n\n"+
				validation.CopyPasteHintYAML+
				"Please remove server-managed fields from your patch content.", field),
		)
		return
	}

	// Check for provider internal annotations
	if hasAnnotations, key := validation.HasProviderAnnotations(obj); hasAnnotations {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Provider Internal Annotation in Patch",
			fmt.Sprintf("The annotation '%s' is used internally by the provider and should not be patched.\n\n"+
				"Provider internal annotations (k8sconnect.terraform.io/*) are used for:\n"+
				"• Resource tracking\n"+
				"• State management\n"+
				"• Lifecycle operations\n\n"+
				"Attempting to patch these annotations could interfere with the provider's operation.\n\n"+
				validation.CopyPasteHint+
				"Please remove k8sconnect.terraform.io/* annotations from your patch content.", key),
		)
		return
	}

	// Check for status field
	if validation.HasStatusField(obj) {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Status Field in Patch",
			"The 'status' field is a read-only subresource and cannot be patched through this resource.\n\n"+
				"The status field is managed by Kubernetes controllers and can only be updated via the status subresource API.\n\n"+
				"If you need to wait for specific status conditions, use the 'wait_for' attribute instead:\n\n"+
				"wait_for = {\n"+
				"  field = \"status.conditions\"\n"+
				"  timeout = \"5m\"\n"+
				"}\n\n"+
				validation.CopyPasteHintYAML+
				"Please remove the status field from your patch content.",
		)
		return
	}
}

// JSONPatchValidator validates JSON Patch (RFC 6902) operations
type JSONPatchValidator struct{}

func (v JSONPatchValidator) Description(ctx context.Context) string {
	return "validates JSON Patch (RFC 6902) structure and operations"
}

func (v JSONPatchValidator) MarkdownDescription(ctx context.Context) string {
	return "validates JSON Patch (RFC 6902) structure and operations"
}

func (v JSONPatchValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	patchContent := req.ConfigValue.ValueString()

	// Skip validation if patch contains interpolations
	if validation.ContainsInterpolation(patchContent) {
		return
	}

	// JSON Patch must be a JSON array
	var operations []map[string]interface{}
	if err := json.Unmarshal([]byte(patchContent), &operations); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid JSON Patch",
			fmt.Sprintf("JSON Patch must be a valid JSON array of operations.\n\n"+
				"Parse error: %s\n\n"+
				"Example of valid JSON Patch:\n"+
				"[\n"+
				"  {\"op\": \"add\", \"path\": \"/metadata/labels/foo\", \"value\": \"bar\"},\n"+
				"  {\"op\": \"replace\", \"path\": \"/spec/replicas\", \"value\": 3}\n"+
				"]", err),
		)
		return
	}

	// Validate each operation
	validOps := map[string]bool{"add": true, "remove": true, "replace": true, "move": true, "copy": true, "test": true}
	for i, op := range operations {
		// Check required fields
		opType, hasOp := op["op"].(string)
		if !hasOp {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid JSON Patch Operation",
				fmt.Sprintf("Operation at index %d is missing required 'op' field.\n\n"+
					"Each operation must have an 'op' field with one of: add, remove, replace, move, copy, test", i),
			)
			return
		}

		// Validate op type
		if !validOps[opType] {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid JSON Patch Operation",
				fmt.Sprintf("Operation at index %d has invalid 'op' value: '%s'.\n\n"+
					"Valid operations are: add, remove, replace, move, copy, test", i, opType),
			)
			return
		}

		// Check path field
		path, hasPath := op["path"].(string)
		if !hasPath {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid JSON Patch Operation",
				fmt.Sprintf("Operation at index %d is missing required 'path' field.\n\n"+
					"All operations must have a 'path' field specifying the JSON Pointer to the target location.", i),
			)
			return
		}

		// Warn about server-managed fields
		if isServerManagedPath(path) {
			resp.Diagnostics.AddAttributeWarning(
				req.Path,
				"Patching Server-Managed Field",
				fmt.Sprintf("Operation at index %d attempts to modify a server-managed field: %s\n\n"+
					"This patch operation is likely to fail. Server-managed fields are automatically set by Kubernetes.\n\n"+
					"Consider removing this operation from your patch.", i, path),
			)
		}

		// Check value field for operations that require it
		if opType == "add" || opType == "replace" || opType == "test" {
			if _, hasValue := op["value"]; !hasValue {
				resp.Diagnostics.AddAttributeError(
					req.Path,
					"Invalid JSON Patch Operation",
					fmt.Sprintf("Operation '%s' at index %d is missing required 'value' field.", opType, i),
				)
				return
			}
		}

		// Check 'from' field for operations that require it
		if opType == "move" || opType == "copy" {
			if _, hasFrom := op["from"]; !hasFrom {
				resp.Diagnostics.AddAttributeError(
					req.Path,
					"Invalid JSON Patch Operation",
					fmt.Sprintf("Operation '%s' at index %d is missing required 'from' field.", opType, i),
				)
				return
			}
		}
	}
}

// MergePatchValidator validates JSON Merge Patch (RFC 7386) content
type MergePatchValidator struct{}

func (v MergePatchValidator) Description(ctx context.Context) string {
	return "validates JSON Merge Patch (RFC 7386) structure"
}

func (v MergePatchValidator) MarkdownDescription(ctx context.Context) string {
	return "validates JSON Merge Patch (RFC 7386) structure"
}

func (v MergePatchValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	patchContent := req.ConfigValue.ValueString()

	// Skip validation if patch contains interpolations
	if validation.ContainsInterpolation(patchContent) {
		return
	}

	// JSON Merge Patch must be a JSON object
	var patchObj map[string]interface{}
	if err := json.Unmarshal([]byte(patchContent), &patchObj); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid JSON Merge Patch",
			fmt.Sprintf("JSON Merge Patch must be a valid JSON object.\n\n"+
				"Parse error: %s\n\n"+
				"Example of valid JSON Merge Patch:\n"+
				"{\n"+
				"  \"metadata\": {\n"+
				"    \"labels\": {\n"+
				"      \"foo\": \"bar\"\n"+
				"    }\n"+
				"  }\n"+
				"}", err),
		)
		return
	}

	// Convert to unstructured for validation
	obj := &unstructured.Unstructured{Object: patchObj}

	// Check for server-managed metadata fields
	if hasFields, field := validation.HasServerManagedFields(obj); hasFields {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Server-Managed Field in Merge Patch",
			fmt.Sprintf("The field 'metadata.%s' is managed by the Kubernetes API server and cannot be patched.\n\n"+
				"Server-managed fields are:\n"+
				"• uid\n"+
				"• resourceVersion\n"+
				"• generation\n"+
				"• creationTimestamp\n"+
				"• managedFields\n\n"+
				validation.CopyPasteHintYAML+
				"Please remove server-managed fields from your merge patch content.", field),
		)
		return
	}

	// Check for provider internal annotations
	if hasAnnotations, key := validation.HasProviderAnnotations(obj); hasAnnotations {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Provider Internal Annotation in Merge Patch",
			fmt.Sprintf("The annotation '%s' is used internally by the provider and should not be patched.\n\n"+
				"Provider internal annotations (k8sconnect.terraform.io/*) are used for resource tracking and state management.\n\n"+
				validation.CopyPasteHint+
				"Please remove k8sconnect.terraform.io/* annotations from your merge patch content.", key),
		)
		return
	}

	// Check for status field
	if validation.HasStatusField(obj) {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Status Field in Merge Patch",
			"The 'status' field is a read-only subresource and cannot be patched.\n\n"+
				"Use the 'wait_for' attribute if you need to wait for specific status conditions.\n\n"+
				validation.CopyPasteHintYAML+
				"Please remove the status field from your merge patch content.",
		)
		return
	}
}

// isServerManagedPath checks if a JSON Pointer path targets a server-managed field
func isServerManagedPath(path string) bool {
	serverPaths := []string{
		"/metadata/uid",
		"/metadata/resourceVersion",
		"/metadata/generation",
		"/metadata/creationTimestamp",
		"/metadata/managedFields",
		"/status",
	}

	for _, serverPath := range serverPaths {
		if path == serverPath || len(path) > len(serverPath) && path[:len(serverPath)+1] == serverPath+"/" {
			return true
		}
	}

	return false
}
