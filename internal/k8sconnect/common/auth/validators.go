// internal/k8sconnect/common/auth/validators.go
package auth

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"gopkg.in/yaml.v3"
)

// urlValidator validates that a string is a valid URL
type urlValidator struct{}

func (v urlValidator) Description(ctx context.Context) string {
	return "validates that the value is a valid URL"
}

func (v urlValidator) MarkdownDescription(ctx context.Context) string {
	return "validates that the value is a valid URL"
}

func (v urlValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return // Skip validation for unknown/null values
	}

	value := req.ConfigValue.ValueString()
	parsedURL, err := url.Parse(value)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid URL",
			fmt.Sprintf("The value '%s' is not a valid URL: %s", value, err),
		)
		return
	}

	// Ensure it has a scheme (http/https)
	if parsedURL.Scheme == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid URL",
			"URL must include a scheme (e.g., https://)",
		)
	}
}

// kubeconfigValidator validates that a string is valid YAML
type kubeconfigValidator struct{}

func (v kubeconfigValidator) Description(ctx context.Context) string {
	return "validates that the kubeconfig is valid YAML"
}

func (v kubeconfigValidator) MarkdownDescription(ctx context.Context) string {
	return "validates that the kubeconfig is valid YAML"
}

func (v kubeconfigValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return // Skip validation for unknown/null values
	}

	value := req.ConfigValue.ValueString()
	var data interface{}
	if err := yaml.Unmarshal([]byte(value), &data); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Kubeconfig",
			fmt.Sprintf("The kubeconfig is not valid YAML: %s", err),
		)
	}
}
