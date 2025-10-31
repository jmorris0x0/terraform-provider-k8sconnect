package object

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

// loadKubeconfig finds and loads the kubeconfig file
// Returns the path, file contents, and true on success; empty values and false on error
func (r *objectResource) loadKubeconfig(ctx context.Context, resp *resource.ImportStateResponse) (string, []byte, bool) {
	// Read kubeconfig from KUBECONFIG env var or default location
	kubeconfigPath := os.Getenv("KUBECONFIG")

	if kubeconfigPath == "" {
		homeDir := os.Getenv("HOME")

		if homeDir == "" {
			resp.Diagnostics.AddError(
				"Import Failed: KUBECONFIG Not Found",
				"KUBECONFIG environment variable is not set and HOME directory could not be determined.\n\n"+
					"Set KUBECONFIG environment variable:\n"+
					"  export KUBECONFIG=~/.kube/config\n"+
					"  terraform import k8sconnect_object.example \"prod/default/Pod/nginx\"",
			)
			return "", nil, false
		}
		kubeconfigPath = filepath.Join(homeDir, ".kube", "config")
	}

	tflog.Info(ctx, "Using kubeconfig", map[string]interface{}{
		"path": kubeconfigPath,
	})

	// Check if kubeconfig file exists
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		resp.Diagnostics.AddError(
			"Import Failed: Kubeconfig File Not Found",
			fmt.Sprintf("Kubeconfig file not found at: %s\n\n"+
				"Ensure your kubeconfig file exists or set KUBECONFIG environment variable:\n"+
				"  export KUBECONFIG=/path/to/your/kubeconfig\n"+
				"  terraform import k8sconnect_object.example \"prod/default/Pod/nginx\"", kubeconfigPath),
		)
		return "", nil, false
	}

	// Read the kubeconfig file contents
	kubeconfigData, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: Cannot Read Kubeconfig",
			fmt.Sprintf("Failed to read kubeconfig file at %s: %s", kubeconfigPath, err.Error()),
		)
		return "", nil, false
	}

	return kubeconfigPath, kubeconfigData, true
}

// createImportClient creates a Kubernetes client for import operations
// Returns the client and true on success; nil and false on error
func (r *objectResource) createImportClient(ctx context.Context, kubeconfigData []byte, kubeconfigPath, kubeContext string, resp *resource.ImportStateResponse) (k8sclient.K8sClient, bool) {
	// Create temporary connection model for import
	tempConn := auth.ClusterModel{
		Kubeconfig: types.StringValue(string(kubeconfigData)),
		Context:    types.StringValue(kubeContext),
	}

	// Create REST config from connection model
	restConfig, err := auth.CreateRESTConfig(ctx, tempConn)
	if err != nil {
		// Provide context-specific error messages
		if strings.Contains(err.Error(), "context") && strings.Contains(err.Error(), "not found") {
			resp.Diagnostics.AddError(
				"Import Failed: Context Not Found",
				fmt.Sprintf("Context \"%s\" not found in kubeconfig.\n\n"+
					"Available contexts:\n"+
					"  kubectl config get-contexts\n\n"+
					"Details: %s", kubeContext, err.Error()),
			)
		} else if strings.Contains(err.Error(), "kubeconfig") {
			resp.Diagnostics.AddError(
				"Import Failed: Invalid Kubeconfig",
				fmt.Sprintf("Failed to parse kubeconfig file at %s.\n\n"+
					"Ensure your kubeconfig is valid:\n"+
					"  kubectl config view\n\n"+
					"Details: %s", kubeconfigPath, err.Error()),
			)
		} else {
			resp.Diagnostics.AddError(
				"Import Failed: Connection Error",
				fmt.Sprintf("Failed to create Kubernetes client from kubeconfig.\n\n"+
					"This usually means:\n"+
					"1. Invalid kubeconfig file\n"+
					"2. Cluster is unreachable\n"+
					"3. Context credentials have expired\n\n"+
					"Details: %s", err.Error()),
			)
		}
		return nil, false
	}

	// Create K8s client
	client, err := k8sclient.NewDynamicK8sClient(restConfig)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: Client Creation Error",
			fmt.Sprintf("Failed to create Kubernetes client: %s", err.Error()),
		)
		return nil, false
	}

	return client, true
}

