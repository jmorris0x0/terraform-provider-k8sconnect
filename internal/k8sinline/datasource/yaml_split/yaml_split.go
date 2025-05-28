// internal/k8sinline/datasource/yaml_split/yaml_split.go
package yaml_split

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
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
		Description: "Splits multi-document YAML content into individual manifests with stable, human-readable IDs.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Data source identifier.",
			},
			"content": schema.StringAttribute{
				Optional:    true,
				Description: "Raw YAML content containing one or more Kubernetes manifests separated by '---'.",
			},
			"pattern": schema.StringAttribute{
				Optional:    true,
				Description: "Glob pattern to match YAML files (e.g., './manifests/*.yaml'). Mutually exclusive with 'content'.",
			},
			"manifests": schema.MapAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Map of stable manifest IDs to YAML content. IDs are in format: kind.name or kind.namespace.name",
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

	var yamlContent string
	var sourceID string

	if hasContent {
		yamlContent = data.Content.ValueString()
		sourceID = fmt.Sprintf("content-%s", hashString(yamlContent)[:8])
	} else {
		// Handle pattern-based loading
		pattern := data.Pattern.ValueString()
		files, err := filepath.Glob(pattern)
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
				fmt.Sprintf("No files matched pattern %q", pattern),
			)
			return
		}

		// Combine all files with --- separators
		var combined []string
		for _, file := range files {
			content, err := readFile(file)
			if err != nil {
				resp.Diagnostics.AddError(
					"File Read Error",
					fmt.Sprintf("Failed to read file %q: %s", file, err),
				)
				return
			}
			combined = append(combined, content)
		}
		yamlContent = strings.Join(combined, "\n---\n")
		sourceID = fmt.Sprintf("pattern-%s", hashString(pattern)[:8])
	}

	// Split YAML documents
	manifests, err := d.splitYAML(yamlContent)
	if err != nil {
		resp.Diagnostics.AddError(
			"YAML Processing Error",
			fmt.Sprintf("Failed to process YAML: %s", err),
		)
		return
	}

	// Set results
	data.ID = types.StringValue(sourceID)
	data.Manifests = manifests

	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

// splitYAML processes multi-document YAML and returns a map with stable IDs
func (d *yamlSplitDataSource) splitYAML(content string) (map[string]types.String, error) {
	// Split on YAML document separator
	documents := strings.Split(content, "---")
	manifests := make(map[string]types.String)

	for i, doc := range documents {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue // Skip empty documents
		}

		// Try to parse as Kubernetes resource to extract metadata
		var obj unstructured.Unstructured
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			// If it's not valid YAML, use position-based ID
			id := fmt.Sprintf("doc-%d", i)
			manifests[id] = types.StringValue(doc)
			continue
		}

		// Generate stable, human-readable ID
		id := d.generateStableID(&obj, i)
		manifests[id] = types.StringValue(doc)
	}

	return manifests, nil
}

// generateStableID creates human-readable, stable IDs for resources
func (d *yamlSplitDataSource) generateStableID(obj *unstructured.Unstructured, fallbackIndex int) string {
	kind := obj.GetKind()
	name := obj.GetName()
	namespace := obj.GetNamespace()

	// Handle edge cases
	if kind == "" {
		return fmt.Sprintf("unknown-%d", fallbackIndex)
	}
	if name == "" {
		return fmt.Sprintf("%s-%d", strings.ToLower(kind), fallbackIndex)
	}

	// Create stable ID: kind.name or kind.namespace.name
	if namespace == "" {
		return fmt.Sprintf("%s.%s", strings.ToLower(kind), name)
	}
	return fmt.Sprintf("%s.%s.%s", strings.ToLower(kind), namespace, name)
}

// Helper functions
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func readFile(path string) (string, error) {
	// Implementation would read file from filesystem
	// This is a placeholder - you'd use os.ReadFile or similar
	return "", fmt.Errorf("not implemented")
}
