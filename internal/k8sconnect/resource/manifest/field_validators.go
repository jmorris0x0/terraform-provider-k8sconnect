// internal/k8sconnect/resource/manifest/field_validators.go
package manifest

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/util/jsonpath"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/validation"
)

// durationValidator validates that a string is a valid duration
type durationValidator struct{}

func (v durationValidator) Description(ctx context.Context) string {
	return "validates that the value is a valid duration"
}

func (v durationValidator) MarkdownDescription(ctx context.Context) string {
	return "validates that the value is a valid Go duration (e.g., '30s', '5m', '1h')"
}

func (v durationValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return // Skip validation for unknown/null values
	}

	value := req.ConfigValue.ValueString()
	_, err := time.ParseDuration(value)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Duration",
			fmt.Sprintf("The value '%s' is not a valid duration: %s. Use format like '30s', '5m', '1h'", value, err),
		)
	}
}

// yamlValidator validates that a string is valid YAML
type yamlValidator struct {
	singleDoc bool // If true, ensure it's a single document
}

func (v yamlValidator) Description(ctx context.Context) string {
	return "validates that the value is valid YAML"
}

func (v yamlValidator) MarkdownDescription(ctx context.Context) string {
	if v.singleDoc {
		return "validates that the value is valid single-document YAML"
	}
	return "validates that the value is valid YAML"
}

func (v yamlValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	value := req.ConfigValue.ValueString()

	// Skip validation if YAML contains interpolations (will be resolved during apply)
	if validation.ContainsInterpolation(value) {
		return
	}

	// Validate YAML syntax
	var data interface{}
	if err := yaml.Unmarshal([]byte(value), &data); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid YAML",
			fmt.Sprintf("The value is not valid YAML: %s", err),
		)
	}
}

// jsonPathMapKeysValidator validates that all keys in a map are valid JSONPath expressions
type jsonPathMapKeysValidator struct{}

func (v jsonPathMapKeysValidator) Description(ctx context.Context) string {
	return "validates that all map keys are valid JSONPath expressions"
}

func (v jsonPathMapKeysValidator) MarkdownDescription(ctx context.Context) string {
	return "validates that all map keys are valid JSONPath expressions"
}

func (v jsonPathMapKeysValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return // Skip validation for unknown/null values
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
