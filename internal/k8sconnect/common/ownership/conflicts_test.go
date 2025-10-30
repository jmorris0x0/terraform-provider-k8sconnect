package ownership

import (
	"strings"
	"testing"
)

// TestClassifyConflict_AllRows tests all 16 rows of the ADR-021 state machine exhaustively.
// This ensures mathematical completeness - every possible (prev, now, config, external) combination is tested.
func TestClassifyConflict_AllRows(t *testing.T) {
	tests := []struct {
		name            string
		prevOwned       bool
		nowOwned        bool
		configChanged   bool
		externalChanged bool
		want            ConflictType
		rowNumber       int // ADR-021 row number for reference
		scenario        string
	}{
		// Row 0: (F, F, F, F) - Unmanaged Stable
		{
			name:            "row_0_unmanaged_stable",
			prevOwned:       false,
			nowOwned:        false,
			configChanged:   false,
			externalChanged: false,
			want:            NoConflict,
			rowNumber:       0,
			scenario:        "Field in ignore_fields, nothing changes",
		},

		// Row 1: (F, F, F, T) - Unmanaged External Change
		{
			name:            "row_1_unmanaged_external_change",
			prevOwned:       false,
			nowOwned:        false,
			configChanged:   false,
			externalChanged: true,
			want:            NoConflict,
			rowNumber:       1,
			scenario:        "Field in ignore_fields, external modifies it (expected)",
		},

		// Row 2: (F, F, T, F) - Config Change, Still Unmanaged
		{
			name:            "row_2_config_still_unmanaged",
			prevOwned:       false,
			nowOwned:        false,
			configChanged:   true,
			externalChanged: false,
			want:            NoConflict,
			rowNumber:       2,
			scenario:        "Modify yaml_body value of ignored field (redundant)",
		},

		// Row 3: (F, F, T, T) - Unmanaged, Both Changed
		{
			name:            "row_3_unmanaged_both_changed",
			prevOwned:       false,
			nowOwned:        false,
			configChanged:   true,
			externalChanged: true,
			want:            NoConflict,
			rowNumber:       3,
			scenario:        "Add to ignore_fields + external modifies simultaneously",
		},

		// Row 4: (F, T, F, F) - IMPOSSIBLE
		// Can't gain ownership without config or external trigger
		{
			name:            "row_4_impossible_spontaneous_gain",
			prevOwned:       false,
			nowOwned:        true,
			configChanged:   false,
			externalChanged: false,
			want:            NoConflict, // Safe default for impossible state
			rowNumber:       4,
			scenario:        "IMPOSSIBLE: Spontaneous ownership gain",
		},

		// Row 5: (F, T, F, T) - IMPOSSIBLE
		// External change alone can't cause us to claim ownership
		{
			name:            "row_5_impossible_external_causes_gain",
			prevOwned:       false,
			nowOwned:        true,
			configChanged:   false,
			externalChanged: true,
			want:            NoConflict, // Safe default for impossible state
			rowNumber:       5,
			scenario:        "IMPOSSIBLE: External causes us to gain ownership",
		},

		// Row 6: (F, T, T, F) - TAKING (first time, no conflict)
		{
			name:            "row_6_taking_first_time",
			prevOwned:       false,
			nowOwned:        true,
			configChanged:   true,
			externalChanged: false,
			want:            NoConflict,
			rowNumber:       6,
			scenario:        "Remove field from ignore_fields (clean taking)",
		},

		// Row 7: (F, T, T, T) - TAKING with Conflict
		{
			name:            "row_7_taking_with_conflict",
			prevOwned:       false,
			nowOwned:        true,
			configChanged:   true,
			externalChanged: true,
			want:            TakingConflict,
			rowNumber:       7,
			scenario:        "Remove from ignore_fields, but external was managing it",
		},

		// Row 8: (T, F, F, F) - IMPOSSIBLE
		// Can't lose ownership without external or config trigger
		{
			name:            "row_8_impossible_spontaneous_loss",
			prevOwned:       true,
			nowOwned:        false,
			configChanged:   false,
			externalChanged: false,
			want:            NoConflict, // Safe default for impossible state
			rowNumber:       8,
			scenario:        "IMPOSSIBLE: Spontaneous ownership loss",
		},

		// Row 9: (T, F, F, T) - DRIFT (external took ownership)
		// During PLAN: We owned it before, external took it, we'll revert with force=true
		// This is the most common drift scenario (e.g., kubectl scales deployment)
		{
			name:            "row_9_drift_external_took_ownership",
			prevOwned:       true,
			nowOwned:        false,
			configChanged:   false,
			externalChanged: true,
			want:            DriftConflict,
			rowNumber:       9,
			scenario:        "External took ownership, we're reverting (drift detection)",
		},

		// Row 10: (T, F, T, F) - INTENTIONAL RELEASE
		{
			name:            "row_10_intentional_release",
			prevOwned:       true,
			nowOwned:        false,
			configChanged:   true,
			externalChanged: false,
			want:            NoConflict,
			rowNumber:       10,
			scenario:        "Add field to ignore_fields (intentional release)",
		},

		// Row 11: (T, F, T, T) - RELEASE + External
		{
			name:            "row_11_release_plus_external",
			prevOwned:       true,
			nowOwned:        false,
			configChanged:   true,
			externalChanged: true,
			want:            NoConflict,
			rowNumber:       11,
			scenario:        "Add to ignore_fields + external takes it immediately",
		},

		// Row 12: (T, T, F, F) - HOLD (normal, most common)
		{
			name:            "row_12_hold_normal",
			prevOwned:       true,
			nowOwned:        true,
			configChanged:   false,
			externalChanged: false,
			want:            NoConflict,
			rowNumber:       12,
			scenario:        "Normal terraform apply with no drift, no config changes",
		},

		// Row 13: (T, T, F, T) - RE-TAKING (drift revert)
		{
			name:            "row_13_retaking_drift",
			prevOwned:       true,
			nowOwned:        true,
			configChanged:   false,
			externalChanged: true,
			want:            DriftConflict,
			rowNumber:       13,
			scenario:        "External modified field, we revert with force=true",
		},

		// Row 14: (T, T, T, F) - HOLD with Config Update
		{
			name:            "row_14_hold_with_update",
			prevOwned:       true,
			nowOwned:        true,
			configChanged:   true,
			externalChanged: false,
			want:            NoConflict,
			rowNumber:       14,
			scenario:        "User modified yaml_body, normal update",
		},

		// Row 15: (T, T, T, T) - UPDATE + Conflict
		{
			name:            "row_15_update_with_conflict",
			prevOwned:       true,
			nowOwned:        true,
			configChanged:   true,
			externalChanged: true,
			want:            UpdateConflict,
			rowNumber:       15,
			scenario:        "User modified yaml_body + external also changed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyConflict(tt.prevOwned, tt.nowOwned, tt.configChanged, tt.externalChanged)
			if got != tt.want {
				t.Errorf("ClassifyConflict() Row %d (%s) = %v, want %v\nScenario: %s",
					tt.rowNumber,
					tt.name,
					got,
					tt.want,
					tt.scenario,
				)
			}
		})
	}
}

