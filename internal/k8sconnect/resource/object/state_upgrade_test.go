package object

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestUpgradeObjectStateV0toV2(t *testing.T) {
	tests := []struct {
		name          string
		v0State       map[string]tftypes.Value
		expectError   bool
		validateState func(t *testing.T, state objectResourceModel)
	}{
		{
			name: "basic v0 state upgrade",
			v0State: map[string]tftypes.Value{
				"id":        tftypes.NewValue(tftypes.String, "test-id-123"),
				"yaml_body": tftypes.NewValue(tftypes.String, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test\n"),
				"cluster": tftypes.NewValue(tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"host":                   tftypes.String,
						"client_certificate":     tftypes.String,
						"client_key":             tftypes.String,
						"cluster_ca_certificate": tftypes.String,
						"token":                  tftypes.String,

						"insecure":   tftypes.Bool,
						"kubeconfig": tftypes.String,
						"context":    tftypes.String,
						"proxy_url":  tftypes.String,
						"exec": tftypes.Object{
							AttributeTypes: map[string]tftypes.Type{
								"api_version": tftypes.String,
								"command":     tftypes.String,
								"args":        tftypes.List{ElementType: tftypes.String},
								"env":         tftypes.Map{ElementType: tftypes.String},
							},
						},
					},
				}, map[string]tftypes.Value{
					"host":                   tftypes.NewValue(tftypes.String, "https://127.0.0.1:6443"),
					"client_certificate":     tftypes.NewValue(tftypes.String, nil),
					"client_key":             tftypes.NewValue(tftypes.String, nil),
					"cluster_ca_certificate": tftypes.NewValue(tftypes.String, nil),
					"token":                  tftypes.NewValue(tftypes.String, "test-token"),

					"insecure":   tftypes.NewValue(tftypes.Bool, nil),
					"kubeconfig": tftypes.NewValue(tftypes.String, nil),
					"context":    tftypes.NewValue(tftypes.String, nil),
					"proxy_url":  tftypes.NewValue(tftypes.String, nil),
					"exec":       tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"api_version": tftypes.String, "command": tftypes.String, "args": tftypes.List{ElementType: tftypes.String}, "env": tftypes.Map{ElementType: tftypes.String}}}, nil),
				}),
				"delete_protection": tftypes.NewValue(tftypes.Bool, nil),
				"delete_timeout":    tftypes.NewValue(tftypes.String, nil),
				"force_destroy":     tftypes.NewValue(tftypes.Bool, nil),
				"ignore_fields":     tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, nil),
				"managed_state_projection": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
					"metadata.name": tftypes.NewValue(tftypes.String, "test"),
					"kind":          tftypes.NewValue(tftypes.String, "Namespace"),
				}),
				"object_ref": tftypes.NewValue(tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"api_version": tftypes.String,
						"kind":        tftypes.String,
						"name":        tftypes.String,
						"namespace":   tftypes.String,
					},
				}, map[string]tftypes.Value{
					"api_version": tftypes.NewValue(tftypes.String, "v1"),
					"kind":        tftypes.NewValue(tftypes.String, "Namespace"),
					"name":        tftypes.NewValue(tftypes.String, "test"),
					"namespace":   tftypes.NewValue(tftypes.String, nil),
				}),
			},
			validateState: func(t *testing.T, state objectResourceModel) {
				if state.ID.ValueString() != "test-id-123" {
					t.Errorf("Expected ID test-id-123, got %s", state.ID.ValueString())
				}
				if state.ManagedFields.IsNull() != true {
					t.Errorf("Expected ManagedFields to be null Map, got %v", state.ManagedFields)
				}
				if !state.ManagedFields.IsNull() && len(state.ManagedFields.Elements()) != 0 {
					t.Errorf("Expected ManagedFields to be null, got non-null with elements")
				}
			},
		},
		{
			name: "v0 state with null values",
			v0State: map[string]tftypes.Value{
				"id":        tftypes.NewValue(tftypes.String, "test-id-null"),
				"yaml_body": tftypes.NewValue(tftypes.String, "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test-pod\n"),
				"cluster": tftypes.NewValue(tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"host":                   tftypes.String,
						"client_certificate":     tftypes.String,
						"client_key":             tftypes.String,
						"cluster_ca_certificate": tftypes.String,
						"token":                  tftypes.String,

						"insecure":   tftypes.Bool,
						"kubeconfig": tftypes.String,
						"context":    tftypes.String,
						"proxy_url":  tftypes.String,
						"exec": tftypes.Object{
							AttributeTypes: map[string]tftypes.Type{
								"api_version": tftypes.String,
								"command":     tftypes.String,
								"args":        tftypes.List{ElementType: tftypes.String},
								"env":         tftypes.Map{ElementType: tftypes.String},
							},
						},
					},
				}, map[string]tftypes.Value{
					"host":                   tftypes.NewValue(tftypes.String, "https://127.0.0.1:6443"),
					"client_certificate":     tftypes.NewValue(tftypes.String, nil),
					"client_key":             tftypes.NewValue(tftypes.String, nil),
					"cluster_ca_certificate": tftypes.NewValue(tftypes.String, nil),
					"token":                  tftypes.NewValue(tftypes.String, nil),

					"insecure":   tftypes.NewValue(tftypes.Bool, nil),
					"kubeconfig": tftypes.NewValue(tftypes.String, nil),
					"context":    tftypes.NewValue(tftypes.String, nil),
					"proxy_url":  tftypes.NewValue(tftypes.String, nil),
					"exec":       tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"api_version": tftypes.String, "command": tftypes.String, "args": tftypes.List{ElementType: tftypes.String}, "env": tftypes.Map{ElementType: tftypes.String}}}, nil),
				}),
				"delete_protection":        tftypes.NewValue(tftypes.Bool, nil),
				"delete_timeout":           tftypes.NewValue(tftypes.String, nil),
				"force_destroy":            tftypes.NewValue(tftypes.Bool, nil),
				"ignore_fields":            tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, nil),
				"managed_state_projection": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil),
				"object_ref": tftypes.NewValue(tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"api_version": tftypes.String,
						"kind":        tftypes.String,
						"name":        tftypes.String,
						"namespace":   tftypes.String,
					},
				}, map[string]tftypes.Value{
					"api_version": tftypes.NewValue(tftypes.String, "v1"),
					"kind":        tftypes.NewValue(tftypes.String, "Pod"),
					"name":        tftypes.NewValue(tftypes.String, "test-pod"),
					"namespace":   tftypes.NewValue(tftypes.String, "default"),
				}),
			},
			validateState: func(t *testing.T, state objectResourceModel) {
				if state.ManagedFields.IsNull() != true {
					t.Errorf("Expected ManagedFields to be null")
				}
				if state.ManagedStateProjection.IsNull() != true {
					t.Errorf("Expected ManagedStateProjection to remain null")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create the raw state
			rawStateValue := tftypes.NewValue(
				tftypes.Object{AttributeTypes: map[string]tftypes.Type{
					"id":                       tftypes.String,
					"yaml_body":                tftypes.String,
					"cluster":                  tt.v0State["cluster"].Type(),
					"delete_protection":        tftypes.Bool,
					"delete_timeout":           tftypes.String,
					"force_destroy":            tftypes.Bool,
					"ignore_fields":            tftypes.List{ElementType: tftypes.String},
					"managed_state_projection": tftypes.Map{ElementType: tftypes.String},
					"object_ref":               tt.v0State["object_ref"].Type(),
				}},
				tt.v0State,
			)

			// Create request
			req := resource.UpgradeStateRequest{
				State: &tfsdk.State{
					Raw:    rawStateValue,
					Schema: *getObjectSchemaV1(),
				},
			}

			// Get current v2 schema
			var schemaResp resource.SchemaResponse
			(&objectResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)

			// Create response
			resp := &resource.UpgradeStateResponse{
				State: tfsdk.State{
					Schema: schemaResp.Schema,
				},
			}

			// Run the upgrader
			upgradeObjectState(ctx, req, resp)

			// Check for errors
			if tt.expectError && !resp.Diagnostics.HasError() {
				t.Fatal("Expected error but got none")
			}
			if !tt.expectError && resp.Diagnostics.HasError() {
				t.Fatalf("Unexpected error: %v", resp.Diagnostics)
			}

			if !tt.expectError {
				// Extract the upgraded state
				var upgradedState objectResourceModel
				diags := resp.State.Get(ctx, &upgradedState)
				if diags.HasError() {
					t.Fatalf("Failed to get upgraded state: %v", diags)
				}

				// Run custom validation
				tt.validateState(t, upgradedState)
			}
		})
	}
}

