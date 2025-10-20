package k8sclient

import (
	"sync"
)

// WarningCollector implements rest.WarningHandler and collects Kubernetes API warnings
// These warnings typically indicate deprecated API usage or other actionable issues
type WarningCollector struct {
	mu       sync.Mutex
	warnings []string
}

// NewWarningCollector creates a new warning collector
func NewWarningCollector() *WarningCollector {
	return &WarningCollector{
		warnings: make([]string, 0),
	}
}

// HandleWarningHeader implements rest.WarningHandler
// It's called by client-go when the API server returns Warning headers
func (w *WarningCollector) HandleWarningHeader(code int, agent string, text string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Deduplicate warnings - don't add if already present
	for _, existing := range w.warnings {
		if existing == text {
			return
		}
	}

	w.warnings = append(w.warnings, text)
}

// GetWarnings returns all collected warnings and clears the collector
func (w *WarningCollector) GetWarnings() []string {
	w.mu.Lock()
	defer w.mu.Unlock()

	warnings := make([]string, len(w.warnings))
	copy(warnings, w.warnings)

	// Clear warnings after retrieval
	w.warnings = w.warnings[:0]

	return warnings
}

// HasWarnings returns true if there are any warnings
func (w *WarningCollector) HasWarnings() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return len(w.warnings) > 0
}
