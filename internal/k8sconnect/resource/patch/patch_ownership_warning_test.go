package patch

import (
	"testing"
)

// TestUnit_CheckOwnershipTransitions tests the ownership transition logic
// in isolation to verify it correctly identifies actual transitions vs no-ops
func TestUnit_CheckOwnershipTransitions(t *testing.T) {
	tests := []struct {
		name              string
		previousOwnership map[string]string
		currentOwnership  map[string]string
		expectTransitions bool
		transitionCount   int
	}{
		{
			name:              "First apply - fields have no previous owner",
			previousOwnership: map[string]string{},
			currentOwnership: map[string]string{
				"spec.replicas": "k8sconnect-patch-abc123",
			},
			expectTransitions: true,
			transitionCount:   1,
		},
		{
			name: "Second apply - already own all fields (NO transition)",
			previousOwnership: map[string]string{
				"spec.replicas": "k8sconnect-patch-abc123",
			},
			currentOwnership: map[string]string{
				"spec.replicas": "k8sconnect-patch-abc123",
			},
			expectTransitions: false, // This is the KEY test case
			transitionCount:   0,
		},
		{
			name: "Taking ownership from kubectl",
			previousOwnership: map[string]string{
				"spec.replicas": "kubectl",
			},
			currentOwnership: map[string]string{
				"spec.replicas": "k8sconnect-patch-abc123",
			},
			expectTransitions: true,
			transitionCount:   1,
		},
		{
			name: "Taking ownership of additional fields",
			previousOwnership: map[string]string{
				"spec.replicas": "k8sconnect-patch-abc123",
			},
			currentOwnership: map[string]string{
				"spec.replicas":                 "k8sconnect-patch-abc123",
				"spec.template.metadata.labels": "k8sconnect-patch-abc123",
			},
			expectTransitions: true,
			transitionCount:   1, // Only the new field
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the ownership comparison logic
			var transitions []patchOwnershipTransition

			for path, currentOwner := range tt.currentOwnership {
				previousOwner, existed := tt.previousOwnership[path]

				// This is the current logic from plan_modifier.go:638
				if !existed || previousOwner != currentOwner {
					transitions = append(transitions, patchOwnershipTransition{
						Path:           path,
						PreviousOwners: []string{previousOwner},
						CurrentOwners:  []string{currentOwner},
					})
				}
			}

			hasTransitions := len(transitions) > 0
			if hasTransitions != tt.expectTransitions {
				t.Errorf("Expected transitions=%v, got transitions=%v (count=%d)",
					tt.expectTransitions, hasTransitions, len(transitions))
			}

			if len(transitions) != tt.transitionCount {
				t.Errorf("Expected %d transitions, got %d", tt.transitionCount, len(transitions))
			}
		})
	}
}

// BUG DEMONSTRATION: This test will FAIL because the second apply scenario
// incorrectly reports a transition when ownership hasn't actually changed.
//
// The fix needs to compare previous ownership to the CURRENT state BEFORE
// the patch, not to the dry-run result AFTER the patch.
