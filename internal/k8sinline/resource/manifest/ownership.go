// internal/k8sinline/resource/manifest/ownership.go
package manifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// generateID creates a unique identifier for the resource
func (r *manifestResource) generateID(obj *unstructured.Unstructured, conn ClusterConnectionModel) string {
	// Create a deterministic ID based on cluster + object identity
	data := fmt.Sprintf("%s/%s/%s/%s",
		r.getClusterID(conn),
		obj.GetNamespace(),
		obj.GetKind(),
		obj.GetName(),
	)

	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
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

func (r *manifestResource) validateOwnership(ctx context.Context, data manifestResourceModel) error {
	obj, err := r.parseYAML(data.YAMLBody.ValueString())
	if err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Convert connection object to model
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		return fmt.Errorf("failed to convert connection: %w", err)
	}

	client, err := r.clientGetter(conn)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	gvr, err := r.getGVR(ctx, client, obj)
	if err != nil {
		return fmt.Errorf("failed to determine GVR: %w", err)
	}

	liveObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			return nil // Object doesn't exist - safe
		}
		return fmt.Errorf("failed to check existing resource: %w", err)
	}

	// Check ownership annotation
	annotations := liveObj.GetAnnotations()
	if annotations == nil {
		return fmt.Errorf("resource exists but has no ownership annotation - may be unmanaged")
	}

	actualID := annotations["k8sinline.terraform.io/id"]
	expectedID := data.ID.ValueString()

	if actualID != expectedID {
		return fmt.Errorf("connection targets different cluster - resource %s %q exists but is not managed by this Terraform resource (different ID: %s vs %s)",
			obj.GetKind(), obj.GetName(), actualID, expectedID)
	}

	return nil // Same resource, safe to proceed
}

// getClusterID creates a stable identifier for the cluster connection
func (r *manifestResource) getClusterID(conn ClusterConnectionModel) string {
	// Use host if available, otherwise hash the kubeconfig
	if !conn.Host.IsNull() {
		return conn.Host.ValueString()
	}

	var data string
	if !conn.KubeconfigFile.IsNull() {
		data = conn.KubeconfigFile.ValueString()
	} else if !conn.KubeconfigRaw.IsNull() {
		data = conn.KubeconfigRaw.ValueString()
	}

	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:8]) // Use first 8 bytes for shorter ID
}

func (r *manifestResource) anyConnectionFieldChanged(plan, state ClusterConnectionModel) bool {
	return !plan.Host.Equal(state.Host) ||
		!plan.ClusterCACertificate.Equal(state.ClusterCACertificate) ||
		!plan.KubeconfigFile.Equal(state.KubeconfigFile) ||
		!plan.KubeconfigRaw.Equal(state.KubeconfigRaw) ||
		!plan.Context.Equal(state.Context) ||
		!reflect.DeepEqual(plan.Exec, state.Exec)
}

// isEmptyConnection checks if the cluster connection is empty/unconfigured
func (r *manifestResource) isEmptyConnection(conn ClusterConnectionModel) bool {
	hasInline := !conn.Host.IsNull() || !conn.ClusterCACertificate.IsNull()
	hasFile := !conn.KubeconfigFile.IsNull()
	hasRaw := !conn.KubeconfigRaw.IsNull()

	return !hasInline && !hasFile && !hasRaw
}
