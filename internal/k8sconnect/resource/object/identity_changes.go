// internal/k8sconnect/resource/object/identity_changes.go
package object

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// IdentityChange represents a change to a Kubernetes resource identity field
type IdentityChange struct {
	Field    string
	OldValue string
	NewValue string
}

// checkResourceIdentityChanges detects changes to Kubernetes resource identity fields
// and marks the resource for replacement if any identity field changes.
// Returns true if replacement is required.
//
// This function implements ADR-010 to prevent orphan resources when users change
// resource identity in their YAML configuration.
func (r *objectResource) checkResourceIdentityChanges(
	ctx context.Context,
	req resource.ModifyPlanRequest,
	plannedData *objectResourceModel,
	resp *resource.ModifyPlanResponse,
) bool {
	// Get current state
	var stateData objectResourceModel
	diags := req.State.Get(ctx, &stateData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return false
	}

	// Parse state YAML (what we last applied)
	stateObj, err := r.parseYAML(stateData.YAMLBody.ValueString())
	if err != nil {
		// Can't compare - let Update handle the error
		tflog.Warn(ctx, "Failed to parse state YAML for identity check",
			map[string]interface{}{"error": err.Error()})
		return false
	}

	// Parse plan YAML (what user wants to apply now)
	planObj, err := r.parseYAML(plannedData.YAMLBody.ValueString())
	if err != nil {
		// Invalid YAML in plan - will be caught by validators
		// Don't trigger replacement for invalid YAML
		return false
	}

	// Detect identity changes
	identityChanges := r.detectIdentityChanges(stateObj, planObj)

	if len(identityChanges) > 0 {
		// Build detailed message about what changed
		changeDetails := make([]string, 0, len(identityChanges))
		for _, change := range identityChanges {
			changeDetails = append(changeDetails,
				fmt.Sprintf("  %s: %q → %q", change.Field, change.OldValue, change.NewValue))
		}

		tflog.Info(ctx, "Resource identity changed, triggering replacement",
			map[string]interface{}{
				"changes": identityChanges,
				"old":     formatResourceIdentity(stateObj),
				"new":     formatResourceIdentity(planObj),
			})

		// Mark yaml_body for replacement
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("yaml_body"))

		// Add informative warning diagnostic
		resp.Diagnostics.AddWarning(
			"Resource Identity Changed - Replacement Required",
			fmt.Sprintf("The following Kubernetes resource identity fields have changed:\n%s\n\n"+
				"Terraform will delete the old resource and create a new one.\n\n"+
				"Old: %s\n"+
				"New: %s",
				strings.Join(changeDetails, "\n"),
				formatResourceIdentity(stateObj),
				formatResourceIdentity(planObj)),
		)

		return true
	}

	return false
}

// detectIdentityChanges compares identity fields between state and plan objects.
// Returns a list of changes found.
func (r *objectResource) detectIdentityChanges(stateObj, planObj *unstructured.Unstructured) []IdentityChange {
	var changes []IdentityChange

	// Check Kind
	if stateObj.GetKind() != planObj.GetKind() {
		changes = append(changes, IdentityChange{
			Field:    "kind",
			OldValue: stateObj.GetKind(),
			NewValue: planObj.GetKind(),
		})
	}

	// Check apiVersion
	if stateObj.GetAPIVersion() != planObj.GetAPIVersion() {
		changes = append(changes, IdentityChange{
			Field:    "apiVersion",
			OldValue: stateObj.GetAPIVersion(),
			NewValue: planObj.GetAPIVersion(),
		})
	}

	// Check metadata.name
	if stateObj.GetName() != planObj.GetName() {
		changes = append(changes, IdentityChange{
			Field:    "metadata.name",
			OldValue: stateObj.GetName(),
			NewValue: planObj.GetName(),
		})
	}

	// Check metadata.namespace
	// NOTE: Both cluster-scoped and namespaced resources are handled correctly.
	// For cluster-scoped resources, both GetNamespace() calls return "" (no change detected).
	if stateObj.GetNamespace() != planObj.GetNamespace() {
		changes = append(changes, IdentityChange{
			Field:    "metadata.namespace",
			OldValue: stateObj.GetNamespace(),
			NewValue: planObj.GetNamespace(),
		})
	}

	return changes
}

// formatResourceIdentity creates a human-readable resource identity string
// for displaying in diagnostic messages.
func formatResourceIdentity(obj *unstructured.Unstructured) string {
	if obj.GetNamespace() != "" {
		return fmt.Sprintf("%s/%s %s/%s",
			obj.GetAPIVersion(), obj.GetKind(),
			obj.GetNamespace(), obj.GetName())
	}
	// Cluster-scoped resource
	return fmt.Sprintf("%s/%s %s",
		obj.GetAPIVersion(), obj.GetKind(),
		obj.GetName())
}