func TestUpgradeObjectStateV1toV2(t *testing.T) {
	// v1 is identical to v0 (no managed_fields in either)
	// So we just verify the same behavior
	t.Run("v1 to v2 upgrade identical to v0 to v2", func(t *testing.T) {
		ctx := context.Background()

		v1State := map[string]tftypes.Value{
			"id":        tftypes.NewValue(tftypes.String, "test-v1"),
			"yaml_body": tftypes.NewValue(tftypes.String, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n"),
			"cluster": tftypes.NewValue(tftypes.Object{
				AttributeTypes: map[string]tftypes.Type{
					"host":                   tftypes.String,
					"client_certificate":     tftypes.String,
					"client_key":             tftypes.String,
					"cluster_ca_certificate": tftypes.String,
					"token":                  tftypes.String,
					"insecure":               tftypes.Bool,
					"kubeconfig":             tftypes.String,
					"context":                tftypes.String,
					"proxy_url":              tftypes.String,
					"exec": tftypes.Object{
						AttributeTypes: map[string]tftypes.Type{
							"api_version": tftypes.String,
							"command":     tftypes.String,
							"args":        tftypes.List{ElementType: tftypes.String},
							"env":         tftypes.Map{ElementType: tftypes.String},
						},
					},
				},
			}, map[string]tftypes.Value{
				"host":                   tftypes.NewValue(tftypes.String, "https://127.0.0.1:6443"),
				"client_certificate":     tftypes.NewValue(tftypes.String, nil),
				"client_key":             tftypes.NewValue(tftypes.String, nil),
				"cluster_ca_certificate": tftypes.NewValue(tftypes.String, nil),
				"token":                  tftypes.NewValue(tftypes.String, "test-token"),
				"insecure":               tftypes.NewValue(tftypes.Bool, nil),
				"kubeconfig":             tftypes.NewValue(tftypes.String, nil),
				"context":                tftypes.NewValue(tftypes.String, nil),
				"proxy_url":              tftypes.NewValue(tftypes.String, nil),
				"exec":                   tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"api_version": tftypes.String, "command": tftypes.String, "args": tftypes.List{ElementType: tftypes.String}, "env": tftypes.Map{ElementType: tftypes.String}}}, nil),
			}),
			"delete_protection":        tftypes.NewValue(tftypes.Bool, nil),
			"delete_timeout":           tftypes.NewValue(tftypes.String, nil),
			"force_destroy":            tftypes.NewValue(tftypes.Bool, nil),
			"ignore_fields":            tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, nil),
			"managed_state_projection": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil),
			"object_ref": tftypes.NewValue(tftypes.Object{
				AttributeTypes: map[string]tftypes.Type{
					"api_version": tftypes.String,
					"kind":        tftypes.String,
					"name":        tftypes.String,
					"namespace":   tftypes.String,
				},
			}, map[string]tftypes.Value{
				"api_version": tftypes.NewValue(tftypes.String, "v1"),
				"kind":        tftypes.NewValue(tftypes.String, "ConfigMap"),
				"name":        tftypes.NewValue(tftypes.String, "test"),
				"namespace":   tftypes.NewValue(tftypes.String, "default"),
			}),
		}

		rawStateValue := tftypes.NewValue(
			tftypes.Object{AttributeTypes: map[string]tftypes.Type{
				"id":                       tftypes.String,
				"yaml_body":                tftypes.String,
				"cluster":                  v1State["cluster"].Type(),
				"delete_protection":        tftypes.Bool,
				"delete_timeout":           tftypes.String,
				"force_destroy":            tftypes.Bool,
				"ignore_fields":            tftypes.List{ElementType: tftypes.String},
				"managed_state_projection": tftypes.Map{ElementType: tftypes.String},
				"object_ref":               v1State["object_ref"].Type(),
			}},
			v1State,
		)

		req := resource.UpgradeStateRequest{
			State: &tfsdk.State{
				Raw:    rawStateValue,
				Schema: *getObjectSchemaV1(),
			},
		}

		// Get current v2 schema
		var schemaResp resource.SchemaResponse
		(&objectResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)

		resp := &resource.UpgradeStateResponse{
			State: tfsdk.State{
				Schema: schemaResp.Schema,
			},
		}

		upgradeObjectState(ctx, req, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("Unexpected error: %v", resp.Diagnostics)
		}

		var upgradedState objectResourceModel
		diags := resp.State.Get(ctx, &upgradedState)
		if diags.HasError() {
			t.Fatalf("Failed to get upgraded state: %v", diags)
		}

		if upgradedState.ManagedFields.IsNull() != true {
			t.Errorf("Expected ManagedFields to be null Map")
		}
	})
}
