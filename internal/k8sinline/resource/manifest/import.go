// internal/k8sinline/resource/manifest/import.go
package manifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
)

// ImportState method implementing kubeconfig strategy
func (r *manifestResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Parse import ID: "context/namespace/kind/name" or "context/kind/name" for cluster-scoped
	kubeContext, namespace, kind, name, err := r.parseImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected format: <context>/<namespace>/<kind>/<name> or <context>/<kind>/<name>\n\nExamples:\n"+
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
				"Format: <context>/<namespace>/<kind>/<name> or <context>/<kind>/<name>\n\n"+
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
					"3. Authentication failed\n\n"+
					"Kubeconfig: %s\n"+
					"Context: %s\n"+
					"Details: %s", kubeconfigPath, kubeContext, err.Error()),
			)
		}
		return
	}

	// Discover GVR and fetch the live object in one step
	_, liveObj, err := client.GetGVRFromKind(ctx, kind, namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "no API resource found for kind") {
			resp.Diagnostics.AddError(
				"Import Failed: Unknown Resource Kind",
				fmt.Sprintf("The resource kind \"%s\" was not found in the cluster.\n\n"+
					"This usually means:\n"+
					"1. The kind name is misspelled (check capitalization)\n"+
					"2. A CRD needs to be installed first\n"+
					"3. The resource type doesn't exist in this Kubernetes version\n\n"+
					"Check available resource types:\n"+
					"  kubectl api-resources | grep -i %s", kind, strings.ToLower(kind)),
			)
		} else if strings.Contains(err.Error(), "not found") {
			resp.Diagnostics.AddError(
				"Import Failed: Resource Not Found",
				fmt.Sprintf("The %s \"%s\" was not found in the cluster.\n\n"+
					"Verify the resource exists:\n"+
					"  kubectl get %s %s %s\n\n"+
					"Context: %s\n"+
					"Details: %s",
					kind, name, strings.ToLower(kind), name,
					func() string {
						if namespace != "" {
							return fmt.Sprintf("-n %s", namespace)
						}
						return ""
					}(), kubeContext, err.Error()),
			)
		} else {
			resp.Diagnostics.AddError(
				"Import Failed: Discovery/Fetch Error",
				fmt.Sprintf("Failed to discover or fetch the %s \"%s\".\n\n"+
					"Context: %s\n"+
					"Details: %s", kind, name, kubeContext, err.Error()),
			)
		}
		return
	}

	// Convert live object back to clean YAML
	yamlBytes, err := r.objectToYAML(liveObj)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: YAML Conversion Error",
			fmt.Sprintf("Failed to convert the imported object to YAML: %s", err.Error()),
		)
		return
	}

	// Generate resource ID using a special import-based approach
	// Since we don't have the final cluster connection yet, we'll use the context
	resourceID := r.generateIDFromImport(liveObj, kubeContext)

	// Create connection model for import
	connModel := ClusterConnectionModel{
		Host:                 types.StringNull(),
		ClusterCACertificate: types.StringNull(),
		KubeconfigFile:       types.StringValue(kubeconfigPath),
		KubeconfigRaw:        types.StringNull(),
		Context:              types.StringValue(kubeContext),
		Exec:                 nil,
	}

	// Convert to types.Object
	connObj, err := r.convertConnectionModelToObject(ctx, connModel)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: Connection Conversion Error",
			fmt.Sprintf("Failed to convert connection model to object: %s", err.Error()),
		)
		return
	}

	// Populate state with imported data
	importedData := manifestResourceModel{
		ID:                types.StringValue(resourceID),
		YAMLBody:          types.StringValue(string(yamlBytes)),
		ClusterConnection: connObj,                // Now using types.Object
		DeleteProtection:  types.BoolValue(false), // default
	}

	// Set the imported state
	diags := resp.State.Set(ctx, &importedData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "successfully imported resource", map[string]interface{}{
		"import_id":   req.ID,
		"resource_id": resourceID,
		"kind":        liveObj.GetKind(),
		"name":        liveObj.GetName(),
		"namespace":   liveObj.GetNamespace(),
		"context":     kubeContext,
		"kubeconfig":  kubeconfigPath,
	})

	// Add informational message about next steps
	resp.Diagnostics.AddWarning(
		"Import Successful - Configuration Required",
		"The resource has been imported successfully. You must now configure the cluster_connection block in your Terraform configuration to match your desired connection method.\n\n"+
			"Example configuration:\n"+
			"  resource \"k8sinline_manifest\" \"example\" {\n"+
			"    yaml_body = \"# Populated by import\"\n"+
			"    \n"+
			"    cluster_connection = {\n"+
			"      # Choose your preferred connection method:\n"+
			"      kubeconfig_file = \"~/.kube/config\"\n"+
			"      context         = \""+kubeContext+"\"\n"+
			"    }\n"+
			"  }\n\n"+
			"Run 'terraform plan' to see if your configuration matches the imported resource.",
	)
}

// Updated parseImportID function to handle new format with context
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

// Helper function to generateID after import:
func (r *manifestResource) generateIDFromImport(obj *unstructured.Unstructured, context string) string {
	data := fmt.Sprintf("%s/%s/%s/%s",
		context, // Use context as cluster identifier for imports
		obj.GetNamespace(),
		obj.GetKind(),
		obj.GetName(),
	)

	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
