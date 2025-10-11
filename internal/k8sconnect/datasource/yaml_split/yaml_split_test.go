// internal/k8sconnect/datasource/yaml_split/yaml_split_test.go
package yaml_split

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/datasource/yaml_common"
)

func TestSplitYAMLDocuments(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{
			name: "basic separation",
			content: `apiVersion: v1
kind: Namespace
metadata:
  name: test1
---
apiVersion: v1
kind: Namespace
metadata:
  name: test2`,
			expected: []string{
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test1",
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test2",
			},
		},
		{
			name: "separator with comments",
			content: `apiVersion: v1
kind: Namespace
metadata:
  name: test1
--- # This is a comment
apiVersion: v1
kind: Namespace
metadata:
  name: test2`,
			expected: []string{
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test1",
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test2",
			},
		},
		{
			name: "quoted string with separator",
			content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  script: |
    echo "Some script with --- in it"
    echo "More content"
---
apiVersion: v1
kind: Namespace
metadata:
  name: test2`,
			expected: []string{
				"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\ndata:\n  script: |\n    echo \"Some script with --- in it\"\n    echo \"More content\"",
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test2",
			},
		},
		{
			name: "empty documents filtered",
			content: `apiVersion: v1
kind: Namespace
metadata:
  name: test1
---

---
# Just a comment
---
apiVersion: v1
kind: Namespace
metadata:
  name: test2
---`,
			expected: []string{
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test1",
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test2",
			},
		},
		{
			name:    "windows line endings",
			content: "apiVersion: v1\r\nkind: Namespace\r\nmetadata:\r\n  name: test1\r\n---\r\napiVersion: v1\r\nkind: Namespace\r\nmetadata:\r\n  name: test2",
			expected: []string{
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test1",
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := yaml_common.SplitYAMLDocuments(tt.content)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d documents, got %d", len(tt.expected), len(result))
				return
			}

			for i, expected := range tt.expected {
				// Normalize line endings and trim whitespace for comparison
				resultNorm := strings.TrimSpace(strings.ReplaceAll(result[i], "\r\n", "\n"))
				expectedNorm := strings.TrimSpace(strings.ReplaceAll(expected, "\r\n", "\n"))

				if resultNorm != expectedNorm {
					t.Errorf("document %d mismatch:\nexpected:\n%q\n\ngot:\n%q", i, expectedNorm, resultNorm)
				}
			}
		})
	}
}

func TestGenerateBaseID(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		expectedID string
	}{
		{
			name: "cluster-scoped resource",
			yaml: `apiVersion: v1
kind: Namespace
metadata:
  name: my-namespace`,
			expectedID: "namespace.my-namespace",
		},
		{
			name: "namespaced resource",
			yaml: `apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: default`,
			expectedID: "pod.default.my-pod",
		},
		{
			name: "custom resource",
			yaml: `apiVersion: custom.io/v1
kind: MyResource
metadata:
  name: my-custom
  namespace: test`,
			expectedID: "myresource.test.my-custom",
		},
		{
			name: "missing name",
			yaml: `apiVersion: v1
kind: Pod
metadata:
  namespace: default`,
			expectedID: "pod.default.unnamed",
		},
		{
			name: "missing kind",
			yaml: `apiVersion: v1
metadata:
  name: test`,
			expectedID: "unknown.test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs, err := yaml_common.ParseDocuments(tt.yaml, "test")
			if err != nil && !strings.Contains(tt.name, "missing kind") {
				t.Fatalf("failed to parse document: %v", err)
			}

			if len(docs) != 1 {
				t.Fatalf("expected 1 document, got %d", len(docs))
			}

			// For missing kind test, the document will have a parse error
			if strings.Contains(tt.name, "missing kind") {
				if docs[0].ParseError == nil {
					t.Fatal("expected parse error for missing kind")
				}
				// This will fail in generateManifests, which is correct behavior
				return
			}

			if docs[0].Object == nil {
				t.Fatal("expected parsed object")
			}

			id := yaml_common.GenerateResourceID(docs[0].Object)
			if id != tt.expectedID {
				t.Errorf("expected ID %q, got %q", tt.expectedID, id)
			}
		})
	}
}

func TestDuplicateHandling(t *testing.T) {
	d := &yamlSplitDataSource{}

	content := `apiVersion: v1
kind: Pod
metadata:
  name: nginx
  namespace: default
spec:
  containers:
  - name: nginx
    image: public.ecr.aws/nginx/nginx:1.21
---
apiVersion: v1
kind: Pod
metadata:
  name: nginx
  namespace: default
spec:
  containers:
  - name: nginx
    image: public.ecr.aws/nginx/nginx:1.21`

	docs, err := yaml_common.ParseDocuments(content, "test")
	if err != nil {
		t.Fatalf("failed to parse documents: %v", err)
	}

	// Should fail when generating manifests due to duplicate
	_, err = d.generateManifests(docs)
	if err == nil {
		t.Fatal("expected error for duplicate resources")
	}

	if !strings.Contains(err.Error(), "duplicate resource ID") {
		t.Errorf("error should mention duplicate resource ID: %s", err.Error())
	}

	if !strings.Contains(err.Error(), "pod.default.nginx") {
		t.Errorf("error should mention the specific duplicate ID: %s", err.Error())
	}
}

func TestInvalidYAMLHandling(t *testing.T) {
	d := &yamlSplitDataSource{}

	content := `apiVersion: v1
kind: Namespace
metadata:
  name: valid
---
invalid: yaml: content: [
  missing: bracket
---
apiVersion: v1
kind: Pod
metadata:
  name: another-valid
  namespace: default`

	docs, err := yaml_common.ParseDocuments(content, "test")

	// Should have parsing errors but still return documents
	if err == nil {
		t.Error("expected parsing error for invalid YAML")
	}

	if len(docs) != 3 {
		t.Errorf("expected 3 documents, got %d", len(docs))
	}

	// First and third docs should parse successfully
	if docs[0].ParseError != nil {
		t.Errorf("first document should parse successfully: %v", docs[0].ParseError)
	}
	if docs[2].ParseError != nil {
		t.Errorf("third document should parse successfully: %v", docs[2].ParseError)
	}

	// Second document should have parse error
	if docs[1].ParseError == nil {
		t.Error("second document should have parse error")
	}

	// Test manifest generation should fail due to invalid doc
	_, err = d.generateManifests(docs)
	if err == nil {
		t.Error("expected error when generating manifests with invalid documents")
	}

	if !strings.Contains(err.Error(), "invalid YAML") {
		t.Errorf("error should mention invalid YAML: %s", err.Error())
	}
}

func TestFilePatternExpansion(t *testing.T) {
	// Create temporary directory structure
	tmpDir, err := os.MkdirTemp("", "yaml_split_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	testFiles := map[string]string{
		"app1.yaml": `apiVersion: v1
kind: Namespace
metadata:
  name: app1`,
		"app2.yml": `apiVersion: v1
kind: Namespace
metadata:
  name: app2`,
		"config.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: config`,
		"subdir/nested.yaml": `apiVersion: v1
kind: Secret
metadata:
  name: nested`,
		"ignore.txt": "not yaml content",
	}

	for path, content := range testFiles {
		fullPath := filepath.Join(tmpDir, path)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Change to temp directory for relative patterns
	oldDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldDir)

	tests := []struct {
		name     string
		pattern  string
		expected []string
	}{
		{
			name:     "simple glob",
			pattern:  "*.yaml",
			expected: []string{"app1.yaml", "config.yaml"},
		},
		{
			name:     "yaml and yml extensions",
			pattern:  "*.y*ml",
			expected: []string{"app1.yaml", "app2.yml", "config.yaml"},
		},
		{
			name:     "recursive pattern",
			pattern:  "**/*.yaml",
			expected: []string{"app1.yaml", "config.yaml", "subdir/nested.yaml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := yaml_common.ExpandPattern(tt.pattern)
			if err != nil {
				t.Fatalf("failed to expand pattern: %v", err)
			}

			if len(files) != len(tt.expected) {
				t.Errorf("expected %d files, got %d: %v", len(tt.expected), len(files), files)
				return
			}

			for i, expected := range tt.expected {
				if files[i] != expected {
					t.Errorf("file %d: expected %q, got %q", i, expected, files[i])
				}
			}
		})
	}
}

// TestCommentOnlyDetection removed - isCommentOnly is now an internal function in yaml_common

// Integration test that exercises the full Read flow
func TestDataSourceRead(t *testing.T) {
	// Test with inline content
	content := `apiVersion: v1
kind: Namespace
metadata:
  name: test-ns
---
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
  namespace: test-ns
spec:
  containers:
  - name: nginx
    image: public.ecr.aws/nginx/nginx:1.21`

	d := &yamlSplitDataSource{}

	docs, err := yaml_common.ParseDocuments(content, "<test>")
	if err != nil {
		t.Fatalf("failed to parse documents: %v", err)
	}

	if len(docs) != 2 {
		t.Errorf("expected 2 documents, got %d", len(docs))
	}

	manifests, err := d.generateManifests(docs)
	if err != nil {
		t.Fatalf("failed to generate manifests: %v", err)
	}

	expectedKeys := []string{"namespace.test-ns", "pod.test-ns.test-pod"}
	if len(manifests) != 2 {
		t.Errorf("expected 2 manifests, got %d", len(manifests))
	}

	for _, key := range expectedKeys {
		if _, exists := manifests[key]; !exists {
			t.Errorf("expected key %q not found in manifests", key)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	t.Run("document with only metadata", func(t *testing.T) {
		content := `apiVersion: v1
kind: ConfigMap
metadata:
  name: empty-config`

		docs, err := yaml_common.ParseDocuments(content, "test")
		if err != nil {
			t.Fatalf("failed to parse: %v", err)
		}

		if len(docs) != 1 {
			t.Errorf("expected 1 document, got %d", len(docs))
		}

		id := yaml_common.GenerateResourceID(docs[0].Object)
		expected := "configmap.empty-config"
		if id != expected {
			t.Errorf("expected ID %q, got %q", expected, id)
		}
	})

	t.Run("document with special characters in name", func(t *testing.T) {
		content := `apiVersion: v1
kind: Service
metadata:
  name: my-service-123
  namespace: kube-system`

		docs, err := yaml_common.ParseDocuments(content, "test")
		if err != nil {
			t.Fatalf("failed to parse: %v", err)
		}

		id := yaml_common.GenerateResourceID(docs[0].Object)
		expected := "service.kube-system.my-service-123"
		if id != expected {
			t.Errorf("expected ID %q, got %q", expected, id)
		}
	})

	t.Run("very long document names", func(t *testing.T) {
		longName := strings.Repeat("very-long-name", 20)
		content := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: default`, longName)

		docs, err := yaml_common.ParseDocuments(content, "test")
		if err != nil {
			t.Fatalf("failed to parse: %v", err)
		}

		id := yaml_common.GenerateResourceID(docs[0].Object)
		expected := fmt.Sprintf("secret.default.%s", longName)
		if id != expected {
			t.Errorf("expected ID %q, got %q", expected, id)
		}
	})
}

func TestLineNumberEstimation(t *testing.T) {
	content := `# Header comment
apiVersion: v1
kind: Namespace
metadata:
  name: first
---
# Another comment
apiVersion: v1  
kind: Pod
metadata:
  name: second`

	docs, err := yaml_common.ParseDocuments(content, "test")
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if len(docs) != 2 {
		t.Errorf("expected 2 documents, got %d", len(docs))
	}

	// First document should start around line 2
	if docs[0].LineNumber < 1 || docs[0].LineNumber > 3 {
		t.Errorf("first document line number %d seems wrong", docs[0].LineNumber)
	}

	// Second document should start around line 7-8
	if docs[1].LineNumber < 6 || docs[1].LineNumber > 9 {
		t.Errorf("second document line number %d seems wrong", docs[1].LineNumber)
	}
}
