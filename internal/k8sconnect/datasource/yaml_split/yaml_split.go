package yaml_split

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/validators"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/yaml_common"
)

var _ datasource.DataSource = (*yamlSplitDataSource)(nil)
var _ datasource.DataSourceWithConfigValidators = (*yamlSplitDataSource)(nil)

type yamlSplitDataSource struct{}

type yamlSplitDataSourceModel struct {
	ID            types.String            `tfsdk:"id"`
	Content       types.String            `tfsdk:"content"`
	Pattern       types.String            `tfsdk:"pattern"`
	KustomizePath types.String            `tfsdk:"kustomize_path"`
	Manifests     map[string]types.String `tfsdk:"manifests"`
}

func NewYamlSplitDataSource() datasource.DataSource {
	return &yamlSplitDataSource{}
}

func (d *yamlSplitDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_yaml_split"
}

// ConfigValidators implements datasource.DataSourceWithConfigValidators
func (d *yamlSplitDataSource) ConfigValidators(ctx context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		validators.ExactlyOneOfThree{
			Attribute1: "content",
			Attribute2: "pattern",
			Attribute3: "kustomize_path",
		},
	}
}

func (d *yamlSplitDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Splits multi-document YAML content into individual manifests with stable, human-readable IDs. Supports inline content, file patterns, or kustomize builds.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier based on input content hash.",
			},
			"content": schema.StringAttribute{
				Optional:    true,
				Description: "Raw YAML content containing one or more Kubernetes manifests separated by '---'. Mutually exclusive with 'pattern' and 'kustomize_path'.",
			},
			"pattern": schema.StringAttribute{
				Optional:    true,
				Description: "Glob pattern to match YAML files (e.g., './manifests/*.yaml', './configs/**/*.yml'). Supports recursive patterns. Mutually exclusive with 'content' and 'kustomize_path'.",
			},
			"kustomize_path": schema.StringAttribute{
				Optional:    true,
				Description: "Path to a kustomization directory (containing kustomization.yaml). Runs 'kustomize build' and parses the output. Mutually exclusive with 'content' and 'pattern'.",
			},
			"manifests": schema.MapAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Map of stable manifest IDs to YAML content. IDs follow the format 'kind.name' (cluster-scoped) or 'kind.namespace.name' (namespaced). Duplicate IDs will cause an error.",
			},
		},
	}
}

func (d *yamlSplitDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data yamlSplitDataSourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Determine which input mode to use (validation handled by ConfigValidators)
	hasContent := !data.Content.IsNull() && data.Content.ValueString() != ""

	// Load documents from content, pattern, or kustomize
	documents, sourceID, err := yaml_common.LoadDocuments(
		hasContent,
		data.Content.ValueString(),
		data.Pattern.ValueString(),
		data.KustomizePath.ValueString(),
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Document Loading Error",
			err.Error(),
		)
		return
	}

	// Generate manifests - this will fail on duplicates
	manifests, err := d.generateManifests(documents)
	if err != nil {
		resp.Diagnostics.AddError(
			"Manifest Generation Error",
			fmt.Sprintf("Failed to generate manifests: %s", err),
		)
		return
	}

	// Set results
	data.ID = types.StringValue(sourceID)
	data.Manifests = manifests

	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

// generateManifests creates the final manifest map with stable IDs
func (d *yamlSplitDataSource) generateManifests(documents []yaml_common.DocumentInfo) (map[string]types.String, error) {
	manifests := make(map[string]types.String)
	seenIDs := make(map[string]yaml_common.DocumentInfo) // Track which IDs we've seen and where

	for _, doc := range documents {
		if doc.ParseError != nil {
			// Fail fast on parse errors - don't try to work around invalid YAML
			return nil, fmt.Errorf("invalid YAML in %s at document %d (around line %d): %w",
				doc.SourceFile, doc.DocumentIndex+1, doc.LineNumber, doc.ParseError)
		}

		id := yaml_common.GenerateResourceID(doc.Object)

		// Check for duplicates - this is always an error
		if existingDoc, exists := seenIDs[id]; exists {
			return nil, fmt.Errorf("duplicate resource ID %q:\n  First defined: %s (document %d)\n  Duplicate found: %s (document %d)\n\nKubernetes resources must have unique kind/namespace/name combinations",
				id,
				existingDoc.SourceFile, existingDoc.DocumentIndex+1,
				doc.SourceFile, doc.DocumentIndex+1)
		}

		// Record this ID and add to manifests
		seenIDs[id] = doc
		manifests[id] = types.StringValue(doc.Content)
	}

	return manifests, nil
}
