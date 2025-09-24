# K8sconnect Examples

Working examples showing k8sconnect provider usage patterns.

## Basic Resources
- `basic-deployment/` - Simple namespace and deployment
- `loadbalancer-service/` - Service with LoadBalancer type

## Wait For Feature
- `wait-for-loadbalancer/` - Wait for LoadBalancer to get IP/hostname (`wait_for.field`)
- `wait-for-job-completion/` - Wait for Job to complete successfully (`wait_for.field_value`)

## YAML Split Data Source
- `yaml-split-inline/` - Split inline multi-document YAML
- `yaml-split-files/` - Load from file patterns
- `yaml-split-dependency-ordering/` - Apply CRDs before dependent resources  
- `yaml-split-templated/` - Dynamic content with templatefile()

## Running Examples

Examples assume you have the provider installed and cluster access configured.

