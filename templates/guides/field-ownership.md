---
page_title: "Managed Fields and Controller Conflicts - k8sconnect Provider"
subcategory: "Guides"
description: |-
  Understanding field ownership, handling controller conflicts, and using ignore_fields to coexist with Kubernetes controllers.
---

# Managed Fields and Controller Conflicts

## What is Managed Fields?

k8sconnect uses **Server-Side Apply (SSA)** with field ownership tracking to safely coexist with Kubernetes controllers.

When k8sconnect creates or updates a Kubernetes resource, it claims ownership of the fields you specify in your Terraform configuration. Kubernetes tracks which field manager (controller) owns which fields using the `managedFields` metadata.

**Example:** If you create a Deployment with `replicas: 3`, k8sconnect owns the `spec.replicas` field.

## Common Conflict Scenarios

### Scenario 1: HorizontalPodAutoscaler (HPA)

The most common conflict occurs with HPA, which automatically manages the `spec.replicas` field based on metrics.

**What happens:**
1. You create a Deployment with `replicas: 3`
2. You add an HPA to autoscale between 2-10 replicas
3. HPA changes replicas to 5 based on CPU usage
4. k8sconnect detects a conflict: both you and HPA want to manage `spec.replicas`

**You'll see this warning during `terraform plan`:**

```
Warning: Managed Fields Override

Forcing ownership of fields managed by other controllers:
  - spec.replicas (managed by "hpa-controller")

These fields will be forcibly taken over. The other controllers may fight back.

To release ownership and allow other controllers to manage these fields, add:

  ignore_fields = ["spec.replicas"]
```

### Scenario 2: LoadBalancer Services

When you create a LoadBalancer Service, Kubernetes automatically assigns:
- `spec.ports[*].nodePort` (random ports 30000-32767)
- `status.loadBalancer.ingress` (external IP/hostname)

k8sconnect **does NOT conflict** here because it only tracks fields it owns. Server-side apply with field ownership tracking prevents false drift on server-added fields.

### Scenario 3: Manual kubectl Changes

If you or a teammate runs `kubectl apply` on a resource managed by k8sconnect:

```bash
kubectl apply -f deployment.yaml
```

kubectl claims ownership of fields in that file. On the next `terraform plan`, you'll see warnings about ownership takeover.

## Resolving Conflicts with `ignore_fields`

The `ignore_fields` attribute tells k8sconnect to release ownership of specific fields, allowing other controllers to manage them.

### Example: HPA-managed Deployment

<!-- runnable-test: field-ownership-hpa -->
```terraform
resource "k8sconnect_object" "app" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: nginx
      namespace: default
    spec:
      replicas: 3  # Initial value, HPA will manage this
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
            resources:
              requests:
                cpu: 100m
  YAML

  cluster = local.cluster

  # Release ownership of replicas to HPA
  ignore_fields = ["spec.replicas"]
}

resource "k8sconnect_object" "hpa" {
  yaml_body = <<-YAML
    apiVersion: autoscaling/v2
    kind: HorizontalPodAutoscaler
    metadata:
      name: nginx-hpa
      namespace: default
    spec:
      scaleTargetRef:
        apiVersion: apps/v1
        kind: Deployment
        name: nginx
      minReplicas: 2
      maxReplicas: 10
      metrics:
      - type: Resource
        resource:
          name: cpu
          target:
            type: Utilization
            averageUtilization: 80
  YAML

  cluster = local.cluster
  depends_on = [k8sconnect_object.app]
}
```
<!-- /runnable-test -->

**What happens:**
1. k8sconnect creates the Deployment with `replicas: 3`
2. k8sconnect immediately releases ownership of `spec.replicas` (due to `ignore_fields`)
3. HPA takes ownership and adjusts replicas based on load
4. Terraform shows no drift on `spec.replicas` - HPA owns it

### Example: Multiple Ignored Fields

```terraform
resource "k8sconnect_object" "configmap" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: app-config
      namespace: default
      annotations:
        config-version: "1.0"
    data:
      app.conf: |
        setting = value
  YAML

  cluster = local.cluster

  # External operator manages these fields
  ignore_fields = [
    "metadata.annotations.last-updated",
    "metadata.annotations.checksum",
    "data.runtime-config"
  ]
}
```

## Field Path Syntax

The `ignore_fields` attribute uses dot-notation for field paths:

**Simple fields:**
```terraform
ignore_fields = ["spec.replicas"]
```

**Nested fields:**
```terraform
ignore_fields = ["metadata.annotations.my-annotation"]
```

**Array elements (by index):**
```terraform
ignore_fields = ["spec.containers[0].image"]
```

**Array elements (by name - strategic merge):**
```terraform
ignore_fields = ["spec.containers[name=nginx].image"]
```

**Wildcard for all array elements:**
```terraform
ignore_fields = ["spec.ports[*].nodePort"]
```

## When to Use `ignore_fields` vs k8sconnect_patch

### Use `ignore_fields` when:
- ✅ You want full lifecycle management (create/update/delete)
- ✅ A controller manages a few specific fields (like HPA managing replicas)
- ✅ You control the base resource and just need to ignore controller-managed fields

### Use `k8sconnect_patch` when:
- ✅ Another tool (Helm, operators) owns the resource lifecycle
- ✅ You only want to modify specific fields without full ownership
- ✅ You're patching cloud-managed resources (EKS/GKE defaults)

