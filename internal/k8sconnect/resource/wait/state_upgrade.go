package wait

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure interface is implemented
var _ resource.ResourceWithUpgradeState = (*waitResource)(nil)

// UpgradeState handles state migration from older schema versions
func (r *waitResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		// State version 0 -> 1: v0.1.7 -> v0.2.0
		// Breaking changes:
		// - Renamed cluster_connection to cluster
		0: {
			PriorSchema: nil, // Framework will use raw state
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				tflog.Info(ctx, "Upgrading k8sconnect_wait state from v0.1.7 to v0.2.0")

				// Get the raw state as a map by unmarshaling JSON
				var rawState map[string]interface{}
				if err := json.Unmarshal(req.RawState.JSON, &rawState); err != nil {
					resp.Diagnostics.AddError(
						"Unable to Unmarshal Prior State",
						err.Error(),
					)
					return
				}

				// Rename cluster_connection -> cluster
				if clusterConn, ok := rawState["cluster_connection"]; ok {
					rawState["cluster"] = clusterConn
					delete(rawState, "cluster_connection")
					tflog.Debug(ctx, "Migrated cluster_connection to cluster")
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
