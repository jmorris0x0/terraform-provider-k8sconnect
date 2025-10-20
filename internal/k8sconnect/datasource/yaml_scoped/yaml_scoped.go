package yaml_scoped

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/validators"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/yaml_common"
)

var _ datasource.DataSource = (*yamlScopedDataSource)(nil)
var _ datasource.DataSourceWithConfigValidators = (*yamlScopedDataSource)(nil)

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

// ConfigValidators implements datasource.DataSourceWithConfigValidators
func (d *yamlScopedDataSource) ConfigValidators(ctx context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		validators.ExactlyOneOf{
			Attribute1: "content",
			Attribute2: "pattern",
		},
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

	// Determine which input mode to use (validation handled by ConfigValidators)
	hasContent := !data.Content.IsNull() && data.Content.ValueString() != ""

	// Load documents from either inline content or file pattern
	documents, sourceID, err := yaml_common.LoadDocuments(
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
	// Extract group from apiVersion
	// apiVersion formats:
	// - "v1" -> no group (core)
	// - "apps/v1" -> group is "apps"
	// - "rbac.authorization.k8s.io/v1" -> group is "rbac.authorization.k8s.io"
	var group string
	if strings.Contains(apiVersion, "/") {
		parts := strings.Split(apiVersion, "/")
		group = strings.ToLower(parts[0])
	}

	// Normalize kind to lowercase
	kind = strings.ToLower(kind)

	// Build lookup key: "group.kind" or just "kind" for core resources
	var lookupKey string
	if group != "" {
		lookupKey = group + "." + kind
	} else {
		lookupKey = kind
	}

	// Hardcoded list of cluster-scoped resources (29 resources)
	// Based on kubectl api-resources --namespaced=false output and official K8s API reference
	// Excludes deprecated ComponentStatus and removed PodSecurityPolicy
	clusterScopedResources := map[string]bool{
		// Core (apiVersion: v1)
		"namespace":        true,
		"node":             true,
		"persistentvolume": true,

		// admissionregistration.k8s.io/v1
		"admissionregistration.k8s.io.mutatingwebhookconfiguration":     true,
		"admissionregistration.k8s.io.validatingwebhookconfiguration":   true,
		"admissionregistration.k8s.io.validatingadmissionpolicy":        true,
		"admissionregistration.k8s.io.validatingadmissionpolicybinding": true,

		// apiextensions.k8s.io/v1
		"apiextensions.k8s.io.customresourcedefinition": true,

		// apiregistration.k8s.io/v1
		"apiregistration.k8s.io.apiservice": true,

		// authentication.k8s.io/v1
		"authentication.k8s.io.tokenreview":       true,
		"authentication.k8s.io.selfsubjectreview": true,

		// authorization.k8s.io/v1
		"authorization.k8s.io.selfsubjectaccessreview": true,
		"authorization.k8s.io.selfsubjectrulesreview":  true,
		"authorization.k8s.io.subjectaccessreview":     true,

		// certificates.k8s.io/v1
		"certificates.k8s.io.certificatesigningrequest": true,

		// flowcontrol.apiserver.k8s.io/v1
		"flowcontrol.apiserver.k8s.io.flowschema":                 true,
		"flowcontrol.apiserver.k8s.io.prioritylevelconfiguration": true,

		// networking.k8s.io/v1
		"networking.k8s.io.ingressclass": true,

		// node.k8s.io/v1
		"node.k8s.io.runtimeclass": true,

		// rbac.authorization.k8s.io/v1
		"rbac.authorization.k8s.io.clusterrole":        true,
		"rbac.authorization.k8s.io.clusterrolebinding": true,

		// scheduling.k8s.io/v1
		"scheduling.k8s.io.priorityclass": true,

		// storage.k8s.io/v1
		"storage.k8s.io.csidriver":        true,
		"storage.k8s.io.csinode":          true,
		"storage.k8s.io.storageclass":     true,
		"storage.k8s.io.volumeattachment": true,
	}

	// Smart fallback: unknown resources return false → go to namespaced bucket
	// This handles cluster-scoped CRDs gracefully (they'll be in wrong bucket but still work)
	return clusterScopedResources[lookupKey]
}
