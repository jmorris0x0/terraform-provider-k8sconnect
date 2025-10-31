package ownership

import (
	"fmt"
	"strings"
)

// ConflictType classifies the type of ownership conflict based on ADR-021's 16-row state machine.
// Each type corresponds to specific rows in the (prev_owned, now_owned, config_changed, external_changed) table.
type ConflictType int

const (
	// NoConflict indicates no ownership conflict exists (Rows 0-6, 10-12, 14)
	// These are normal operations: unmanaged fields, clean taking, intentional release, or normal updates
	NoConflict ConflictType = iota

	// TakingConflict indicates taking ownership from an active external controller (Row 7)
	// Pattern: (false, true, true, true) - User removed from ignore_fields, but external was managing it
	// Risk: External controller will continue trying to manage field → persistent drift
	TakingConflict

	// DriftConflict indicates reverting external changes (Row 13)
	// Pattern: (true, true, false, true) - We owned it, external modified it, we're reverting
	// Risk: If external controller keeps modifying, endless ping-pong
	DriftConflict

	// UpdateConflict indicates updating while external also changing (Row 15)
	// Pattern: (true, true, true, true) - User updated yaml_body, external also modified same field
	// Risk: External controller might revert our change → potential fight
	UpdateConflict
)

// String returns human-readable name for conflict type
func (c ConflictType) String() string {
	switch c {
	case NoConflict:
		return "NoConflict"
	case TakingConflict:
		return "TakingConflict"
	case DriftConflict:
		return "DriftConflict"
	case UpdateConflict:
		return "UpdateConflict"
	default:
		return fmt.Sprintf("Unknown(%d)", c)
	}
}

// ClassifyConflict determines the conflict type using ADR-021's 16-row state machine.
//
// Parameters:
//   - prevOwned: Did k8sconnect own this field in previous Terraform state (T-1)?
//   - nowOwned: Does k8sconnect own this field after current operation (T+1)?
//   - configChanged: Did user modify yaml_body or ignore_fields between T-1 and T?
//   - externalChanged: Did external field manager change the field value between T-1 and T? (Option A: value-based)
//
// Returns the ConflictType according to the 16-row classification table.
//
// The conflict pattern is: external_changed=true AND now_owned=true (we're asserting ownership despite external activity)
func ClassifyConflict(prevOwned, nowOwned, configChanged, externalChanged bool) ConflictType {
	// Row 11: (true, false, false, true) = Drift - external took ownership
	// We owned it before, external took it, we're about to revert
	// This is DriftConflict even though nowOwned=false (external has it right now)
	if prevOwned && !nowOwned && !configChanged && externalChanged {
		return DriftConflict
	}

	// Pattern: external=true AND now=true = potential conflict
	// (External was active, we're taking/maintaining ownership)
	if !externalChanged || !nowOwned {
		return NoConflict
	}

	// Row 7: (false, true, true, true) = Taking from active external controller
	// User removed field from ignore_fields, but external controller was managing it
	if !prevOwned && configChanged {
		return TakingConflict
	}

	// Row 13: (true, true, false, true) = Drift revert
	// We owned it before, external changed it, we're reverting (no config change)
	if prevOwned && !configChanged {
		return DriftConflict
	}

	// Row 15: (true, true, true, true) = Update + conflict
	// We owned it, user changed config, but external also modified it
	if prevOwned && configChanged {
		return UpdateConflict
	}

	// Shouldn't reach here if logic is correct, but return NoConflict as safe default
	return NoConflict
}

// FieldChange represents a single field undergoing an ownership transition
type FieldChange struct {
	Path            string      // JSON path (e.g., "spec.replicas")
	PreviousValue   interface{} // Previous value from state (nil if new field)
	CurrentValue    interface{} // Current value in K8s
	PlannedValue    interface{} // Value that will be applied
	PreviousManager string      // Manager in previous state ("" if new)
	CurrentManager  string      // Manager currently in K8s
	PlannedManager  string      // Manager after apply (usually "k8sconnect")
}

// ConflictDetection aggregates all field changes and classifies them by conflict type.
// This enables resource-level warning messages instead of per-field warnings.
type ConflictDetection struct {
	takingConflicts []FieldChange // Row 7: Taking from active controller
	driftConflicts  []FieldChange // Row 13: Reverting drift
	updateConflicts []FieldChange // Row 15: Update during concurrent change
}

