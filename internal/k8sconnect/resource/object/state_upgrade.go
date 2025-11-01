package object

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// Ensure the resource implements the UpgradeState interface
var _ resource.ResourceWithUpgradeState = (*objectResource)(nil)

// UpgradeState implements resource.ResourceWithUpgradeState
func (r *objectResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		// State upgrade from v0 to v2 - add managed_fields
		0: {
			PriorSchema:   getObjectSchemaV1(),
			StateUpgrader: upgradeObjectState,
		},
		// State upgrade from v1 to v2 - add managed_fields
		1: {
			PriorSchema:   getObjectSchemaV1(),
			StateUpgrader: upgradeObjectState,
		},
	}
}

// getObjectSchemaV1 returns the schema for v0/v1 (without managed_fields)
func getObjectSchemaV1() *schema.Schema {
	return &schema.Schema{
		Version: 1,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"yaml_body": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					yamlValidator{singleDoc: true},
					serverManagedFieldsValidator{},
				},
			},
			"cluster": schema.SingleNestedAttribute{
				Required:   true,
				Attributes: auth.GetConnectionSchemaForResource(),
			},
			"delete_protection": schema.BoolAttribute{
				Optional: true,
			},
			"delete_timeout": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					durationValidator{},
				},
			},
			"force_destroy": schema.BoolAttribute{
				Optional: true,
			},
			"ignore_fields": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.ValueStringsAre(ignoreFieldsValidator{}),
				},
			},
			"managed_state_projection": schema.MapAttribute{
				Computed:    true,
				ElementType: types.StringType,
			},
			"object_ref": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"api_version": schema.StringAttribute{
						Computed: true,
					},
					"kind": schema.StringAttribute{
						Computed: true,
					},
					"name": schema.StringAttribute{
						Computed: true,
					},
					"namespace": schema.StringAttribute{
						Computed: true,
					},
				},
			},
		},
	}
}

// upgradeObjectState upgrades state from v0/v1 to v2 by adding managed_fields
func upgradeObjectState(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
	// Define a struct matching v1 schema (without managed_fields)
	type objectResourceModelV1 struct {
		ID                     types.String `tfsdk:"id"`
		YAMLBody               types.String `tfsdk:"yaml_body"`
		Cluster                types.Object `tfsdk:"cluster"`
		DeleteProtection       types.Bool   `tfsdk:"delete_protection"`
		DeleteTimeout          types.String `tfsdk:"delete_timeout"`
		ForceDestroy           types.Bool   `tfsdk:"force_destroy"`
		IgnoreFields           types.List   `tfsdk:"ignore_fields"`
		ManagedStateProjection types.Map    `tfsdk:"managed_state_projection"`
		ObjectRef              types.Object `tfsdk:"object_ref"`
	}

	var dataV1 objectResourceModelV1
	resp.Diagnostics.Append(req.State.Get(ctx, &dataV1)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create upgraded state with managed_fields added
	upgradedData := objectResourceModel{
		ID:                     dataV1.ID,
		YAMLBody:               dataV1.YAMLBody,
		Cluster:                dataV1.Cluster,
		DeleteProtection:       dataV1.DeleteProtection,
		DeleteTimeout:          dataV1.DeleteTimeout,
		ForceDestroy:           dataV1.ForceDestroy,
		IgnoreFields:           dataV1.IgnoreFields,
		ManagedStateProjection: dataV1.ManagedStateProjection,
		ObjectRef:              dataV1.ObjectRef,
		ManagedFields:          types.MapNull(types.StringType), // Add managed_fields as null
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, upgradedData)...)
}
