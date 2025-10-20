// internal/k8sconnect/common/k8sclient/client.go
package k8sclient

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// K8sClient abstracts operations against Kubernetes resources using client-go.
// It supports server-side apply and provides a clean interface for
// Kubernetes operations without depending on kubectl.
type K8sClient interface {
	// Apply applies the given unstructured object using server-side apply.
	Apply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) error

	// Get retrieves an object by GVR, namespace, and name.
	Get(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error)

	// Delete deletes an object by GVR, namespace, and name.
	Delete(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, options DeleteOptions) error

	// DryRunApply performs a dry-run server-side apply and returns the result.
	DryRunApply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) (*unstructured.Unstructured, error)

	// SetFieldManager sets the field manager name for server-side apply.
	SetFieldManager(name string) K8sClient

	// GetGVR determines the GroupVersionResource for an unstructured object.
	GetGVR(ctx context.Context, obj *unstructured.Unstructured) (schema.GroupVersionResource, error)

	Patch(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, patchType types.PatchType, data []byte, options metav1.PatchOptions) (*unstructured.Unstructured, error)

	// Watch returns a watcher that handles reconnection automatically
	Watch(ctx context.Context, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions) (watch.Interface, error)

	// GetWarnings retrieves any Kubernetes API warnings collected since the last call
	// Returns a slice of warning messages (typically about deprecated APIs)
	GetWarnings() []string

	// List retrieves all resources of a given GVR in a namespace (or cluster-wide if namespace is empty)
	List(ctx context.Context, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions) (*unstructured.UnstructuredList, error)
}

// ApplyOptions holds options for server-side apply operations.
type ApplyOptions struct {
	FieldManager    string
	Force           bool
	DryRun          []string
	FieldValidation string // "Strict", "Warn", or "Ignore" - validates fields against OpenAPI schema
}

// DeleteOptions holds options for delete operations.
type DeleteOptions struct {
	GracePeriodSeconds *int64
	PropagationPolicy  *metav1.DeletionPropagation
}

// resilientWatcher wraps a watch.Interface and handles reconnection
type resilientWatcher struct {
	ctx       context.Context
	client    *DynamicK8sClient
	gvr       schema.GroupVersionResource
	namespace string
	opts      metav1.ListOptions

	resultChan chan watch.Event
	stopCh     chan struct{}
}

// ===================== DynamicK8sClient =====================
// DynamicK8sClient uses client-go's Dynamic Client for operations.
type DynamicK8sClient struct {
	client           dynamic.Interface
	discovery        discovery.DiscoveryInterface
	fieldManager     string
	warningCollector *WarningCollector
}

// NewDynamicK8sClient creates a new DynamicK8sClient from a REST config.
// The config should have a WarningHandler set if you want to collect API warnings.
func NewDynamicK8sClient(config *rest.Config) (*DynamicK8sClient, error) {
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	// Extract warning collector from config if present
	var warningCollector *WarningCollector
	if wc, ok := config.WarningHandler.(*WarningCollector); ok {
		warningCollector = wc
	}

	return &DynamicK8sClient{
		client:           dynamicClient,
		discovery:        discoveryClient,
		fieldManager:     "k8sconnect",
		warningCollector: warningCollector,
	}, nil
}

func (d *DynamicK8sClient) SetFieldManager(name string) K8sClient {
	d.fieldManager = name
	return d
}

