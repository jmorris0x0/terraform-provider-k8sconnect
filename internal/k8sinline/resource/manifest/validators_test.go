// internal/k8sinline/resource/manifest/validators_test.go
package manifest

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestClusterConnectionValidator(t *testing.T) {
	validator := &clusterConnectionValidator{}
	ctx := context.Background()

	tests := []struct {
		name          string
		data          manifestResourceModel
		expectError   bool
		errorContains string
	}{
		{
			name: "valid inline connection",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					Host:                 types.StringValue("https://example.com"),
					ClusterCACertificate: types.StringValue("LS0tLS1CRUdJTi0="), // base64 dummy
				},
			},
			expectError: false,
		},
		{
			name: "valid kubeconfig file connection",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigFile: types.StringValue("/path/to/kubeconfig"),
				},
			},
			expectError: false,
		},
		{
			name: "valid kubeconfig raw connection",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config"),
				},
			},
			expectError: false,
		},
		{
			name: "no connection specified",
			data: manifestResourceModel{
				YAMLBody:          types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{},
			},
			expectError:   true,
			errorContains: "Missing Cluster Connection Configuration",
		},
		{
			name: "multiple connections specified",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					Host:           types.StringValue("https://example.com"),
					KubeconfigFile: types.StringValue("/path/to/kubeconfig"),
				},
			},
			expectError:   true,
			errorContains: "Multiple Cluster Connection Modes Specified",
		},
		{
			name: "incomplete inline connection - missing host",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					ClusterCACertificate: types.StringValue("LS0tLS1CRUdJTi0="),
				},
			},
			expectError:   true,
			errorContains: "Missing Required Field for Inline Connection",
		},
		{
			name: "incomplete inline connection - missing ca cert",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					Host: types.StringValue("https://example.com"),
				},
			},
			expectError:   true,
			errorContains: "Missing Required Field for Inline Connection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock config
			config := createMockConfig(t, tt.data)

			req := resource.ValidateConfigRequest{
				Config: config,
			}
			resp := &resource.ValidateConfigResponse{}

			validator.ValidateResource(ctx, req, resp)

			hasError := resp.Diagnostics.HasError()
			if hasError != tt.expectError {
				t.Errorf("expected error: %v, got error: %v", tt.expectError, hasError)
				if hasError {
					t.Logf("Diagnostics: %v", resp.Diagnostics)
				}
				return
			}

			if tt.expectError && tt.errorContains != "" {
				found := false
				for _, diag := range resp.Diagnostics {
					if diag.Summary() != nil && strings.Contains(*diag.Summary(), tt.errorContains) {
						found = true
						break
					}
					if diag.Detail() != nil && strings.Contains(*diag.Detail(), tt.errorContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q, but not found in diagnostics: %v", tt.errorContains, resp.Diagnostics)
				}
			}
		})
	}
}

