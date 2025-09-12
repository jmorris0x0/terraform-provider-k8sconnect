// internal/k8sconnect/resource/manifest/status_pruner_test.go
package manifest

import (
	"reflect"
	"testing"
)

func TestPruneStatusToField(t *testing.T) {
	tests := []struct {
		name       string
		fullStatus map[string]interface{}
		fieldPath  string
		want       map[string]interface{}
	}{
		{
			name: "simple field",
			fullStatus: map[string]interface{}{
				"phase":              "Running",
				"observedGeneration": 2,
			},
			fieldPath: "phase",
			want: map[string]interface{}{
				"phase": "Running",
			},
		},
		{
			name: "nested field",
			fullStatus: map[string]interface{}{
				"observedGeneration": 2,
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{
							"hostname": "abc.elb.com",
						},
					},
				},
			},
			fieldPath: "loadBalancer.ingress",
			want: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{
							"hostname": "abc.elb.com",
						},
					},
				},
			},
		},
		{
			name: "array element field",
			fullStatus: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{
							"hostname": "abc.elb.com",
							"ip":       "1.2.3.4",
						},
					},
				},
			},
			fieldPath: "loadBalancer.ingress[0].hostname",
			want: map[string]interface{}{
				"loadBalancer": map[string]interface{}{
					"ingress": []interface{}{
						map[string]interface{}{
							"hostname": "abc.elb.com",
						},
					},
				},
			},
		},
		{
			name: "middle array element preserves index",
			fullStatus: map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Ready", "status": "False"},
					map[string]interface{}{"type": "Progressing", "status": "True"},
				},
			},
			fieldPath: "conditions[1].status",
			want: map[string]interface{}{
				"conditions": []interface{}{
					nil, // Preserves indexing
					map[string]interface{}{
						"status": "True",
					},
				},
			},
		},
		{
			name: "non-existent field returns nil",
			fullStatus: map[string]interface{}{
				"phase": "Running",
			},
			fieldPath: "nonexistent",
			want:      nil,
		},
		{
			name: "array out of bounds returns nil",
			fullStatus: map[string]interface{}{
				"items": []interface{}{"a", "b"},
			},
			fieldPath: "items[5]",
			want:      nil,
		},
		{
			name: "strips status prefix",
			fullStatus: map[string]interface{}{
				"phase": "Running",
			},
			fieldPath: "status.phase",
			want: map[string]interface{}{
				"phase": "Running",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pruneStatusToField(tt.fullStatus, tt.fieldPath)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("pruneStatusToField()\ngot:  %#v\nwant: %#v", got, tt.want)
			}
		})
	}
}
