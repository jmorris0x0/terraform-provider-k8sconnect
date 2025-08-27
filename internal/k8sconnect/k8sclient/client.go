// internal/k8sconnect/k8sclient/client.go
package k8sclient

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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

	// GetGVRFromKind discovers the GVR and fetches the object when only the kind is known
	// This is primarily used for import operations where API version is unknown
	// Returns the GVR, the live object, and any error
	GetGVRFromKind(ctx context.Context, kind, namespace, name string) (schema.GroupVersionResource, *unstructured.Unstructured, error)

	Patch(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, patchType types.PatchType, data []byte, options metav1.PatchOptions) (*unstructured.Unstructured, error)
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
		fieldManager:   "k8sconnect",
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
	gvr, err := d.getGVR(ctx, obj)
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
	gvr, err := d.getGVR(ctx, obj)
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

	// Use discovery to map GVK to GVR - fresh call each time, simple and predictable
	resources, err := d.discovery.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		// Provide helpful error message for common scenarios
		if d.isDiscoveryError(err) {
			return schema.GroupVersionResource{}, fmt.Errorf(
				"failed to discover resources for %s: %w\n\nThis usually means:\n"+
					"1. The API group/version doesn't exist in the cluster\n"+
					"2. A CRD needs to be installed first (try: depends_on = [k8sconnect_manifest.your_crd])\n"+
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
			"2. A CRD needs to be installed first (try: depends_on = [k8sconnect_manifest.your_crd])\n"+
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

// GetGVRFromKind discovers the GVR for a resource kind without knowing the API version
// This is useful for import operations where we only have the kind
func (d *DynamicK8sClient) GetGVRFromKind(ctx context.Context, kind, namespace, name string) (schema.GroupVersionResource, *unstructured.Unstructured, error) {
	tflog.Debug(ctx, "Discovering GVR from kind", map[string]interface{}{
		"kind":      kind,
		"namespace": namespace,
		"name":      name,
	})

	// Get all API resources from the cluster
	// IMPORTANT: ServerGroupsAndResources can return partial results with a non-nil error
	// This happens when some API groups are unavailable (like metrics-server during startup)
	// We should continue with the resources we did get
	_, apiResources, err := d.discovery.ServerGroupsAndResources()
	if err != nil {
		// Check if we got any resources at all
		if apiResources == nil || len(apiResources) == 0 {
			// Complete failure - no resources discovered
			return schema.GroupVersionResource{}, nil, fmt.Errorf("failed to discover server resources: %w", err)
		}
		// Partial failure - log warning but continue with what we have
		tflog.Warn(ctx, "Partial API discovery failure (continuing with available APIs)", map[string]interface{}{
			"error":           err.Error(),
			"apis_discovered": len(apiResources),
		})
	}

	// Find all possible GVRs for this kind
	var candidates []candidateResource
	for _, apiResourceList := range apiResources {
		if apiResourceList == nil {
			continue
		}

		gv, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if err != nil {
			continue
		}

		for _, apiResource := range apiResourceList.APIResources {
			if apiResource.Kind == kind {
				gvr := schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: apiResource.Name,
				}
				candidates = append(candidates, candidateResource{
					GVR:        gvr,
					Namespaced: apiResource.Namespaced,
				})
			}
		}
	}

	if len(candidates) == 0 {
		return schema.GroupVersionResource{}, nil, fmt.Errorf("no API resource found for kind %q", kind)
	}

	// Sort candidates by preference: v1 first, then newer versions, then older
	sortCandidatesByPreference(candidates)

	// Try candidates in order - most likely first
	for i, candidate := range candidates {
		obj, err := d.tryGetResource(ctx, candidate.GVR, candidate.Namespaced, namespace, name)
		if err == nil && obj != nil {
			tflog.Debug(ctx, "Found resource using candidate GVR", map[string]interface{}{
				"candidate_index": i,
				"gvr":             candidate.GVR.String(),
				"kind":            kind,
				"name":            name,
				"namespace":       namespace,
			})
			return candidate.GVR, obj, nil
		}
	}

	// None of the candidates worked
	candidateStrings := make([]string, len(candidates))
	for i, c := range candidates {
		candidateStrings[i] = c.GVR.String()
	}

	return schema.GroupVersionResource{}, nil, fmt.Errorf(
		"resource %s/%s (kind: %s) not found. Tried API versions: %v",
		namespace, name, kind, candidateStrings)
}

type candidateResource struct {
	GVR        schema.GroupVersionResource
	Namespaced bool
}

// sortCandidatesByPreference orders candidates to try the most likely versions first
func sortCandidatesByPreference(candidates []candidateResource) {
	sort.Slice(candidates, func(i, j int) bool {
		return versionPriority(candidates[i].GVR) > versionPriority(candidates[j].GVR)
	})
}

// versionPriority returns priority score for API versions (higher = try first)
func versionPriority(gvr schema.GroupVersionResource) int {
	version := gvr.Version

	// v1 gets highest priority (most stable)
	if version == "v1" {
		return 1000
	}

	// v2, v3, etc. get high priority
	if matched, _ := regexp.MatchString(`^v\d+$`, version); matched {
		return 900
	}

	// v1beta1, v2beta1, etc. get medium priority
	if matched, _ := regexp.MatchString(`^v\d+beta\d+$`, version); matched {
		return 500
	}

	// v1alpha1, v2alpha1, etc. get lower priority
	if matched, _ := regexp.MatchString(`^v\d+alpha\d+$`, version); matched {
		return 100
	}

	// Everything else gets lowest priority
	return 0
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
	var err error

	if namespace == "" {
		result, err = d.client.Resource(gvr).Patch(ctx, name, patchType, data, options)
	} else {
		result, err = d.client.Resource(gvr).Namespace(namespace).Patch(ctx, name, patchType, data, options)
	}

	return result, err
}

// Interface assertion to ensure DynamicK8sClient satisfies K8sClient
var _ K8sClient = (*DynamicK8sClient)(nil)