func TestExecAuthValidator(t *testing.T) {
	validator := &execAuthValidator{}
	ctx := context.Background()

	tests := []struct {
		name          string
		data          manifestResourceModel
		expectError   bool
		errorContains string
	}{
		{
			name: "valid exec config",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					Host:                 types.StringValue("https://example.com"),
					ClusterCACertificate: types.StringValue("LS0tLS1CRUdJTi0="),
					Exec: &execAuthModel{
						APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
						Command:    types.StringValue("aws"),
						Args: []types.String{
							types.StringValue("eks"),
							types.StringValue("get-token"),
							types.StringValue("--cluster-name"),
							types.StringValue("my-cluster"),
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "no exec config - should pass",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					Host:                 types.StringValue("https://example.com"),
					ClusterCACertificate: types.StringValue("LS0tLS1CRUdJTi0="),
				},
			},
			expectError: false,
		},
		{
			name: "incomplete exec config - missing api_version",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					Host:                 types.StringValue("https://example.com"),
					ClusterCACertificate: types.StringValue("LS0tLS1CRUdJTi0="),
					Exec: &execAuthModel{
						Command: types.StringValue("aws"),
						Args: []types.String{
							types.StringValue("eks"),
							types.StringValue("get-token"),
						},
					},
				},
			},
			expectError:   true,
			errorContains: "Incomplete Exec Authentication Configuration",
		},
		{
			name: "incomplete exec config - missing command",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					Host:                 types.StringValue("https://example.com"),
					ClusterCACertificate: types.StringValue("LS0tLS1CRUdJTi0="),
					Exec: &execAuthModel{
						APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
						Args: []types.String{
							types.StringValue("eks"),
							types.StringValue("get-token"),
						},
					},
				},
			},
			expectError:   true,
			errorContains: "Incomplete Exec Authentication Configuration",
		},
		{
			name: "incomplete exec config - missing args",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					Host:                 types.StringValue("https://example.com"),
					ClusterCACertificate: types.StringValue("LS0tLS1CRUdJTi0="),
					Exec: &execAuthModel{
						APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
						Command:    types.StringValue("aws"),
						Args:       []types.String{}, // Empty args
					},
				},
			},
			expectError:   true,
			errorContains: "Incomplete Exec Authentication Configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createMockConfig(t, tt.data)

			req := resource.ValidateConfigRequest{
				Config: config,
			}
			resp := &resource.ValidateConfigResponse{}

			validator.ValidateResource(ctx, req, resp)

			hasError := resp.Diagnostics.HasError()
			if hasError != tt.expectError {
				t.Errorf("expected error: %v, got error: %v", tt.expectError, hasError)
				if hasError {
					t.Logf("Diagnostics: %v", resp.Diagnostics)
				}
				return
			}

			if tt.expectError && tt.errorContains != "" {
				found := false
				for _, diag := range resp.Diagnostics {
					if diag.Summary() != nil && strings.Contains(*diag.Summary(), tt.errorContains) {
						found = true
						break
					}
					if diag.Detail() != nil && strings.Contains(*diag.Detail(), tt.errorContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q, but not found in diagnostics: %v", tt.errorContains, resp.Diagnostics)
				}
			}
		})
	}
}

func TestConflictingAttributesValidator(t *testing.T) {
	validator := &conflictingAttributesValidator{}
	ctx := context.Background()

	tests := []struct {
		name          string
		data          manifestResourceModel
		expectError   bool
		errorContains string
	}{
		{
			name: "no conflicting attributes",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config"),
				},
				DeleteProtection: types.BoolValue(false),
				ForceDestroy:     types.BoolValue(false),
			},
			expectError: false,
		},
		{
			name: "delete_protection true, force_destroy false - should pass",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config"),
				},
				DeleteProtection: types.BoolValue(true),
				ForceDestroy:     types.BoolValue(false),
			},
			expectError: false,
		},
		{
			name: "delete_protection false, force_destroy true - should pass",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config"),
				},
				DeleteProtection: types.BoolValue(false),
				ForceDestroy:     types.BoolValue(true),
			},
			expectError: false,
		},
		{
			name: "both delete_protection and force_destroy true - should fail",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config"),
				},
				DeleteProtection: types.BoolValue(true),
				ForceDestroy:     types.BoolValue(true),
			},
			expectError:   true,
			errorContains: "Conflicting Deletion Settings",
		},
		{
			name: "null values - should pass",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config"),
				},
				DeleteProtection: types.BoolNull(),
				ForceDestroy:     types.BoolNull(),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createMockConfig(t, tt.data)

			req := resource.ValidateConfigRequest{
				Config: config,
			}
			resp := &resource.ValidateConfigResponse{}

			validator.ValidateResource(ctx, req, resp)

			hasError := resp.Diagnostics.HasError()
			if hasError != tt.expectError {
				t.Errorf("expected error: %v, got error: %v", tt.expectError, hasError)
				if hasError {
					t.Logf("Diagnostics: %v", resp.Diagnostics)
				}
				return
			}

			if tt.expectError && tt.errorContains != "" {
				found := false
				for _, diag := range resp.Diagnostics {
					if diag.Summary() != nil && strings.Contains(*diag.Summary(), tt.errorContains) {
						found = true
						break
					}
					if diag.Detail() != nil && strings.Contains(*diag.Detail(), tt.errorContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q, but not found in diagnostics: %v", tt.errorContains, resp.Diagnostics)
				}
			}
		})
	}
}