// getResourceInterface returns the appropriate ResourceInterface, handling default namespace inference
func (d *DynamicK8sClient) getResourceInterface(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) (dynamic.ResourceInterface, error) {
	var resourceList *metav1.APIResourceList

	// Wrap the discovery call in retry
	err := withRetry(ctx, DefaultRetryConfig, func() error {
		var err error
		resourceList, err = d.discovery.ServerResourcesForGroupVersion(gvr.GroupVersion().String())
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get resource info: %w", err)
	}

	var isNamespaced bool
	for _, apiResource := range resourceList.APIResources {
		if apiResource.Name == gvr.Resource {
			isNamespaced = apiResource.Namespaced
			break
		}
	}

	if isNamespaced {
		namespace := obj.GetNamespace()
		if namespace == "" {
			namespace = "default" // Default namespace inference
		}
		return d.client.Resource(gvr).Namespace(namespace), nil
	} else {
		// Truly cluster-scoped (like Namespaces)
		return d.client.Resource(gvr), nil
	}
}

// getResourceInterfaceByNamespace handles Get/Delete operations that take namespace as parameter
func (d *DynamicK8sClient) getResourceInterfaceByNamespace(ctx context.Context, gvr schema.GroupVersionResource, namespace string) (dynamic.ResourceInterface, error) {
	// Always check discovery to determine if resource is actually namespaced
	// This prevents errors when user provides namespace for cluster-scoped resources
	var resourceList *metav1.APIResourceList

	// Wrap the discovery call in retry
	err := withRetry(ctx, DefaultRetryConfig, func() error {
		var err error
		resourceList, err = d.discovery.ServerResourcesForGroupVersion(gvr.GroupVersion().String())
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get resource info: %w", err)
	}

	// Find if this resource is actually namespaced
	var isNamespaced bool
	for _, apiResource := range resourceList.APIResources {
		if apiResource.Name == gvr.Resource {
			isNamespaced = apiResource.Namespaced
			break
		}
	}

	// Only use namespace if resource is actually namespaced
	if isNamespaced {
		if namespace == "" {
			namespace = "default"
		}
		return d.client.Resource(gvr).Namespace(namespace), nil
	}

	// Cluster-scoped resource - ignore provided namespace
	return d.client.Resource(gvr), nil
}

// Apply performs server-side apply on the given object.
func (d *DynamicK8sClient) Apply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) error {
	return withRetry(ctx, DefaultRetryConfig, func() error {
		gvr, err := d.getGVR(ctx, obj)
		if err != nil {
			return fmt.Errorf("failed to determine GVR: %w", err)
		}

		fieldManager := options.FieldManager
		if fieldManager == "" {
			fieldManager = d.fieldManager
		}

		applyOpts := metav1.ApplyOptions{
			FieldManager: fieldManager,
			Force:        options.Force,
		}

		if len(options.DryRun) > 0 {
			applyOpts.DryRun = options.DryRun
		}

		resource, err := d.getResourceInterface(ctx, gvr, obj)
		if err != nil {
			return err
		}

		_, err = resource.Apply(ctx, obj.GetName(), obj, applyOpts)
		return err
	})
}

// DryRunApply performs a dry-run server-side apply.
func (d *DynamicK8sClient) DryRunApply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) (*unstructured.Unstructured, error) {
	var result *unstructured.Unstructured

	err := withRetry(ctx, DefaultRetryConfig, func() error {
		gvr, err := d.getGVR(ctx, obj)
		if err != nil {
			return fmt.Errorf("failed to determine GVR: %w", err)
		}

		fieldManager := options.FieldManager
		if fieldManager == "" {
			fieldManager = d.fieldManager
		}

		applyOpts := metav1.ApplyOptions{
			FieldManager: fieldManager,
			Force:        options.Force,
			DryRun:       []string{metav1.DryRunAll},
		}

		resource, err := d.getResourceInterface(ctx, gvr, obj)
		if err != nil {
			return err
		}

		result, err = resource.Apply(ctx, obj.GetName(), obj, applyOpts)
		return err
	})

	return result, err
}

// Get retrieves an object from the cluster.
func (d *DynamicK8sClient) Get(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	var result *unstructured.Unstructured

	err := withRetry(ctx, DefaultRetryConfig, func() error {
		resource, err := d.getResourceInterfaceByNamespace(ctx, gvr, namespace)
		if err != nil {
			return err
		}

		result, err = resource.Get(ctx, name, metav1.GetOptions{})
		return err
	})

	return result, err
}

// Delete removes an object from the cluster.
func (d *DynamicK8sClient) Delete(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, options DeleteOptions) error {
	return withRetry(ctx, DefaultRetryConfig, func() error {
		deleteOpts := metav1.DeleteOptions{}
		if options.GracePeriodSeconds != nil {
			deleteOpts.GracePeriodSeconds = options.GracePeriodSeconds
		}
		if options.PropagationPolicy != nil {
			deleteOpts.PropagationPolicy = options.PropagationPolicy
		}

		resource, err := d.getResourceInterfaceByNamespace(ctx, gvr, namespace)
		if err != nil {
			return err
		}

		return resource.Delete(ctx, name, deleteOpts)
	})
}

// GetGVR determines the GroupVersionResource for an unstructured object.
func (d *DynamicK8sClient) GetGVR(ctx context.Context, obj *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	return d.getGVR(ctx, obj)
}

