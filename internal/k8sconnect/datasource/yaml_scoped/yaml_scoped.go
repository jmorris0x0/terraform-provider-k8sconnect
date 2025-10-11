// internal/k8sconnect/datasource/yaml_scoped/yaml_scoped.go
package yaml_scoped

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/yaml_common"
)

var _ datasource.DataSource = (*yamlScopedDataSource)(nil)

type yamlScopedDataSource struct{}

type yamlScopedDataSourceModel struct {
	ID            types.String            `tfsdk:"id"`
	Content       types.String            `tfsdk:"content"`
	Pattern       types.String            `tfsdk:"pattern"`
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
				Description: "Raw YAML content containing one or more Kubernetes manifests separated by '---'. Mutually exclusive with 'pattern'.",
			},
			"pattern": schema.StringAttribute{
				Optional:    true,
				Description: "Glob pattern to match YAML files (e.g., './manifests/*.yaml', './configs/**/*.yml'). Supports recursive patterns. Mutually exclusive with 'content'.",
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
		} else if isClusterScopedKind(kind) {
			clusterScoped[id] = yamlValue
		} else {
			namespaced[id] = yamlValue
		}
	}

	return crds, clusterScoped, namespaced, nil
}

// isClusterScopedKind returns true if the resource kind is cluster-scoped (not namespaced)
// This is a hardcoded list of well-known cluster-scoped resources
func isClusterScopedKind(kind string) bool {
	// Normalize to lowercase for case-insensitive comparison
	kind = strings.ToLower(kind)

	clusterScopedKinds := map[string]bool{
		// Core cluster-scoped resources
		"namespace":                      true,
		"node":                           true,
		"persistentvolume":               true,
		"clusterrole":                    true,
		"clusterrolebinding":             true,
		"storageclass":                   true,
		"priorityclass":                  true,
		"runtimeclass":                   true,
		"volumeattachment":               true,
		"csidriver":                      true,
		"csinode":                        true,
		"csistoragecapacity":             true,
		"mutatingwebhookconfiguration":   true,
		"validatingwebhookconfiguration": true,
		"apiservice":                     true,
		"certificatesigningrequest":      true,
		"flowschema":                     true,
		"prioritylevelconfiguration":     true,
		"ingressclass":                   true,
		"podsecuritypolicy":              true, // Deprecated but still exists
		"selfsubjectaccessreview":        true,
		"selfsubjectrulesreview":         true,
		"subjectaccessreview":            true,
		"tokenreview":                    true,
	}

	return clusterScopedKinds[kind]
}