// fetchImportResource fetches the resource from Kubernetes for import
// Returns the resource and true on success; nil and false on error
func (r *objectResource) fetchImportResource(ctx context.Context, client k8sclient.K8sClient, apiVersion, kind, namespace, name, kubeContext string, resp *resource.ImportStateResponse) (*unstructured.Unstructured, bool) {
	// Discover GVR using apiVersion and kind (both required)
	tflog.Info(ctx, "Discovering GVR for import", map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"namespace":  namespace,
		"name":       name,
	})

	gvr, err := client.DiscoverGVR(ctx, apiVersion, kind)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: Resource Type Discovery",
			fmt.Sprintf("Failed to discover resource type for kind %q in apiVersion %q: %s", kind, apiVersion, err.Error()),
		)
		return nil, false
	}

	tflog.Info(ctx, "Fetching resource from cluster", map[string]interface{}{
		"gvr":       gvr.String(),
		"namespace": namespace,
		"name":      name,
	})

	liveObj, err := client.Get(ctx, gvr, namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			// Build kubectl command (with or without namespace)
			kubectlCmd := fmt.Sprintf("kubectl get %s %s", strings.ToLower(kind), name)
			if namespace != "" {
				kubectlCmd += fmt.Sprintf(" -n %s", namespace)
			}
			kubectlCmd += fmt.Sprintf(" --context=%s", kubeContext)

			resourceDesc := name
			if namespace != "" {
				resourceDesc = fmt.Sprintf("%s/%s", namespace, name)
			}

			resp.Diagnostics.AddError(
				"Import Failed: Resource not found",
				fmt.Sprintf("Resource %s (kind: %s) not found in context %q.\n\n"+
					"Verify that:\n"+
					"- The resource exists: %s\n"+
					"- You have permission to read this resource",
					resourceDesc, kind, kubeContext, kubectlCmd),
			)
		} else {
			resp.Diagnostics.AddError(
				"Import Failed",
				fmt.Sprintf("Failed to fetch resource: %s", err.Error()),
			)
		}
		return nil, false
	}

	return liveObj, true
}

// extractProjectionAndOwnership extracts YAML, projection, and ownership from imported resource
// Returns yamlBytes, projectionMap, managedFieldsMap, paths, and true on success; empty values and false on error
func (r *objectResource) extractProjectionAndOwnership(ctx context.Context, liveObj *unstructured.Unstructured, resp *resource.ImportStateResponse) ([]byte, types.Map, types.Map, []string, bool) {
	// Convert to YAML for state
	yamlBytes, err := r.objectToYAML(liveObj)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: YAML conversion error",
			fmt.Sprintf("Failed to convert resource to YAML: %s", err.Error()),
		)
		return nil, types.MapNull(types.StringType), types.MapNull(types.StringType), nil, false
	}

	// Extract field paths from the imported object
	paths := extractAllFieldsFromYAML(liveObj.Object, "")

	// Project the current state for managed fields
	projection, err := projectFields(liveObj.Object, paths)
	if err != nil {
		resp.Diagnostics.AddError("Projection Failed",
			fmt.Sprintf("Failed to project managed fields during import: %s", err))
		return nil, types.MapNull(types.StringType), types.MapNull(types.StringType), nil, false
	}

	// Convert projection to flat map for clean diff display
	projectionMap := flattenProjectionToMap(projection, paths)

	// Convert projection to types.Map
	projectionMapValue, projDiags := types.MapValueFrom(ctx, types.StringType, projectionMap)
	if projDiags.HasError() {
		tflog.Warn(ctx, "Failed to convert projection to map during import", map[string]interface{}{
			"diagnostics": projDiags,
		})
		// Set empty map on error
		projectionMapValue, _ = types.MapValueFrom(ctx, types.StringType, map[string]string{})
	}

	// Extract field ownership from the imported object using the new flattening approach
	ownership := fieldmanagement.ExtractAllManagedFields(liveObj)

	// Filter out status fields - they're always owned by controllers and provide no actionable information
	filteredOwnership := make(map[string][]string)
	for path, managers := range ownership {
		if strings.HasPrefix(path, "status.") || path == "status" {
			continue
		}
		filteredOwnership[path] = managers
	}

	// Flatten using the common logic
	ownershipMap := fieldmanagement.FlattenManagedFields(filteredOwnership)

	managedFieldsMap, ownershipDiags := types.MapValueFrom(ctx, types.StringType, ownershipMap)
	if ownershipDiags.HasError() {
		tflog.Warn(ctx, "Failed to convert field ownership during import", map[string]interface{}{
			"diagnostics": ownershipDiags,
		})
		// Set empty map on error
		managedFieldsMap, _ = types.MapValueFrom(ctx, types.StringType, map[string]string{})
	}

	return yamlBytes, projectionMapValue, managedFieldsMap, paths, true
}

