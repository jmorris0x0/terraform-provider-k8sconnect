# K8sconnect Examples

Working examples showing k8sconnect provider usage patterns.

## Basic Resources
- `basic-deployment/` - Simple namespace and deployment

## Wait For Feature
- `wait-for-loadbalancer/` - Wait for LoadBalancer to get IP/hostname (`wait_for.field`)
- `wait-for-job-completion/` - Wait for Job to complete successfully (`wait_for.field_value`)

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

