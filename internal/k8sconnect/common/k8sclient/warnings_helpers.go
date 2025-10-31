package k8sclient

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// SurfaceK8sWarnings checks for Kubernetes API warnings and adds them as Terraform diagnostics
// This is the simple version without resource identity
func SurfaceK8sWarnings(ctx context.Context, client K8sClient, diagnostics *diag.Diagnostics) {
	warnings := client.GetWarnings()
	for _, warning := range warnings {
		diagnostics.AddWarning(
			"Kubernetes API Warning",
			fmt.Sprintf("The Kubernetes API server returned a warning:\n\n%s", warning),
		)
		tflog.Warn(ctx, "Kubernetes API warning", map[string]interface{}{
			"warning": warning,
		})
	}
}

// SurfaceK8sWarningsWithIdentity checks for Kubernetes API warnings and adds them as Terraform diagnostics
// This version includes resource identity (kind/name) in the warning message
func SurfaceK8sWarningsWithIdentity(ctx context.Context, client K8sClient, obj interface {
	GetKind() string
	GetName() string
}, diagnostics *diag.Diagnostics) {
	warnings := client.GetWarnings()
	for _, warning := range warnings {
		diagnostics.AddWarning(
			fmt.Sprintf("Kubernetes API Warning (%s/%s)", obj.GetKind(), obj.GetName()),
			fmt.Sprintf("The Kubernetes API server returned a warning:\n\n%s", warning),
		)
		tflog.Warn(ctx, "Kubernetes API warning", map[string]interface{}{
			"warning": warning,
			"kind":    obj.GetKind(),
			"name":    obj.GetName(),
		})
	}
}
