// internal/k8sconnect/datasource/yaml_split/yaml_split.go
package yaml_split

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// DocumentInfo holds metadata about a parsed document
type DocumentInfo struct {
	Content       string
	SourceFile    string
	DocumentIndex int
	LineNumber    int
	Object        *unstructured.Unstructured
	ParseError    error
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
				Description: "Glob pattern to match YAML files (e.g., './manifests/*.yaml', './configs/**/*.yml'). Supports recursive patterns. Mutually exclusive with 'content'.",
			},
			"manifests": schema.MapAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Map of stable manifest IDs to YAML content. IDs follow the format 'kind.name' (cluster-scoped) or 'kind.namespace.name' (namespaced). Duplicates get numeric suffixes.",
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

	var documents []DocumentInfo
	var sourceID string

	if hasContent {
		// Parse inline content
		content := data.Content.ValueString()
		docs, err := d.parseDocuments(content, "<inline>")
		if err != nil {
			resp.Diagnostics.AddError(
				"Content Parsing Error",
				fmt.Sprintf("Failed to parse inline YAML content: %s", err),
			)
			return
		}
		documents = docs
		sourceID = fmt.Sprintf("content-%s", hashString(content)[:8])
	} else {
		// Handle pattern-based loading
		pattern := data.Pattern.ValueString()
		files, err := d.expandPattern(pattern)
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
			content, err := d.readFile(file)
			if err != nil {
				resp.Diagnostics.AddError(
					"File Read Error",
					fmt.Sprintf("Failed to read file %q: %s", file, err),
				)
				return
			}

			docs, err := d.parseDocuments(content, file)
			if err != nil {
				resp.Diagnostics.AddError(
					"File Parsing Error",
					fmt.Sprintf("Failed to parse YAML in file %q: %s", file, err),
				)
				return
			}
			documents = append(documents, docs...)
		}
		sourceID = fmt.Sprintf("pattern-%s", hashString(pattern)[:8])
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

// Splits and parses YAML documents from content
func (d *yamlSplitDataSource) parseDocuments(content, sourceFile string) ([]DocumentInfo, error) {
	// Split documents using smart separator detection
	rawDocs := d.splitYAMLDocuments(content)

	var documents []DocumentInfo
	var errors []string

	for i, rawDoc := range rawDocs {
		doc := DocumentInfo{
			Content:       rawDoc,
			SourceFile:    sourceFile,
			DocumentIndex: i,
			LineNumber:    d.estimateLineNumber(content, rawDoc),
		}

		// Try to parse as Kubernetes resource
		var obj unstructured.Unstructured
		if err := yaml.Unmarshal([]byte(rawDoc), &obj); err != nil {
			// Document failed to parse - record error but continue
			doc.ParseError = fmt.Errorf("invalid YAML at document %d: %w", i+1, err)
			errors = append(errors, fmt.Sprintf("%s (document %d): %s", sourceFile, i+1, err.Error()))
		} else {
			doc.Object = &obj
		}

		documents = append(documents, doc)
	}

	var err error
	if len(errors) > 0 {
		err = fmt.Errorf("parsing errors: %s", strings.Join(errors, "; "))
	}

	return documents, err
}

// Splits YAML content on document separators
func (d *yamlSplitDataSource) splitYAMLDocuments(content string) []string {
	separatorRegex := regexp.MustCompile(`(?m)^---\s*(?:#.*)?(?:\r?\n|$)`)

	parts := separatorRegex.Split(content, -1)
	var documents []string

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		// Skip empty documents and comment-only documents
		if trimmed != "" && !d.isCommentOnly(trimmed) {
			documents = append(documents, trimmed)
		}
	}

	return documents
}

// isCommentOnly checks if a document contains only comments and whitespace
func (d *yamlSplitDataSource) isCommentOnly(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return false
		}
	}
	return true
}

