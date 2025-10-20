package wait

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
)

// updateStatus populates the result field after a successful wait
// Following ADR-008: "You only get what you wait for"
// Only wait_for.field populates result (extracted resource fields)
func (r *waitResource) updateStatus(ctx context.Context, wc *waitContext) error {
	// Only field waits populate result
	if wc.WaitConfig.Field.IsNull() || wc.WaitConfig.Field.ValueString() == "" {
		wc.Data.Result = types.DynamicNull()
		tflog.Debug(ctx, "Not populating result - not a field wait")
		return nil
	}

	// Get current state from cluster
	currentObj, err := wc.Client.Get(ctx, wc.GVR, wc.ObjectRef.Namespace.ValueString(), wc.ObjectRef.Name.ValueString())
	if err != nil {
		tflog.Warn(ctx, "Failed to read after wait", map[string]interface{}{"error": err.Error()})
		wc.Data.Result = types.DynamicNull()
		return nil
	}

	// Extract and prune fields from the ENTIRE resource (not just .status object)
	// The waited-for field can be anywhere: spec.volumeName, metadata.uid, status.phase, etc.
	// The full path structure is preserved in the wait resource's result attribute:
	//   field="spec.volumeName" → wait.result.spec.volumeName
	//   field="metadata.uid" → wait.result.metadata.uid
	//   field="status.succeeded" → wait.result.status.succeeded
	prunedResource := pruneStatusToField(currentObj.Object, wc.WaitConfig.Field.ValueString())

	if prunedResource != nil {
		tflog.Debug(ctx, "Extracted waited field from resource", map[string]interface{}{
			"field": wc.WaitConfig.Field.ValueString(),
		})

		objectValue, err := common.ConvertToAttrValue(ctx, prunedResource)
		if err != nil {
			tflog.Warn(ctx, "Failed to convert extracted value", map[string]interface{}{"error": err.Error()})
			wc.Data.Result = types.DynamicNull()
		} else {
			// Success! Set the actual value
			wc.Data.Result = types.DynamicValue(objectValue)
		}
	} else {
		tflog.Debug(ctx, "Field not found in resource", map[string]interface{}{
			"field": wc.WaitConfig.Field.ValueString(),
		})
		wc.Data.Result = types.DynamicNull()
	}

	return nil
}
