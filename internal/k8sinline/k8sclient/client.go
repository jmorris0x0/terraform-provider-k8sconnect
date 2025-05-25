// internal/k8sinline/k8sclient/client.go
package k8sclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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

	// WithForceConflicts enables force conflicts resolution.
	WithForceConflicts(force bool) K8sClient

	// GetGVR determines the GroupVersionResource for an unstructured object.
	GetGVR(ctx context.Context, obj *unstructured.Unstructured) (schema.GroupVersionResource, error)
}

// CacheInvalidator provides methods to invalidate caches for handling stale discovery
type CacheInvalidator interface {
	// InvalidateDiscoveryCache clears cached discovery information to handle new CRDs
	InvalidateDiscoveryCache(ctx context.Context) error
}

// ApplyOptions holds options for server-side apply operations.
type ApplyOptions struct {
	FieldManager string
	Force        bool
	DryRun       []string
}

// DeleteOptions holds options for delete operations.
type DeleteOptions struct {
	GracePeriodSeconds *int64
	PropagationPolicy  *metav1.DeletionPropagation
}

// ===================== DynamicK8sClient =====================
// DynamicK8sClient uses client-go's Dynamic Client for operations.
type DynamicK8sClient struct {
	client         dynamic.Interface
	discovery      discovery.DiscoveryInterface
	fieldManager   string
	forceConflicts bool
}

// NewDynamicK8sClient creates a new DynamicK8sClient from a REST config.
func NewDynamicK8sClient(config *rest.Config) (*DynamicK8sClient, error) {
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	return &DynamicK8sClient{
		client:         dynamicClient,
		discovery:      discoveryClient,
		fieldManager:   "k8sinline",
		forceConflicts: false,
	}, nil
}

// NewDynamicK8sClientFromKubeconfig creates a client from kubeconfig bytes and context.
func NewDynamicK8sClientFromKubeconfig(kubeconfigData []byte, context string) (*DynamicK8sClient, error) {
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	if context != "" {
		// Load kubeconfig and set context
		clientConfig, err := clientcmd.Load(kubeconfigData)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
		}

		if _, exists := clientConfig.Contexts[context]; !exists {
			return nil, fmt.Errorf("context %q not found in kubeconfig", context)
		}

		clientConfig.CurrentContext = context
		config, err = clientcmd.NewDefaultClientConfig(*clientConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to build config for context %q: %w", context, err)
		}
	}

	return NewDynamicK8sClient(config)
}

// NewDynamicK8sClientFromKubeconfigFile creates a client from a kubeconfig file path.
func NewDynamicK8sClientFromKubeconfigFile(kubeconfigPath, context string) (*DynamicK8sClient, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig file %q: %w", kubeconfigPath, err)
	}

	if context != "" {
		// Load kubeconfig file and set context
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
			&clientcmd.ConfigOverrides{CurrentContext: context},
		)
		config, err = clientConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to build config for context %q: %w", context, err)
		}
	}

	return NewDynamicK8sClient(config)
}

func (d *DynamicK8sClient) SetFieldManager(name string) K8sClient {
	d.fieldManager = name
	return d
}

func (d *DynamicK8sClient) WithForceConflicts(force bool) K8sClient {
	d.forceConflicts = force
	return d
}

// InvalidateDiscoveryCache implements CacheInvalidator interface
func (d *DynamicK8sClient) InvalidateDiscoveryCache(ctx context.Context) error {
	if cachedDiscovery, ok := d.discovery.(discovery.CachedDiscoveryInterface); ok {
		tflog.Debug(ctx, "Invalidating discovery cache to handle potential new CRDs")
		cachedDiscovery.Invalidate()
		return nil
	}
	// If discovery client doesn't support caching, that's fine - no-op
	tflog.Debug(ctx, "Discovery client doesn't support caching, skipping invalidation")
	return nil
}

// getResourceInterface returns the appropriate ResourceInterface, handling default namespace inference
func (d *DynamicK8sClient) getResourceInterface(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) (dynamic.ResourceInterface, error) {
	// Use discovery to check if this resource type is namespaced
	resourceList, err := d.discovery.ServerResourcesForGroupVersion(gvr.GroupVersion().String())
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
	// If namespace is empty, check if the resource is namespaced and use default
	if namespace == "" {
		resourceList, err := d.discovery.ServerResourcesForGroupVersion(gvr.GroupVersion().String())
		if err != nil {
			return nil, fmt.Errorf("failed to get resource info: %w", err)
		}

		for _, apiResource := range resourceList.APIResources {
			if apiResource.Name == gvr.Resource && apiResource.Namespaced {
				namespace = "default"
				break
			}
		}
	}

	if namespace != "" {
		return d.client.Resource(gvr).Namespace(namespace), nil
	} else {
		return d.client.Resource(gvr), nil
	}
}

// Apply performs server-side apply on the given object.
func (d *DynamicK8sClient) Apply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) error {
	gvr, err := d.getGVR(obj)
	if err != nil {
		return fmt.Errorf("failed to determine GVR: %w", err)
	}

	fieldManager := options.FieldManager
	if fieldManager == "" {
		fieldManager = d.fieldManager
	}

	force := options.Force || d.forceConflicts

	applyOpts := metav1.ApplyOptions{
		FieldManager: fieldManager,
		Force:        force,
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
}

// DryRunApply performs a dry-run server-side apply.
func (d *DynamicK8sClient) DryRunApply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) (*unstructured.Unstructured, error) {
	gvr, err := d.getGVR(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to determine GVR: %w", err)
	}

	fieldManager := options.FieldManager
	if fieldManager == "" {
		fieldManager = d.fieldManager
	}

	force := options.Force || d.forceConflicts

	applyOpts := metav1.ApplyOptions{
		FieldManager: fieldManager,
		Force:        force,
		DryRun:       []string{metav1.DryRunAll},
	}

	resource, err := d.getResourceInterface(ctx, gvr, obj)
	if err != nil {
		return nil, err
	}

	return resource.Apply(ctx, obj.GetName(), obj, applyOpts)
}

