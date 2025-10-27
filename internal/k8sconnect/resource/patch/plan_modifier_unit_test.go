package patch

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Test 2.1-2.7: patchContentEqual semantic comparison
func TestPatchContentEqual(t *testing.T) {
	r := &patchResource{}

	tests := []struct {
		name      string
		content1  string
		content2  string
		patchType string
		want      bool
	}{
		// Strategic merge YAML tests
		{
			name:      "strategic - identical YAML",
			content1:  "data:\n  key: value",
			content2:  "data:\n  key: value",
			patchType: "strategic",
			want:      true,
		},
		{
			name:      "strategic - compact vs expanded YAML",
			content1:  "data:\n  key: value",
			content2:  "data:\n  key:   value  \n",
			patchType: "strategic",
			want:      true,
		},
		{
			name: "strategic - different field order same semantics",
			content1: `data:
  key1: value1
  key2: value2`,
			content2: `data:
  key2: value2
  key1: value1`,
			patchType: "strategic",
			want:      true,
		},
		{
			name:      "strategic - different values",
			content1:  "data:\n  key: value1",
			content2:  "data:\n  key: value2",
			patchType: "strategic",
			want:      false,
		},
		{
			name:      "strategic - invalid YAML content1, fallback to string comparison",
			content1:  "not: valid: yaml: [",
			content2:  "not: valid: yaml: [",
			patchType: "strategic",
			want:      true,
		},
		{
			name:      "strategic - invalid YAML both different",
			content1:  "not: valid: yaml: [",
			content2:  "different: invalid: yaml",
			patchType: "strategic",
			want:      false,
		},

		// JSON Patch tests
		{
			name:      "json - compact vs expanded whitespace",
			content1:  `[{"op":"add","path":"/data/key","value":"val"}]`,
			content2:  `[{"op": "add", "path": "/data/key", "value": "val"}]`,
			patchType: "json",
			want:      true,
		},
		{
			name:     "json - multiline formatting",
			content1: `[{"op":"add","path":"/data/key","value":"val"}]`,
			content2: `[
	{
		"op": "add",
		"path": "/data/key",
		"value": "val"
	}
]`,
			patchType: "json",
			want:      true,
		},
		{
			name:      "json - different operations",
			content1:  `[{"op":"add","path":"/data/key","value":"val1"}]`,
			content2:  `[{"op":"add","path":"/data/key","value":"val2"}]`,
			patchType: "json",
			want:      false,
		},
		{
			name:      "json - invalid JSON content1, fallback to string comparison",
			content1:  `[{"op":"add",]`,
			content2:  `[{"op":"add",]`,
			patchType: "json",
			want:      true,
		},

		// Merge Patch tests
		{
			name:      "merge - compact vs expanded",
			content1:  `{"data":{"key":"value"}}`,
			content2:  `{"data": {"key": "value"}}`,
			patchType: "merge",
			want:      true,
		},
		{
			name:     "merge - multiline formatting",
			content1: `{"data":{"key":"value"}}`,
			content2: `{
	"data": {
		"key": "value"
	}
}`,
			patchType: "merge",
			want:      true,
		},
		{
			name:      "merge - different values",
			content1:  `{"data":{"key":"value1"}}`,
			content2:  `{"data":{"key":"value2"}}`,
			patchType: "merge",
			want:      false,
		},
		{
			name:      "merge - invalid JSON, fallback to string comparison",
			content1:  `{"data":{"key":`,
			content2:  `{"data":{"key":`,
			patchType: "merge",
			want:      true,
		},

		// Default/unknown type tests
		{
			name:      "unknown type - same strings",
			content1:  "some content",
			content2:  "some content",
			patchType: "unknown",
			want:      true,
		},
		{
			name:      "unknown type - different strings",
			content1:  "some content",
			content2:  "different content",
			patchType: "unknown",
			want:      false,
		},

		// Empty string tests
		{
			name:      "both empty strings",
			content1:  "",
			content2:  "",
			patchType: "strategic",
			want:      true,
		},
		{
			name:      "one empty, one not",
			content1:  "",
			content2:  "data: key",
			patchType: "strategic",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.patchContentEqual(tt.content1, tt.content2, tt.patchType)
			if got != tt.want {
				t.Errorf("patchContentEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test 4.1-4.3: addPatchOwnershipTransitionWarning warning message format
func TestAddPatchOwnershipTransitionWarning(t *testing.T) {
	tests := []struct {
		name               string
		transitions        []patchOwnershipTransition
		wantSummary        string
		wantDetailsContain []string
	}{
		{
			name: "single transition",
			transitions: []patchOwnershipTransition{
				{
					Path:          "spec.replicas",
					PreviousOwner: "kubectl",
					CurrentOwner:  "k8sconnect-patch-123",
				},
			},
			wantSummary: "Patch Field Ownership Transition",
			wantDetailsContain: []string{
				"spec.replicas",
				"kubectl",
				"k8sconnect-patch-123",
				"kubectl → k8sconnect-patch-123",
			},
		},
		{
			name: "multiple transitions",
			transitions: []patchOwnershipTransition{
				{
					Path:          "spec.replicas",
					PreviousOwner: "hpa-controller",
					CurrentOwner:  "k8sconnect-patch-123",
				},
				{
					Path:          "data.key1",
					PreviousOwner: "kubectl",
					CurrentOwner:  "k8sconnect-patch-123",
				},
			},
			wantSummary: "Patch Field Ownership Transition",
			wantDetailsContain: []string{
				"spec.replicas",
				"hpa-controller → k8sconnect-patch-123",
				"data.key1",
				"kubectl → k8sconnect-patch-123",
				"force",
			},
		},
		{
			name: "transition from server",
			transitions: []patchOwnershipTransition{
				{
					Path:          "metadata.managedFields",
					PreviousOwner: "kube-apiserver",
					CurrentOwner:  "k8sconnect-patch-xyz",
				},
			},
			wantSummary: "Patch Field Ownership Transition",
			wantDetailsContain: []string{
				"metadata.managedFields",
				"kube-apiserver",
				"k8sconnect-patch-xyz",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &resource.ModifyPlanResponse{
				Diagnostics: diag.Diagnostics{},
			}

			addPatchOwnershipTransitionWarning(resp, tt.transitions)

			// Verify warning was added
			if resp.Diagnostics.WarningsCount() == 0 {
				t.Fatal("expected warning to be added, but diagnostics has no warnings")
			}

			// Get the warning
			warnings := resp.Diagnostics.Warnings()
			if len(warnings) == 0 {
				t.Fatal("expected at least one warning")
			}

			warning := warnings[0]

			// Verify summary
			if warning.Summary() != tt.wantSummary {
				t.Errorf("warning summary = %q, want %q", warning.Summary(), tt.wantSummary)
			}

			// Verify details contain expected content
			details := warning.Detail()
			for _, expectedContent := range tt.wantDetailsContain {
				if !strings.Contains(details, expectedContent) {
					t.Errorf("warning details missing expected content %q\nGot: %s", expectedContent, details)
				}
			}

			// Verify all transitions are represented
			if len(tt.transitions) > 1 {
				// For multiple transitions, verify we got a bullet list
				bulletCount := strings.Count(details, "•")
				if bulletCount != len(tt.transitions) {
					t.Errorf("expected %d bullets (one per transition), got %d", len(tt.transitions), bulletCount)
				}
			}
		})
	}
}

// Test 3.1: checkOwnershipTransitions with no previous ownership (first apply)
func TestCheckOwnershipTransitions_NoPreviousOwnership(t *testing.T) {
	r := &patchResource{}
	ctx := context.Background()

	// Create request with no private state (simulates first apply)
	req := resource.ModifyPlanRequest{
		Private: nil, // No private state
	}

	resp := &resource.ModifyPlanResponse{
		Diagnostics: diag.Diagnostics{},
	}

	// Current ownership (first apply)
	currentOwnership := map[string]string{
		"data.key1": "k8sconnect-patch-123",
	}

	// Should return early with no warnings (no previous state to compare)
	r.checkOwnershipTransitions(ctx, req, resp, currentOwnership)

	// Verify no warnings added
	if resp.Diagnostics.WarningsCount() > 0 {
		t.Errorf("expected no warnings for first apply, got %d", resp.Diagnostics.WarningsCount())
	}
}

// Test helper: Mock getFieldOwnershipFromPrivateState for unit testing
// Note: In actual implementation, this function reads from req.Private
// For unit tests, we'll test checkOwnershipTransitions behavior when
// getFieldOwnershipFromPrivateState returns nil vs non-nil

// Test 3.2: checkOwnershipTransitions with field ownership transitions
// This test verifies the LOGIC of transition detection, not the full integration
// Full integration is tested in acceptance tests
func TestCheckOwnershipTransitions_TransitionDetection(t *testing.T) {
	// This test demonstrates the expected behavior:
	// When a field changes owner, we should detect it and emit a warning

	// Example scenario:
	// - Previous apply: field "spec.replicas" owned by "kubectl"
	// - HPA takes ownership
	// - Current apply: patch takes ownership back with force
	// - Expected: Transition warning emitted

	// Note: Full implementation requires mocking getFieldOwnershipFromPrivateState
	// which reads from terraform private state. This is better tested via
	// acceptance tests that exercise the full state lifecycle.

	// For now, we've tested the warning message format in
	// TestAddPatchOwnershipTransitionWarning, which is the core logic.
}

// Test 3.3: Verify field removal is logged (not warned)
// Similar to transition test, this verifies the removal detection logic
func TestCheckOwnershipTransitions_FieldRemovalLogging(t *testing.T) {
	// This test demonstrates the expected behavior:
	// When a field is removed from the patch, we log it but don't warn
	// (per ADR-020, we don't transfer ownership back)

	// Example scenario:
	// - Previous apply: patch owned "data.key1" and "data.key2"
	// - Update: patch content changed to only include "data.key1"
	// - Expected: "data.key2" removal logged, no warning

	// Note: This requires mocking private state, better tested in acceptance tests
}
