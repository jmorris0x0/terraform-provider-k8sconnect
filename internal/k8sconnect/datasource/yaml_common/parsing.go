package yaml_common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

// LoadDocuments loads YAML documents from either inline content or file pattern.
// Returns the parsed documents, a sourceID for caching, and any error.
// If hasContent is true, loads from content string. Otherwise, loads from pattern.
func LoadDocuments(hasContent bool, content, pattern string) ([]DocumentInfo, string, error) {
	var documents []DocumentInfo
	var sourceID string

	if hasContent {
		// Parse inline content
		docs, err := ParseDocuments(content, "<inline>")
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse inline YAML content: %w", err)
		}
		documents = docs
		sourceID = fmt.Sprintf("content-%s", HashString(content)[:8])
	} else {
		// Handle pattern-based loading
		files, err := ExpandPattern(pattern)
		if err != nil {
			return nil, "", fmt.Errorf("failed to resolve pattern %q: %w", pattern, err)
		}

		if len(files) == 0 {
			return nil, "", fmt.Errorf("No files matched pattern %q. Check that the path exists and contains YAML files.", pattern)
		}

		// Process all matching files
		for _, file := range files {
			fileContent, err := ReadFile(file)
			if err != nil {
				return nil, "", fmt.Errorf("failed to read file %q: %w", file, err)
			}

			docs, err := ParseDocuments(fileContent, file)
			if err != nil {
				return nil, "", fmt.Errorf("failed to parse YAML in file %q: %w", file, err)
			}
			documents = append(documents, docs...)
		}
		sourceID = fmt.Sprintf("pattern-%s", HashString(pattern)[:8])
	}

	return documents, sourceID, nil
}
