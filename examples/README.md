# K8sconnect Examples

Working examples showing k8sconnect provider usage patterns.

## Basic Resources
- `basic-deployment/` - Simple namespace and deployment

## Wait For Feature

The `wait_for` block supports four strategies. **Only `field` waits populate `.status` for resource chaining.**

### Field Waits (with status output)
- `wait-for-loadbalancer/` - Wait for LoadBalancer IP/hostname and use it in other resources (`wait_for.field`)
  - **Populates `.status`** for chaining
  - Example: `${resource.status.loadBalancer.ingress[0].ip}`

### Field Value Waits (no status output)
- `wait-for-job-completion/` - Wait for Job completion (`wait_for.field_value`)
  - Waits for specific field values (e.g., `status.succeeded = "1"`)
  - No `.status` output - use `depends_on` for chaining

### Condition Waits (no status output)
- `wait-for-condition/` - Wait for Kubernetes conditions (`wait_for.condition`)
  - Waits for condition types like "Available", "Ready", "Progressing"
  - No `.status` output - use `depends_on` for chaining

### Rollout Waits (no status output)
- `wait-for-deployment-rollout/` - Wait for Deployment rollout completion (`wait_for.rollout`)
  - Ensures all replicas are updated and ready
  - Works with Deployments, StatefulSets, DaemonSets
  - No `.status` output - use `depends_on` for chaining

## Ignore Fields
- `ignore-fields-hpa/` - Ignore HPA-managed replicas to prevent drift

## YAML Split Data Source
- `yaml-split-inline/` - Split inline multi-document YAML
- `yaml-split-files/` - Load from file patterns
- `yaml-split-templated/` - Dynamic content with templatefile()

## YAML Scoped Data Source
- `yaml-scoped-dependency-ordering/` - Automatic dependency ordering by scope (CRDs → cluster-scoped → namespaced)

## Resource Data Source
- `manifest-datasource-kubernetes-service/` - Read existing cluster resources and use their data

## Patch Resource
- `patch-strategic-merge/` - Strategic Merge Patch with Server-Side Apply (SSA) field ownership
- `patch-json-patch/` - JSON Patch (RFC 6902) with array operations
- `patch-merge-patch/` - Merge Patch (RFC 7386) for simple field merging

## Running Examples

Examples assume you have the provider installed and cluster access configured.