// buildImportState builds the final import state and sets it on the response
// Returns true on success; false on error
func (r *objectResource) buildImportState(ctx context.Context, resourceID string, yamlBytes []byte, kubeconfigData []byte, kubeContext string, liveObj *unstructured.Unstructured, projectionMapValue, managedFieldsMap types.Map, kubeconfigPath string, namespace, name, kind string, paths []string, resp *resource.ImportStateResponse) bool {
	// Create connection model for import - use the file contents, not the path
	conn := auth.ClusterModel{
		Host:                 types.StringNull(),
		ClusterCACertificate: types.StringNull(),
		Kubeconfig:           types.StringValue(string(kubeconfigData)), // Use contents, not path!
		Context:              types.StringValue(kubeContext),
		Exec:                 nil,
	}

	// Convert to types.Object
	connectionObj, err := r.convertConnectionToObject(ctx, conn)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: Connection Conversion Error",
			fmt.Sprintf("Failed to convert connection model: %s", err.Error()),
		)
		return false
	}

	// Populate object_ref from imported resource
	objRef := objectRefModel{
		APIVersion: types.StringValue(liveObj.GetAPIVersion()),
		Kind:       types.StringValue(liveObj.GetKind()),
		Name:       types.StringValue(liveObj.GetName()),
	}

	// Namespace is optional (null for cluster-scoped resources)
	if ns := liveObj.GetNamespace(); ns != "" {
		objRef.Namespace = types.StringValue(ns)
	} else {
		objRef.Namespace = types.StringNull()
	}

	// Convert object_ref to types.Object
	objRefValue, objRefDiags := types.ObjectValueFrom(ctx, map[string]attr.Type{
		"api_version": types.StringType,
		"kind":        types.StringType,
		"name":        types.StringType,
		"namespace":   types.StringType,
	}, objRef)

	if objRefDiags.HasError() {
		resp.Diagnostics.AddError(
			"Import Failed: ObjectRef Conversion Error",
			fmt.Sprintf("Failed to convert object_ref: %v", objRefDiags),
		)
		return false
	}

	// Create imported data with managed state projection
	importedData := objectResourceModel{
		ID:                     types.StringValue(resourceID),
		YAMLBody:               types.StringValue(string(yamlBytes)),
		Cluster:                connectionObj,
		DeleteProtection:       types.BoolValue(false),
		IgnoreFields:           types.ListNull(types.StringType),
		ManagedStateProjection: projectionMapValue,
		ManagedFields:          managedFieldsMap,
		ObjectRef:              objRefValue,
	}

	diags := resp.State.Set(ctx, &importedData)
	resp.Diagnostics.Append(diags...)

	// Store imported_without_annotations in private state
	diags = resp.Private.SetKey(ctx, "imported_without_annotations", []byte("true"))
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return false
	}

	tflog.Info(ctx, "import completed with managed fields tracking", map[string]interface{}{
		"id":            resourceID,
		"kind":          kind,
		"name":          name,
		"namespace":     namespace,
		"kubeconfig":    kubeconfigPath,
		"context":       kubeContext,
		"managed_paths": len(paths),
	})

	return true
}