// NewConflictDetection creates an empty conflict detection aggregator
func NewConflictDetection() *ConflictDetection {
	return &ConflictDetection{
		takingConflicts: []FieldChange{},
		driftConflicts:  []FieldChange{},
		updateConflicts: []FieldChange{},
	}
}

// AddField classifies and adds a field change to the appropriate conflict bucket
func (c *ConflictDetection) AddField(conflictType ConflictType, field FieldChange) {
	switch conflictType {
	case TakingConflict:
		c.takingConflicts = append(c.takingConflicts, field)
	case DriftConflict:
		c.driftConflicts = append(c.driftConflicts, field)
	case UpdateConflict:
		c.updateConflicts = append(c.updateConflicts, field)
	case NoConflict:
		// No action needed - not a conflict
	}
}

// HasConflicts returns true if any conflicts were detected
func (c *ConflictDetection) HasConflicts() bool {
	return len(c.takingConflicts) > 0 || len(c.driftConflicts) > 0 || len(c.updateConflicts) > 0
}

// FormatWarnings generates resource-level warning messages for all detected conflicts.
// Returns a slice of warning summaries (one per conflict type) and detailed messages.
func (c *ConflictDetection) FormatWarnings() []Warning {
	var warnings []Warning

	// Order matters: Show drift first (most common), then taking, then update
	if len(c.driftConflicts) > 0 {
		warnings = append(warnings, c.formatDriftWarning())
	}
	if len(c.takingConflicts) > 0 {
		warnings = append(warnings, c.formatTakingWarning())
	}
	if len(c.updateConflicts) > 0 {
		warnings = append(warnings, c.formatUpdateWarning())
	}

	return warnings
}

// Warning represents a structured warning message
type Warning struct {
	Summary string // Short summary for warning title
	Detail  string // Detailed explanation with field list
}

// formatDriftWarning creates warning for Row 13 (RE-TAKING) scenarios
func (c *ConflictDetection) formatDriftWarning() Warning {
	var details []string
	for _, field := range c.driftConflicts {
		details = append(details, fmt.Sprintf("  • %s: %v → %v (modified by: %s)",
			field.Path,
			formatValue(field.CurrentValue),
			formatValue(field.PlannedValue),
			field.CurrentManager,
		))
	}

	detailMsg := fmt.Sprintf(
		"The following fields were modified externally and will be reverted to your configuration:\n\n%s\n\n"+
			"To allow external management of these fields, add them to ignore_fields.",
		strings.Join(details, "\n"),
	)

	return Warning{
		Summary: "Drift Detected - Reverting External Changes",
		Detail:  detailMsg,
	}
}

// formatTakingWarning creates warning for Row 7 (TAKING with Conflict) scenarios
func (c *ConflictDetection) formatTakingWarning() Warning {
	var details []string
	for _, field := range c.takingConflicts {
		details = append(details, fmt.Sprintf("  • %s (managed by: %s)",
			field.Path,
			field.CurrentManager,
		))
	}

	detailMsg := fmt.Sprintf(
		"Removed from ignore_fields, but external controllers are actively managing:\n\n%s\n\n"+
			"Risk: These controllers will continue trying to manage these fields, causing persistent drift.\n"+
			"Consider: Add these fields back to ignore_fields to let external controllers manage them.",
		strings.Join(details, "\n"),
	)

	return Warning{
		Summary: "Ownership Conflict - Taking Field from Active Controller",
		Detail:  detailMsg,
	}
}

// formatUpdateWarning creates warning for Row 15 (UPDATE + Conflict) scenarios
func (c *ConflictDetection) formatUpdateWarning() Warning {
	var details []string
	for _, field := range c.updateConflicts {
		details = append(details, fmt.Sprintf("  • %s: your value: %v, external value: %v (managed by: %s)",
			field.Path,
			formatValue(field.PlannedValue),
			formatValue(field.CurrentValue),
			field.CurrentManager,
		))
	}

	detailMsg := fmt.Sprintf(
		"Updated yaml_body, but external controllers also modified these fields:\n\n%s\n\n"+
			"Your configuration will be applied (overwriting external changes).",
		strings.Join(details, "\n"),
	)

	return Warning{
		Summary: "Ownership Conflict - Overwriting Concurrent External Changes",
		Detail:  detailMsg,
	}
}

// formatValue formats a field value for display in warnings
func formatValue(v interface{}) string {
	if v == nil {
		return "(none)"
	}
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case int, int32, int64:
		return fmt.Sprintf("%d", val)
	case float32, float64:
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}
