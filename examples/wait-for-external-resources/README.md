# Wait for External Resources

This example demonstrates using `k8sconnect_wait` **standalone** - without any `k8sconnect_object` resources.

## Key Insight

`k8sconnect_wait` can wait for ANY Kubernetes resource, regardless of how it was created:
- Helm charts
- kubectl apply
- Operators
- Cluster bootstrapping tools (kubeadm, kops, k3d, etc.)
- Other Terraform states/workspaces
- Manual creation

**You don't need `k8sconnect_object` to use `k8sconnect_wait`.**

## Pattern

```
External Resource (Helm/kubectl/etc.) → k8sconnect_wait → Your Terraform Resources
```

## Use Cases

**Infrastructure dependencies:**
- Wait for metrics-server before creating HPAs
- Wait for cert-manager before creating Certificates
- Wait for ingress controller before creating Ingresses
- Wait for storage provisioner before creating PVCs

**Sequencing against other automation:**
- Bootstrap tools created the cluster infrastructure
- Operators installed CRDs
- Another team manages shared services via Helm
- You just need to wait for them to be ready

## How It Works

Instead of:
```hcl
resource "k8sconnect_wait" "thing" {
  object_ref = k8sconnect_object.thing.object_ref  # ← Reference to your resource
  # ...
}
```

You directly specify the resource identity:
```hcl
resource "k8sconnect_wait" "thing" {
  object_ref = {
    api_version = "apps/v1"
    kind        = "Deployment"
    namespace   = "kube-system"
    name        = "metrics-server"
  }
  # ...
}
```

## Real-World Example

Your cluster has metrics-server installed via Helm. You want to create an HPA but only after metrics-server is ready:

```hcl
# Wait for metrics-server (you didn't create it)
resource "k8sconnect_wait" "metrics_server" {
  object_ref = {
    api_version = "apps/v1"
    kind        = "Deployment"
    namespace   = "kube-system"
    name        = "metrics-server"
  }

  wait_for = {
    condition = "Available"
    timeout   = "5m"
  }

  cluster = var.cluster
}

# Create your HPA (only after metrics-server is ready)
resource "k8sconnect_object" "my_hpa" {
  yaml_body = file("hpa.yaml")
  cluster = var.cluster

  depends_on = [k8sconnect_wait.metrics_server]
}
```

## This Example

This example waits for three cluster infrastructure components that k3d installs automatically:
1. **metrics-server** - For resource metrics and HPA
2. **coredns** - For DNS resolution
3. **local-path-provisioner** - For dynamic storage provisioning

The `terraform apply` succeeds only after all three are available. That's it - no resources are created, just waiting.

## Running This Example

```bash
terraform init
terraform apply
```

You'll see output confirming the cluster infrastructure is ready.

## Checking What Already Exists

```bash
# See what's already running in your cluster
kubectl get deployments -n kube-system

# Wait for any of them
terraform apply  # Uses k8sconnect_wait with object_ref
```

## Adapting to Your Cluster

Change the `object_ref` to match your actual resources:

```hcl
resource "k8sconnect_wait" "your_thing" {
  object_ref = {
    api_version = "v1"              # Find with: kubectl api-resources
    kind        = "Service"         # The resource kind
    namespace   = "your-namespace"  # Omit for cluster-scoped resources
    name        = "your-resource"   # The actual resource name
  }

  wait_for = {
    condition = "Ready"  # Or use field, rollout, field_value
    timeout   = "5m"
  }

  cluster = var.cluster
}
```

## Why This Matters

Most Kubernetes clusters have infrastructure managed outside Terraform:
- Cloud provider installs storage CSI drivers, metrics-server
- Platform teams install shared services via Helm
- Operators install and manage CRDs

With `k8sconnect_wait`, you can sequence your Terraform resources against this infrastructure without having to import or manage it.

This is much cleaner than:
- Manual waits with `null_resource` + `local-exec` + `sleep`
- Custom scripts checking for readiness
- Hoping resources exist and handling errors
