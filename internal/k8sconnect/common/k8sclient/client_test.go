// internal/k8sconnect/common/k8sclient/client_test.go
package k8sclient

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestDiscoveryErrorDetection(t *testing.T) {
	client := &DynamicK8sClient{}

	tests := []struct {
		name        string
		err         error
		shouldMatch bool
	}{
		{
			name:        "nil error",
			err:         nil,
			shouldMatch: false,
		},
		{
			name:        "discovery error - could not find requested resource",
			err:         fmt.Errorf("could not find the requested resource"),
			shouldMatch: true,
		},
		{
			name:        "discovery error - unable to retrieve server apis",
			err:         fmt.Errorf("unable to retrieve the complete list of server APIs"),
			shouldMatch: true,
		},
		{
			name:        "discovery error - server could not find requested resource",
			err:         fmt.Errorf("the server could not find the requested resource"),
			shouldMatch: true,
		},
		{
			name:        "discovery error - no resources found",
			err:         fmt.Errorf("no resources found"),
			shouldMatch: true,
		},
		{
			name:        "network error",
			err:         fmt.Errorf("connection refused"),
			shouldMatch: false,
		},
		{
			name:        "permission error",
			err:         fmt.Errorf("forbidden: access denied"),
			shouldMatch: false,
		},
		{
			name:        "timeout error",
			err:         fmt.Errorf("request timeout"),
			shouldMatch: false,
		},
		{
			name:        "invalid error",
			err:         fmt.Errorf("invalid request"),
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.isDiscoveryError(tt.err)
			if result != tt.shouldMatch {
				t.Errorf("isDiscoveryError(%v) = %v, want %v", tt.err, result, tt.shouldMatch)
			}
		})
	}
}

func TestListAvailableKinds(t *testing.T) {
	client := &DynamicK8sClient{}

	tests := []struct {
		name     string
		input    *metav1.APIResourceList
		expected string
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: "none",
		},
		{
			name: "empty list",
			input: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{},
			},
			expected: "none",
		},
		{
			name: "single resource",
			input: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{
					{Kind: "Pod"},
				},
			},
			expected: "Pod",
		},
		{
			name: "multiple resources",
			input: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{
					{Kind: "Pod"},
					{Kind: "Service"},
					{Kind: "Deployment"},
				},
			},
			expected: "Pod, Service, Deployment",
		},
		{
			name: "resources with empty kinds",
			input: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{
					{Kind: "Pod"},
					{Kind: ""}, // Empty kind should be skipped
					{Kind: "Service"},
				},
			},
			expected: "Pod, Service",
		},
		{
			name: "more than 10 resources",
			input: &metav1.APIResourceList{
				APIResources: []metav1.APIResource{
					{Kind: "Pod"}, {Kind: "Service"}, {Kind: "Deployment"}, {Kind: "ConfigMap"}, {Kind: "Secret"},
					{Kind: "Namespace"}, {Kind: "Node"}, {Kind: "PersistentVolume"}, {Kind: "PersistentVolumeClaim"}, {Kind: "ServiceAccount"},
					{Kind: "Role"}, {Kind: "RoleBinding"},
				},
			},
			expected: "Pod, Service, Deployment, ConfigMap, Secret, Namespace, Node, PersistentVolume, PersistentVolumeClaim, ServiceAccount, ...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.listAvailableKinds(tt.input)
			if result != tt.expected {
				t.Errorf("listAvailableKinds() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestStubK8sClient_GetGVR(t *testing.T) {
	stubClient := NewStubK8sClient()
	ctx := context.Background()

	tests := []struct {
		name             string
		obj              *unstructured.Unstructured
		expectedGroup    string
		expectedVersion  string
		expectedResource string
	}{
		{
			name: "namespace",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Namespace",
					"metadata": map[string]interface{}{
						"name": "test-namespace",
					},
				},
			},
			expectedGroup:    "",
			expectedVersion:  "v1",
			expectedResource: "namespaces",
		},
		{
			name: "deployment",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment",
					},
				},
			},
			expectedGroup:    "apps",
			expectedVersion:  "v1",
			expectedResource: "deployments",
		},
		{
			name: "pod",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata": map[string]interface{}{
						"name": "test-pod",
					},
				},
			},
			expectedGroup:    "",
			expectedVersion:  "v1",
			expectedResource: "pods",
		},
		{
			name: "custom resource",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "custom.io/v1",
					"kind":       "MyCustomResource",
					"metadata": map[string]interface{}{
						"name": "test-custom",
					},
				},
			},
			expectedGroup:    "custom.io",
			expectedVersion:  "v1",
			expectedResource: "mycustomresources",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvr, err := stubClient.GetGVR(ctx, tt.obj)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if gvr.Group != tt.expectedGroup {
				t.Errorf("expected group %q, got %q", tt.expectedGroup, gvr.Group)
			}

			if gvr.Version != tt.expectedVersion {
				t.Errorf("expected version %q, got %q", tt.expectedVersion, gvr.Version)
			}

			if gvr.Resource != tt.expectedResource {
				t.Errorf("expected resource %q, got %q", tt.expectedResource, gvr.Resource)
			}
		})
	}
}