// getGVR determines the GroupVersionResource for an unstructured object with helpful error messages
func (d *DynamicK8sClient) getGVR(ctx context.Context, obj *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	gvk := obj.GroupVersionKind()
	if gvk.Empty() {
		return schema.GroupVersionResource{}, fmt.Errorf("object has no GroupVersionKind")
	}

	tflog.Debug(ctx, "Discovering GVR", map[string]interface{}{
		"gvk": gvk.String(),
	})

	var resources *metav1.APIResourceList

	// Wrap the discovery call in retry
	err := withRetry(ctx, DefaultRetryConfig, func() error {
		var err error
		resources, err = d.discovery.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
		return err
	})

	if err != nil {
		// Provide helpful error message for common scenarios
		if d.isDiscoveryError(err) {
			return schema.GroupVersionResource{}, fmt.Errorf(
				"failed to discover resources for %s: %w\n\nThis usually means:\n"+
					"1. The API group/version doesn't exist in the cluster\n"+
					"2. A CRD needs to be installed first (try: depends_on = [k8sconnect_object.your_crd])\n"+
					"3. Your cluster connection has insufficient permissions\n"+
					"4. The cluster is unreachable",
				gvk.GroupVersion(), err)
		}
		return schema.GroupVersionResource{}, fmt.Errorf("failed to discover resources for %s: %w", gvk.GroupVersion(), err)
	}

	for _, resource := range resources.APIResources {
		if resource.Kind == gvk.Kind {
			gvr := schema.GroupVersionResource{
				Group:    gvk.Group,
				Version:  gvk.Version,
				Resource: resource.Name,
			}
			tflog.Debug(ctx, "Discovered GVR", map[string]interface{}{
				"gvk": gvk.String(),
				"gvr": gvr.String(),
			})
			return gvr, nil
		}
	}

	return schema.GroupVersionResource{}, fmt.Errorf(
		"no resource found for %s\n\nAvailable kinds in %s: %s\n\n"+
			"This usually means:\n"+
			"1. The resource kind doesn't exist (check spelling)\n"+
			"2. A CRD needs to be installed first (try: depends_on = [k8sconnect_object.your_crd])\n"+
			"3. The API version is incorrect",
		gvk, gvk.GroupVersion(), d.listAvailableKinds(resources))
}

// isDiscoveryError checks if the error is related to discovery
func (d *DynamicK8sClient) isDiscoveryError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	discoveryErrorPatterns := []string{
		"could not find the requested resource",
		"unable to retrieve the complete list of server apis",
		"the server could not find the requested resource",
		"no resources found",
	}

	for _, pattern := range discoveryErrorPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}
	return false
}

// listAvailableKinds helps with error messages
func (d *DynamicK8sClient) listAvailableKinds(resources *metav1.APIResourceList) string {
	if resources == nil || len(resources.APIResources) == 0 {
		return "none"
	}

	kinds := make([]string, 0, len(resources.APIResources))
	for _, resource := range resources.APIResources {
		if resource.Kind != "" {
			kinds = append(kinds, resource.Kind)
		}
	}

	if len(kinds) == 0 {
		return "none"
	}

	// Limit to first 10 to avoid overwhelming error messages
	if len(kinds) > 10 {
		return strings.Join(kinds[:10], ", ") + ", ..."
	}
	return strings.Join(kinds, ", ")
}

// GetGVRFromAPIVersionKind discovers the GVR and fetches the resource when apiVersion and kind are known
// This is used for import operations where the user provided the full apiVersion/kind
// Returns the GVR, the live object, and any error
func (d *DynamicK8sClient) GetGVRFromAPIVersionKind(ctx context.Context, apiVersion, kind, namespace, name string) (schema.GroupVersionResource, *unstructured.Unstructured, error) {
	tflog.Debug(ctx, "Discovering GVR from apiVersion and kind", map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"namespace":  namespace,
		"name":       name,
	})

	// Parse the apiVersion to get group and version
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, nil, fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}

	// Get API resources for this group/version
	var resourceList *metav1.APIResourceList
	err = withRetry(ctx, DefaultRetryConfig, func() error {
		var err error
		resourceList, err = d.discovery.ServerResourcesForGroupVersion(apiVersion)
		return err
	})

	if err != nil {
		return schema.GroupVersionResource{}, nil, fmt.Errorf(
			"failed to discover resources for %s: %w\n\n"+
				"This usually means:\n"+
				"1. The API group/version doesn't exist in the cluster\n"+
				"2. A CRD needs to be installed\n"+
				"3. The apiVersion is misspelled",
			apiVersion, err)
	}

	// Find the resource name for this kind
	var resourceName string
	var isNamespaced bool
	for _, apiResource := range resourceList.APIResources {
		if apiResource.Kind == kind {
			resourceName = apiResource.Name
			isNamespaced = apiResource.Namespaced
			break
		}
	}

	if resourceName == "" {
		// Build list of available kinds to help user
		availableKinds := make([]string, 0, len(resourceList.APIResources))
		for _, apiResource := range resourceList.APIResources {
			if apiResource.Kind != "" {
				availableKinds = append(availableKinds, apiResource.Kind)
			}
		}

		return schema.GroupVersionResource{}, nil, fmt.Errorf(
			"kind %q not found in apiVersion %q\n\n"+
				"Available kinds: %s\n\n"+
				"Check spelling or try: kubectl api-resources --api-group=%s",
			kind, apiVersion, strings.Join(availableKinds, ", "), gv.Group)
	}

	// Construct the GVR
	gvr := schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resourceName,
	}

	tflog.Debug(ctx, "Discovered GVR from apiVersion/kind", map[string]interface{}{
		"gvr":        gvr.String(),
		"apiVersion": apiVersion,
		"kind":       kind,
	})

	// Try to fetch the resource
	obj, err := d.tryGetResource(ctx, gvr, isNamespaced, namespace, name)
	if err != nil {
		return schema.GroupVersionResource{}, nil, fmt.Errorf(
			"resource %s/%s (kind: %s, apiVersion: %s) not found: %w",
			namespace, name, kind, apiVersion, err)
	}

	return gvr, obj, nil
}

