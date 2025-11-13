package validators

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestExactlyOneOfThree(t *testing.T) {
	ctx := context.Background()

	// Define a test schema that matches yaml_split/yaml_scoped
	testSchema := schema.Schema{
		Attributes: map[string]schema.Attribute{
			"content": schema.StringAttribute{
				Optional: true,
			},
			"pattern": schema.StringAttribute{
				Optional: true,
			},
			"kustomize_path": schema.StringAttribute{
				Optional: true,
			},
		},
	}

	tests := []struct {
		name          string
		content       types.String
		pattern       types.String
		kustomizePath types.String
		expectError   bool
		errorContains string
	}{
		{
			name:          "content is set",
			content:       types.StringValue("some yaml"),
			pattern:       types.StringNull(),
			kustomizePath: types.StringNull(),
			expectError:   false,
		},
		{
			name:          "pattern is set",
			content:       types.StringNull(),
			pattern:       types.StringValue("*.yaml"),
			kustomizePath: types.StringNull(),
			expectError:   false,
		},
		{
			name:          "kustomize_path is set",
			content:       types.StringNull(),
			pattern:       types.StringNull(),
			kustomizePath: types.StringValue("./kustomize"),
			expectError:   false,
		},
		{
			name:          "none set",
			content:       types.StringNull(),
			pattern:       types.StringNull(),
			kustomizePath: types.StringNull(),
			expectError:   true,
			errorContains: "Missing Configuration",
		},
		{
			name:          "multiple set",
			content:       types.StringValue("yaml"),
			pattern:       types.StringValue("*.yaml"),
			kustomizePath: types.StringNull(),
			expectError:   true,
			errorContains: "Conflicting Configuration",
		},
		{
			name:          "content is unknown - should NOT error",
			content:       types.StringUnknown(),
			pattern:       types.StringNull(),
			kustomizePath: types.StringNull(),
			expectError:   false, // This is the key test case - unknown values should be allowed
		},
		{
			name:          "pattern is unknown - should NOT error",
			content:       types.StringNull(),
			pattern:       types.StringUnknown(),
			kustomizePath: types.StringNull(),
			expectError:   false,
		},
		{
			name:          "kustomize_path is unknown - should NOT error",
			content:       types.StringNull(),
			pattern:       types.StringNull(),
			kustomizePath: types.StringUnknown(),
			expectError:   false,
		},
		{
			name:          "content known + pattern unknown - should NOT error",
			content:       types.StringValue("yaml"),
			pattern:       types.StringUnknown(),
			kustomizePath: types.StringNull(),
			expectError:   false, // Unknown values should not trigger conflict validation
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := ExactlyOneOfThree{
				Attribute1: "content",
				Attribute2: "pattern",
				Attribute3: "kustomize_path",
			}

			// Create config with test values
			configData := map[string]attr.Value{
				"content":        tt.content,
				"pattern":        tt.pattern,
				"kustomize_path": tt.kustomizePath,
			}

			objectValue := types.ObjectValueMust(
				map[string]attr.Type{
					"content":        types.StringType,
					"pattern":        types.StringType,
					"kustomize_path": types.StringType,
				},
				configData,
			)

			config := tfsdk.Config{
				Schema: testSchema,
			}

			// Set the config using the ToTerraformValue method
			rawValue, err := objectValue.ToTerraformValue(ctx)
			if err != nil {
				t.Fatalf("failed to convert to terraform value: %v", err)
			}
			config.Raw = rawValue

			req := datasource.ValidateConfigRequest{
				Config: config,
			}
			resp := &datasource.ValidateConfigResponse{}

			v.ValidateDataSource(ctx, req, resp)

			hasError := resp.Diagnostics.HasError()
			if hasError != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, hasError)
				if hasError {
					t.Logf("Diagnostics: %v", resp.Diagnostics)
				}
				return
			}

			if tt.expectError && tt.errorContains != "" {
				found := false
				for _, diag := range resp.Diagnostics {
					summary := diag.Summary()
					if contains(summary, tt.errorContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error to contain '%s', but it didn't. Diagnostics: %v",
						tt.errorContains, resp.Diagnostics)
				}
			}
		})
	}
}