// Get retrieves an object from the cluster.
func (d *DynamicK8sClient) Get(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	resource, err := d.getResourceInterfaceByNamespace(ctx, gvr, namespace)
	if err != nil {
		return nil, err
	}

	return resource.Get(ctx, name, metav1.GetOptions{})
}

// Delete removes an object from the cluster.
func (d *DynamicK8sClient) Delete(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, options DeleteOptions) error {
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
}

// GetGVR determines the GroupVersionResource for an unstructured object.
func (d *DynamicK8sClient) GetGVR(ctx context.Context, obj *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	return d.getGVR(obj) // Use existing private method
}

// getGVR determines the GroupVersionResource for an unstructured object (private method)
func (d *DynamicK8sClient) getGVR(obj *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	gvk := obj.GroupVersionKind()
	if gvk.Empty() {
		return schema.GroupVersionResource{}, fmt.Errorf("object has no GroupVersionKind")
	}

	// Use discovery to map GVK to GVR
	resources, err := d.discovery.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to discover resources for %s: %w", gvk.GroupVersion(), err)
	}

	for _, resource := range resources.APIResources {
		if resource.Kind == gvk.Kind {
			return schema.GroupVersionResource{
				Group:    gvk.Group,
				Version:  gvk.Version,
				Resource: resource.Name,
			}, nil
		}
	}

	return schema.GroupVersionResource{}, fmt.Errorf("no resource found for %s", gvk)
}

// ===================== stubK8sClient =====================
// stubK8sClient records operations for unit tests.
type stubK8sClient struct {
	ApplyCalls     []ApplyCall
	GetCalls       []GetCall
	DeleteCalls    []DeleteCall
	DryRunCalls    []DryRunCall
	fieldManager   string
	forceConflicts bool

	// Configurable responses
	GetResponse    *unstructured.Unstructured
	GetError       error
	ApplyError     error
	DeleteError    error
	DryRunResponse *unstructured.Unstructured
	DryRunError    error
}

type ApplyCall struct {
	Object  *unstructured.Unstructured
	Options ApplyOptions
}

type GetCall struct {
	GVR       schema.GroupVersionResource
	Namespace string
	Name      string
}

type DeleteCall struct {
	GVR       schema.GroupVersionResource
	Namespace string
	Name      string
	Options   DeleteOptions
}

type DryRunCall struct {
	Object  *unstructured.Unstructured
	Options ApplyOptions
}

// NewStubK8sClient creates a new stubK8sClient for testing.
func NewStubK8sClient() *stubK8sClient {
	return &stubK8sClient{
		fieldManager: "k8sinline",
	}
}

func (s *stubK8sClient) SetFieldManager(name string) K8sClient {
	s.fieldManager = name
	return s
}

func (s *stubK8sClient) WithForceConflicts(force bool) K8sClient {
	s.forceConflicts = force
	return s
}

// InvalidateDiscoveryCache implements CacheInvalidator interface for testing
func (s *stubK8sClient) InvalidateDiscoveryCache(ctx context.Context) error {
	// No-op for stub client
	return nil
}

func (s *stubK8sClient) Apply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) error {
	s.ApplyCalls = append(s.ApplyCalls, ApplyCall{
		Object:  obj.DeepCopy(),
		Options: options,
	})
	return s.ApplyError
}

func (s *stubK8sClient) Get(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	s.GetCalls = append(s.GetCalls, GetCall{
		GVR:       gvr,
		Namespace: namespace,
		Name:      name,
	})
	return s.GetResponse, s.GetError
}

func (s *stubK8sClient) Delete(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, options DeleteOptions) error {
	s.DeleteCalls = append(s.DeleteCalls, DeleteCall{
		GVR:       gvr,
		Namespace: namespace,
		Name:      name,
		Options:   options,
	})
	return s.DeleteError
}

func (s *stubK8sClient) DryRunApply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) (*unstructured.Unstructured, error) {
	s.DryRunCalls = append(s.DryRunCalls, DryRunCall{
		Object:  obj.DeepCopy(),
		Options: options,
	})
	return s.DryRunResponse, s.DryRunError
}

func (s *stubK8sClient) GetGVR(ctx context.Context, obj *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	// For testing, return predictable GVRs
	gvk := obj.GroupVersionKind()
	switch gvk.Kind {
	case "Namespace":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}, nil
	case "Pod":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, nil
	case "Service":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, nil
	case "Deployment":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, nil
	case "ConfigMap":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}, nil
	case "Secret":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}, nil
	default:
		// For unknown types in tests, make a reasonable guess
		resource := strings.ToLower(gvk.Kind) + "s"
		return schema.GroupVersionResource{Group: gvk.Group, Version: gvk.Version, Resource: resource}, nil
	}
}

// Interface assertions ensure concrete types satisfy K8sClient and CacheInvalidator.
var _ K8sClient = (*DynamicK8sClient)(nil)
var _ K8sClient = (*stubK8sClient)(nil)
var _ CacheInvalidator = (*DynamicK8sClient)(nil)
var _ CacheInvalidator = (*stubK8sClient)(nil)
