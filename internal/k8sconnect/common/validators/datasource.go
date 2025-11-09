package validators

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ExactlyOneOfThree validates that exactly one of three string attributes is non-empty
// This is a datasource-level validator for mutual exclusivity among three options
type ExactlyOneOfThree struct {
	Attribute1 string
	Attribute2 string
	Attribute3 string
}

// Description returns a plain text description of the validator's behavior
func (v ExactlyOneOfThree) Description(ctx context.Context) string {
	return fmt.Sprintf("validates that exactly one of '%s', '%s', or '%s' is specified", v.Attribute1, v.Attribute2, v.Attribute3)
}

// MarkdownDescription returns a markdown formatted description of the validator's behavior
func (v ExactlyOneOfThree) MarkdownDescription(ctx context.Context) string {
	return fmt.Sprintf("validates that exactly one of `%s`, `%s`, or `%s` is specified", v.Attribute1, v.Attribute2, v.Attribute3)
}

// ValidateDataSource validates that exactly one of the three attributes is non-empty
func (v ExactlyOneOfThree) ValidateDataSource(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	// Get the three attribute values
	var attr1 types.String
	var attr2 types.String
	var attr3 types.String

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

	diags = req.Config.GetAttribute(ctx, path.Root(v.Attribute3), &attr3)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if each attribute is set (non-null and non-unknown)
	// Note: We don't check for empty strings here - that's validated in LoadDocuments()
	// where we can provide more specific error messages
	hasAttr1 := !attr1.IsNull() && !attr1.IsUnknown()
	hasAttr2 := !attr2.IsNull() && !attr2.IsUnknown()
	hasAttr3 := !attr3.IsNull() && !attr3.IsUnknown()

	// Count how many are set
	count := 0
	if hasAttr1 {
		count++
	}
	if hasAttr2 {
		count++
	}
	if hasAttr3 {
		count++
	}

	if count > 1 {
		resp.Diagnostics.AddError(
			"Conflicting Configuration",
			fmt.Sprintf("Exactly one of '%s', '%s', or '%s' must be specified, not multiple.", v.Attribute1, v.Attribute2, v.Attribute3),
		)
		return
	}

	if count == 0 {
		resp.Diagnostics.AddError(
			"Missing Configuration",
			fmt.Sprintf("Exactly one of '%s', '%s', or '%s' must be specified.", v.Attribute1, v.Attribute2, v.Attribute3),
		)
		return
	}
}