**Example: Patch EKS-managed ConfigMap**
```terraform
resource "k8sconnect_patch" "coredns_custom" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "coredns-custom"
    namespace   = "kube-system"
  }

  patch_content = <<-YAML
    data:
      custom.override: |
        log
        errors
  YAML

  cluster = local.cluster
}
```

## Understanding the Warnings

### During Plan (terraform plan)

**Managed Fields Override (object resource):**
```
Warning: Managed Fields Override

Forcing ownership of fields managed by other controllers:
  - spec.replicas (managed by "hpa-controller")

These fields will be forcibly taken over. The other controllers may fight back.

To release ownership and allow other controllers to manage these fields, add:

  ignore_fields = ["spec.replicas"]
```

**Action:** Add `ignore_fields` if you want the other controller to manage those fields.

**Managed Fields Takeover (patch resource):**
```
Warning: Managed Fields Takeover

This patch will forcefully take ownership of fields managed by other controllers:
  - spec.replicas (managed by "hpa-controller")

These fields will be taken over with force=true. The other controllers may fight back for control.
```

**Action:** This is expected for patches. The warning reminds you that:
- Patches always use `force=true` (by design)
- External controllers may revert your changes
- Consider `k8sconnect_object` with `ignore_fields` if you need better control

### During Apply (terraform apply)

**Resource Already Managed:**
```
Error: Resource Already Managed

ConfigMap my-config (namespace: default) is already managed by a different
k8sconnect resource (Terraform ID: a1b2c3d4e5f6)
```

**Action:** You're trying to manage the same resource with two different k8sconnect resources. Choose one.

## Best Practices

### 1. Plan Before You Apply
Always run `terraform plan` and read the warnings. They tell you exactly which fields will cause conflicts.

### 2. Copy the Suggested Configuration
The warnings include ready-to-use `ignore_fields` configuration. Copy and paste it:

```
To release ownership and allow other controllers to manage these fields, add:

  ignore_fields = [
    "spec.replicas",
    "metadata.annotations.autoscaling-config"
  ]
```

### 3. Don't Ignore Too Much
Only ignore fields that external controllers actually manage. Ignoring too many fields defeats the purpose of infrastructure-as-code.

**Bad:**
```terraform
ignore_fields = ["spec.*"]  # Too broad - you lose control
```

**Good:**
```terraform
ignore_fields = ["spec.replicas"]  # Specific - HPA manages this
```

### 4. Document Why You're Ignoring Fields
```terraform
resource "k8sconnect_object" "app" {
  yaml_body = file("deployment.yaml")

  # Release replicas to HPA for autoscaling
  # HPA config: 2-10 replicas based on CPU > 80%
  ignore_fields = ["spec.replicas"]

  cluster = local.cluster
}
```

## Advanced: Managed Fields in Multi-Controller Scenarios

When multiple controllers interact with a resource:

```terraform
resource "k8sconnect_object" "service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: my-app
      namespace: default
      annotations:
        service.beta.kubernetes.io/aws-load-balancer-type: nlb
    spec:
      type: LoadBalancer
      selector:
        app: my-app
      ports:
      - port: 80
        targetPort: 8080
  YAML

  cluster = local.cluster

  # AWS Load Balancer Controller manages these annotations
  ignore_fields = [
    "metadata.annotations.service.beta.kubernetes.io/aws-load-balancer-backend-protocol",
    "metadata.annotations.service.beta.kubernetes.io/aws-load-balancer-healthcheck-path"
  ]
}
```

k8sconnect manages:
- `metadata.name`, `namespace`, `labels`
- `spec.type`, `spec.selector`, `spec.ports`
- The NLB annotation you specified

AWS LB Controller manages:
- Additional annotations it adds for health checks, backend protocol, etc.

## Troubleshooting

### "The other controllers may fight back"

**Problem:** You see the warning but don't understand what it means.

**Explanation:** Without `ignore_fields`, k8sconnect uses `force=true` to take ownership. The other controller (HPA, operator, etc.) sees the field change and fights back by resetting it. You get a "tug-of-war" where values oscillate.

**Solution:** Add `ignore_fields` to release ownership cleanly.

### Terraform shows drift even with `ignore_fields`

**Problem:** You added `ignore_fields` but still see drift in plan output.

**Possible causes:**
1. **Wrong field path** - Check the path syntax matches the warning
2. **Field not in ignore list** - You might need to ignore additional fields
3. **Stale state** - Run `terraform refresh` to update state

**Debug:** Check which field is drifting and compare to your `ignore_fields` list.

### Can I use wildcards?

**Yes, but carefully:**

```terraform
# Ignore all nodePort assignments
ignore_fields = ["spec.ports[*].nodePort"]

# Ignore all annotations (risky - you lose control)
ignore_fields = ["metadata.annotations.*"]
```

Strategic merge patch supports `[*]` and `[name=...]` selectors. Use specific paths when possible.

## Related Resources

- [k8sconnect_object](../resources/object.md) - Full lifecycle management with `ignore_fields`
- [k8sconnect_patch](../resources/patch.md) - Surgical modifications without full ownership
- [ADR-005: Managed Fields Strategy](../ADRs/005-field-ownership-strategy.md) - Technical deep-dive

## Summary

**Key takeaways:**
- k8sconnect uses Server-Side Apply with field ownership tracking
- `ignore_fields` releases ownership of specific fields to other controllers
- Warnings during plan show exact `ignore_fields` configuration to copy
- Use `k8sconnect_object` + `ignore_fields` for full lifecycle management
- Use `k8sconnect_patch` for surgical modifications without ownership

When in doubt, run `terraform plan` and read the warnings - they tell you exactly what to do.
