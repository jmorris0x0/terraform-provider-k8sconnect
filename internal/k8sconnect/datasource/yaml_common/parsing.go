package yaml_common

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

// DocumentInfo holds metadata about a parsed document
type DocumentInfo struct {
	Content       string
	SourceFile    string
	DocumentIndex int
	LineNumber    int
	Object        *unstructured.Unstructured
	ParseError    error
}

// ParseDocuments splits and parses YAML documents from content
func ParseDocuments(content, sourceFile string) ([]DocumentInfo, error) {
	// Split documents using smart separator detection
	rawDocs := SplitYAMLDocuments(content)

	var documents []DocumentInfo
	var errors []string

	for i, rawDoc := range rawDocs {
		doc := DocumentInfo{
			Content:       rawDoc,
			SourceFile:    sourceFile,
			DocumentIndex: i,
			LineNumber:    EstimateLineNumber(content, rawDoc),
		}

		// Try to parse as Kubernetes resource
		var obj unstructured.Unstructured
		if err := yaml.Unmarshal([]byte(rawDoc), &obj); err != nil {
			// Document failed to parse - record error but continue
			doc.ParseError = fmt.Errorf("invalid YAML at document %d: %w", i+1, err)
			errors = append(errors, fmt.Sprintf("%s (document %d): %s", sourceFile, i+1, err.Error()))
		} else {
			doc.Object = &obj
		}

		documents = append(documents, doc)
	}

	var err error
	if len(errors) > 0 {
		err = fmt.Errorf("parsing errors: %s", strings.Join(errors, "; "))
	}

	return documents, err
}

// SplitYAMLDocuments splits YAML content on document separators
func SplitYAMLDocuments(content string) []string {
	separatorRegex := regexp.MustCompile(`(?m)^---\s*(?:#.*)?(?:\r?\n|$)`)

	parts := separatorRegex.Split(content, -1)
	var documents []string

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		// Skip empty documents and comment-only documents
		if trimmed != "" && !isCommentOnly(trimmed) {
			documents = append(documents, trimmed)
		}
	}

	return documents
}

// isCommentOnly checks if a document contains only comments and whitespace
func isCommentOnly(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return false
		}
	}
	return true
}

// EstimateLineNumber provides approximate line number for error reporting
func EstimateLineNumber(fullContent, docContent string) int {
	beforeDoc := strings.Split(fullContent, docContent)[0]
	return strings.Count(beforeDoc, "\n") + 1
}

// GenerateResourceID creates a human-readable, stable ID for a Kubernetes resource
// Format: [group.]kind[.namespace].name
// - Core resources (apiVersion: v1): kind.name or kind.namespace.name
// - Grouped resources (apiVersion: apps/v1): group.kind.name or group.kind.namespace.name
// - Custom resources (apiVersion: custom.example.com/v1): group.kind.name or group.kind.namespace.name
func GenerateResourceID(obj *unstructured.Unstructured) string {
	apiVersion := obj.GetAPIVersion()
	kind := strings.ToLower(obj.GetKind())
	name := obj.GetName()
	namespace := obj.GetNamespace()

	// Extract group from apiVersion
	// apiVersion formats:
	// - "v1" -> no group (core)
	// - "apps/v1" -> group is "apps"
	// - "custom.example.com/v1" -> group is "custom.example.com"
	var group string
	if strings.Contains(apiVersion, "/") {
		parts := strings.Split(apiVersion, "/")
		group = strings.ToLower(parts[0])
	}
	// If no "/" then it's core group (v1, v1beta1, etc.) - no group prefix

	// Handle edge cases
	if kind == "" {
		kind = "unknown"
	}
	if name == "" {
		name = "unnamed"
	}

	// Create stable ID with group (if present)
	// Format: [group.]kind[.namespace].name
	var idParts []string
	if group != "" {
		idParts = append(idParts, group)
	}
	idParts = append(idParts, kind)
	if namespace != "" {
		idParts = append(idParts, namespace)
	}
	idParts = append(idParts, name)

	return strings.Join(idParts, ".")
}

// ExpandPattern resolves glob patterns using doublestar for full glob support
func ExpandPattern(pattern string) ([]string, error) {
	// Use doublestar for robust glob matching (handles **, *, ?, etc.)
	// FilepathGlob works with real filesystem and handles both relative and absolute paths
	matches, err := doublestar.FilepathGlob(pattern)
	if err != nil {
		return nil, err
	}

	// Filter to only include files (not directories)
	var files []string
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && !info.IsDir() {
			files = append(files, match)
		}
	}

	// Sort for consistent ordering
	sort.Strings(files)
	return files, nil
}

// ReadFile reads a file and returns its content as string
func ReadFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file %q: %w", path, err)
	}
	return string(content), nil
}

