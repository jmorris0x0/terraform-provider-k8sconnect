# Flux CD Bootstrap

Bootstrap Flux CD in a single `terraform apply` without the Flux Terraform provider.

## The Problem

The [Flux Terraform provider](https://github.com/fluxcd/terraform-provider-flux) requires cluster credentials in the `provider "flux"` block, which is evaluated at plan time. If you're creating the cluster in the same apply (EKS, GKE, AKS), those credentials don't exist yet.

k8sconnect solves this with inline, per-resource connections that resolve during apply.

## How It Works

Flux's install manifest is just Kubernetes resources. This example:

1. Fetches the official Flux install YAML from the GitHub release
2. Uses `k8sconnect_yaml_scoped` to categorize resources into CRDs, cluster-scoped, and namespaced
3. Applies them in dependency order (CRDs first, then Namespaces, then controllers)
4. Waits for source-controller to be ready
5. Creates the GitRepository and Kustomization that point Flux at your fleet repo

After this, Flux is fully operational and syncing from your Git repository.

## Flux Upgrades

Terraform owns the Flux lifecycle. To upgrade, bump the version in the `data "http"` URL and run `terraform apply`. This is equivalent to `flux install --export --version=v2.5.0` but integrated into your infrastructure-as-code workflow.

## Customization

- **Git repository URL**: Change the `url` in `flux_source` to your fleet repo
- **Sync path**: Change `path` in `flux_kustomization` to match your cluster directory
- **SSH key**: You'll need a `k8sconnect_object` for the deploy key Secret in `flux-system`
- **Flux version**: Change the version in the `data "http"` URL

## Usage

```bash
terraform init
terraform plan
terraform apply
```

The `cluster` variable should be provided via `-var`, `-var-file`, or environment variables.

## Learn More

- [Bootstrap Patterns guide](../../docs/guides/bootstrap-patterns.md)
- [yaml_scoped data source](../../docs/data-sources/yaml_scoped.md)
- [Wait Strategies guide](../../docs/guides/wait-strategies.md)
