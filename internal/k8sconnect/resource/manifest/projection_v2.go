// internal/k8sconnect/resource/manifest/projection_v2.go
package manifest

import (
	"context"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// extractOwnedPaths extracts field paths based on SSA field ownership
// TODO: Implement actual field ownership parsing
func extractOwnedPaths(ctx context.Context, managedFields []metav1.ManagedFieldsEntry, userJSON map[string]interface{}) []string {
	tflog.Warn(ctx, "extractOwnedPaths called but not implemented, falling back to extractFieldPaths")

	// For now, just fall back to the existing behavior
	return extractFieldPaths(userJSON, "")
}
