# k8sinline_yaml_split Enhancement Plan

## 🎯 Vision
Make `k8sinline_yaml_split` the **best-in-class** Terraform data source for handling multi-document YAML files, specifically designed for Kubernetes but flexible enough for any YAML use case.

## 🔥 Key Problems We're Solving

1. **Terraform's yamldecode() limitation** - Cannot handle multi-document YAML
2. **Brittle regex-based workarounds** - Complex, error-prone splitting logic
3. **Unstable resource IDs** - Causes unnecessary recreations when order changes
4. **Poor error messages** - Hard to debug when YAML is malformed

## 🚀 Enhancement Categories

### 1. **Robust YAML Processing**

#### Current Issues:
- Simple `strings.Split("---")` approach is fragile
- No handling of edge cases (comments, quoted strings, etc.)
- No validation of individual documents

#### Enhancements:
- **Smart YAML document separation** using proper parsing
- **Comment-aware splitting** - Handle `# comments` after `---`
- **Quoted string protection** - Don't split on `"---"` in strings
- **Empty document filtering** - Skip blank/comment-only sections
- **YAML validation** - Ensure each document is valid YAML

```hcl
data "k8sinline_yaml_split" "robust" {
  content = <<YAML
# This is a comment
---
apiVersion: v1
kind: Namespace
metadata:
  name: "my---namespace"  # Won't split on this ---
---
# Another comment
apiVersion: apps/v1
kind: Deployment
# ... rest of document
YAML
```

### 3. **Intelligent ID Generation**

#### Current Issues:
- Simple concatenation creates unstable IDs
- No handling of duplicate names
- No customization options
- No namespace inference

#### Enhancements:
- **Stable, predictable IDs** that don't change with document order
- **Duplicate handling** with automatic suffixes
- **Custom ID templates** for flexibility
- **Namespace inference** for Kubernetes resources
- **Resource relationship tracking**


### 5. **Superior Error Handling & Debugging**

#### Current Issues:
- Generic error messages
- No context about which document failed
- No line number information
- Hard to debug complex YAML files

#### Enhancements:
- **Detailed error context** with file names and line numbers
- **Progressive error handling** - continue processing valid documents
- **Validation warnings** for common issues
- **Debug output** for troubleshooting

```hcl
data "k8sinline_yaml_split" "debug" {
  patterns = ["manifests/**/*.yaml"]
}  
```
