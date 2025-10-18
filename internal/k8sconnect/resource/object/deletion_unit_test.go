// internal/k8sconnect/resource/object/deletion_unit_test.go
package object

import (
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestExplainFinalizer(t *testing.T) {
	tests := []struct {
		name              string
		finalizer         string
		expectKnown       bool
		expectExplanation string
		expectSource      bool // Does it have a source URL?
	}{
		{
			name:              "PVC protection finalizer",
			finalizer:         "kubernetes.io/pvc-protection",
			expectKnown:       true,
			expectExplanation: "Volume is still attached to a pod",
			expectSource:      true,
		},
		{
			name:              "PV protection finalizer",
			finalizer:         "kubernetes.io/pv-protection",
			expectKnown:       true,
			expectExplanation: "PersistentVolume is still bound to a claim",
			expectSource:      true,
		},
		{
			name:              "Kubernetes namespace finalizer",
			finalizer:         "kubernetes",
			expectKnown:       true,
			expectExplanation: "Namespace is deleting all contained resources",
			expectSource:      true,
		},
		{
			name:              "Foreground deletion finalizer",
			finalizer:         "foregroundDeletion",
			expectKnown:       true,
			expectExplanation: "Waiting for owned resources to delete first",
			expectSource:      true,
		},
		{
			name:              "Orphan finalizer",
			finalizer:         "orphan",
			expectKnown:       true,
			expectExplanation: "Dependents will be orphaned (not deleted)",
			expectSource:      true,
		},
		{
			name:        "Custom unknown finalizer",
			finalizer:   "custom.example.com/finalizer",
			expectKnown: false,
		},
		{
			name:        "Another custom finalizer",
			finalizer:   "my-operator/cleanup",
			expectKnown: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result := explainFinalizer(tc.finalizer)

			// Should always contain the finalizer name
			if !strings.Contains(result, tc.finalizer) {
				t.Errorf("expected result to contain finalizer %q, got: %s", tc.finalizer, result)
			}

			if tc.expectKnown {
				// Known finalizers should have explanation and source
				if !strings.Contains(result, tc.expectExplanation) {
					t.Errorf("expected explanation containing %q, got: %s", tc.expectExplanation, result)
				}
				if tc.expectSource && !strings.Contains(result, "https://") {
					t.Errorf("expected source URL in result, got: %s", result)
				}
			} else {
				// Unknown finalizers should mention custom
				if !strings.Contains(result, "custom finalizer") {
					t.Errorf("expected 'custom finalizer' for unknown finalizer, got: %s", result)
				}
			}
		})
	}
}

func TestFormatNamespaceDeletionDiagnostics(t *testing.T) {
	tests := []struct {
		name                    string
		resourceCounts          map[string]int
		resourcesWithFinalizers []string
		totalResources          int
		expectContains          []string
	}{
		{
			name: "single resource type",
			resourceCounts: map[string]int{
				"Pod": 5,
			},
			resourcesWithFinalizers: []string{},
			totalResources:          5,
			expectContains: []string{
				"5 resources",
				"5 Pod",
			},
		},
		{
			name: "multiple resource types sorted by count",
			resourceCounts: map[string]int{
				"Pod":        10,
				"Service":    3,
				"ConfigMap":  7,
				"Deployment": 2,
			},
			resourcesWithFinalizers: []string{},
			totalResources:          22,
			expectContains: []string{
				"22 resources",
				"10 Pod",
				"7 ConfigMap",
				"3 Service",
				"2 Deployment",
			},
		},
		{
			name: "more than 5 types shows truncation",
			resourceCounts: map[string]int{
				"Pod":         10,
				"Service":     8,
				"ConfigMap":   6,
				"Deployment":  4,
				"ReplicaSet":  3,
				"StatefulSet": 2,
				"DaemonSet":   1,
				"Job":         1,
			},
			resourcesWithFinalizers: []string{},
			totalResources:          35,
			expectContains: []string{
				"35 resources",
				"10 Pod",
				"8 Service",
				"6 ConfigMap",
				"4 Deployment",
				"3 ReplicaSet",
				"other types", // Truncation message
			},
		},
		{
			name: "resources with finalizers shown",
			resourceCounts: map[string]int{
				"Pod": 5,
				"PVC": 2,
			},
			resourcesWithFinalizers: []string{
				"PVC/data-volume (finalizers: [kubernetes.io/pvc-protection])",
				"Pod/stuck-pod (finalizers: [custom.io/finalizer])",
			},
			totalResources: 7,
			expectContains: []string{
				"7 resources",
				"Resources with finalizers",
				"PVC/data-volume",
				"kubernetes.io/pvc-protection",
				"Pod/stuck-pod",
				"custom.io/finalizer",
			},
		},
		{
			name: "many finalizers shows truncation",
			resourceCounts: map[string]int{
				"Pod": 10,
			},
			resourcesWithFinalizers: []string{
				"Pod/pod1 (finalizers: [f1])",
				"Pod/pod2 (finalizers: [f2])",
				"Pod/pod3 (finalizers: [f3])",
				"Pod/pod4 (finalizers: [f4])",
				"Pod/pod5 (finalizers: [f5])",
				"Pod/pod6 (finalizers: [f6])",
				"Pod/pod7 (finalizers: [f7])",
			},
			totalResources: 10,
			expectContains: []string{
				"10 resources",
				"Resources with finalizers",
				"Pod/pod1",
				"Pod/pod5",
				"and 2 more", // Shows truncation for 6 and 7
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result := formatNamespaceDeletionDiagnostics(tc.resourceCounts, tc.resourcesWithFinalizers, tc.totalResources)

			for _, expected := range tc.expectContains {
				if !strings.Contains(result, expected) {
					t.Errorf("expected result to contain %q, got:\n%s", expected, result)
				}
			}
		})
	}
}