// tryGetResource attempts to get a resource using a specific GVR
func (d *DynamicK8sClient) tryGetResource(ctx context.Context, gvr schema.GroupVersionResource, isNamespaced bool, namespace, name string) (*unstructured.Unstructured, error) {
	var resource dynamic.ResourceInterface

	if isNamespaced {
		// For namespaced resources, use the provided namespace or default
		ns := namespace
		if ns == "" {
			ns = "default"
		}
		resource = d.client.Resource(gvr).Namespace(ns)
	} else {
		// For cluster-scoped resources, don't specify namespace
		resource = d.client.Resource(gvr)
	}

	return resource.Get(ctx, name, metav1.GetOptions{})
}

func (d *DynamicK8sClient) Patch(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, patchType types.PatchType, data []byte, options metav1.PatchOptions) (*unstructured.Unstructured, error) {
	var result *unstructured.Unstructured

	err := withRetry(ctx, DefaultRetryConfig, func() error {
		var err error
		if namespace == "" {
			result, err = d.client.Resource(gvr).Patch(ctx, name, patchType, data, options)
		} else {
			result, err = d.client.Resource(gvr).Namespace(namespace).Patch(ctx, name, patchType, data, options)
		}
		return err
	})

	return result, err
}

func (c *DynamicK8sClient) Watch(ctx context.Context, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions) (watch.Interface, error) {
	rw := &resilientWatcher{
		ctx:        ctx,
		client:     c,
		gvr:        gvr,
		namespace:  namespace,
		opts:       opts,
		resultChan: make(chan watch.Event),
		stopCh:     make(chan struct{}),
	}

	go rw.run()
	return rw, nil
}

func (rw *resilientWatcher) run() {
	defer close(rw.resultChan)

	for {
		select {
		case <-rw.ctx.Done():
			return
		case <-rw.stopCh:
			return
		default:
			// Create the actual watch
			var watcher watch.Interface
			var err error

			if rw.namespace != "" {
				watcher, err = rw.client.client.Resource(rw.gvr).Namespace(rw.namespace).Watch(rw.ctx, rw.opts)
			} else {
				watcher, err = rw.client.client.Resource(rw.gvr).Watch(rw.ctx, rw.opts)
			}

			if err != nil {
				// Send error event and retry
				rw.resultChan <- watch.Event{Type: watch.Error, Object: &metav1.Status{Message: err.Error()}}
				time.Sleep(2 * time.Second)
				continue
			}

			// Forward events until watch closes
			for event := range watcher.ResultChan() {
				select {
				case rw.resultChan <- event:
					// Update resource version for reconnection
					if event.Type != watch.Error {
						if obj, ok := event.Object.(*unstructured.Unstructured); ok {
							rw.opts.ResourceVersion = obj.GetResourceVersion()
						}
					}
				case <-rw.stopCh:
					watcher.Stop()
					return
				case <-rw.ctx.Done():
					watcher.Stop()
					return
				}
			}

			// Watch closed, will reconnect
			watcher.Stop()
			time.Sleep(time.Second) // Brief pause before reconnect
		}
	}
}

func (rw *resilientWatcher) Stop() {
	close(rw.stopCh)
}

func (rw *resilientWatcher) ResultChan() <-chan watch.Event {
	return rw.resultChan
}

// GetWarnings retrieves any Kubernetes API warnings collected since the last call
func (d *DynamicK8sClient) GetWarnings() []string {
	if d.warningCollector == nil {
		return nil
	}
	return d.warningCollector.GetWarnings()
}

// List retrieves all resources of a given GVR in a namespace
func (d *DynamicK8sClient) List(ctx context.Context, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	var result *unstructured.UnstructuredList

	err := withRetry(ctx, DefaultRetryConfig, func() error {
		resource, err := d.getResourceInterfaceByNamespace(ctx, gvr, namespace)
		if err != nil {
			return err
		}

		result, err = resource.List(ctx, opts)
		return err
	})

	return result, err
}

// ServerGroupsAndResources returns the supported groups and resources for all groups and versions
// This is used for dynamic resource discovery (e.g., listing all resources in a namespace)
func (d *DynamicK8sClient) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return d.discovery.ServerGroupsAndResources()
}

// Interface assertion to ensure DynamicK8sClient satisfies K8sClient
var _ K8sClient = (*DynamicK8sClient)(nil)