// validateImportIDParts validates the parsed import ID components
// Returns true if validation succeeds, false if any errors were added to resp.Diagnostics
func (r *objectResource) validateImportIDParts(kubeContext, kind, name string, resp *resource.ImportStateResponse) bool {
	if kubeContext == "" {
		resp.Diagnostics.AddError(
			"Import Failed: Missing Context",
			"The import ID must include a kubeconfig context as the first part.\n\n"+
				"Format:\n"+
				"  Namespaced: context:namespace:kind:name\n"+
				"  Cluster-scoped: context:kind:name\n\n"+
				"Available contexts: kubectl config get-contexts",
		)
		return false
	}
	if kind == "" {
		resp.Diagnostics.AddError(
			"Import Failed: Missing Kind",
			"The resource kind cannot be empty in the import ID.\n\n"+
				"Example: prod:default:Deployment:nginx",
		)
		return false
	}
	if name == "" {
		resp.Diagnostics.AddError(
			"Import Failed: Missing Name",
			"The resource name cannot be empty in the import ID.\n\n"+
				"Example: prod:default:Deployment:nginx",
		)
		return false
	}
	return true
}

// ImportState method implementing kubeconfig strategy with managed fields tracking
func (r *objectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	tflog.Info(ctx, "ImportState called", map[string]interface{}{"import_id": req.ID})

	// Parse import ID: "context:namespace:kind:name" or "context:kind:name" for cluster-scoped
	kubeContext, namespace, apiVersion, kind, name, err := r.parseImportID(req.ID)

	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid Import ID Format",
			fmt.Sprintf("%s\n\n"+
				"Import ID format:\n"+
				"  Namespaced: context:namespace:kind:name\n"+
				"  Cluster-scoped: context:kind:name\n\n"+
				"Examples:\n"+
				"  prod:default:Deployment:nginx\n"+
				"  prod:kube-system:Service:coredns\n"+
				"  prod:Namespace:my-namespace\n"+
				"  prod:ClusterRole:admin\n\n"+
				"For disambiguation, include apiVersion:\n"+
				"  prod:default:apps/v1/Deployment:nginx\n"+
				"  prod:networking.k8s.io/v1/Ingress:my-ingress\n"+
				"  prod:stable.example.com/v1/MyCustomResource:instance-1",
				err.Error()),
		)
		return
	}

	// Validate required parts
	if !r.validateImportIDParts(kubeContext, kind, name, resp) {
		return
	}

	// Load kubeconfig file
	kubeconfigPath, kubeconfigData, ok := r.loadKubeconfig(ctx, resp)
	if !ok {
		return
	}

	tflog.Info(ctx, "import using kubeconfig", map[string]interface{}{
		"path":      kubeconfigPath,
		"context":   kubeContext,
		"kind":      kind,
		"name":      name,
		"namespace": namespace,
	})

	// Create Kubernetes client for import
	client, ok := r.createImportClient(ctx, kubeconfigData, kubeconfigPath, kubeContext, resp)
	if !ok {
		return
	}

	// Fetch the resource from Kubernetes
	liveObj, ok := r.fetchImportResource(ctx, client, apiVersion, kind, namespace, name, kubeContext, resp)
	if !ok {
		return
	}

	// Check for existing ownership and generate ID accordingly
	existingID := r.getOwnershipID(liveObj)

	var resourceID string

	if existingID != "" {
		// Resource already managed - use existing ID and warn
		resourceID = existingID

		resp.Diagnostics.AddWarning(
			"Importing Already-Managed Resource",
			fmt.Sprintf("This resource is already managed by k8sconnect (ID: %s).\n"+
				"The existing ownership will be maintained.\n"+
				"If this resource is managed by another Terraform state, you may experience conflicts.\n"+
				"To transfer ownership cleanly, remove the annotation first:\n"+
				"kubectl annotate %s %s k8sconnect.terraform.io/terraform-id-",
				existingID, strings.ToLower(kind), name),
		)

		tflog.Warn(ctx, "importing already-managed resource", map[string]interface{}{
			"existing_id": existingID,
			"kind":        kind,
			"name":        name,
			"namespace":   namespace,
		})
	} else {
		// Resource not yet managed - generate new ID
		resourceID = common.GenerateID()
	}

	// Extract YAML, projection, and ownership
	yamlBytes, projectionMapValue, managedFieldsMap, paths, ok := r.extractProjectionAndOwnership(ctx, liveObj, resp)
	if !ok {
		return
	}

	// Build and set final import state
	if !r.buildImportState(ctx, resourceID, yamlBytes, kubeconfigData, kubeContext, liveObj, projectionMapValue, managedFieldsMap, kubeconfigPath, namespace, name, kind, paths, resp) {
		return
	}
}

