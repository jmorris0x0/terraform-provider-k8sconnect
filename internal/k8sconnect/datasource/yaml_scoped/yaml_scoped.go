package yaml_scoped

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/yaml_common"
)

var _ datasource.DataSource = (*yamlScopedDataSource)(nil)
var _ datasource.DataSourceWithConfigValidators = (*yamlScopedDataSource)(nil)

type yamlScopedDataSource struct{}

type yamlScopedDataSourceModel struct {
	ID            types.String            `tfsdk:"id"`
	Content       types.String            `tfsdk:"content"`
	Pattern       types.String            `tfsdk:"pattern"`
	KustomizePath types.String            `tfsdk:"kustomize_path"`
	CRDs          map[string]types.String `tfsdk:"crds"`
	ClusterScoped map[string]types.String `tfsdk:"cluster_scoped"`
	Namespaced    map[string]types.String `tfsdk:"namespaced"`
}

func NewYamlScopedDataSource() datasource.DataSource {
	return &yamlScopedDataSource{}
}

func (d *yamlScopedDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_yaml_scoped"
}

// ConfigValidators implements datasource.DataSourceWithConfigValidators
func (d *yamlScopedDataSource) ConfigValidators(ctx context.Context) []datasource.ConfigValidator {
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

func (d *yamlScopedDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Splits multi-document YAML content into categorized manifests for dependency ordering. Resources are grouped by scope: CRDs first, then cluster-scoped resources (Namespaces, ClusterRoles), then namespaced resources (Deployments, Services, etc.).",
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
			"crds": schema.MapAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Map of CustomResourceDefinition manifests. Apply these first with depends_on to ensure CRDs exist before custom resources.",
			},
			"cluster_scoped": schema.MapAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Map of cluster-scoped resource manifests (Namespaces, ClusterRoles, PersistentVolumes, etc). Apply these second after CRDs.",
			},
			"namespaced": schema.MapAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Map of namespaced resource manifests (Deployments, Services, ConfigMaps, etc). Apply these last after cluster-scoped resources.",
			},
		},
	}
}

func (d *yamlScopedDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data yamlScopedDataSourceModel

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

	// Categorize manifests by scope
	crds, clusterScoped, namespaced, err := d.categorizeManifests(documents)
	if err != nil {
		resp.Diagnostics.AddError(
			"Categorization Error",
			fmt.Sprintf("Failed to categorize manifests: %s", err),
		)
		return
	}

	// Set results
	data.ID = types.StringValue(sourceID)
	data.CRDs = crds
	data.ClusterScoped = clusterScoped
	data.Namespaced = namespaced

	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

// categorizeManifests splits documents into CRDs, cluster-scoped, and namespaced resources
func (d *yamlScopedDataSource) categorizeManifests(documents []yaml_common.DocumentInfo) (
	crds map[string]types.String,
	clusterScoped map[string]types.String,
	namespaced map[string]types.String,
	err error,
) {
	crds = make(map[string]types.String)
	clusterScoped = make(map[string]types.String)
	namespaced = make(map[string]types.String)

	seenIDs := make(map[string]yaml_common.DocumentInfo) // Track duplicates across all categories

	for _, doc := range documents {
		if doc.ParseError != nil {
			// Fail fast on parse errors
			return nil, nil, nil, fmt.Errorf("invalid YAML in %s at document %d (around line %d): %w",
				doc.SourceFile, doc.DocumentIndex+1, doc.LineNumber, doc.ParseError)
		}

		id := yaml_common.GenerateResourceID(doc.Object)
		apiVersion := doc.Object.GetAPIVersion()
		kind := doc.Object.GetKind()

		// Check for duplicates across all categories
		if existingDoc, exists := seenIDs[id]; exists {
			return nil, nil, nil, fmt.Errorf("duplicate resource ID %q:\n  First defined: %s (document %d)\n  Duplicate found: %s (document %d)\n\nKubernetes resources must have unique kind/namespace/name combinations",
				id,
				existingDoc.SourceFile, existingDoc.DocumentIndex+1,
				doc.SourceFile, doc.DocumentIndex+1)
		}

		seenIDs[id] = doc
		yamlValue := types.StringValue(doc.Content)

		// Categorize by scope
		if kind == "CustomResourceDefinition" {
			crds[id] = yamlValue
		} else if isClusterScopedKind(apiVersion, kind) {
			clusterScoped[id] = yamlValue
		} else {
			namespaced[id] = yamlValue
		}
	}

	return crds, clusterScoped, namespaced, nil
}

// isClusterScopedKind returns true if the resource kind is cluster-scoped (not namespaced)
// This is a hardcoded list of well-known cluster-scoped Kubernetes resources.
// Unknown resources return false (smart fallback → namespaced bucket, which is safer).
func isClusterScopedKind(apiVersion, kind string) bool {
	// Use common function from k8sclient package
	// Smart fallback: unknown resources return false → go to namespaced bucket
	// This handles cluster-scoped CRDs gracefully (they'll be in wrong bucket but still work)
	return k8sclient.IsClusterScopedResource(apiVersion, kind)
}
