# k8sconnect_yaml_split Data Source

The `k8sconnect_yaml_split` data source intelligently splits multi-document YAML content into individual manifests with stable, human-readable IDs. It's designed specifically for Kubernetes YAML files but works with any well-formed YAML content.

## Key Features

###  Robust YAML Processing
- **Smart document separation** - Uses proper regex patterns instead of naive string splitting
- **Comment-aware splitting** - Handles comments after `---` separators
- **Quoted string protection** - Won't split on `---` found inside YAML strings
- **Empty document filtering** - Automatically removes blank and comment-only sections
- **Cross-platform support** - Handles both Unix (`\n`) and Windows (`\r\n`) line endings

###  Stable ID Generation
- **Human-readable IDs** - Format: `kind.name` (cluster-scoped) or `kind.namespace.name` (namespaced)
- **Duplicate handling** - Automatic numeric suffixes for resources with identical IDs
- **Deterministic ordering** - Same input always produces same output
- **Case normalization** - Consistent lowercase kind names

###  Flexible Input Methods
- **Inline content** - Embed YAML directly in Terraform configuration
- **File patterns** - Support for glob patterns including recursive `**` matching
- **Multiple files** - Automatically combines multiple files into single manifest map

###  Excellent Error Handling
- **Progressive processing** - Continues processing valid documents even if some fail
- **Detailed error context** - File names, line numbers, and specific error messages
- **Warning system** - Non-fatal issues reported as warnings, not errors

## Basic Usage

### Inline Content
```hcl
data "k8sconnect_yaml_split" "app" {
  content = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: my-app
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  namespace: my-app
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.21
YAML
}

# Apply each manifest individually
resource "k8sconnect_manifest" "app" {
  for_each = data.k8sconnect_yaml_split.app.manifests
  
  yaml_body = each.value
  
  cluster_connection = {
    kubeconfig = var.kubeconfig
  }
}
```

**Result**: Creates manifests with IDs:
- `namespace.my-app`
- `deployment.my-app.nginx`

### File Patterns
```hcl
# Load all YAML files from a directory
data "k8sconnect_yaml_split" "configs" {
  pattern = "./k8s-configs/*.yaml"
}

# Recursive directory search
data "k8sconnect_yaml_split" "all_manifests" {
  pattern = "./manifests/**/*.{yaml,yml}"
}

resource "k8sconnect_manifest" "all" {
  for_each = data.k8sconnect_yaml_split.all_manifests.manifests
  
  yaml_body = each.value
  
  cluster_connection = {
    kubeconfig_file = "~/.kube/config"
    context         = var.kube_context
  }
}
```

## Advanced Examples

### Dependency Ordering
```hcl
# Split all manifests
data "k8sconnect_yaml_split" "all" {
  pattern = "./k8s/**/*.yaml"
}

# Apply CRDs first
resource "k8sconnect_manifest" "crds" {
  for_each = {
    for key, yaml in data.k8sconnect_yaml_split.all.manifests :
    key => yaml
    if can(regex("(?i)customresourcedefinition", yaml))
  }
  
  yaml_body = each.value
  
  cluster_connection = {
    kubeconfig = var.kubeconfig
  }
}

# Apply everything else after CRDs
resource "k8sconnect_manifest" "resources" {
  for_each = {
    for key, yaml in data.k8sconnect_yaml_split.all.manifests :
    key => yaml
    if !can(regex("(?i)customresourcedefinition", yaml))
  }
  
  yaml_body = each.value
  
  cluster_connection = {
    kubeconfig = var.kubeconfig
  }
  
  depends_on = [k8sconnect_manifest.crds]
}
```

### Multi-Cluster Deployment
```hcl
data "k8sconnect_yaml_split" "apps" {
  pattern = "./apps/*.yaml"
}

locals {
  # Define cluster connections
  clusters = {
    prod = {
      kubeconfig = var.prod_kubeconfig
      context        = "prod-cluster"
    }
    staging = {
      kubeconfig = var.staging_kubeconfig
      context        = "staging-cluster"
    }
  }
  
  # Route manifests to clusters based on naming
  cluster_manifests = {
    for key, yaml in data.k8sconnect_yaml_split.apps.manifests : 
    key => {
      yaml    = yaml
      cluster = contains(key, "prod") ? "prod" : "staging"
    }
  }
}

# Deploy to appropriate clusters
resource "k8sconnect_manifest" "multi_cluster" {
  for_each = local.cluster_manifests
  
  yaml_body = each.value.yaml
  
  cluster_connection = local.clusters[each.value.cluster]
}
```

### Dynamic Content with Templates
```hcl
data "k8sconnect_yaml_split" "templated" {
  content = templatefile("${path.module}/app-template.yaml", {
    app_name     = var.app_name
    environment  = var.environment
    replicas     = var.replicas
    image_tag    = var.image_tag
    namespace    = var.namespace
  })
}

resource "k8sconnect_manifest" "templated_app" {
  for_each = data.k8sconnect_yaml_split.templated.manifests
  
  yaml_body = each.value
  
  cluster_connection = {
    host                   = var.k8s_host
    cluster_ca_certificate = var.k8s_ca
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", var.cluster_name]
    }
  }
}
```

## Pattern Matching

The `pattern` attribute supports standard glob patterns plus recursive matching:

| Pattern | Description | Example Matches |
|---------|-------------|-----------------|
| `*.yaml` | Files in current directory | `app.yaml`, `config.yaml` |
| `**/*.yaml` | Files in any subdirectory | `dir1/app.yaml`, `a/b/c/config.yaml` |
| `configs/*.{yaml,yml}` | Multiple extensions | `configs/app.yaml`, `configs/db.yml` |
| `apps/*/manifests/*.yaml` | Specific directory structure | `apps/web/manifests/deploy.yaml` |

## ID Generation Rules

The data source generates predictable, stable IDs based on Kubernetes resource metadata:

### Format
- **Cluster-scoped resources**: `kind.name`
  - Examples: `namespace.production`, `clusterrole.admin`
- **Namespaced resources**: `kind.namespace.name`  
  - Examples: `pod.default.nginx`, `service.kube-system.coredns`

## Error Handling

### Hard Failures (Terraform Apply Fails)
- **Duplicate resource IDs** - Same kind/namespace/name defined multiple times  
- **Invalid YAML** - Malformed YAML that cannot be parsed
- **Missing required fields** - Resources without kind or name
- **File read failures** - Cannot read files matched by pattern
- **No input specified** - Must provide either `content` or `pattern`
- **Both inputs specified** - Cannot use `content` and `pattern` together
- **Pattern not found** - No files match the specified pattern

### Example Error Conditions
```hcl
# This will FAIL - duplicate resources
data "k8sconnect_yaml_split" "invalid" {
  content = <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: nginx
  namespace: default
---
apiVersion: v1  
kind: Pod
metadata:
  name: nginx  # Same name/namespace/kind = ERROR
  namespace: default
YAML
}

# Error: duplicate resource ID "pod.default.nginx":
#   First defined: <inline> (document 1)  
#   Duplicate found: <inline> (document 2)
# Kubernetes resources must have unique kind/namespace/name combinations
```

The data source follows a **fail-fast philosophy**: if there are any issues that would prevent successful deployment to Kubernetes, Terraform will fail immediately with a clear error message.

### Edge Cases
- **Missing name**: Uses `unnamed` → `pod.default.unnamed`
- **Missing kind**: Uses `unknown` → `unknown.my-resource`
- **Invalid YAML**: Uses position → `invalid-doc-1`, `invalid-doc-2`

## Error Handling

### Warning Conditions
*None* - The data source uses a fail-fast approach. All issues that would prevent successful Kubernetes deployment result in errors, not warnings.

### Error Conditions
- **Duplicate resource IDs** - Same kind/namespace/name defined multiple times
- **Invalid YAML** - Malformed YAML that cannot be parsed  
- **Missing metadata** - Resources without required kind or name fields
- **File system issues** - Cannot read files, pattern doesn't match anything
- **Configuration errors** - Invalid input combinations

### Example Error Handling
```hcl
# This will produce warnings but continue processing
data "k8sconnect_yaml_split" "mixed_quality" {
  content = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: good-ns
---
invalid: yaml: content: [
  missing: bracket
---
apiVersion: v1
kind: Pod
metadata:
  name: good-pod
  namespace: good-ns
YAML
}

# Results in:
# - namespace.good-ns (valid)
# - invalid-doc-2 (preserved invalid YAML)
# - pod.good-ns.good-pod (valid)
# Plus warnings about the invalid document
```

## Schema Reference

| Attribute | Type | Required | Description |
|-----------|------|----------|-------------|
| `content` | string | No* | Raw YAML content with document separators |
| `pattern` | string | No* | Glob pattern for YAML files |
| `manifests` | map(string) | Computed | Map of stable IDs to YAML content |
| `id` | string | Computed | Data source identifier |

*Exactly one of `content` or `pattern` must be specified.

## Migration from Other Providers

### From `gavin_bunney/yaml_split`
```hcl
# Old approach
data "yaml_split" "docs" {
  yaml = file("manifests.yaml")
}

# New approach  
data "k8sconnect_yaml_split" "docs" {
  content = file("manifests.yaml")
}

# IDs are more stable and human-readable
# Old: numeric indices that change with reordering
# New: semantic IDs based on resource metadata
```

### From `kubectl-style` workflows
```bash
# Old bash approach
kubectl apply -f manifests/

# New Terraform approach
data "k8sconnect_yaml_split" "manifests" {
  pattern = "./manifests/*.yaml"
}

resource "k8sconnect_manifest" "all" {
  for_each = data.k8sconnect_yaml_split.manifests.manifests
  
  yaml_body = each.value
  cluster_connection = { /* ... */ }
}
```

## Best Practices

1. **Validate YAML first** - Ensure all YAML is valid and has unique resource identifiers
2. **Use semantic file organization** - Structure files to take advantage of stable ID generation  
3. **Avoid duplicates** - Each resource must have a unique kind/namespace/name combination
4. **Leverage dependency ordering** - Use `for_each` filtering for proper resource sequencing
5. **Template complex scenarios** - Use `templatefile()` for dynamic content generation
6. **Test with realistic data** - Verify behavior with actual Kubernetes manifests
7. **Handle errors explicitly** - Plan for data source failures in your Terraform workflow

## Limitations

- **Single cluster per data source** - Use multiple data sources for multi-cluster scenarios
- **Memory usage** - Large YAML files are loaded entirely into memory
- **No YAML validation** - Documents are parsed but not validated against Kubernetes schemas
- **No dependency analysis** - Resource relationships must be managed with `depends_on`

## Performance Considerations

- **File caching** - Results are cached based on content hash
- **Incremental processing** - Only changed files trigger re-processing
- **Memory efficient** - Documents are processed sequentially, not held in memory simultaneously
- **Pattern optimization** - Use specific patterns to avoid scanning unnecessary files
