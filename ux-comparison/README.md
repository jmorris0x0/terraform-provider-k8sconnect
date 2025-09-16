# Terraform Kubernetes Provider Comparison

This test compares the UX differences between Kubernetes Terraform providers when deploying the same resources.

## Providers Being Compared

1. **k8sconnect** - A new provider with inline, per-resource cluster connections
2. **kubectl** - The popular community provider by alekc
3. **kubernetes** (hashicorp) - The official provider (has issues with dynamic clusters)

