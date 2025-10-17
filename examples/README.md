# K8sconnect Examples

Working examples showing k8sconnect provider usage patterns.

## Basic Resources
- [`basic-deployment/`](basic-deployment/) - Simple namespace and deployment

## Wait For Feature

The `wait_for` block supports four strategies. **Only `field` waits populate `.status` for resource chaining.**

### Standalone Wait (no k8sconnect_object needed)

- [`wait-for-external-resources/`](wait-for-external-resources/) - **Wait for resources you don't manage** - Use k8sconnect_wait standalone to wait for Helm charts, operators, kubectl-applied resources, etc.

### Field Waits (with status output)

Infrastructure resources that need status values for DNS, outputs, or chaining:

- [`wait-for-loadbalancer/`](wait-for-loadbalancer/) - Wait for LoadBalancer IP/hostname and use it in DNS/config
- [`wait-for-ingress/`](wait-for-ingress/) - Wait for Ingress hostname and use it for external access
- [`wait-for-pvc-volume/`](wait-for-pvc-volume/) - Wait for PVC volumeName and track bound PV

**Pattern:** Use `wait_for.field` → populates `.status` → use in outputs/other resources

### Workload Waits (no status output)

Workload resources that just need readiness confirmation for sequencing:

- [`wait-for-deployment-rollout/`](wait-for-deployment-rollout/) - Wait for Deployment rollout (`wait_for.rollout`)
- [`wait-for-job-completion/`](wait-for-job-completion/) - Wait for Job completion (`wait_for.field_value`)
- [`wait-for-condition/`](wait-for-condition/) - Wait for Kubernetes conditions (`wait_for.condition`)

**Pattern:** Use `rollout`, `condition`, or `field_value` → no `.status` → use `depends_on` for sequencing

## Ignore Fields
- [`ignore-fields-hpa/`](ignore-fields-hpa/) - Ignore HPA-managed replicas to prevent drift

## YAML Split Data Source
- [`yaml-split-inline/`](yaml-split-inline/) - Split inline multi-document YAML
- [`yaml-split-files/`](yaml-split-files/) - Load from file patterns
- [`yaml-split-templated/`](yaml-split-templated/) - Dynamic content with templatefile()

## YAML Scoped Data Source
- [`yaml-scoped-dependency-ordering/`](yaml-scoped-dependency-ordering/) - Automatic dependency ordering by scope (CRDs → cluster-scoped → namespaced)

## Resource Data Source
- [`object-datasource-kubernetes-service/`](object-datasource-kubernetes-service/) - Read existing cluster resources and use their data

## Patch Resource
- [`patch-strategic-merge/`](patch-strategic-merge/) - Strategic Merge Patch with Server-Side Apply (SSA) field ownership
- [`patch-json-patch/`](patch-json-patch/) - JSON Patch (RFC 6902) with array operations
- [`patch-merge-patch/`](patch-merge-patch/) - Merge Patch (RFC 7386) for simple field merging

## Running Examples

Examples assume you have the provider installed and cluster access configured.

