package yaml_split

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"

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
		&exactlyOneOfThreeValidator{},
	}
}

// exactlyOneOfThreeValidator validates that exactly one of content/pattern/kustomize_path is set
type exactlyOneOfThreeValidator struct{}

func (v *exactlyOneOfThreeValidator) Description(ctx context.Context) string {
	return "validates that exactly one of content, pattern, or kustomize_path is specified"
}

func (v *exactlyOneOfThreeValidator) MarkdownDescription(ctx context.Context) string {
	return "validates that exactly one of `content`, `pattern`, or `kustomize_path` is specified"
}

func (v *exactlyOneOfThreeValidator) ValidateDataSource(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var content, pattern, kustomizePath types.String

	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("content"), &content)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("pattern"), &pattern)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("kustomize_path"), &kustomizePath)...)

	if resp.Diagnostics.HasError() {
		return
	}

	hasContent := !content.IsNull() && !content.IsUnknown() && content.ValueString() != ""
	hasPattern := !pattern.IsNull() && !pattern.IsUnknown() && pattern.ValueString() != ""
	hasKustomize := !kustomizePath.IsNull() && !kustomizePath.IsUnknown() && kustomizePath.ValueString() != ""

	count := 0
	if hasContent {
		count++
	}
	if hasPattern {
		count++
	}
	if hasKustomize {
		count++
	}

	if count > 1 {
		resp.Diagnostics.AddError(
			"Conflicting Configuration",
			"Exactly one of 'content', 'pattern', or 'kustomize_path' must be specified, not multiple.",
		)
	} else if count == 0 {
		resp.Diagnostics.AddError(
			"Missing Configuration",
			"Exactly one of 'content', 'pattern', or 'kustomize_path' must be specified.",
		)
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

	var documents []yaml_common.DocumentInfo
	var sourceID string
	var err error

	// Determine which input mode to use (validation handled by ConfigValidators)
	hasContent := !data.Content.IsNull() && data.Content.ValueString() != ""
	hasKustomize := !data.KustomizePath.IsNull() && data.KustomizePath.ValueString() != ""

	if hasKustomize {
		// Build kustomization and parse output
		kustomizePath := data.KustomizePath.ValueString()
		yamlContent, err := d.buildKustomization(kustomizePath)
		if err != nil {
			resp.Diagnostics.AddError(
				"Kustomize Build Failed",
				d.formatKustomizeError(kustomizePath, err),
			)
			return
		}

		// Parse the built YAML
		documents, err = yaml_common.ParseDocuments(yamlContent, kustomizePath)
		if err != nil {
			resp.Diagnostics.AddError(
				"YAML Parse Error",
				fmt.Sprintf("Kustomize build succeeded but output contains invalid YAML: %s", err),
			)
			return
		}
		sourceID = fmt.Sprintf("kustomize-%s", yaml_common.HashString(kustomizePath)[:8])
	} else {
		// Load documents from either inline content or file pattern
		documents, sourceID, err = yaml_common.LoadDocuments(
			hasContent,
			data.Content.ValueString(),
			data.Pattern.ValueString(),
		)
		if err != nil {
			resp.Diagnostics.AddError(
				"Document Loading Error",
				err.Error(),
			)
			return
		}
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

// buildKustomization runs kustomize build on the given path and returns the generated YAML
func (d *yamlSplitDataSource) buildKustomization(path string) (string, error) {
	// Create kustomizer with default secure options
	opts := krusty.MakeDefaultOptions()
	k := krusty.MakeKustomizer(opts)

	// Run kustomize build
	resMap, err := k.Run(filesys.MakeFsOnDisk(), path)
	if err != nil {
		return "", err
	}

	// Convert resource map to YAML
	yaml, err := resMap.AsYaml()
	if err != nil {
		return "", fmt.Errorf("failed to convert kustomize output to YAML: %w", err)
	}

	return string(yaml), nil
}

// formatKustomizeError formats kustomize errors following k8sconnect's WHAT/WHY/HOW pattern
func (d *yamlSplitDataSource) formatKustomizeError(path string, err error) string {
	return fmt.Sprintf(`Unable to build kustomization at %q

Kustomize build failed with error:
%s

Common causes:
  1. Missing kustomization.yaml in the specified directory
  2. Base path references a directory outside the kustomization root (security restriction)
  3. Patch file references don't exist or have invalid paths
  4. Strategic merge conflict in overlays or patches
  5. Invalid YAML syntax in kustomization.yaml or referenced files

How to fix:
  1. Verify kustomization.yaml exists in the path: %s
  2. Check that all base paths in kustomization.yaml are within the allowed directory
  3. Ensure all patch files and resources referenced in kustomization.yaml exist
  4. Run 'kustomize build %s' locally to test the configuration
  5. Review kustomize documentation: https://kubectl.docs.kubernetes.io/references/kustomize/

If you need to reference files outside the kustomization root, this is blocked for security reasons.
Consider restructuring your kustomization to keep all files within a single directory tree.`,
		path, err.Error(), path, path)
}
