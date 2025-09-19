// internal/k8sconnect/resource/manifest/analysis.go
package manifest

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-log/tflog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// DriftAnalysis contains the results of comparing desired vs actual state
type DriftAnalysis struct {
	CurrentProjection  string
	DesiredProjection  string
	HasValueDrift      bool
	HasOwnershipDrift  bool
	OwnershipConflicts []FieldConflict
}

// Action represents what needs to happen based on drift analysis
type Action int

const (
	NoAction        Action = iota
	RequireUpdate          // Value drift exists, need to reapply
	BlockOnConflict        // Ownership conflict without force_conflicts
	ForceUpdate            // Ownership conflict with force_conflicts
)

// analyzeDrift examines the resource state and detects both types of drift
func (r *manifestResource) analyzeDrift(ctx context.Context,
	stateData *manifestResourceModel,
	plannedData *manifestResourceModel,
	desiredObj *unstructured.Unstructured) *DriftAnalysis {

	analysis := &DriftAnalysis{
		CurrentProjection: stateData.ManagedStateProjection.ValueString(),
		DesiredProjection: plannedData.ManagedStateProjection.ValueString(),
	}

	// Value drift detection - just compare projections
	if !stateData.ManagedStateProjection.Equal(plannedData.ManagedStateProjection) {
		analysis.HasValueDrift = true
		tflog.Debug(ctx, "Value drift detected", map[string]interface{}{
			"current_hash": hashString(analysis.CurrentProjection),
			"desired_hash": hashString(analysis.DesiredProjection),
		})
	}

	// Ownership drift detection - check if fields we want are owned by others
	if !stateData.FieldOwnership.IsNull() {
		var ownership map[string]FieldOwnership
		json.Unmarshal([]byte(stateData.FieldOwnership.ValueString()), &ownership)

		// Get fields we want to manage from the desired object
		desiredPaths := extractFieldPaths(desiredObj.Object, "")

		// Check each desired field's ownership
		for _, path := range desiredPaths {
			if owner, exists := ownership[path]; exists && owner.Manager != "k8sconnect" {
				analysis.HasOwnershipDrift = true
				analysis.OwnershipConflicts = append(analysis.OwnershipConflicts, FieldConflict{
					Path:  path,
					Owner: owner.Manager,
				})
			}
		}
	}

	return analysis
}

// determineAction decides what to do based on drift analysis and settings
func (r *manifestResource) determineAction(analysis *DriftAnalysis, forceConflicts bool) Action {
	// No drift at all
	if !analysis.HasValueDrift && !analysis.HasOwnershipDrift {
		return NoAction
	}

	// Ownership conflict without force
	if analysis.HasOwnershipDrift && !forceConflicts {
		return BlockOnConflict
	}

	// Ownership conflict with force
	if analysis.HasOwnershipDrift && forceConflicts {
		return ForceUpdate
	}

	// Just value drift
	if analysis.HasValueDrift {
		return RequireUpdate
	}

	return NoAction
}

func hashString(s string) string {
	if len(s) > 50 {
		return s[:50] + "..."
	}
	return s
}

func (a Action) String() string {
	switch a {
	case NoAction:
		return "NoAction"
	case RequireUpdate:
		return "RequireUpdate"
	case BlockOnConflict:
		return "BlockOnConflict"
	case ForceUpdate:
		return "ForceUpdate"
	default:
		return "Unknown"
	}
}