func TestStubK8sClient_Apply(t *testing.T) {
	stubClient := NewStubK8sClient()
	ctx := context.Background()

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "test-namespace",
			},
		},
	}

	options := ApplyOptions{
		FieldManager: "test-manager",
		Force:        true,
	}

	err := stubClient.Apply(ctx, obj, options)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the call was recorded
	if len(stubClient.ApplyCalls) != 1 {
		t.Fatalf("expected 1 apply call, got %d", len(stubClient.ApplyCalls))
	}

	call := stubClient.ApplyCalls[0]
	if call.Object.GetKind() != "Namespace" {
		t.Errorf("expected kind Namespace, got %s", call.Object.GetKind())
	}

	if call.Options.FieldManager != "test-manager" {
		t.Errorf("expected field manager test-manager, got %s", call.Options.FieldManager)
	}

	if call.Options.Force != true {
		t.Errorf("expected force true, got %v", call.Options.Force)
	}
}

func TestStubK8sClient_Get(t *testing.T) {
	stubClient := NewStubK8sClient()
	ctx := context.Background()

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	// Set up response
	expectedObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "test-namespace",
			},
		},
	}
	stubClient.GetResponse = expectedObj

	result, err := stubClient.Get(ctx, gvr, "", "test-namespace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != expectedObj {
		t.Errorf("expected same object reference")
	}

	// Verify the call was recorded
	if len(stubClient.GetCalls) != 1 {
		t.Fatalf("expected 1 get call, got %d", len(stubClient.GetCalls))
	}

	call := stubClient.GetCalls[0]
	if call.GVR != gvr {
		t.Errorf("expected GVR %v, got %v", gvr, call.GVR)
	}

	if call.Name != "test-namespace" {
		t.Errorf("expected name test-namespace, got %s", call.Name)
	}
}

func TestStubK8sClient_Delete(t *testing.T) {
	stubClient := NewStubK8sClient()
	ctx := context.Background()

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	gracePeriod := int64(30)
	options := DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	}

	err := stubClient.Delete(ctx, gvr, "", "test-namespace", options)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the call was recorded
	if len(stubClient.DeleteCalls) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(stubClient.DeleteCalls))
	}

	call := stubClient.DeleteCalls[0]
	if call.GVR != gvr {
		t.Errorf("expected GVR %v, got %v", gvr, call.GVR)
	}

	if call.Name != "test-namespace" {
		t.Errorf("expected name test-namespace, got %s", call.Name)
	}

	if call.Options.GracePeriodSeconds == nil || *call.Options.GracePeriodSeconds != 30 {
		t.Errorf("expected grace period 30, got %v", call.Options.GracePeriodSeconds)
	}
}