func TestRequiredFieldsValidator(t *testing.T) {
	validator := &requiredFieldsValidator{}
	ctx := context.Background()

	tests := []struct {
		name          string
		data          manifestResourceModel
		expectError   bool
		errorContains string
	}{
		{
			name: "all required fields present",
			data: manifestResourceModel{
				YAMLBody: types.StringValue("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test"),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config"),
				},
			},
			expectError: false,
		},
		{
			name: "missing yaml_body",
			data: manifestResourceModel{
				YAMLBody: types.StringNull(),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config"),
				},
			},
			expectError:   true,
			errorContains: "Missing Required Field",
		},
		{
			name: "empty yaml_body",
			data: manifestResourceModel{
				YAMLBody: types.StringValue(""),
				ClusterConnection: ClusterConnectionModel{
					KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config"),
				},
			},
			expectError:   true,
			errorContains: "Empty YAML Content",
		},
		{
			name: "empty cluster_connection",
			data: manifestResourceModel{
				YAMLBody:          types.StringValue("apiVersion: v1\nkind: Namespace"),
				ClusterConnection: ClusterConnectionModel{},
			},
			expectError:   true,
			errorContains: "Missing Required Configuration Block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createMockConfig(t, tt.data)

			req := resource.ValidateConfigRequest{
				Config: config,
			}
			resp := &resource.ValidateConfigResponse{}

			validator.ValidateResource(ctx, req, resp)

			hasError := resp.Diagnostics.HasError()
			if hasError != tt.expectError {
				t.Errorf("expected error: %v, got error: %v", tt.expectError, hasError)
				if hasError {
					t.Logf("Diagnostics: %v", resp.Diagnostics)
				}
				return
			}

			if tt.expectError && tt.errorContains != "" {
				found := false
				for _, diag := range resp.Diagnostics {
					if diag.Summary() != nil && strings.Contains(*diag.Summary(), tt.errorContains) {
						found = true
						break
					}
					if diag.Detail() != nil && strings.Contains(*diag.Detail(), tt.errorContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q, but not found in diagnostics: %v", tt.errorContains, resp.Diagnostics)
				}
			}
		})
	}
}