// TestClassifyConflict_ConflictPattern verifies the core conflict pattern:
// external_changed=true AND now_owned=true → potential conflict
func TestClassifyConflict_ConflictPattern(t *testing.T) {
	tests := []struct {
		name            string
		prevOwned       bool
		configChanged   bool
		externalChanged bool
		nowOwned        bool
		expectConflict  bool // Should ANY conflict be detected?
		description     string
	}{
		{
			name:            "no_external_no_conflict",
			externalChanged: false,
			nowOwned:        true,
			prevOwned:       true,
			configChanged:   true,
			expectConflict:  false,
			description:     "No external changes = no conflict (even if we own it)",
		},
		{
			name:            "external_but_not_owned_no_conflict",
			externalChanged: true,
			nowOwned:        false,
			prevOwned:       false,
			configChanged:   true,
			expectConflict:  false,
			description:     "External changes but we don't own it = no conflict",
		},
		{
			name:            "external_and_owned_conflict",
			externalChanged: true,
			nowOwned:        true,
			prevOwned:       true,
			configChanged:   false,
			expectConflict:  true,
			description:     "External changes AND we own it = conflict (drift)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyConflict(tt.prevOwned, tt.nowOwned, tt.configChanged, tt.externalChanged)
			hasConflict := got != NoConflict
			if hasConflict != tt.expectConflict {
				t.Errorf("ClassifyConflict() hasConflict = %v, want %v\n%s",
					hasConflict, tt.expectConflict, tt.description)
			}
		})
	}
}

