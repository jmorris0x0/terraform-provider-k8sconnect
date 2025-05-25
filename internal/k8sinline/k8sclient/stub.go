// internal/k8sinline/k8sclient/stub.go
package k8sclient

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// stubK8sClient is a test implementation of K8sClient that records method calls
type stubK8sClient struct {
	fieldManager   string
	forceConflicts bool

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
		fieldManager:   "k8sinline",
		forceConflicts: false,
		ApplyCalls:     []ApplyCall{},
		GetCalls:       []GetCall{},
		DeleteCalls:    []DeleteCall{},
		DryRunCalls:    []DryRunCall{},
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

func (s *stubK8sClient) WithForceConflicts(force bool) K8sClient {
	s.forceConflicts = force
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

// Interface assertion to ensure stubK8sClient satisfies K8sClient
var _ K8sClient = (*stubK8sClient)(nil)
