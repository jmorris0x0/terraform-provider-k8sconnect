// internal/k8sconnect/resource/wait/update_status.go
package wait

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
)

// updateStatus populates the status field after a successful wait
// Following ADR-008: "You only get what you wait for"
// Only wait_for.field populates status
func (r *waitResource) updateStatus(ctx context.Context, wc *waitContext) error {
	// Only field waits populate status
	if wc.WaitConfig.Field.IsNull() || wc.WaitConfig.Field.ValueString() == "" {
		wc.Data.Status = types.DynamicNull()
		tflog.Debug(ctx, "Not populating status - not a field wait")
		return nil
	}

	// Get current state from cluster
	currentObj, err := wc.Client.Get(ctx, wc.GVR, wc.ObjectRef.Namespace.ValueString(), wc.ObjectRef.Name.ValueString())
	if err != nil {
		tflog.Warn(ctx, "Failed to read after wait", map[string]interface{}{"error": err.Error()})
		wc.Data.Status = types.DynamicNull()
		return nil
	}

	// Extract and prune status
	if statusRaw, found, _ := unstructured.NestedMap(currentObj.Object, "status"); found && len(statusRaw) > 0 {
		// PRUNE to only the waited-for field
		prunedStatus := pruneStatusToField(statusRaw, wc.WaitConfig.Field.ValueString())

		if prunedStatus != nil {
			tflog.Debug(ctx, "Pruned status to waited field", map[string]interface{}{
				"field": wc.WaitConfig.Field.ValueString(),
			})

			statusValue, err := common.ConvertToAttrValue(ctx, prunedStatus)
			if err != nil {
				tflog.Warn(ctx, "Failed to convert pruned status", map[string]interface{}{"error": err.Error()})
				wc.Data.Status = types.DynamicNull()
			} else {
				// Success! Set the actual value
				wc.Data.Status = types.DynamicValue(statusValue)
			}
		} else {
			tflog.Debug(ctx, "Field not found in status", map[string]interface{}{
				"field": wc.WaitConfig.Field.ValueString(),
			})
			wc.Data.Status = types.DynamicNull()
		}
	} else {
		// No status in K8s resource
		tflog.Debug(ctx, "No status in K8s resource")
		wc.Data.Status = types.DynamicNull()
	}

	return nil
}
