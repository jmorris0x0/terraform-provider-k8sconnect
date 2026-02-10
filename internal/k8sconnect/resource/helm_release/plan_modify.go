package helm_release

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// ModifyPlan validates configuration at plan time, catching errors before apply.
func (r *helmReleaseResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// No validation needed on destroy
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan helmReleaseResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Validate timeout format
	if !plan.Timeout.IsNull() && !plan.Timeout.IsUnknown() {
		timeoutStr := plan.Timeout.ValueString()
		if _, err := time.ParseDuration(timeoutStr); err != nil {
			resp.Diagnostics.AddError(
				"Invalid Timeout Format",
				fmt.Sprintf("Could not parse timeout '%s'. Use Go duration format: '300s', '5m', '1h'.", timeoutStr),
			)
		}
	}

	// Validate local chart path exists (only when chart value is known)
	if !plan.Chart.IsUnknown() {
		chartRef := plan.Chart.ValueString()
		if isLocalChart(chartRef) {
			absPath, err := filepath.Abs(chartRef)
			if err == nil {
				if _, statErr := os.Stat(filepath.Join(absPath, "Chart.yaml")); statErr != nil {
					resp.Diagnostics.AddError(
						"Chart Not Found",
						fmt.Sprintf("Local chart path '%s' does not contain a Chart.yaml file.\n\n"+
							"Resolved path: %s\n"+
							"Verify the chart path is correct and contains a valid Helm chart.",
							chartRef, absPath),
					)
				}
			}
		}
	}

	// Validate OCI charts have a version specified
	if !plan.Repository.IsNull() && !plan.Repository.IsUnknown() {
		repo := plan.Repository.ValueString()
		if strings.HasPrefix(repo, "oci://") {
			if plan.Version.IsNull() || plan.Version.ValueString() == "" {
				resp.Diagnostics.AddError(
					"Version Required for OCI Registry",
					fmt.Sprintf("OCI registry '%s' requires an explicit version. "+
						"Add: version = \"1.0.0\"", repo),
				)
			}
		}
	}

	// Validate registry_config_path exists if specified
	if !plan.RegistryConfigPath.IsNull() && !plan.RegistryConfigPath.IsUnknown() {
		configPath := plan.RegistryConfigPath.ValueString()
		if _, err := os.Stat(configPath); err != nil {
			resp.Diagnostics.AddError(
				"Registry Config Not Found",
				fmt.Sprintf("Registry config file '%s' does not exist.\n\n"+
					"If using the default Docker config, omit registry_config_path entirely.",
					configPath),
			)
		}
	}
}
