// internal/k8sconnect/resource/patch/validators.go
package patch

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// ConfigValidators implements resource.ResourceWithConfigValidators
func (r *patchResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		&patchClusterConnectionValidator{},
		&patchExecAuthValidator{},
	}
}

// =============================================================================
// patchClusterConnectionValidator ensures exactly one connection mode is specified
// =============================================================================

type patchClusterConnectionValidator struct{}

func (v *patchClusterConnectionValidator) Description(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (host + cluster_ca_certificate or insecure) or kubeconfig"
}

func (v *patchClusterConnectionValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures exactly one cluster connection mode is specified: inline (`host` + `cluster_ca_certificate` or `insecure`), `kubeconfig`"
}

func (v *patchClusterConnectionValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data patchResourceModel
	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip validation for unknown connections (during planning)
	if data.ClusterConnection.IsUnknown() {
		return
	}

	// Check connection exists
	if data.ClusterConnection.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Missing Cluster Connection Configuration",
			"cluster_connection block is required.",
		)
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		// Unknown values during planning - skip validation
		return
	}

	// Use common validation logic
	err = auth.ValidateConnectionWithUnknowns(ctx, connModel)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("cluster_connection"),
			"Invalid Cluster Connection Configuration",
			err.Error(),
		)
	}
}

// =============================================================================
// patchExecAuthValidator ensures complete exec configuration when present
// =============================================================================

type patchExecAuthValidator struct{}

func (v *patchExecAuthValidator) Description(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (api_version, command) are provided"
}

func (v *patchExecAuthValidator) MarkdownDescription(ctx context.Context) string {
	return "Ensures that if exec auth is specified, all required fields (`api_version`, `command`) are provided"
}

func (v *patchExecAuthValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data patchResourceModel
	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If connection is unknown or null, skip validation
	if data.ClusterConnection.IsUnknown() || data.ClusterConnection.IsNull() {
		return
	}

	// Convert to connection model
	connModel, err := auth.ObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		// Unknown values during planning
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

// isManagedByThisState checks if a resource is managed by k8sconnect_manifest
// This is the critical safety mechanism to prevent self-patching
func (r *patchResource) isManagedByThisState(ctx context.Context, obj *unstructured.Unstructured) bool {
	// Check 1: Does it have our ownership annotation?
	annotations := obj.GetAnnotations()
	if annotations != nil {
		// Check for k8sconnect manifest ownership annotation
		if ownedBy, exists := annotations["k8sconnect.terraform.io/terraform-id"]; exists {
			tflog.Debug(ctx, "Resource has k8sconnect ownership annotation",
				map[string]interface{}{"terraform_id": ownedBy})
			return true
		}

		// Also check the old annotation format for backward compatibility
		if ownedBy, exists := annotations["k8sconnect.io/owned-by"]; exists {
			tflog.Debug(ctx, "Resource has k8sconnect legacy ownership annotation",
				map[string]interface{}{"owner": ownedBy})
			return true
		}
	}

	// Check 2: Is field manager name a manifest manager (not patch manager)?
	for _, mf := range obj.GetManagedFields() {
		manager := mf.Manager
		// k8sconnect manifest uses "k8sconnect" as field manager
		// k8sconnect_patch uses "k8sconnect-patch-{id}" as field manager
		if manager == "k8sconnect" || (strings.HasPrefix(manager, "k8sconnect") && !strings.Contains(manager, "patch")) {
			tflog.Debug(ctx, "Resource managed by k8sconnect_manifest",
				map[string]interface{}{"manager": manager})
			return true
		}
	}

	return false
}

// determinePatchType returns the patch type based on which field is set
func (r *patchResource) determinePatchType(data patchResourceModel) string {
	if !data.Patch.IsNull() && data.Patch.ValueString() != "" {
		return "application/strategic-merge-patch+json"
	}
	if !data.JSONPatch.IsNull() && data.JSONPatch.ValueString() != "" {
		return "application/json-patch+json"
	}
	if !data.MergePatch.IsNull() && data.MergePatch.ValueString() != "" {
		return "application/merge-patch+json"
	}
	return "application/strategic-merge-patch+json" // Default
}

// getPatchContent returns the patch content based on which field is set
func (r *patchResource) getPatchContent(data patchResourceModel) string {
	if !data.Patch.IsNull() && data.Patch.ValueString() != "" {
		return data.Patch.ValueString()
	}
	if !data.JSONPatch.IsNull() && data.JSONPatch.ValueString() != "" {
		return data.JSONPatch.ValueString()
	}
	if !data.MergePatch.IsNull() && data.MergePatch.ValueString() != "" {
		return data.MergePatch.ValueString()
	}
	return ""
}

// targetsEqual checks if two targets are the same
func targetsEqual(t1, t2 types.Object) bool {
	if t1.IsNull() != t2.IsNull() {
		return false
	}
	if t1.IsNull() {
		return true
	}

	var target1, target2 patchTargetModel
	t1.As(context.Background(), &target1, basetypes.ObjectAsOptions{})
	t2.As(context.Background(), &target2, basetypes.ObjectAsOptions{})

	return target1.APIVersion.Equal(target2.APIVersion) &&
		target1.Kind.Equal(target2.Kind) &&
		target1.Name.Equal(target2.Name) &&
		target1.Namespace.Equal(target2.Namespace)
}

// formatTarget returns a human-readable string for a target
func formatTarget(target patchTargetModel) string {
	if target.Namespace.IsNull() || target.Namespace.ValueString() == "" {
		return fmt.Sprintf("%s %s/%s",
			target.APIVersion.ValueString(),
			target.Kind.ValueString(),
			target.Name.ValueString())
	}
	return fmt.Sprintf("%s %s/%s (namespace: %s)",
		target.APIVersion.ValueString(),
		target.Kind.ValueString(),
		target.Name.ValueString(),
		target.Namespace.ValueString())
}
