// internal/k8sconnect/resource/manifest/deletion.go
package manifest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

// FinalizerInfo provides explanation and documentation for a finalizer
type FinalizerInfo struct {
	Explanation string
	Source      string
}

// knownFinalizers contains explanations for built-in Kubernetes finalizers
// Only includes officially documented, stable finalizers that are guaranteed to be accurate
var knownFinalizers = map[string]FinalizerInfo{
	"kubernetes.io/pvc-protection": {
		Explanation: "Volume is still attached to a pod",
		Source:      "https://kubernetes.io/docs/concepts/storage/persistent-volumes/#storage-object-in-use-protection",
	},
	"kubernetes.io/pv-protection": {
		Explanation: "PersistentVolume is still bound to a claim",
		Source:      "https://kubernetes.io/docs/concepts/storage/persistent-volumes/#storage-object-in-use-protection",
	},
	"kubernetes": {
		Explanation: "Namespace is deleting all contained resources",
		Source:      "https://kubernetes.io/docs/concepts/overview/working-with-objects/namespaces/#automatic-deletion",
	},
	"foregroundDeletion": {
		Explanation: "Waiting for owned resources to delete first",
		Source:      "https://kubernetes.io/docs/concepts/architecture/garbage-collection/#foreground-deletion",
	},
	"orphan": {
		Explanation: "Dependents will be orphaned (not deleted)",
		Source:      "https://kubernetes.io/docs/concepts/architecture/garbage-collection/#orphan-dependents",
	},
}

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
	kind := obj.GetKind()
	name := obj.GetName()

	if deletionTimestamp != nil && len(finalizers) > 0 {
		// Object is terminating but blocked by finalizers
		var msg strings.Builder
		msg.WriteString(fmt.Sprintf("%s \"%s\" cannot be deleted due to finalizers:\n\n", kind, name))

		// Explain each finalizer
		for _, finalizer := range finalizers {
			msg.WriteString(explainFinalizer(finalizer))
			msg.WriteString("\n")
		}

		// For namespace resources, perform diagnostic API call to show what's inside
		if kind == "Namespace" {
			diagnostics := r.explainNamespaceDeletionFailure(ctx, client, name)
			msg.WriteString(diagnostics)
		}

		msg.WriteString("\n\nOptions:\n")
		msg.WriteString(fmt.Sprintf("• Wait longer: delete_timeout = \"20m\"\n"))
		msg.WriteString(fmt.Sprintf("• Investigate: kubectl describe %s %s %s\n", strings.ToLower(kind), name, r.namespaceFlag(obj)))
		msg.WriteString(fmt.Sprintf("• Force delete: force_destroy = true"))

		resp.Diagnostics.AddError("Deletion Blocked by Finalizers", msg.String())

	} else if deletionTimestamp != nil {
		// Object is terminating but no finalizers
		var msg strings.Builder

		// For namespaces without finalizers, still check what's inside
		if kind == "Namespace" {
			msg.WriteString(fmt.Sprintf("Namespace \"%s\" did not delete within %v", name, timeout))
			diagnostics := r.explainNamespaceDeletionFailure(ctx, client, name)
			msg.WriteString(diagnostics)

			msg.WriteString("\n\nOptions:\n")
			msg.WriteString("• Increase timeout: delete_timeout = \"20m\"\n")
			msg.WriteString(fmt.Sprintf("• Check status: kubectl get all -n %s\n", name))
			msg.WriteString("• Force delete: force_destroy = true")

			resp.Diagnostics.AddError("Namespace Deletion Timeout", msg.String())
		} else {
			msg.WriteString(fmt.Sprintf("%s \"%s\" did not delete within %v\n\n", kind, name, timeout))
			msg.WriteString("Options:\n")
			msg.WriteString(fmt.Sprintf("• Increase timeout: delete_timeout = \"20m\"\n"))
			msg.WriteString(fmt.Sprintf("• Check status: kubectl describe %s %s %s\n", strings.ToLower(kind), name, r.namespaceFlag(obj)))
			msg.WriteString("• Force delete: force_destroy = true")

			resp.Diagnostics.AddError("Deletion Timeout", msg.String())
		}

	} else {
		// Object exists but no deletion timestamp - delete call may have failed silently
		var msg strings.Builder
		msg.WriteString(fmt.Sprintf("%s \"%s\" was not marked for deletion (timeout: %v)\n\n", kind, name, timeout))
		msg.WriteString("This may indicate insufficient permissions or a cluster issue.\n\n")
		msg.WriteString("Options:\n")
		msg.WriteString(fmt.Sprintf("• Check permissions: kubectl auth can-i delete %s\n", strings.ToLower(kind)))
		msg.WriteString(fmt.Sprintf("• Check status: kubectl describe %s %s %s\n", strings.ToLower(kind), name, r.namespaceFlag(obj)))
		msg.WriteString("• Force delete: force_destroy = true")

		resp.Diagnostics.AddError("Deletion Not Initiated", msg.String())
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

// explainFinalizer returns a helpful explanation for a finalizer
func explainFinalizer(finalizer string) string {
	if info, ok := knownFinalizers[finalizer]; ok {
		return fmt.Sprintf("  • %s: %s\n    (See: %s)", finalizer, info.Explanation, info.Source)
	}
	return fmt.Sprintf("  • %s (custom finalizer - check controller logs)", finalizer)
}

// explainNamespaceDeletionFailure performs a diagnostic API call to understand why namespace deletion is slow
func (r *manifestResource) explainNamespaceDeletionFailure(ctx context.Context, client k8sclient.K8sClient, namespace string) string {
	// Get all API resources to list everything in the namespace
	// This is dynamic - no hardcoded resource types
	_, apiResourcesList, err := r.getDiscoveryClient(client).ServerGroupsAndResources()
	if err != nil && apiResourcesList == nil {
		return "(Could not check namespace contents: check cluster connectivity)"
	}

	// Count resources by kind
	resourceCounts := make(map[string]int)
	resourcesWithFinalizers := []string{}
	totalResources := 0

	// Iterate through all discoverable resource types
	for _, apiResources := range apiResourcesList {
		if apiResources == nil {
			continue
		}

		gv, err := k8sschema.ParseGroupVersion(apiResources.GroupVersion)
		if err != nil {
			continue
		}

		for _, apiResource := range apiResources.APIResources {
			// Skip subresources (like pods/log, pods/exec)
			if strings.Contains(apiResource.Name, "/") {
				continue
			}

			// Only list namespaced resources
			if !apiResource.Namespaced {
				continue
			}

			// Skip if List verb not available
			hasListVerb := false
			for _, verb := range apiResource.Verbs {
				if verb == "list" {
					hasListVerb = true
					break
				}
			}
			if !hasListVerb {
				continue
			}

			gvr := k8sschema.GroupVersionResource{
				Group:    gv.Group,
				Version:  gv.Version,
				Resource: apiResource.Name,
			}

			// List resources of this type in the namespace
			list, err := client.List(ctx, gvr, namespace, metav1.ListOptions{})
			if err != nil {
				// Ignore errors for individual resource types (might be deprecated or inaccessible)
				continue
			}

			for _, item := range list.Items {
				kind := item.GetKind()
				if kind == "" {
					kind = apiResource.Kind
				}
				resourceCounts[kind]++
				totalResources++

				// Check for finalizers
				if finalizers := item.GetFinalizers(); len(finalizers) > 0 {
					resourcesWithFinalizers = append(resourcesWithFinalizers,
						fmt.Sprintf("%s/%s (finalizers: %v)", kind, item.GetName(), finalizers))
				}
			}
		}
	}

	if totalResources == 0 {
		return "Namespace is empty but deletion is taking longer than expected."
	}

	// Build the diagnostic message
	return formatNamespaceDeletionDiagnostics(resourceCounts, resourcesWithFinalizers, totalResources)
}

// formatNamespaceDeletionDiagnostics creates a helpful message about namespace deletion progress
func formatNamespaceDeletionDiagnostics(resourceCounts map[string]int, resourcesWithFinalizers []string, totalResources int) string {
	// Sort kinds by count (most common first)
	type kindCount struct {
		Kind  string
		Count int
	}
	var kinds []kindCount
	for kind, count := range resourceCounts {
		kinds = append(kinds, kindCount{Kind: kind, Count: count})
	}
	sort.Slice(kinds, func(i, j int) bool {
		return kinds[i].Count > kinds[j].Count
	})

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("\nNamespace still contains %d resources:\n", totalResources))

	// Show top resource types
	shown := 0
	const maxTypesToShow = 5
	for _, kc := range kinds {
		if shown >= maxTypesToShow {
			remaining := totalResources
			for i := 0; i < shown; i++ {
				remaining -= kinds[i].Count
			}
			if remaining > 0 {
				msg.WriteString(fmt.Sprintf("  - %d resources of %d other types\n", remaining, len(kinds)-shown))
			}
			break
		}
		msg.WriteString(fmt.Sprintf("  - %d %s\n", kc.Count, kc.Kind))
		shown++
	}

	// Show resources with finalizers
	if len(resourcesWithFinalizers) > 0 {
		msg.WriteString("\nResources with finalizers:\n")
		const maxFinalizersToShow = 5
		for i, res := range resourcesWithFinalizers {
			if i >= maxFinalizersToShow {
				msg.WriteString(fmt.Sprintf("  ... and %d more\n",
					len(resourcesWithFinalizers)-maxFinalizersToShow))
				break
			}
			msg.WriteString(fmt.Sprintf("  - %s\n", res))
		}
	}

	return msg.String()
}

// getDiscoveryClient extracts the discovery client from the K8sClient
// This is a helper to access discovery for dynamic resource listing
func (r *manifestResource) getDiscoveryClient(client k8sclient.K8sClient) interface {
	ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error)
} {
	// Type assertion to access the underlying discovery client
	if dc, ok := client.(interface {
		ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error)
	}); ok {
		return dc
	}

	// Fallback: return a stub that returns an error
	return &stubDiscovery{}
}

// stubDiscovery is a fallback when we can't get the discovery client
type stubDiscovery struct{}

func (s *stubDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, nil, fmt.Errorf("discovery client not available")
}