// Helper function to create a mock config for testing
func createMockConfig(t *testing.T, data manifestResourceModel) tfsdk.Config {
	// Create schema that matches your resource schema
	schema := tfsdk.Schema{
		Attributes: map[string]tfsdk.Attribute{
			"id": {
				Type:     types.StringType,
				Computed: true,
			},
			"yaml_body": {
				Type:     types.StringType,
				Required: true,
			},
			"delete_protection": {
				Type:     types.BoolType,
				Optional: true,
			},
			"delete_timeout": {
				Type:     types.StringType,
				Optional: true,
			},
			"force_destroy": {
				Type:     types.BoolType,
				Optional: true,
			},
		},
		Blocks: map[string]tfsdk.Block{
			"cluster_connection": {
				NestingMode: tfsdk.BlockNestingModeSingle,
				Attributes: map[string]tfsdk.Attribute{
					"host": {
						Type:      types.StringType,
						Optional:  true,
						Sensitive: true,
					},
					"cluster_ca_certificate": {
						Type:      types.StringType,
						Optional:  true,
						Sensitive: true,
					},
					"kubeconfig_file": {
						Type:      types.StringType,
						Optional:  true,
						Sensitive: true,
					},
					"kubeconfig_raw": {
						Type:      types.StringType,
						Optional:  true,
						Sensitive: true,
					},
					"context": {
						Type:      types.StringType,
						Optional:  true,
						Sensitive: true,
					},
					"exec": {
						Type: types.ObjectType{
							AttrTypes: map[string]attr.Type{
								"api_version": types.StringType,
								"command":     types.StringType,
								"args":        types.ListType{ElemType: types.StringType},
							},
						},
						Optional:  true,
						Sensitive: true,
					},
				},
			},
		},
	}

	// Create config with the test data
	config := tfsdk.Config{
		Schema: schema,
	}

	// Set the raw config data
	configData := tftypes.NewValue(schema.Type().TerraformType(context.Background()), map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, data.ID.ValueString()),
		"yaml_body": tftypes.NewValue(tftypes.String, data.YAMLBody.ValueString()),
		"delete_protection": func() tftypes.Value {
			if data.DeleteProtection.IsNull() {
				return tftypes.NewValue(tftypes.Bool, nil)
			}
			return tftypes.NewValue(tftypes.Bool, data.DeleteProtection.ValueBool())
		}(),
		"delete_timeout": func() tftypes.Value {
			if data.DeleteTimeout.IsNull() {
				return tftypes.NewValue(tftypes.String, nil)
			}
			return tftypes.NewValue(tftypes.String, data.DeleteTimeout.ValueString())
		}(),
		"force_destroy": func() tftypes.Value {
			if data.ForceDestroy.IsNull() {
				return tftypes.NewValue(tftypes.Bool, nil)
			}
			return tftypes.NewValue(tftypes.Bool, data.ForceDestroy.ValueBool())
		}(),
		"cluster_connection": tftypes.NewValue(
			tftypes.Object{
				AttributeTypes: map[string]tftypes.Type{
					"host":                   tftypes.String,
					"cluster_ca_certificate": tftypes.String,
					"kubeconfig_file":        tftypes.String,
					"kubeconfig_raw":         tftypes.String,
					"context":                tftypes.String,
					"exec": tftypes.Object{
						AttributeTypes: map[string]tftypes.Type{
							"api_version": tftypes.String,
							"command":     tftypes.String,
							"args":        tftypes.List{ElementType: tftypes.String},
						},
					},
				},
			},
			map[string]tftypes.Value{
				"host": func() tftypes.Value {
					if data.ClusterConnection.Host.IsNull() {
						return tftypes.NewValue(tftypes.String, nil)
					}
					return tftypes.NewValue(tftypes.String, data.ClusterConnection.Host.ValueString())
				}(),
				"cluster_ca_certificate": func() tftypes.Value {
					if data.ClusterConnection.ClusterCACertificate.IsNull() {
						return tftypes.NewValue(tftypes.String, nil)
					}
					return tftypes.NewValue(tftypes.String, data.ClusterConnection.ClusterCACertificate.ValueString())
				}(),
				"kubeconfig_file": func() tftypes.Value {
					if data.ClusterConnection.KubeconfigFile.IsNull() {
						return tftypes.NewValue(tftypes.String, nil)
					}
					return tftypes.NewValue(tftypes.String, data.ClusterConnection.KubeconfigFile.ValueString())
				}(),
				"kubeconfig_raw": func() tftypes.Value {
					if data.ClusterConnection.KubeconfigRaw.IsNull() {
						return tftypes.NewValue(tftypes.String, nil)
					}
					return tftypes.NewValue(tftypes.String, data.ClusterConnection.KubeconfigRaw.ValueString())
				}(),
				"context": func() tftypes.Value {
					if data.ClusterConnection.Context.IsNull() {
						return tftypes.NewValue(tftypes.String, nil)
					}
					return tftypes.NewValue(tftypes.String, data.ClusterConnection.Context.ValueString())
				}(),
				"exec": func() tftypes.Value {
					if data.ClusterConnection.Exec == nil {
						return tftypes.NewValue(tftypes.Object{
							AttributeTypes: map[string]tftypes.Type{
								"api_version": tftypes.String,
								"command":     tftypes.String,
								"args":        tftypes.List{ElementType: tftypes.String},
							},
						}, nil)
					}

					args := make([]tftypes.Value, len(data.ClusterConnection.Exec.Args))
					for i, arg := range data.ClusterConnection.Exec.Args {
						args[i] = tftypes.NewValue(tftypes.String, arg.ValueString())
					}

					return tftypes.NewValue(
						tftypes.Object{
							AttributeTypes: map[string]tftypes.Type{
								"api_version": tftypes.String,
								"command":     tftypes.String,
								"args":        tftypes.List{ElementType: tftypes.String},
							},
						},
						map[string]tftypes.Value{
							"api_version": func() tftypes.Value {
								if data.ClusterConnection.Exec.APIVersion.IsNull() {
									return tftypes.NewValue(tftypes.String, nil)
								}
								return tftypes.NewValue(tftypes.String, data.ClusterConnection.Exec.APIVersion.ValueString())
							}(),
							"command": func() tftypes.Value {
								if data.ClusterConnection.Exec.Command.IsNull() {
									return tftypes.NewValue(tftypes.String, nil)
								}
								return tftypes.NewValue(tftypes.String, data.ClusterConnection.Exec.Command.ValueString())
							}(),
							"args": tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, args),
						},
					)
				}(),
			},
		),
	})

	config.Raw = configData
	return config
}

