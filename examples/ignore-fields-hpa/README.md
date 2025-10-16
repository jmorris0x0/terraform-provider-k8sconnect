# Ignore Fields with HPA Example

This example demonstrates how to use `ignore_fields` to prevent drift detection on fields managed by Kubernetes controllers.

## Use Case

When using a Horizontal Pod Autoscaler (HPA), the HPA controller modifies the `spec.replicas` field of Deployments dynamically based on load. Without `ignore_fields`, Terraform would:

1. Detect drift every time HPA changes replicas
2. Try to reset replicas to the configured value
3. Create a constant conflict between Terraform and HPA

## Solution

Use `ignore_fields` to exclude controller-managed fields from drift detection:

```hcl
resource "k8sconnect_object" "app" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-with-hpa
spec:
  replicas: 2  # Initial value only
  ...
YAML

  # Ignore spec.replicas - HPA manages this
  ignore_fields = ["spec.replicas"]
}
```

## Common Use Cases

### HPA Managing Replicas
```hcl
ignore_fields = ["spec.replicas"]
```

### Cert-Manager Injecting CA Bundle
```hcl
ignore_fields = [
  "webhooks[0].clientConfig.caBundle"
]
```

### Controller Managing Status or Annotations
```hcl
ignore_fields = [
  "metadata.annotations.last-applied-configuration",
  "status"
]
```

### Multiple Fields
```hcl
ignore_fields = [
  "spec.replicas",
  "metadata.annotations.kubectl.kubernetes.io/restartedAt"
]
```

## Path Syntax

The `ignore_fields` attribute supports:

- **Dot notation**: `spec.replicas`, `metadata.annotations`
- **Array indices**: `webhooks[0].clientConfig.caBundle`
- **Strategic merge keys**: `spec.containers[name=nginx].image`

When you specify a parent field, all child fields are ignored:
- `metadata.annotations` ignores all annotations
- `status` ignores the entire status block

## Running the Example

```bash
terraform init
terraform plan
terraform apply

# Verify deployment is created
kubectl get deployment nginx-with-hpa

# HPA will scale replicas based on load
kubectl get hpa nginx-hpa

# Terraform won't detect drift on replicas
terraform plan  # Shows no changes
```
