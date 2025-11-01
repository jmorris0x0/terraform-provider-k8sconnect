package patch

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// Ensure the resource implements the UpgradeState interface
var _ resource.ResourceWithUpgradeState = (*patchResource)(nil)

// UpgradeState implements resource.ResourceWithUpgradeState
func (r *patchResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		// State upgrade from v0 to v2 - no managed_fields to Map managed_fields
		0: {
			PriorSchema:   getPatchSchemaV0(),
			StateUpgrader: upgradePatchStateV0,
		},
		// State upgrade from v1 to v2 - String managed_fields to Map managed_fields
		1: {
			PriorSchema:   getPatchSchemaV1(),
			StateUpgrader: upgradePatchStateV1,
		},
	}
}

// getPatchSchemaV0 returns the schema for v0 (no managed_fields)
func getPatchSchemaV0() *schema.Schema {
	return &schema.Schema{
		Version: 0,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"target": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"api_version": schema.StringAttribute{
						Required: true,
					},
					"kind": schema.StringAttribute{
						Required: true,
					},
					"name": schema.StringAttribute{
						Required: true,
					},
					"namespace": schema.StringAttribute{
						Optional: true,
					},
				},
			},
			"patch": schema.StringAttribute{
				Required: true,
			},
			"json_patch": schema.StringAttribute{
				Optional: true,
			},
			"merge_patch": schema.StringAttribute{
				Optional: true,
			},
			"cluster": schema.SingleNestedAttribute{
				Required:   true,
				Attributes: auth.GetConnectionSchemaForResource(),
			},
			"managed_state_projection": schema.MapAttribute{
				Computed:    true,
				ElementType: types.StringType,
			},
		},
	}
}

// getPatchSchemaV1 returns the schema for v1 (managed_fields as String)
func getPatchSchemaV1() *schema.Schema {
	return &schema.Schema{
		Version: 1,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"target": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"api_version": schema.StringAttribute{
						Required: true,
					},
					"kind": schema.StringAttribute{
						Required: true,
					},
					"name": schema.StringAttribute{
						Required: true,
					},
					"namespace": schema.StringAttribute{
						Optional: true,
					},
				},
			},
			"patch": schema.StringAttribute{
				Required: true,
			},
			"json_patch": schema.StringAttribute{
				Optional: true,
			},
			"merge_patch": schema.StringAttribute{
				Optional: true,
			},
			"cluster": schema.SingleNestedAttribute{
				Required:   true,
				Attributes: auth.GetConnectionSchemaForResource(),
			},
			"managed_state_projection": schema.MapAttribute{
				Computed:    true,
				ElementType: types.StringType,
			},
			"managed_fields": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

// upgradePatchStateV0 upgrades from v0 (no managed_fields) to v2 (Map managed_fields)
func upgradePatchStateV0(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
	type patchResourceModelV0 struct {
		ID                     types.String `tfsdk:"id"`
		Target                 types.Object `tfsdk:"target"`
		Patch                  types.String `tfsdk:"patch"`
		JSONPatch              types.String `tfsdk:"json_patch"`
		MergePatch             types.String `tfsdk:"merge_patch"`
		Cluster                types.Object `tfsdk:"cluster"`
		ManagedStateProjection types.Map    `tfsdk:"managed_state_projection"`
	}

	var dataV0 patchResourceModelV0
	resp.Diagnostics.Append(req.State.Get(ctx, &dataV0)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create upgraded state with managed_fields as Map
	upgradedData := patchResourceModel{
		ID:                     dataV0.ID,
		Target:                 dataV0.Target,
		Patch:                  dataV0.Patch,
		JSONPatch:              dataV0.JSONPatch,
		MergePatch:             dataV0.MergePatch,
		Cluster:                dataV0.Cluster,
		ManagedStateProjection: dataV0.ManagedStateProjection,
		ManagedFields:          types.MapNull(types.StringType), // Add as null Map
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, upgradedData)...)
}

// upgradePatchStateV1 upgrades from v1 (String managed_fields) to v2 (Map managed_fields)
func upgradePatchStateV1(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
	type patchResourceModelV1 struct {
		ID                     types.String `tfsdk:"id"`
		Target                 types.Object `tfsdk:"target"`
		Patch                  types.String `tfsdk:"patch"`
		JSONPatch              types.String `tfsdk:"json_patch"`
		MergePatch             types.String `tfsdk:"merge_patch"`
		Cluster                types.Object `tfsdk:"cluster"`
		ManagedStateProjection types.Map    `tfsdk:"managed_state_projection"`
		ManagedFields          types.String `tfsdk:"managed_fields"` // Was String in v1
	}

	var dataV1 patchResourceModelV1
	resp.Diagnostics.Append(req.State.Get(ctx, &dataV1)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create upgraded state with managed_fields as Map (discard old String value)
	// The old String was raw JSON FieldsV1 format - we don't migrate it, just set to null
	// On next read, it will be repopulated with the new flattened format
	upgradedData := patchResourceModel{
		ID:                     dataV1.ID,
		Target:                 dataV1.Target,
		Patch:                  dataV1.Patch,
		JSONPatch:              dataV1.JSONPatch,
		MergePatch:             dataV1.MergePatch,
		Cluster:                dataV1.Cluster,
		ManagedStateProjection: dataV1.ManagedStateProjection,
		ManagedFields:          types.MapNull(types.StringType), // Convert from String to null Map
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, upgradedData)...)
}