// HashString creates a SHA256 hash of the input string
func HashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// LoadDocuments loads YAML documents from inline content, file pattern, or kustomize build.
// Returns the parsed documents, a sourceID for caching, any warnings, and any error.
// Exactly one of content, pattern, or kustomizePath should be non-empty.
func LoadDocuments(hasContent bool, content, pattern, kustomizePath string) ([]DocumentInfo, string, []string, error) {
	var documents []DocumentInfo
	var sourceID string
	var warnings []string

	// Determine input mode
	hasKustomize := kustomizePath != ""

	if hasKustomize {
		// Build kustomization and parse output
		yamlContent, kustomizeWarnings, err := BuildKustomization(kustomizePath)
		if err != nil {
			return nil, "", nil, fmt.Errorf("kustomize build failed: %w\n\n%s", err, FormatKustomizeError(kustomizePath, err))
		}
		warnings = kustomizeWarnings

		// Parse the built YAML
		documents, err = ParseDocuments(yamlContent, kustomizePath)
		if err != nil {
			return nil, "", nil, fmt.Errorf("kustomize build succeeded but output contains invalid YAML: %w", err)
		}
		sourceID = fmt.Sprintf("kustomize-%s", HashString(kustomizePath)[:8])
	} else if hasContent {
		// Parse inline content
		docs, err := ParseDocuments(content, "<inline>")
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to parse inline YAML content: %w", err)
		}
		if len(docs) == 0 {
			return nil, "", nil, fmt.Errorf("No Kubernetes resources found in YAML content. The content appears to be empty or contains only comments.")
		}
		documents = docs
		sourceID = fmt.Sprintf("content-%s", HashString(content)[:8])
	} else {
		// Handle pattern-based loading
		files, err := ExpandPattern(pattern)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to resolve pattern %q: %w", pattern, err)
		}

		if len(files) == 0 {
			return nil, "", nil, fmt.Errorf("No files matched pattern %q. Check that the path exists and contains YAML files.", pattern)
		}

		// Process all matching files
		for _, file := range files {
			fileContent, err := ReadFile(file)
			if err != nil {
				return nil, "", nil, fmt.Errorf("failed to read file %q: %w", file, err)
			}

			docs, err := ParseDocuments(fileContent, file)
			if err != nil {
				return nil, "", nil, fmt.Errorf("failed to parse YAML in file %q: %w", file, err)
			}
			documents = append(documents, docs...)
		}
		sourceID = fmt.Sprintf("pattern-%s", HashString(pattern)[:8])
	}

	return documents, sourceID, warnings, nil
}

// BuildKustomization runs kustomize build on the given path and returns the generated YAML and any warnings
func BuildKustomization(path string) (yamlContent string, warnings []string, err error) {
	// Capture stderr to get kustomize warnings
	oldStderr := os.Stderr
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		// If we can't create pipe, just run without capturing warnings
		return buildKustomizationWithoutWarnings(path)
	}
	os.Stderr = w

	// Create kustomizer with default secure options
	opts := krusty.MakeDefaultOptions()
	k := krusty.MakeKustomizer(opts)

	// Run kustomize build
	resMap, buildErr := k.Run(filesys.MakeFsOnDisk(), path)

	// Restore stderr and capture warnings
	w.Close()
	os.Stderr = oldStderr
	var stderrBuf bytes.Buffer
	io.Copy(&stderrBuf, r)
	r.Close()

	if buildErr != nil {
		return "", nil, buildErr
	}

	// Convert resource map to YAML
	yamlBytes, yamlErr := resMap.AsYaml()
	if yamlErr != nil {
		return "", nil, fmt.Errorf("failed to convert kustomize output to YAML: %w", yamlErr)
	}

	// Parse warnings from stderr
	warnings = parseKustomizeWarnings(stderrBuf.String())

	return string(yamlBytes), warnings, nil
}

// buildKustomizationWithoutWarnings is a fallback when stderr capture fails
func buildKustomizationWithoutWarnings(path string) (string, []string, error) {
	opts := krusty.MakeDefaultOptions()
	k := krusty.MakeKustomizer(opts)

	resMap, err := k.Run(filesys.MakeFsOnDisk(), path)
	if err != nil {
		return "", nil, err
	}

	yaml, err := resMap.AsYaml()
	if err != nil {
		return "", nil, fmt.Errorf("failed to convert kustomize output to YAML: %w", err)
	}

	return string(yaml), nil, nil
}

// parseKustomizeWarnings extracts warning messages from kustomize stderr output
func parseKustomizeWarnings(stderr string) []string {
	if stderr == "" {
		return nil
	}

	var warnings []string
	lines := strings.Split(stderr, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Kustomize warnings typically start with "# Warning:" or "Warning:"
		if strings.HasPrefix(trimmed, "# Warning:") || strings.HasPrefix(trimmed, "Warning:") {
			// Remove the "# " prefix if present
			warning := strings.TrimPrefix(trimmed, "# ")
			warnings = append(warnings, warning)
		}
	}
	return warnings
}

// FormatKustomizeError formats kustomize errors following k8sconnect's WHAT/WHY/HOW pattern
func FormatKustomizeError(path string, err error) string {
	return fmt.Sprintf(`Unable to build kustomization at %q

Kustomize build failed with error:
%s

Common causes:
  1. Missing kustomization.yaml in the specified directory
  2. Base path references a directory outside the kustomization root (security restriction)
  3. Patch file references don't exist or have invalid paths
  4. Strategic merge conflict in overlays or patches
  5. Invalid YAML syntax in kustomization.yaml or referenced files

How to fix:
  1. Verify kustomization.yaml exists in the path: %s
  2. Check that all base paths in kustomization.yaml are within the allowed directory
  3. Ensure all patch files and resources referenced in kustomization.yaml exist
  4. Run 'kustomize build %s' locally to test the configuration
  5. Review kustomize documentation: https://kubectl.docs.kubernetes.io/references/kustomize/

If you need to reference files outside the kustomization root, this is blocked for security reasons.
Consider restructuring your kustomization to keep all files within a single directory tree.`,
		path, err.Error(), path, path)
}