// estimateLineNumber provides approximate line number for error reporting
func (d *yamlSplitDataSource) estimateLineNumber(fullContent, docContent string) int {
	beforeDoc := strings.Split(fullContent, docContent)[0]
	return strings.Count(beforeDoc, "\n") + 1
}

// generateManifests creates the final manifest map with stable IDs
func (d *yamlSplitDataSource) generateManifests(documents []DocumentInfo) (map[string]types.String, error) {
	manifests := make(map[string]types.String)
	seenIDs := make(map[string]DocumentInfo) // Track which IDs we've seen and where

	for _, doc := range documents {
		if doc.ParseError != nil {
			// Fail fast on parse errors - don't try to work around invalid YAML
			return nil, fmt.Errorf("invalid YAML in %s at document %d (around line %d): %w",
				doc.SourceFile, doc.DocumentIndex+1, doc.LineNumber, doc.ParseError)
		}

		id := d.generateBaseID(doc.Object)

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

// generateBaseID creates a human-readable, stable ID for a Kubernetes resource
func (d *yamlSplitDataSource) generateBaseID(obj *unstructured.Unstructured) string {
	kind := strings.ToLower(obj.GetKind())
	name := obj.GetName()
	namespace := obj.GetNamespace()

	// Handle edge cases
	if kind == "" {
		kind = "unknown"
	}
	if name == "" {
		name = "unnamed"
	}

	// Create stable ID: kind.name or kind.namespace.name
	if namespace == "" {
		return fmt.Sprintf("%s.%s", kind, name)
	}
	return fmt.Sprintf("%s.%s.%s", kind, namespace, name)
}

// expandPattern resolves glob patterns, including recursive patterns
func (d *yamlSplitDataSource) expandPattern(pattern string) ([]string, error) {
	// Handle recursive patterns with **
	if strings.Contains(pattern, "**") {
		return d.walkPattern(pattern)
	}

	// Standard glob
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	// Filter to only include files (not directories)
	var files []string
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && !info.IsDir() {
			files = append(files, match)
		}
	}

	// Sort for consistent ordering
	sort.Strings(files)
	return files, nil
}

// walkPattern handles recursive directory patterns with **
func (d *yamlSplitDataSource) walkPattern(pattern string) ([]string, error) {
	var files []string

	// Convert ** glob pattern to a more usable form
	err := filepath.WalkDir(".", func(path string, dirEntry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if dirEntry.IsDir() {
			return nil
		}

		// Use custom pattern matching for ** patterns
		if d.matchesPattern(pattern, path) {
			files = append(files, path)
		}

		return nil
	})

	sort.Strings(files)
	return files, err
}

// matchesPattern checks if a file path matches a pattern with ** support
func (d *yamlSplitDataSource) matchesPattern(pattern, path string) bool {
	// Handle ** patterns
	if strings.Contains(pattern, "**") {
		// For now, handle the common case: **/*.ext
		if strings.HasPrefix(pattern, "**/") {
			suffix := pattern[3:] // Remove "**/"
			matched, err := filepath.Match(suffix, filepath.Base(path))
			return err == nil && matched
		}
		// For other ** patterns, try a different approach
		// Convert ** to match any number of path segments
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			prefix := strings.TrimSuffix(parts[0], "/")
			suffix := strings.TrimPrefix(parts[1], "/")

			// Check prefix
			if prefix != "" && !strings.HasPrefix(path, prefix) {
				return false
			}

			// Check suffix using glob on filename
			if suffix != "" {
				matched, err := filepath.Match(suffix, filepath.Base(path))
				return err == nil && matched
			}
			return true
		}
	}

	// Fallback to standard filepath.Match for non-recursive patterns
	matched, err := filepath.Match(pattern, path)
	return err == nil && matched
}

// readFile reads a file and returns its content as string
func (d *yamlSplitDataSource) readFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file %q: %w", path, err)
	}
	return string(content), nil
}

// hashString creates a SHA256 hash of the input string
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
