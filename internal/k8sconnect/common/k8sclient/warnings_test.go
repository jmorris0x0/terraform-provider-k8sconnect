package k8sclient

import (
	"testing"
)

func TestWarningCollector_HandleWarningHeader(t *testing.T) {
	collector := NewWarningCollector()

	// Test adding a single warning
	collector.HandleWarningHeader(299, "kubernetes", "test warning 1")

	if !collector.HasWarnings() {
		t.Error("Expected warnings to be present")
	}

	warnings := collector.GetWarnings()
	if len(warnings) != 1 {
		t.Errorf("Expected 1 warning, got %d", len(warnings))
	}
	if warnings[0] != "test warning 1" {
		t.Errorf("Expected 'test warning 1', got %s", warnings[0])
	}

	// Warnings should be cleared after GetWarnings()
	if collector.HasWarnings() {
		t.Error("Expected warnings to be cleared after GetWarnings()")
	}
}

func TestWarningCollector_Deduplication(t *testing.T) {
	collector := NewWarningCollector()

	// Add the same warning multiple times
	collector.HandleWarningHeader(299, "kubernetes", "duplicate warning")
	collector.HandleWarningHeader(299, "kubernetes", "duplicate warning")
	collector.HandleWarningHeader(299, "kubernetes", "duplicate warning")

	warnings := collector.GetWarnings()
	if len(warnings) != 1 {
		t.Errorf("Expected 1 warning after deduplication, got %d", len(warnings))
	}
}

func TestWarningCollector_MultipleWarnings(t *testing.T) {
	collector := NewWarningCollector()

	// Add multiple different warnings
	collector.HandleWarningHeader(299, "kubernetes", "warning 1")
	collector.HandleWarningHeader(299, "kubernetes", "warning 2")
	collector.HandleWarningHeader(299, "kubernetes", "warning 3")

	warnings := collector.GetWarnings()
	if len(warnings) != 3 {
		t.Errorf("Expected 3 warnings, got %d", len(warnings))
	}

	// Verify order is preserved
	expectedWarnings := []string{"warning 1", "warning 2", "warning 3"}
	for i, expected := range expectedWarnings {
		if warnings[i] != expected {
			t.Errorf("Expected warning[%d] to be '%s', got '%s'", i, expected, warnings[i])
		}
	}
}

func TestWarningCollector_EmptyWarnings(t *testing.T) {
	collector := NewWarningCollector()

	if collector.HasWarnings() {
		t.Error("Expected no warnings initially")
	}

	warnings := collector.GetWarnings()
	if len(warnings) != 0 {
		t.Errorf("Expected 0 warnings, got %d", len(warnings))
	}
}

func TestWarningCollector_ClearAndReuse(t *testing.T) {
	collector := NewWarningCollector()

	// Add warning, get it, then add another
	collector.HandleWarningHeader(299, "kubernetes", "first warning")
	firstWarnings := collector.GetWarnings()

	if len(firstWarnings) != 1 {
		t.Errorf("Expected 1 first warning, got %d", len(firstWarnings))
	}

	// Add a second warning after clearing
	collector.HandleWarningHeader(299, "kubernetes", "second warning")
	secondWarnings := collector.GetWarnings()

	if len(secondWarnings) != 1 {
		t.Errorf("Expected 1 second warning, got %d", len(secondWarnings))
	}
	if secondWarnings[0] != "second warning" {
		t.Errorf("Expected 'second warning', got '%s'", secondWarnings[0])
	}
}