// Test the complete resource validation
func TestManifestResourceValidation_Integration(t *testing.T) {
	ctx := context.Background()
	resource := &manifestResource{}

	// Get all validators
	validators := resource.ConfigValidators(ctx)
	if len(validators) != 4 {
		t.Errorf("expected 4 validators, got %d", len(validators))
	}

	tests := []struct {
		name          string
		data          manifestResourceModel
		expectError   bool
		errorContains []string
	}{
		{
			name: "completely valid configuration",
			data: manifestResourceModel{
				YAMLBody: types.StringValue(`apiVersion: v1
kind: Namespace
metadata:
  name: test-namespace`),
				ClusterConnection: ClusterConnectionModel{
					Host:                 types.StringValue("https://kubernetes.example.com"),
					ClusterCACertificate: types.StringValue("LS0tLS1CRUdJTi0tLS0t"), // dummy base64
					Exec: &execAuthModel{
						APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
						Command:    types.StringValue("aws"),
						Args: []types.String{
							types.StringValue("eks"),
							types.StringValue("get-token"),
							types.StringValue("--cluster-name"),
							types.StringValue("my-cluster"),
						},
					},
				},
				DeleteProtection: types.BoolValue(false),
				ForceDestroy:     types.BoolValue(false),
			},
			expectError: false,
		},
		{
			name: "multiple validation errors",
			data: manifestResourceModel{
				YAMLBody: types.StringValue(""), // Empty YAML
				ClusterConnection: ClusterConnectionModel{
					Host:           types.StringValue("https://example.com"), // Incomplete inline (missing CA)
					KubeconfigFile: types.StringValue("/path/to/config"),     // Multiple connection modes
					Exec: &execAuthModel{
						APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
						// Missing command and args
					},
				},
				DeleteProtection: types.BoolValue(true), // Conflicting
				ForceDestroy:     types.BoolValue(true), // Conflicting
			},
			expectError: true,
			errorContains: []string{
				"Empty YAML Content",
				"Multiple Cluster Connection Modes",
				"Incomplete Exec Authentication Configuration",
				"Conflicting Deletion Settings",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createMockConfig(t, tt.data)

			req := resource.ValidateConfigRequest{
				Config: config,
			}
			resp := &resource.ValidateConfigResponse{}

			// Run all validators
			for _, validator := range validators {
				validator.ValidateResource(ctx, req, resp)
			}

			hasError := resp.Diagnostics.HasError()
			if hasError != tt.expectError {
				t.Errorf("expected error: %v, got error: %v", tt.expectError, hasError)
				if hasError {
					t.Logf("Diagnostics: %v", resp.Diagnostics)
				}
				return
			}

			if tt.expectError && len(tt.errorContains) > 0 {
				for _, expectedError := range tt.errorContains {
					found := false
					for _, diag := range resp.Diagnostics {
						summary := ""
						detail := ""
						if diag.Summary() != nil {
							summary = *diag.Summary()
						}
						if diag.Detail() != nil {
							detail = *diag.Detail()
						}

						if strings.Contains(summary, expectedError) || strings.Contains(detail, expectedError) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, but not found in diagnostics", expectedError)
						t.Logf("All diagnostics: %v", resp.Diagnostics)
					}
				}
			}
		})
	}
}
