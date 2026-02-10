package patch

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestUpgradePatchStateV0toV2(t *testing.T) {
	tests := []struct {
		name          string
		v0State       map[string]tftypes.Value
		expectError   bool
		validateState func(t *testing.T, state patchResourceModel)
	}{
		{
			name: "basic v0 state upgrade",
			v0State: map[string]tftypes.Value{
				"id":    tftypes.NewValue(tftypes.String, "patch-id-123"),
				"patch": tftypes.NewValue(tftypes.String, `{"data":{"foo":"bar"}}`),
				"target": tftypes.NewValue(tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"api_version": tftypes.String,
						"kind":        tftypes.String,
						"name":        tftypes.String,
						"namespace":   tftypes.String,
					},
				}, map[string]tftypes.Value{
					"api_version": tftypes.NewValue(tftypes.String, "v1"),
					"kind":        tftypes.NewValue(tftypes.String, "ConfigMap"),
					"name":        tftypes.NewValue(tftypes.String, "test-config"),
					"namespace":   tftypes.NewValue(tftypes.String, "default"),
				}),
				"json_patch":  tftypes.NewValue(tftypes.String, nil),
				"merge_patch": tftypes.NewValue(tftypes.String, nil),
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
				"managed_state_projection": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
					"data.foo": tftypes.NewValue(tftypes.String, "bar"),
				}),
			},
			validateState: func(t *testing.T, state patchResourceModel) {
				if state.ID.ValueString() != "patch-id-123" {
					t.Errorf("Expected ID patch-id-123, got %s", state.ID.ValueString())
				}
				if state.ManagedFields.IsNull() != true {
					t.Errorf("Expected ManagedFields to be null Map, got %v", state.ManagedFields)
				}
			},
		},
		{
			name: "v0 state with null projection",
			v0State: map[string]tftypes.Value{
				"id":    tftypes.NewValue(tftypes.String, "patch-null"),
				"patch": tftypes.NewValue(tftypes.String, `{"spec":{"replicas":3}}`),
				"target": tftypes.NewValue(tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"api_version": tftypes.String,
						"kind":        tftypes.String,
						"name":        tftypes.String,
						"namespace":   tftypes.String,
					},
				}, map[string]tftypes.Value{
					"api_version": tftypes.NewValue(tftypes.String, "apps/v1"),
					"kind":        tftypes.NewValue(tftypes.String, "Deployment"),
					"name":        tftypes.NewValue(tftypes.String, "test-app"),
					"namespace":   tftypes.NewValue(tftypes.String, nil),
				}),
				"json_patch":  tftypes.NewValue(tftypes.String, nil),
				"merge_patch": tftypes.NewValue(tftypes.String, nil),
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
					"token":                  tftypes.NewValue(tftypes.String, nil),

					"insecure":               tftypes.NewValue(tftypes.Bool, nil),
					"kubeconfig":             tftypes.NewValue(tftypes.String, nil),
					"context":                tftypes.NewValue(tftypes.String, nil),
					"proxy_url":              tftypes.NewValue(tftypes.String, nil),
					"exec":                   tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"api_version": tftypes.String, "command": tftypes.String, "args": tftypes.List{ElementType: tftypes.String}, "env": tftypes.Map{ElementType: tftypes.String}}}, nil),
				}),
				"managed_state_projection": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil),
			},
			validateState: func(t *testing.T, state patchResourceModel) {
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
					"target":                   tt.v0State["target"].Type(),
					"patch":                    tftypes.String,
					"json_patch":               tftypes.String,
					"merge_patch":              tftypes.String,
					"cluster":                  tt.v0State["cluster"].Type(),
					"managed_state_projection": tftypes.Map{ElementType: tftypes.String},
				}},
				tt.v0State,
			)

			// Create request
			req := resource.UpgradeStateRequest{
				State: &tfsdk.State{
					Raw:    rawStateValue,
					Schema: *getPatchSchemaV0(),
				},
			}

			// Get current v2 schema
			var schemaResp resource.SchemaResponse
			(&patchResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)

			// Create response
			resp := &resource.UpgradeStateResponse{
				State: tfsdk.State{
					Schema: schemaResp.Schema,
				},
			}

			// Run the upgrader
			upgradePatchStateV0(ctx, req, resp)

			// Check for errors
			if tt.expectError && !resp.Diagnostics.HasError() {
				t.Fatal("Expected error but got none")
			}
			if !tt.expectError && resp.Diagnostics.HasError() {
				t.Fatalf("Unexpected error: %v", resp.Diagnostics)
			}

			if !tt.expectError {
				// Extract the upgraded state
				var upgradedState patchResourceModel
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

func TestUpgradePatchStateV1toV2(t *testing.T) {
	tests := []struct {
		name          string
		v1State       map[string]tftypes.Value
		expectError   bool
		validateState func(t *testing.T, state patchResourceModel)
	}{
		{
			name: "v1 with managed_fields as String",
			v1State: map[string]tftypes.Value{
				"id":    tftypes.NewValue(tftypes.String, "patch-v1-123"),
				"patch": tftypes.NewValue(tftypes.String, `{"data":{"cache.enabled":"true"}}`),
				"target": tftypes.NewValue(tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"api_version": tftypes.String,
						"kind":        tftypes.String,
						"name":        tftypes.String,
						"namespace":   tftypes.String,
					},
				}, map[string]tftypes.Value{
					"api_version": tftypes.NewValue(tftypes.String, "v1"),
					"kind":        tftypes.NewValue(tftypes.String, "ConfigMap"),
					"name":        tftypes.NewValue(tftypes.String, "app-config"),
					"namespace":   tftypes.NewValue(tftypes.String, "default"),
				}),
				"json_patch":  tftypes.NewValue(tftypes.String, nil),
				"merge_patch": tftypes.NewValue(tftypes.String, nil),
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
				"managed_state_projection": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
					"data.cache.enabled": tftypes.NewValue(tftypes.String, "true"),
				}),
				// Old format: raw JSON string from managedFields
				"managed_fields": tftypes.NewValue(tftypes.String, `{"f:data":{"f:cache.enabled":{}}}`),
			},
			validateState: func(t *testing.T, state patchResourceModel) {
				if state.ID.ValueString() != "patch-v1-123" {
					t.Errorf("Expected ID patch-v1-123, got %s", state.ID.ValueString())
				}
				// The old String value should be discarded, managed_fields should be null Map
				if state.ManagedFields.IsNull() != true {
					t.Errorf("Expected ManagedFields to be null Map (old String value discarded), got %v", state.ManagedFields)
				}
			},
		},
		{
			name: "v1 with null managed_fields String",
			v1State: map[string]tftypes.Value{
				"id":    tftypes.NewValue(tftypes.String, "patch-v1-null"),
				"patch": tftypes.NewValue(tftypes.String, `{"spec":{"replicas":5}}`),
				"target": tftypes.NewValue(tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"api_version": tftypes.String,
						"kind":        tftypes.String,
						"name":        tftypes.String,
						"namespace":   tftypes.String,
					},
				}, map[string]tftypes.Value{
					"api_version": tftypes.NewValue(tftypes.String, "apps/v1"),
					"kind":        tftypes.NewValue(tftypes.String, "Deployment"),
					"name":        tftypes.NewValue(tftypes.String, "web"),
					"namespace":   tftypes.NewValue(tftypes.String, "production"),
				}),
				"json_patch":  tftypes.NewValue(tftypes.String, nil),
				"merge_patch": tftypes.NewValue(tftypes.String, nil),
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
					"token":                  tftypes.NewValue(tftypes.String, nil),

					"insecure":               tftypes.NewValue(tftypes.Bool, nil),
					"kubeconfig":             tftypes.NewValue(tftypes.String, "~/.kube/config"),
					"context":                tftypes.NewValue(tftypes.String, "prod"),
					"proxy_url":              tftypes.NewValue(tftypes.String, nil),
					"exec":                   tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"api_version": tftypes.String, "command": tftypes.String, "args": tftypes.List{ElementType: tftypes.String}, "env": tftypes.Map{ElementType: tftypes.String}}}, nil),
				}),
				"managed_state_projection": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil),
				"managed_fields":           tftypes.NewValue(tftypes.String, nil), // Null in v1
			},
			validateState: func(t *testing.T, state patchResourceModel) {
				if state.ManagedFields.IsNull() != true {
					t.Errorf("Expected ManagedFields to be null Map")
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
					"target":                   tt.v1State["target"].Type(),
					"patch":                    tftypes.String,
					"json_patch":               tftypes.String,
					"merge_patch":              tftypes.String,
					"cluster":                  tt.v1State["cluster"].Type(),
					"managed_state_projection": tftypes.Map{ElementType: tftypes.String},
					"managed_fields":           tftypes.String, // String in v1
				}},
				tt.v1State,
			)

			// Create request
			req := resource.UpgradeStateRequest{
				State: &tfsdk.State{
					Raw:    rawStateValue,
					Schema: *getPatchSchemaV1(),
				},
			}

			// Get current v2 schema
			var schemaResp resource.SchemaResponse
			(&patchResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)

			// Create response
			resp := &resource.UpgradeStateResponse{
				State: tfsdk.State{
					Schema: schemaResp.Schema,
				},
			}

			// Run the upgrader
			upgradePatchStateV1(ctx, req, resp)

			// Check for errors
			if tt.expectError && !resp.Diagnostics.HasError() {
				t.Fatal("Expected error but got none")
			}
			if !tt.expectError && resp.Diagnostics.HasError() {
				t.Fatalf("Unexpected error: %v", resp.Diagnostics)
			}

			if !tt.expectError {
				// Extract the upgraded state
				var upgradedState patchResourceModel
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
