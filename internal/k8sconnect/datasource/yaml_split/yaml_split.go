// internal/k8sconnect/datasource/yaml_split/yaml_split.go
package yaml_split

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/yaml_common"
)

var _ datasource.DataSource = (*yamlSplitDataSource)(nil)

type yamlSplitDataSource struct{}

type yamlSplitDataSourceModel struct {
	ID        types.String            `tfsdk:"id"`
	Content   types.String            `tfsdk:"content"`
	Pattern   types.String            `tfsdk:"pattern"`
	Manifests map[string]types.String `tfsdk:"manifests"`
}

func NewYamlSplitDataSource() datasource.DataSource {
	return &yamlSplitDataSource{}
}

func (d *yamlSplitDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_yaml_split"
}

func (d *yamlSplitDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Splits multi-document YAML content into individual manifests with stable, human-readable IDs. Handles complex YAML edge cases and provides excellent error reporting.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier based on input content hash.",
			},
			"content": schema.StringAttribute{
				Optional:    true,
				Description: "Raw YAML content containing one or more Kubernetes manifests separated by '---'. Mutually exclusive with 'pattern'.",
			},
			"pattern": schema.StringAttribute{
				Optional:    true,
				Description: "Glob pattern to match YAML files (e.g., './manifests/*.yaml', './configs/**/*.yml'). Supports recursive patterns. Mutually exclusive with 'pattern'.",
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

	// Validate exactly one of content or pattern is provided
	hasContent := !data.Content.IsNull() && data.Content.ValueString() != ""
	hasPattern := !data.Pattern.IsNull() && data.Pattern.ValueString() != ""

	if hasContent && hasPattern {
		resp.Diagnostics.AddError(
			"Conflicting Configuration",
			"Exactly one of 'content' or 'pattern' must be specified, not both.",
		)
		return
	}

	if !hasContent && !hasPattern {
		resp.Diagnostics.AddError(
			"Missing Configuration",
			"Either 'content' or 'pattern' must be specified.",
		)
		return
	}

	var documents []yaml_common.DocumentInfo
	var sourceID string

	if hasContent {
		// Parse inline content
		content := data.Content.ValueString()
		docs, err := yaml_common.ParseDocuments(content, "<inline>")
		if err != nil {
			resp.Diagnostics.AddError(
				"Content Parsing Error",
				fmt.Sprintf("Failed to parse inline YAML content: %s", err),
			)
			return
		}
		documents = docs
		sourceID = fmt.Sprintf("content-%s", yaml_common.HashString(content)[:8])
	} else {
		// Handle pattern-based loading
		pattern := data.Pattern.ValueString()
		files, err := yaml_common.ExpandPattern(pattern)
		if err != nil {
			resp.Diagnostics.AddError(
				"Pattern Error",
				fmt.Sprintf("Failed to resolve pattern %q: %s", pattern, err),
			)
			return
		}

		if len(files) == 0 {
			resp.Diagnostics.AddError(
				"No Files Found",
				fmt.Sprintf("No files matched pattern %q. Check that the path exists and contains YAML files.", pattern),
			)
			return
		}

		// Process all matching files
		for _, file := range files {
			content, err := yaml_common.ReadFile(file)
			if err != nil {
				resp.Diagnostics.AddError(
					"File Read Error",
					fmt.Sprintf("Failed to read file %q: %s", file, err),
				)
				return
			}

			docs, err := yaml_common.ParseDocuments(content, file)
			if err != nil {
				resp.Diagnostics.AddError(
					"File Parsing Error",
					fmt.Sprintf("Failed to parse YAML in file %q: %s", file, err),
				)
				return
			}
			documents = append(documents, docs...)
		}
		sourceID = fmt.Sprintf("pattern-%s", yaml_common.HashString(pattern)[:8])
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
