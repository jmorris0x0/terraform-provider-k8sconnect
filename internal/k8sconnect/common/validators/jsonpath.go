package validators

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/client-go/util/jsonpath"
)

// JSONPath validates that a string field contains valid JSONPath syntax
type JSONPath struct{}

// Description returns a plain text description of the validator's behavior
func (v JSONPath) Description(ctx context.Context) string {
	return "validates JSONPath syntax"
}

// MarkdownDescription returns a markdown formatted description of the validator's behavior
func (v JSONPath) MarkdownDescription(ctx context.Context) string {
	return "validates JSONPath syntax"
}

// ValidateString validates that the provided string is valid JSONPath
func (v JSONPath) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	// Skip validation for null or unknown values
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

// JSONPathMapKeys validates that all keys in a map are valid JSONPath expressions
type JSONPathMapKeys struct{}

// Description returns a plain text description of the validator's behavior
func (v JSONPathMapKeys) Description(ctx context.Context) string {
	return "validates that all map keys are valid JSONPath expressions"
}

// MarkdownDescription returns a markdown formatted description of the validator's behavior
func (v JSONPathMapKeys) MarkdownDescription(ctx context.Context) string {
	return "validates that all map keys are valid JSONPath expressions"
}

// ValidateMap validates that all map keys are valid JSONPath expressions
func (v JSONPathMapKeys) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	// Skip validation for null or unknown values
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	elements := req.ConfigValue.Elements()
	for key := range elements {
		jp := jsonpath.New("validator")
		if err := jp.Parse(fmt.Sprintf("{.%s}", key)); err != nil {
			resp.Diagnostics.AddAttributeError(
				req.Path.AtMapKey(key),
				"Invalid JSONPath",
				fmt.Sprintf("The key '%s' is not a valid JSONPath: %s", key, err),
			)
		}
	}
}

// ExactlyOneOf validates that exactly one of two string attributes is non-empty
// This is a datasource-level validator for mutual exclusivity
type ExactlyOneOf struct {
	Attribute1 string
	Attribute2 string
}

// Description returns a plain text description of the validator's behavior
func (v ExactlyOneOf) Description(ctx context.Context) string {
	return fmt.Sprintf("validates that exactly one of '%s' or '%s' is specified", v.Attribute1, v.Attribute2)
}

// MarkdownDescription returns a markdown formatted description of the validator's behavior
func (v ExactlyOneOf) MarkdownDescription(ctx context.Context) string {
	return fmt.Sprintf("validates that exactly one of `%s` or `%s` is specified", v.Attribute1, v.Attribute2)
}

// ValidateDataSource validates that exactly one of the two attributes is non-empty
func (v ExactlyOneOf) ValidateDataSource(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	// Get the two attribute values
	var attr1 types.String
	var attr2 types.String

	diags := req.Config.GetAttribute(ctx, path.Root(v.Attribute1), &attr1)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	diags = req.Config.GetAttribute(ctx, path.Root(v.Attribute2), &attr2)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if both attributes are set (non-null and non-empty)
	hasAttr1 := !attr1.IsNull() && !attr1.IsUnknown() && attr1.ValueString() != ""
	hasAttr2 := !attr2.IsNull() && !attr2.IsUnknown() && attr2.ValueString() != ""

	if hasAttr1 && hasAttr2 {
		resp.Diagnostics.AddError(
			"Conflicting Configuration",
			fmt.Sprintf("Exactly one of '%s' or '%s' must be specified, not both.", v.Attribute1, v.Attribute2),
		)
		return
	}

	if !hasAttr1 && !hasAttr2 {
		resp.Diagnostics.AddError(
			"Missing Configuration",
			fmt.Sprintf("Either '%s' or '%s' must be specified.", v.Attribute1, v.Attribute2),
		)
		return
	}
}
