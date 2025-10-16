# YAML Scoped Dependency Ordering

This example demonstrates using `k8sconnect_yaml_scoped` to automatically categorize Kubernetes manifests by scope and apply them in the correct dependency order.

## What This Example Does

The `k8sconnect_yaml_scoped` datasource automatically splits your YAML manifests into three categories:

1. **CRDs** - CustomResourceDefinitions that must be applied first
2. **Cluster-scoped resources** - Namespaces, ClusterRoles, PersistentVolumes, etc.
3. **Namespaced resources** - Deployments, Services, ConfigMaps, Custom Resources, etc.

This ensures proper dependency ordering without manual regex filtering or complex Terraform logic.

## Benefits Over yaml_split

Compare this example to `yaml-split-dependency-ordering`:

**With yaml_split:**
```hcl
# Manual regex filtering for each category
for_each = {
  for key, yaml in data.k8sconnect_yaml_split.all.manifests :
  key => yaml
  if can(regex("(?i)kind:\\s*CustomResourceDefinition", yaml))
}
```

**With yaml_scoped:**
```hcl
# Automatic categorization - no regex needed!
for_each = data.k8sconnect_yaml_scoped.all.crds
```

## Usage

```bash
# Initialize Terraform
terraform init

# Plan
terraform plan

# Apply
terraform apply
```

The `cluster_connection` variable should be provided via `-var`, `-var-file`, or environment variables.

## Manifest Categories

The `manifests.yaml` file contains:

- **1 CRD**: `databases.storage.example.com` - Applied first
- **2 cluster-scoped resources**: Namespace + ClusterRole - Applied second
- **3 namespaced resources**: Deployment + ConfigMap + Database CR - Applied last

## Dependency Order

Terraform will apply resources in this order:

1. CRD (`k8sconnect_object.crds`)
2. Namespace and ClusterRole (`k8sconnect_object.cluster_scoped`)
3. Deployment, ConfigMap, and Database CR (`k8sconnect_object.namespaced`)

This ensures:
- The CRD exists before the Database custom resource is created
- The Namespace exists before namespaced resources are created
- All dependencies are satisfied automatically

## Outputs

After applying, you'll see:

```
crd_count = 1
cluster_scoped_count = 2
namespaced_count = 3
```

## Clean Up

```bash
terraform destroy
```

## Learn More

See the [yaml_scoped datasource documentation](../../docs/data-sources/yaml_scoped.md) for more details.
