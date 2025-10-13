// internal/k8sconnect/common/validators/jsonpath.go
package validators

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
