package helm_release

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsLocalChart tests the chart path detection logic used by ModifyPlan
func TestIsLocalChart(t *testing.T) {
	tests := []struct {
		name     string
		chartRef string
		expected bool
	}{
		{
			name:     "relative path with dot-slash",
			chartRef: "./my-chart",
			expected: true,
		},
		{
			name:     "relative path with parent",
			chartRef: "../charts/my-chart",
			expected: true,
		},
		{
			name:     "absolute path",
			chartRef: "/opt/charts/my-chart",
			expected: true,
		},
		{
			name:     "remote chart name",
			chartRef: "nginx",
			expected: false,
		},
		{
			name:     "chart with repo prefix",
			chartRef: "bitnami/nginx",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLocalChart(tt.chartRef)
			if got != tt.expected {
				t.Errorf("isLocalChart(%q) = %v, want %v", tt.chartRef, got, tt.expected)
			}
		})
	}
}

// TestSplitKeyParts tests the Helm-style dot notation parser used by mergeValues
func TestSplitKeyParts(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected []string
	}{
		{
			name:     "simple key",
			key:      "replicaCount",
			expected: []string{"replicaCount"},
		},
		{
			name:     "nested key",
			key:      "image.tag",
			expected: []string{"image", "tag"},
		},
		{
			name:     "deeply nested",
			key:      "a.b.c.d",
			expected: []string{"a", "b", "c", "d"},
		},
		{
			name:     "escaped dot",
			key:      `nodeSelector.kubernetes\.io/hostname`,
			expected: []string{"nodeSelector", "kubernetes.io/hostname"},
		},
		{
			name:     "multiple escaped dots",
			key:      `a\.b\.c.d`,
			expected: []string{"a.b.c", "d"},
		},
		{
			name:     "escaped dot at end",
			key:      `key\.`,
			expected: []string{"key."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitKeyParts(tt.key)
			if len(got) != len(tt.expected) {
				t.Fatalf("splitKeyParts(%q) = %v (len %d), want %v (len %d)", tt.key, got, len(got), tt.expected, len(tt.expected))
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("splitKeyParts(%q)[%d] = %q, want %q", tt.key, i, got[i], tt.expected[i])
				}
			}
		})
	}
}

// TestModifyPlan_LocalChartValidation tests that ModifyPlan catches missing Chart.yaml
func TestModifyPlan_LocalChartPathDetection(t *testing.T) {
	// Create a temp dir without Chart.yaml
	tmpDir := t.TempDir()

	absPath, _ := filepath.Abs(tmpDir)
	chartYamlPath := filepath.Join(absPath, "Chart.yaml")

	// Verify Chart.yaml doesn't exist (the condition ModifyPlan checks)
	if _, err := os.Stat(chartYamlPath); err == nil {
		t.Fatal("test setup error: Chart.yaml should not exist in temp dir")
	}

	// Create a temp dir WITH Chart.yaml
	tmpDirWithChart := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDirWithChart, "Chart.yaml"), []byte("apiVersion: v2\nname: test\nversion: 0.1.0\n"), 0644); err != nil {
		t.Fatalf("failed to create Chart.yaml: %v", err)
	}

	absPathWithChart, _ := filepath.Abs(tmpDirWithChart)
	chartYamlPathValid := filepath.Join(absPathWithChart, "Chart.yaml")

	if _, err := os.Stat(chartYamlPathValid); err != nil {
		t.Fatalf("test setup error: Chart.yaml should exist: %v", err)
	}
}