func TestGetDeleteTimeout(t *testing.T) {
	r := &objectResource{}

	tests := []struct {
		name            string
		deleteTimeout   types.String
		yamlBody        string
		expectedTimeout time.Duration
	}{
		{
			name:            "explicit timeout set",
			deleteTimeout:   types.StringValue("20m"),
			yamlBody:        "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test",
			expectedTimeout: 20 * time.Minute,
		},
		{
			name:            "namespace default timeout",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test",
			expectedTimeout: 15 * time.Minute,
		},
		{
			name:            "PVC default timeout",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: test",
			expectedTimeout: 10 * time.Minute,
		},
		{
			name:            "PV default timeout",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "apiVersion: v1\nkind: PersistentVolume\nmetadata:\n  name: test",
			expectedTimeout: 10 * time.Minute,
		},
		{
			name:            "CRD default timeout",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: test",
			expectedTimeout: 15 * time.Minute,
		},
		{
			name:            "StatefulSet default timeout",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: test",
			expectedTimeout: 8 * time.Minute,
		},
		{
			name:            "Job default timeout",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: test",
			expectedTimeout: 8 * time.Minute,
		},
		{
			name:            "CronJob default timeout",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "apiVersion: batch/v1\nkind: CronJob\nmetadata:\n  name: test",
			expectedTimeout: 8 * time.Minute,
		},
		{
			name:            "Pod default timeout",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test",
			expectedTimeout: 5 * time.Minute,
		},
		{
			name:            "Deployment default timeout",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: test",
			expectedTimeout: 5 * time.Minute,
		},
		{
			name:            "invalid timeout fallback",
			deleteTimeout:   types.StringValue("not-a-duration"),
			yamlBody:        "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test",
			expectedTimeout: 5 * time.Minute,
		},
		{
			name:            "invalid YAML fallback",
			deleteTimeout:   types.StringNull(),
			yamlBody:        "invalid: yaml: content:",
			expectedTimeout: 5 * time.Minute,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			data := objectResourceModel{
				DeleteTimeout: tc.deleteTimeout,
				YAMLBody:      types.StringValue(tc.yamlBody),
			}

			timeout := r.getDeleteTimeout(data)

			if timeout != tc.expectedTimeout {
				t.Errorf("expected timeout %v, got %v", tc.expectedTimeout, timeout)
			}
		})
	}
}

func TestNamespaceFlag(t *testing.T) {
	r := &objectResource{}

	tests := []struct {
		name       string
		namespace  string
		expectFlag string
	}{
		{
			name:       "namespaced resource",
			namespace:  "default",
			expectFlag: "-n default",
		},
		{
			name:       "namespaced resource with dashes",
			namespace:  "kube-system",
			expectFlag: "-n kube-system",
		},
		{
			name:       "cluster-scoped resource",
			namespace:  "",
			expectFlag: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{}
			if tc.namespace != "" {
				obj.SetNamespace(tc.namespace)
			}

			flag := r.namespaceFlag(obj)

			if flag != tc.expectFlag {
				t.Errorf("expected flag %q, got %q", tc.expectFlag, flag)
			}
		})
	}
}

func TestGetDiscoveryClient(t *testing.T) {
	r := &objectResource{}

	t.Run("returns stub when client doesn't implement interface", func(t *testing.T) {
		// Use the existing stub client from k8sclient package
		stubClient := k8sclient.NewStubK8sClient()

		dc := r.getDiscoveryClient(stubClient)

		// Should return stubDiscovery (since stub doesn't implement discovery interface)
		if dc == nil {
			t.Fatal("expected non-nil discovery client")
		}

		// Stub should return error
		_, _, err := dc.ServerGroupsAndResources()
		if err == nil {
			t.Error("expected stub to return error")
		}
		if !strings.Contains(err.Error(), "discovery client not available") {
			t.Errorf("expected 'discovery client not available' error, got: %v", err)
		}
	})

	t.Run("returns actual client when interface is implemented", func(t *testing.T) {
		// Create a client that wraps stub and adds discovery interface
		mockClient := &stubWithDiscovery{
			K8sClient: k8sclient.NewStubK8sClient(),
			groups: []*metav1.APIGroup{
				{
					Name: "apps",
					Versions: []metav1.GroupVersionForDiscovery{
						{GroupVersion: "apps/v1", Version: "v1"},
					},
				},
			},
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: "v1",
					APIResources: []metav1.APIResource{
						{Name: "pods", Kind: "Pod", Namespaced: true},
					},
				},
			},
		}

		dc := r.getDiscoveryClient(mockClient)

		// Should return the mock client with discovery
		groups, resources, err := dc.ServerGroupsAndResources()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(groups) != 1 || groups[0].Name != "apps" {
			t.Errorf("expected 1 group named 'apps', got: %v", groups)
		}
		if len(resources) != 1 || resources[0].GroupVersion != "v1" {
			t.Errorf("expected 1 resource list with GroupVersion 'v1', got: %v", resources)
		}
	})
}

// stubWithDiscovery wraps the existing stub and adds discovery interface
type stubWithDiscovery struct {
	k8sclient.K8sClient
	groups    []*metav1.APIGroup
	resources []*metav1.APIResourceList
}

func (s *stubWithDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return s.groups, s.resources, nil
}
