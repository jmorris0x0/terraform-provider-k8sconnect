// internal/k8sconnect/resource/manifest/deletion.go
package manifest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

// forceDestroy removes finalizers and forces deletion
func (r *manifestResource) forceDestroy(ctx context.Context, client k8sclient.K8sClient, gvr k8sschema.GroupVersionResource, obj *unstructured.Unstructured, resp *resource.DeleteResponse) error {
	// Get the current state of the object
	liveObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			// Object disappeared on its own - that's fine
			return nil
		}
		return fmt.Errorf("failed to get object for force destroy: %w", err)
	}

	// Check if object has finalizers
	finalizers := liveObj.GetFinalizers()
	if len(finalizers) == 0 {
		// No finalizers, but still stuck - this is unusual
		tflog.Warn(ctx, "Object has no finalizers but deletion timed out", map[string]interface{}{
			"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
		})

		// Try deleting again in case it was a timing issue
		err = client.Delete(ctx, gvr, obj.GetNamespace(), obj.GetName(), k8sclient.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to re-delete object without finalizers: %w", err)
		}

		// Wait a bit more for the deletion
		return r.waitForDeletion(ctx, client, gvr, obj, 30*time.Second)
	}

	// Log what finalizers we're about to remove
	resp.Diagnostics.AddWarning(
		"Force Destroying Resource with Finalizers",
		fmt.Sprintf("Removing finalizers from %s %s to force deletion: %v\n\n"+
			"⚠️  WARNING: This bypasses Kubernetes safety mechanisms and may cause:\n"+
			"• Data loss or corruption\n"+
			"• Orphaned dependent resources\n"+
			"• Incomplete cleanup operations\n\n"+
			"Only use force_destroy when you understand the implications for your specific resource.",
			obj.GetKind(), obj.GetName(), finalizers),
	)

	// Remove all finalizers
	liveObj.SetFinalizers([]string{})

	// Apply the change (remove finalizers)
	err = client.Apply(ctx, liveObj, k8sclient.ApplyOptions{
		FieldManager: "k8sconnect-force-destroy",
		Force:        true,
	})
	if err != nil {
		return fmt.Errorf("failed to remove finalizers: %w", err)
	}

	// Wait for deletion to complete (should be quick now)
	return r.waitForDeletion(ctx, client, gvr, obj, 60*time.Second)
}

// handleDeletionTimeout provides helpful guidance when normal deletion times out
func (r *manifestResource) handleDeletionTimeout(resp *resource.DeleteResponse, client k8sclient.K8sClient, gvr k8sschema.GroupVersionResource, obj *unstructured.Unstructured, timeout time.Duration, timeoutErr error) {
	ctx := context.Background()

	// Try to get current state to see what's preventing deletion
	liveObj, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		if errors.IsNotFound(err) {
			// Object disappeared between timeout and this check
			tflog.Info(ctx, "Object deleted after timeout check")
			return
		}

		// Can't get object state, provide generic timeout error
		resp.Diagnostics.AddError(
			"Deletion Timeout",
			fmt.Sprintf("Resource %s %s could not be deleted within %v.\n\n"+
				"The resource may still be terminating in the background. "+
				"Check its status with: kubectl get %s %s %s\n\n"+
				"To force deletion (⚠️ may cause data loss), set force_destroy = true",
				obj.GetKind(), obj.GetName(), timeout,
				strings.ToLower(obj.GetKind()), obj.GetName(), r.namespaceFlag(obj)),
		)
		return
	}

	// Check if finalizers are preventing deletion
	finalizers := liveObj.GetFinalizers()
	deletionTimestamp := liveObj.GetDeletionTimestamp()

	if deletionTimestamp != nil && len(finalizers) > 0 {
		// Object is terminating but blocked by finalizers
		resp.Diagnostics.AddError(
			"Deletion Blocked by Finalizers",
			fmt.Sprintf("Resource %s %s has been marked for deletion but is blocked by finalizers: %v\n\n"+
				"Finalizers prevent deletion until cleanup operations complete. Options:\n\n"+
				"1. **Wait longer** - increase delete_timeout (some operations take time):\n"+
				"   delete_timeout = \"20m\"\n\n"+
				"2. **Force deletion** - bypass finalizers (⚠️ may cause data loss):\n"+
				"   force_destroy = true\n\n"+
				"3. **Manual intervention** - check what's preventing cleanup:\n"+
				"   kubectl describe %s %s %s\n"+
				"   kubectl get events --field-selector involvedObject.name=%s\n\n"+
				"4. **Remove finalizers manually** (⚠️ dangerous):\n"+
				"   kubectl patch %s %s %s --type='merge' -p '{\"metadata\":{\"finalizers\":null}}'",
				obj.GetKind(), obj.GetName(), finalizers,
				strings.ToLower(obj.GetKind()), obj.GetName(), r.namespaceFlag(obj), obj.GetName(),
				strings.ToLower(obj.GetKind()), obj.GetName(), r.namespaceFlag(obj)),
		)
	} else if deletionTimestamp != nil {
		// Object is terminating but no finalizers - something else is wrong
		resp.Diagnostics.AddError(
			"Deletion Stuck Without Finalizers",
			fmt.Sprintf("Resource %s %s has been marked for deletion but is not terminating normally.\n\n"+
				"This may indicate a cluster issue. Check:\n"+
				"• kubectl describe %s %s %s\n"+
				"• kubectl get events --field-selector involvedObject.name=%s\n"+
				"• Cluster controller logs\n\n"+
				"To force deletion anyway: set force_destroy = true",
				obj.GetKind(), obj.GetName(),
				strings.ToLower(obj.GetKind()), obj.GetName(), r.namespaceFlag(obj), obj.GetName()),
		)
	} else {
		// Object exists but no deletion timestamp - delete call may have failed silently
		resp.Diagnostics.AddError(
			"Deletion Not Initiated",
			fmt.Sprintf("Resource %s %s still exists and has not been marked for deletion.\n\n"+
				"This may indicate insufficient permissions or a cluster issue.\n"+
				"Check: kubectl describe %s %s %s\n\n"+
				"To force deletion: set force_destroy = true",
				obj.GetKind(), obj.GetName(),
				strings.ToLower(obj.GetKind()), obj.GetName(), r.namespaceFlag(obj)),
		)
	}
}

