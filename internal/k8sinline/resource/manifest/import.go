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

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
)

// ImportState method implementing kubeconfig strategy with managed fields tracking
func (r *manifestResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	tflog.Info(ctx, "ImportState called", map[string]interface{}{"import_id": req.ID})
	fmt.Printf("DEBUG: ImportState called with ID: %s\n", req.ID)

	// Parse import ID: "context/namespace/kind/name" or "context/kind/name" for cluster-scoped
	kubeContext, namespace, kind, name, err := r.parseImportID(req.ID)
	fmt.Printf("DEBUG: Parsed import ID - context: %s, namespace: %s, kind: %s, name: %s, err: %v\n",
		kubeContext, namespace, kind, name, err)

	if err != nil {
		fmt.Printf("DEBUG: Parse error, returning with error\n")
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

	// TODO: Remove after debug
	tflog.Info(ctx, "Parsed import ID", map[string]interface{}{
		"context":   kubeContext,
		"namespace": namespace,
		"kind":      kind,
		"name":      name,
		"error":     err,
	})

	// Validate required parts
	if kubeContext == "" {
		fmt.Printf("DEBUG: Empty context, returning with error\n")
		resp.Diagnostics.AddError(
			"Import Failed: Missing Context",
			"The import ID must include a kubeconfig context as the first part.\n\n"+
				"Format: <context>/<namespace>/<kind>/<n> or <context>/<kind>/<n>\n\n"+
				"Available contexts can be found with: kubectl config get-contexts",
		)
		return
	}
	if kind == "" {
		fmt.Printf("DEBUG: Empty kind, returning with error\n")
		resp.Diagnostics.AddError(
			"Import Failed: Missing Kind",
			"The resource kind cannot be empty in the import ID.",
		)
		return
	}
	if name == "" {
		fmt.Printf("DEBUG: Empty name, returning with error\n")
		resp.Diagnostics.AddError(
			"Import Failed: Missing Name",
			"The resource name cannot be empty in the import ID.",
		)
		return
	}

	// Read kubeconfig from KUBECONFIG env var or default location
	kubeconfigPath := os.Getenv("KUBECONFIG")
	fmt.Printf("DEBUG: KUBECONFIG env var: %s\n", kubeconfigPath)

	if kubeconfigPath == "" {
		homeDir := os.Getenv("HOME")
		fmt.Printf("DEBUG: HOME env var: %s\n", homeDir)

		if homeDir == "" {
			fmt.Printf("DEBUG: No HOME dir, returning with error\n")
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
		fmt.Printf("DEBUG: Using default kubeconfig path: %s\n", kubeconfigPath)
	}

	tflog.Info(ctx, "Using kubeconfig", map[string]interface{}{
		"path": kubeconfigPath,
	})

	// Check if kubeconfig file exists
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		fmt.Printf("DEBUG: Kubeconfig file not found at %s\n", kubeconfigPath)
		resp.Diagnostics.AddError(
			"Import Failed: Kubeconfig File Not Found",
			fmt.Sprintf("Kubeconfig file not found at: %s\n\n"+
				"Ensure your kubeconfig file exists or set KUBECONFIG environment variable:\n"+
				"  export KUBECONFIG=/path/to/your/kubeconfig\n"+
				"  terraform import k8sinline_manifest.example \"prod/default/Pod/nginx\"", kubeconfigPath),
		)
		return
	}
	fmt.Printf("DEBUG: Kubeconfig file exists at %s\n", kubeconfigPath)

	tflog.Info(ctx, "import using kubeconfig", map[string]interface{}{
		"path":      kubeconfigPath,
		"context":   kubeContext,
		"kind":      kind,
		"name":      name,
		"namespace": namespace,
	})

	// Create K8s client using kubeconfig file and context
	fmt.Printf("DEBUG: Creating K8s client with context %s\n", kubeContext)
	client, err := k8sclient.NewDynamicK8sClientFromKubeconfigFile(kubeconfigPath, kubeContext)
	if err != nil {
		fmt.Printf("DEBUG: Failed to create K8s client: %v\n", err)
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
	fmt.Printf("DEBUG: K8s client created successfully\n")

	// Discover GVR from kind and fetch the resource
	fmt.Printf("DEBUG: Getting GVR for kind=%s, namespace=%s, name=%s\n", kind, namespace, name)
	_, liveObj, err := client.GetGVRFromKind(ctx, kind, namespace, name)
	if err != nil {
		fmt.Printf("DEBUG: GetGVRFromKind failed: %v\n", err)
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
	fmt.Printf("DEBUG: Resource found successfully\n")

	// Check for existing ownership and generate ID accordingly
	existingID := r.getOwnershipID(liveObj)
	fmt.Printf("DEBUG: Existing ownership ID: %s\n", existingID)

	var resourceID string

	if existingID != "" {
		fmt.Printf("DEBUG: Resource already managed, returning with error\n")
		// Resource already managed by k8sinline - this is an error
		resp.Diagnostics.AddError(
			"Import Failed: Resource Already Managed",
			fmt.Sprintf("This resource is already managed by another Terraform state (ID: %s).\n\n"+
				"This usually means:\n"+
				"- Another Terraform configuration is managing this resource\n"+
				"- You're trying to import into the wrong state\n\n"+
				"To resolve:\n"+
				"1. Find the correct Terraform state that manages this resource\n"+
				"2. Or remove the annotation first: kubectl annotate %s %s %s-\n"+
				"   WARNING: Only do this if you're certain the other state no longer manages it!",
				existingID, strings.ToLower(kind), name, OwnershipAnnotation),
		)
		return
	}

	// Resource not managed by k8sinline - generate new ID
	resourceID = r.generateID()
	fmt.Printf("DEBUG: Generated new resource ID: %s\n", resourceID)

	// Convert to YAML for state
	fmt.Printf("DEBUG: Converting resource to YAML\n")
	yamlBytes, err := r.objectToYAML(liveObj)
	if err != nil {
		fmt.Printf("DEBUG: YAML conversion failed: %v\n", err)
		resp.Diagnostics.AddError(
			"Import Failed: YAML conversion error",
			fmt.Sprintf("Failed to convert resource to YAML: %s", err.Error()),
		)
		return
	}
	fmt.Printf("DEBUG: YAML conversion successful, %d bytes\n", len(yamlBytes))

	// NEW: Extract field paths from the imported object
	paths := extractFieldPaths(liveObj.Object, "")
	fmt.Printf("DEBUG: Extracted %d field paths\n", len(paths))

	// NEW: Project the current state for managed fields
	projection, err := projectFields(liveObj.Object, paths)
	if err != nil {
		fmt.Printf("DEBUG: Field projection failed: %v\n", err)
		resp.Diagnostics.AddError("Projection Failed",
			fmt.Sprintf("Failed to project managed fields during import: %s", err))
		return
	}
	fmt.Printf("DEBUG: Field projection successful\n")

	// NEW: Convert projection to JSON
	projectionJSON, err := toJSON(projection)
	if err != nil {
		fmt.Printf("DEBUG: JSON conversion failed: %v\n", err)
		resp.Diagnostics.AddError("JSON Conversion Failed",
			fmt.Sprintf("Failed to convert projection to JSON during import: %s", err))
		return
	}
	fmt.Printf("DEBUG: JSON conversion successful, %d bytes\n", len(projectionJSON))

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
	fmt.Printf("DEBUG: Converting connection to object\n")
	connectionObj, err := r.convertConnectionToObject(ctx, conn)
	if err != nil {
		fmt.Printf("DEBUG: Connection conversion failed: %v\n", err)
		resp.Diagnostics.AddError(
			"Import Failed: Connection Conversion Error",
			fmt.Sprintf("Failed to convert connection model: %s", err.Error()),
		)
		return
	}
	fmt.Printf("DEBUG: Connection conversion successful\n")

	// Create imported data with managed state projection
	importedData := manifestResourceModel{
		ID:                         types.StringValue(resourceID),
		YAMLBody:                   types.StringValue(string(yamlBytes)),
		ClusterConnection:          connectionObj,
		DeleteProtection:           types.BoolValue(false),
		ManagedStateProjection:     types.StringValue(projectionJSON),
		ImportedWithoutAnnotations: types.BoolValue(true),
	}

	fmt.Printf("DEBUG: Setting state\n")
	diags := resp.State.Set(ctx, &importedData)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		fmt.Printf("DEBUG: State.Set returned errors\n")
		for _, diag := range resp.Diagnostics.Errors() {
			fmt.Printf("DEBUG: Diagnostic error: %s - %s\n", diag.Summary(), diag.Detail())
		}
		return
	}

	// Debug: Check what was actually set
	fmt.Printf("DEBUG: State set with:\n")
	fmt.Printf("  ID: %s\n", importedData.ID.ValueString())
	fmt.Printf("  YAMLBody length: %d\n", len(importedData.YAMLBody.ValueString()))
	fmt.Printf("  ManagedStateProjection length: %d\n", len(importedData.ManagedStateProjection.ValueString()))
	fmt.Printf("  ImportedWithoutAnnotations: %v\n", importedData.ImportedWithoutAnnotations.ValueBool())
	fmt.Printf("  DeleteProtection: %v\n", importedData.DeleteProtection.ValueBool())
	fmt.Printf("  ClusterConnection is null: %v\n", importedData.ClusterConnection.IsNull())

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

	fmt.Printf("DEBUG: ImportState completed successfully\n")
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