func TestStubK8sClient_DryRunApply(t *testing.T) {
	stubClient := NewStubK8sClient()
	ctx := context.Background()

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "test-namespace",
			},
		},
	}

	options := ApplyOptions{
		FieldManager: "test-manager",
		DryRun:       []string{"All"},
	}

	// Set up response
	expectedObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "test-namespace",
				"uid":  "test-uid",
			},
		},
	}
	stubClient.DryRunResponse = expectedObj

	result, err := stubClient.DryRunApply(ctx, obj, options)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != expectedObj {
		t.Errorf("expected same object reference")
	}

	// Verify the call was recorded
	if len(stubClient.DryRunCalls) != 1 {
		t.Fatalf("expected 1 dry run call, got %d", len(stubClient.DryRunCalls))
	}

	call := stubClient.DryRunCalls[0]
	if call.Object.GetKind() != "Namespace" {
		t.Errorf("expected kind Namespace, got %s", call.Object.GetKind())
	}

	if call.Options.FieldManager != "test-manager" {
		t.Errorf("expected field manager test-manager, got %s", call.Options.FieldManager)
	}

	if len(call.Options.DryRun) != 1 || call.Options.DryRun[0] != "All" {
		t.Errorf("expected dry run [All], got %v", call.Options.DryRun)
	}
}

func TestStubK8sClient_ErrorHandling(t *testing.T) {
	stubClient := NewStubK8sClient()
	ctx := context.Background()

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "test-namespace",
			},
		},
	}

	// Test Apply error
	expectedError := fmt.Errorf("apply failed")
	stubClient.ApplyError = expectedError

	err := stubClient.Apply(ctx, obj, ApplyOptions{})
	if err != expectedError {
		t.Errorf("expected apply error %v, got %v", expectedError, err)
	}

	// Test Get error
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	stubClient.GetError = expectedError

	_, err = stubClient.Get(ctx, gvr, "", "test")
	if err != expectedError {
		t.Errorf("expected get error %v, got %v", expectedError, err)
	}

	// Test Delete error
	stubClient.DeleteError = expectedError

	err = stubClient.Delete(ctx, gvr, "", "test", DeleteOptions{})
	if err != expectedError {
		t.Errorf("expected delete error %v, got %v", expectedError, err)
	}

	// Test DryRunApply error
	stubClient.DryRunError = expectedError

	_, err = stubClient.DryRunApply(ctx, obj, ApplyOptions{})
	if err != expectedError {
		t.Errorf("expected dry run error %v, got %v", expectedError, err)
	}
}

func TestStubK8sClient_CallRecording(t *testing.T) {
	stubClient := NewStubK8sClient()
	ctx := context.Background()

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]interface{}{
				"name": "test-pod",
			},
		},
	}

	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

	// Make multiple calls
	stubClient.Apply(ctx, obj, ApplyOptions{FieldManager: "test1"})
	stubClient.Apply(ctx, obj, ApplyOptions{FieldManager: "test2"})
	stubClient.Get(ctx, gvr, "default", "pod1")
	stubClient.Get(ctx, gvr, "kube-system", "pod2")
	stubClient.Delete(ctx, gvr, "default", "pod1", DeleteOptions{})

	// Verify all calls were recorded
	if len(stubClient.ApplyCalls) != 2 {
		t.Errorf("expected 2 apply calls, got %d", len(stubClient.ApplyCalls))
	}

	if len(stubClient.GetCalls) != 2 {
		t.Errorf("expected 2 get calls, got %d", len(stubClient.GetCalls))
	}

	if len(stubClient.DeleteCalls) != 1 {
		t.Errorf("expected 1 delete call, got %d", len(stubClient.DeleteCalls))
	}

	// Verify call details
	if stubClient.ApplyCalls[0].Options.FieldManager != "test1" {
		t.Errorf("expected first apply field manager test1, got %s", stubClient.ApplyCalls[0].Options.FieldManager)
	}

	if stubClient.ApplyCalls[1].Options.FieldManager != "test2" {
		t.Errorf("expected second apply field manager test2, got %s", stubClient.ApplyCalls[1].Options.FieldManager)
	}

	if stubClient.GetCalls[0].Namespace != "default" {
		t.Errorf("expected first get namespace default, got %s", stubClient.GetCalls[0].Namespace)
	}

	if stubClient.GetCalls[1].Namespace != "kube-system" {
		t.Errorf("expected second get namespace kube-system, got %s", stubClient.GetCalls[1].Namespace)
	}
}