// parseImportID parses the import ID and extracts components
// Format: context:namespace:kind:name (namespaced) or context:kind:name (cluster-scoped)
// The kind field may optionally include apiVersion: apiVersion/kind
func (r *objectResource) parseImportID(importID string) (context, namespace, apiVersion, kind, name string, err error) {
	parts := strings.Split(importID, ":")

	if len(parts) < 3 || len(parts) > 4 {
		return "", "", "", "", "", fmt.Errorf(
			"expected 3 or 4 colon-separated parts, got %d\n\n"+
				"Valid formats:\n"+
				"  Namespaced: context:namespace:kind:name\n"+
				"  Cluster-scoped: context:kind:name",
			len(parts))
	}

	var kindPart string

	switch len(parts) {
	case 3:
		// Cluster-scoped: "context:kind:name"
		context = parts[0]
		kindPart = parts[1]
		name = parts[2]
		namespace = ""

	case 4:
		// Namespaced: "context:namespace:kind:name"
		context = parts[0]
		namespace = parts[1]
		kindPart = parts[2]
		name = parts[3]

	default:
		return "", "", "", "", "", fmt.Errorf("invalid number of parts: %d", len(parts))
	}

	// Validate that none of the parts are empty
	if context == "" {
		return "", "", "", "", "", fmt.Errorf("context cannot be empty")
	}
	if kindPart == "" {
		return "", "", "", "", "", fmt.Errorf("kind cannot be empty")
	}
	if name == "" {
		return "", "", "", "", "", fmt.Errorf("name cannot be empty")
	}

	// Parse the kind field - it MUST contain apiVersion: "apiVersion/kind"
	slashIndex := strings.LastIndex(kindPart, "/")
	if slashIndex == -1 {
		return "", "", "", "", "", fmt.Errorf(
			"kind field must include apiVersion: apiVersion/kind\n\n"+
				"Examples:\n"+
				"  v1/Pod (core resources)\n"+
				"  apps/v1/Deployment\n"+
				"  networking.k8s.io/v1/Ingress\n"+
				"  stable.example.com/v1/MyCustomResource\n\n"+
				"To find the apiVersion:\n"+
				"  kubectl get %s %s -o jsonpath='{.apiVersion}'",
			kindPart, name)
	}

	// Split on the LAST slash to handle cases like "networking.k8s.io/v1/Ingress"
	apiVersion = kindPart[:slashIndex]
	kind = kindPart[slashIndex+1:]

	if apiVersion == "" {
		return "", "", "", "", "", fmt.Errorf("apiVersion cannot be empty")
	}
	if kind == "" {
		return "", "", "", "", "", fmt.Errorf("kind cannot be empty")
	}

	return context, namespace, apiVersion, kind, name, nil
}