// getDeleteTimeout determines the appropriate timeout for resource deletion
func (r *manifestResource) getDeleteTimeout(data manifestResourceModel) time.Duration {
	// If user specified a timeout, use it
	if !data.DeleteTimeout.IsNull() {
		if timeout, err := time.ParseDuration(data.DeleteTimeout.ValueString()); err == nil {
			return timeout
		}
	}

	// Parse YAML to determine resource type for default timeout
	if obj, err := r.parseYAML(data.YAMLBody.ValueString()); err == nil {
		kind := obj.GetKind()

		// Set default timeouts based on resource type
		switch kind {
		case "Namespace":
			return 15 * time.Minute // Namespaces with many resources can take time to cascade delete
		case "PersistentVolume", "PersistentVolumeClaim":
			return 10 * time.Minute // Storage resources often have finalizers
		case "CustomResourceDefinition":
			return 15 * time.Minute // CRDs need extra time for controller cleanup
		case "StatefulSet", "Job", "CronJob":
			return 8 * time.Minute // Ordered deletion or foreground deletion
		default:
			return 5 * time.Minute // Default for most resources
		}
	}

	// Fallback if YAML parsing fails
	return 5 * time.Minute
}

// waitForDeletion waits for a resource to be deleted from the cluster
func (r *manifestResource) waitForDeletion(ctx context.Context, client k8sclient.K8sClient, gvr k8sschema.GroupVersionResource, obj *unstructured.Unstructured, timeout time.Duration, ignoreFinalizers ...bool) error {
	// If timeout is 0, skip waiting
	if timeout == 0 {
		return nil
	}

	ignoreFinalizersFlag := false
	if len(ignoreFinalizers) > 0 {
		ignoreFinalizersFlag = ignoreFinalizers[0]
	}

	// Use a ticker to poll periodically instead of tight loop
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Check if object still exists
			_, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
			if err != nil {
				if errors.IsNotFound(err) {
					// Successfully deleted
					return nil
				}
				// Other errors are not deletion success, log and continue waiting
				tflog.Warn(ctx, "Error checking deletion status", map[string]interface{}{
					"error":     err.Error(),
					"kind":      obj.GetKind(),
					"name":      obj.GetName(),
					"namespace": obj.GetNamespace(),
				})
			}

			// Check if we've exceeded the timeout
			if time.Now().After(deadline) {
				if ignoreFinalizersFlag {
					// When ignoring finalizers, don't treat timeout as error
					return nil
				}
				return fmt.Errorf("timeout after %v waiting for deletion", timeout)
			}
		}
	}
}

// namespaceFlag returns the kubectl namespace flag for the given object
func (r *manifestResource) namespaceFlag(obj *unstructured.Unstructured) string {
	if namespace := obj.GetNamespace(); namespace != "" {
		return fmt.Sprintf("-n %s", namespace)
	}
	return ""
}
