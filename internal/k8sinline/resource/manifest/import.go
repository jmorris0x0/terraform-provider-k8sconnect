// internal/k8sinline/resource/manifest/import.go
package manifest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
)

// ImportState method implementing kubeconfig strategy with managed fields tracking
// ImportState method implementing kubeconfig strategy with managed fields tracking
func (r *manifestResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Parse import ID: "context/namespace/kind/name" or "context/kind/name" for cluster-scoped
	kubeContext, namespace, kind, name, err := r.parseImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected format: <context>/<namespace>/<kind>/<n> or <context>/<kind>/<n>\n\nExamples:\n"+
				"  prod/default/Pod/nginx\n"+
				"  staging/kube-system/Service/coredns\n"+
				"  prod/Namespace/my-namespace\n"+
				"  dev/ClusterRole/admin\n\nError: %s", err.Error()),
		)
		return
	}

	// Validate required parts
	if kubeContext == "" {
		resp.Diagnostics.AddError(
			"Import Failed: Missing Context",
			"The import ID must include a kubeconfig context as the first part.\n\n"+
				"Format: <context>/<namespace>/<kind>/<n> or <context>/<kind>/<n>\n\n"+
				"Available contexts can be found with: kubectl config get-contexts",
		)
		return
	}
	if kind == "" {
		resp.Diagnostics.AddError(
			"Import Failed: Missing Kind",
			"The resource kind cannot be empty in the import ID.",
		)
		return
	}
	if name == "" {
		resp.Diagnostics.AddError(
			"Import Failed: Missing Name",
			"The resource name cannot be empty in the import ID.",
		)
		return
	}

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
					"  terraform import k8sinline_manifest.example \"prod/default/Pod/nginx\"",
			)
			return
		}
		kubeconfigPath = filepath.Join(homeDir, ".kube", "config")
	}

	// Check if kubeconfig file exists
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		resp.Diagnostics.AddError(
			"Import Failed: Kubeconfig File Not Found",
			fmt.Sprintf("Kubeconfig file not found at: %s\n\n"+
				"Ensure your kubeconfig file exists or set KUBECONFIG environment variable:\n"+
				"  export KUBECONFIG=/path/to/your/kubeconfig\n"+
				"  terraform import k8sinline_manifest.example \"prod/default/Pod/nginx\"", kubeconfigPath),
		)
		return
	}

	tflog.Debug(ctx, "import using kubeconfig", map[string]interface{}{
		"path":      kubeconfigPath,
		"context":   kubeContext,
		"kind":      kind,
		"name":      name,
		"namespace": namespace,
	})

	// Create K8s client using kubeconfig file and context
	client, err := k8sclient.NewDynamicK8sClientFromKubeconfigFile(kubeconfigPath, kubeContext)
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
		return
	}

	// Discover GVR from kind and fetch the resource
	_, liveObj, err := client.GetGVRFromKind(ctx, kind, namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			resp.Diagnostics.AddError(
				"Import Failed: Resource not found",
				fmt.Sprintf("Resource %s/%s (kind: %s) not found in context %q.\n\n"+
					"Verify that:\n"+
					"- The resource exists: kubectl get %s %s -n %s --context=%s\n"+
					"- You have permission to read this resource",
					namespace, name, kind, kubeContext,
					strings.ToLower(kind), name, namespace, kubeContext),
			)
		} else {
			resp.Diagnostics.AddError(
				"Import Failed",
				fmt.Sprintf("Failed to fetch resource: %s", err.Error()),
			)
		}
		return
	}

	// Check for existing ownership and generate ID accordingly
	existingID := r.getOwnershipID(liveObj)
	var resourceID string

	if existingID != "" {
		// Resource already managed by k8sinline - use existing ID
		resourceID = existingID
		tflog.Warn(ctx, "importing resource already managed by k8sinline", map[string]interface{}{
			"terraform_id": resourceID,
			"kind":         kind,
			"name":         name,
			"namespace":    namespace,
			"context":      kubeContext,
		})
	} else {
		// Resource not managed by k8sinline - generate new ID and apply ownership
		resourceID = r.generateID()

		// Create a minimal object with ONLY the fields needed for annotation update
		annotationObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": liveObj.GetAPIVersion(),
				"kind":       liveObj.GetKind(),
				"metadata": map[string]interface{}{
					"name":      liveObj.GetName(),
					"namespace": liveObj.GetNamespace(),
				},
			},
		}

		// Set the ownership annotations on the minimal object
		r.setOwnershipAnnotation(annotationObj, resourceID)

		// Apply ONLY the annotation update (not the full liveObj)
		err = client.Apply(ctx, annotationObj, k8sclient.ApplyOptions{
			FieldManager: "k8sinline-import",
			Force:        true,
		})
		if err != nil {
			resp.Diagnostics.AddError(
				"Import Failed: Could not apply ownership",
				fmt.Sprintf("Failed to set ownership annotation on resource: %s", err),
			)
			return
		}

		tflog.Info(ctx, "importing unmanaged resource and applying ownership", map[string]interface{}{
			"terraform_id": resourceID,
			"kind":         kind,
			"name":         name,
			"namespace":    namespace,
			"context":      kubeContext,
		})
	}

	// Convert to YAML for state
	yamlBytes, err := r.objectToYAML(liveObj)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: YAML conversion error",
			fmt.Sprintf("Failed to convert resource to YAML: %s", err.Error()),
		)
		return
	}

	// NEW: Extract field paths from the imported object
	paths := extractFieldPaths(liveObj.Object, "")

	// NEW: Project the current state for managed fields
	projection, err := projectFields(liveObj.Object, paths)
	if err != nil {
		resp.Diagnostics.AddError("Projection Failed",
			fmt.Sprintf("Failed to project managed fields during import: %s", err))
		return
	}

	// NEW: Convert projection to JSON
	projectionJSON, err := toJSON(projection)
	if err != nil {
		resp.Diagnostics.AddError("JSON Conversion Failed",
			fmt.Sprintf("Failed to convert projection to JSON during import: %s", err))
		return
	}

	// Create connection model for import
	conn := auth.ClusterConnectionModel{
		Host:                 types.StringNull(),
		ClusterCACertificate: types.StringNull(),
		KubeconfigFile:       types.StringValue(kubeconfigPath),
		KubeconfigRaw:        types.StringNull(),
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
		return
	}

	// Create imported data with managed state projection
	importedData := manifestResourceModel{
		ID:                     types.StringValue(resourceID),
		YAMLBody:               types.StringValue(string(yamlBytes)),
		ClusterConnection:      connectionObj,
		DeleteProtection:       types.BoolValue(false),
		ManagedStateProjection: types.StringValue(projectionJSON), // NEW: Set the projection
	}

	diags := resp.State.Set(ctx, &importedData)
	resp.Diagnostics.Append(diags...)

	tflog.Info(ctx, "import completed with managed fields tracking", map[string]interface{}{
		"id":              resourceID,
		"kind":            kind,
		"name":            name,
		"namespace":       namespace,
		"kubeconfig":      kubeconfigPath,
		"context":         kubeContext,
		"managed_paths":   len(paths),
		"projection_size": len(projectionJSON),
	})
}

func (r *manifestResource) parseImportID(importID string) (context, namespace, kind, name string, err error) {
	parts := strings.Split(importID, "/")

	switch len(parts) {
	case 3:
		// Cluster-scoped: "context/kind/name"
		return parts[0], "", parts[1], parts[2], nil
	case 4:
		// Namespaced: "context/namespace/kind/name"
		return parts[0], parts[1], parts[2], parts[3], nil
	default:
		return "", "", "", "", fmt.Errorf("expected 3 or 4 parts separated by '/', got %d parts", len(parts))
	}
}
