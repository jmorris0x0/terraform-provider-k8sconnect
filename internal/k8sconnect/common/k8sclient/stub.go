// internal/k8sconnect/common/k8sclient/stub.go
package k8sclient

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

// stubK8sClient is a test implementation of K8sClient that records method calls
type stubK8sClient struct {
	fieldManager string

	// Call recording
	ApplyCalls  []ApplyCall
	GetCalls    []GetCall
	DeleteCalls []DeleteCall
	DryRunCalls []DryRunCall

	// Response configuration
	ApplyError     error
	GetResponse    *unstructured.Unstructured
	GetError       error
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

// NewStubK8sClient creates a new stub client for testing
func NewStubK8sClient() *stubK8sClient {
	return &stubK8sClient{
		fieldManager: "k8sconnect",
		ApplyCalls:   []ApplyCall{},
		GetCalls:     []GetCall{},
		DeleteCalls:  []DeleteCall{},
		DryRunCalls:  []DryRunCall{},
	}
}

func (s *stubK8sClient) Apply(ctx context.Context, obj *unstructured.Unstructured, options ApplyOptions) error {
	s.ApplyCalls = append(s.ApplyCalls, ApplyCall{
		Object:  obj,
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
	if s.GetError != nil {
		return nil, s.GetError
	}
	return s.GetResponse, nil
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
		Object:  obj,
		Options: options,
	})
	if s.DryRunError != nil {
		return nil, s.DryRunError
	}
	return s.DryRunResponse, nil
}

func (s *stubK8sClient) SetFieldManager(name string) K8sClient {
	s.fieldManager = name
	return s
}

func (s *stubK8sClient) GetGVR(ctx context.Context, obj *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	// Simple stub implementation that maps common types
	gvk := obj.GroupVersionKind()

	// Simple mapping for common Kubernetes resources
	var resource string
	switch gvk.Kind {
	case "Namespace":
		resource = "namespaces"
	case "Pod":
		resource = "pods"
	case "Service":
		resource = "services"
	case "Deployment":
		resource = "deployments"
	case "ConfigMap":
		resource = "configmaps"
	case "Secret":
		resource = "secrets"
	default:
		// For custom resources, use a simple plural form
		resource = strings.ToLower(gvk.Kind) + "s"
	}

	return schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: resource,
	}, nil
}

func (s *stubK8sClient) GetGVRFromKind(ctx context.Context, kind, namespace, name string) (schema.GroupVersionResource, *unstructured.Unstructured, error) {
	// For testing, use the same simple mapping as GetGVR
	gvk := schema.GroupVersionKind{Kind: kind}

	// Infer group/version from kind for common resources
	switch kind {
	case "Pod", "Service", "ConfigMap", "Secret", "Namespace":
		gvk.Version = "v1"
	case "Deployment", "ReplicaSet", "DaemonSet", "StatefulSet":
		gvk.Group = "apps"
		gvk.Version = "v1"
	case "Ingress":
		gvk.Group = "networking.k8s.io"
		gvk.Version = "v1"
	default:
		gvk.Version = "v1" // Default for testing
	}

	// Simple mapping for common Kubernetes resources
	var resource string
	switch kind {
	case "Namespace":
		resource = "namespaces"
	case "Pod":
		resource = "pods"
	case "Service":
		resource = "services"
	case "Deployment":
		resource = "deployments"
	case "ConfigMap":
		resource = "configmaps"
	case "Secret":
		resource = "secrets"
	default:
		resource = strings.ToLower(kind) + "s"
	}

	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: resource,
	}

	// Return the GVR and a mock object if GetResponse is set
	if s.GetResponse != nil {
		return gvr, s.GetResponse, s.GetError
	}

	// If no response configured, return an error
	if s.GetError != nil {
		return gvr, nil, s.GetError
	}

	// Default: return a minimal object for testing
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": gvk.GroupVersion().String(),
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
		},
	}

	return gvr, obj, nil
}

func (s *stubK8sClient) Patch(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, patchType types.PatchType, data []byte, options metav1.PatchOptions) (*unstructured.Unstructured, error) {
	// For stub, just return success or configured response
	return s.GetResponse, nil
}

func (s *stubK8sClient) Watch(ctx context.Context, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, fmt.Errorf("watch not implemented in stub")
}

func (s *stubK8sClient) PatchStatus(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, patchType types.PatchType, data []byte, options metav1.PatchOptions) (*unstructured.Unstructured, error) {
	// For stub, just return success or configured response
	return s.GetResponse, nil
}

func (s *stubK8sClient) GetWarnings() []string {
	// Stub never has warnings
	return nil
}

func (s *stubK8sClient) List(ctx context.Context, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	// Stub returns an empty list for testing
	return &unstructured.UnstructuredList{
		Items: []unstructured.Unstructured{},
	}, nil
}

// Interface assertion to ensure stubK8sClient satisfies K8sClient
var _ K8sClient = (*stubK8sClient)(nil)