// TestConflictType_String tests the String() method for ConflictType
func TestConflictType_String(t *testing.T) {
	tests := []struct {
		conflict ConflictType
		want     string
	}{
		{NoConflict, "NoConflict"},
		{TakingConflict, "TakingConflict"},
		{DriftConflict, "DriftConflict"},
		{UpdateConflict, "UpdateConflict"},
		{ConflictType(999), "Unknown(999)"}, // Invalid value
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.conflict.String(); got != tt.want {
				t.Errorf("ConflictType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConflictDetection_AddField tests field aggregation by conflict type
func TestConflictDetection_AddField(t *testing.T) {
	cd := NewConflictDetection()

	// Add one field of each conflict type
	cd.AddField(TakingConflict, FieldChange{Path: "spec.replicas"})
	cd.AddField(DriftConflict, FieldChange{Path: "spec.image"})
	cd.AddField(UpdateConflict, FieldChange{Path: "metadata.labels.app"})
	cd.AddField(NoConflict, FieldChange{Path: "spec.selector"}) // Should be ignored

	if len(cd.takingConflicts) != 1 {
		t.Errorf("takingConflicts count = %d, want 1", len(cd.takingConflicts))
	}
	if len(cd.driftConflicts) != 1 {
		t.Errorf("driftConflicts count = %d, want 1", len(cd.driftConflicts))
	}
	if len(cd.updateConflicts) != 1 {
		t.Errorf("updateConflicts count = %d, want 1", len(cd.updateConflicts))
	}
	if !cd.HasConflicts() {
		t.Error("HasConflicts() = false, want true")
	}
}

// TestConflictDetection_NoConflicts tests empty state
func TestConflictDetection_NoConflicts(t *testing.T) {
	cd := NewConflictDetection()

	if cd.HasConflicts() {
		t.Error("HasConflicts() = true, want false for empty detection")
	}

	warnings := cd.FormatWarnings()
	if len(warnings) != 0 {
		t.Errorf("FormatWarnings() returned %d warnings, want 0 for empty detection", len(warnings))
	}
}

// TestConflictDetection_FormatWarnings tests warning message generation
func TestConflictDetection_FormatWarnings(t *testing.T) {
	cd := NewConflictDetection()

	// Add drift conflict
	cd.AddField(DriftConflict, FieldChange{
		Path:           "spec.replicas",
		CurrentValue:   5,
		PlannedValue:   3,
		CurrentManager: "horizontal-pod-autoscaler",
	})

	// Add taking conflict
	cd.AddField(TakingConflict, FieldChange{
		Path:           "spec.resources.limits.cpu",
		CurrentManager: "vertical-pod-autoscaler",
	})

	warnings := cd.FormatWarnings()

	// Should have 2 warnings (drift + taking)
	if len(warnings) != 2 {
		t.Fatalf("FormatWarnings() returned %d warnings, want 2", len(warnings))
	}

	// Check drift warning (should be first)
	if !strings.Contains(warnings[0].Summary, "Drift") {
		t.Errorf("First warning summary = %q, want to contain 'Drift'", warnings[0].Summary)
	}
	if !strings.Contains(warnings[0].Detail, "spec.replicas") {
		t.Errorf("Drift warning detail should mention spec.replicas, got: %s", warnings[0].Detail)
	}
	if !strings.Contains(warnings[0].Detail, "horizontal-pod-autoscaler") {
		t.Errorf("Drift warning detail should mention HPA, got: %s", warnings[0].Detail)
	}

	// Check taking warning (should be second)
	if !strings.Contains(warnings[1].Summary, "Taking") {
		t.Errorf("Second warning summary = %q, want to contain 'Taking'", warnings[1].Summary)
	}
	if !strings.Contains(warnings[1].Detail, "spec.resources.limits.cpu") {
		t.Errorf("Taking warning detail should mention cpu, got: %s", warnings[1].Detail)
	}
	if !strings.Contains(warnings[1].Detail, "vertical-pod-autoscaler") {
		t.Errorf("Taking warning detail should mention VPA, got: %s", warnings[1].Detail)
	}
}

// TestConflictDetection_MultipleFieldsSameType tests aggregation of multiple fields
func TestConflictDetection_MultipleFieldsSameType(t *testing.T) {
	cd := NewConflictDetection()

	// Add multiple drift conflicts
	cd.AddField(DriftConflict, FieldChange{Path: "spec.replicas"})
	cd.AddField(DriftConflict, FieldChange{Path: "spec.image"})
	cd.AddField(DriftConflict, FieldChange{Path: "metadata.labels.version"})

	warnings := cd.FormatWarnings()

	// Should have 1 warning aggregating all 3 fields
	if len(warnings) != 1 {
		t.Fatalf("FormatWarnings() returned %d warnings, want 1 (aggregated)", len(warnings))
	}

	detail := warnings[0].Detail
	// All fields should be mentioned in the single warning
	if !strings.Contains(detail, "spec.replicas") ||
		!strings.Contains(detail, "spec.image") ||
		!strings.Contains(detail, "metadata.labels.version") {
		t.Errorf("Warning should mention all 3 fields, got: %s", detail)
	}
}

// TestFormatValue tests value formatting for warnings
func TestFormatValue(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  string
	}{
		{"nil", nil, "(none)"},
		{"string", "nginx:1.20", `"nginx:1.20"`},
		{"int", 3, "3"},
		{"int64", int64(12345), "12345"},
		{"float64", 2.5, "2.5"},
		{"bool", true, "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatValue(tt.value); got != tt.want {
				t.Errorf("formatValue(%v) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// TestConflictDetection_WarningOrder tests that warnings appear in correct order (drift, taking, update)
func TestConflictDetection_WarningOrder(t *testing.T) {
	cd := NewConflictDetection()

	// Add in reverse order intentionally
	cd.AddField(UpdateConflict, FieldChange{Path: "field3"})
	cd.AddField(TakingConflict, FieldChange{Path: "field2"})
	cd.AddField(DriftConflict, FieldChange{Path: "field1"})

	warnings := cd.FormatWarnings()

	if len(warnings) != 3 {
		t.Fatalf("FormatWarnings() returned %d warnings, want 3", len(warnings))
	}

	// Check order: drift, taking, update
	if !strings.Contains(warnings[0].Summary, "Drift") {
		t.Errorf("Warning 0 should be Drift, got: %s", warnings[0].Summary)
	}
	if !strings.Contains(warnings[1].Summary, "Taking") {
		t.Errorf("Warning 1 should be Taking, got: %s", warnings[1].Summary)
	}
	if !strings.Contains(warnings[2].Summary, "Overwriting") {
		t.Errorf("Warning 2 should be Update (Overwriting), got: %s", warnings[2].Summary)
	}
}
