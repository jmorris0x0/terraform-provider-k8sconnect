package object

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure interface is implemented
var _ resource.ResourceWithUpgradeState = (*objectResource)(nil)

// UpgradeState handles state migration from older schema versions
func (r *objectResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		// State version 0 -> 1: v0.1.7 -> v0.2.0
		// Breaking changes:
		// - Renamed cluster_connection to cluster
		// - Removed managed_fields (moved to private state)
		0: {
			PriorSchema: nil, // Framework will use raw state
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				tflog.Info(ctx, "Upgrading k8sconnect_object state from v0.1.7 to v0.2.0")

				// Get the raw state as a map by unmarshaling JSON
				var rawState map[string]interface{}
				if err := json.Unmarshal(req.RawState.JSON, &rawState); err != nil {
					resp.Diagnostics.AddError(
						"Unable to Unmarshal Prior State",
						err.Error(),
					)
					return
				}

				// 1. Rename cluster_connection -> cluster
				if clusterConn, ok := rawState["cluster_connection"]; ok {
					rawState["cluster"] = clusterConn
					delete(rawState, "cluster_connection")
					tflog.Debug(ctx, "Migrated cluster_connection to cluster")
				}

				// 2. Remove managed_fields (now tracked in private state)
				if _, ok := rawState["managed_fields"]; ok {
					delete(rawState, "managed_fields")
					tflog.Debug(ctx, "Removed managed_fields from state (now in private state)")
				}

				// 3. object_ref.namespace: null -> "default" fix (from BUG #1)
				// This happens automatically via normal state refresh, but we can document it
				if objRef, ok := rawState["object_ref"].(map[string]interface{}); ok {
					if ns, exists := objRef["namespace"]; exists && ns == nil {
						// Don't modify here - let normal plan/apply refresh handle it
						// Just log that we detected the old null value
						tflog.Debug(ctx, "Detected null object_ref.namespace - will be corrected to 'default' on next refresh")
					}
				}

				// Re-marshal the modified state back to JSON
				upgradedJSON, err := json.Marshal(rawState)
				if err != nil {
					resp.Diagnostics.AddError(
						"Unable to Marshal Upgraded State",
						err.Error(),
					)
					return
				}

				// Set the upgraded state using DynamicValue
				resp.DynamicValue = &tfprotov6.DynamicValue{
					JSON: upgradedJSON,
				}
			},
		},
	}
}
