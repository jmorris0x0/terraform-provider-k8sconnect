---
page_title: "Wait Strategies - k8sconnect Provider"
subcategory: "Guides"
description: |-
  When to use field vs rollout vs condition vs field_value waits, and how to chain resources.
---

# Wait Strategies

## Quick Reference

| Strategy | Use For | Populates `.result`? |
|----------|---------|---------------------|
| **field** | Extract status values | ✅ Yes |
| **rollout** | Wait for workload deployment | ❌ No (use `depends_on`) |
| **condition** | Wait for K8s conditions | ❌ No (use `depends_on`) |
| **field_value** | Wait for specific values | ❌ No (use `depends_on`) |

**Critical rule:** Only `field` waits populate `.result`. Everything else uses `depends_on` for chaining.

## When to Use Each Strategy

### field - Extract Status Values

Extract a status field for use in other resources.

**Use for:** LoadBalancer IPs, Ingress hostnames, Certificate secrets, Crossplane outputs

```terraform
resource "k8sconnect_object" "lb" {
  yaml_body = file("loadbalancer.yaml")
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_wait" "lb" {
  object_ref = k8sconnect_object.lb.object_ref

  wait_for = {
    field   = "status.loadBalancer.ingress"
    timeout = "10m"
  }

  cluster_connection = local.cluster_connection
}

# Use the extracted value
resource "aws_route53_record" "dns" {
  records = [k8sconnect_wait.lb.result.status.loadBalancer.ingress[0].ip]
}
```

**Why only the waited-for field?** Prevents drift from volatile status fields.

### rollout - Wait for Workloads

Wait for Deployment/StatefulSet/DaemonSet to fully deploy.

**Use for:** Sequential deployments, migration jobs, blue-green switches

```terraform
resource "k8sconnect_object" "database" {
  yaml_body = file("postgres.yaml")
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_wait" "database" {
  object_ref = k8sconnect_object.database.object_ref
  wait_for   = { rollout = true, timeout = "5m" }
  cluster_connection = local.cluster_connection
}

# Run migrations after DB is ready
resource "k8sconnect_object" "migration" {
  yaml_body  = file("migration-job.yaml")
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_wait.database]
}
```

**Checks:** replicas == updatedReplicas == readyReplicas, observedGeneration == generation

### condition - Wait for Conditions

Wait for Kubernetes condition status to be "True".

**Use for:** Custom CRDs, operator-managed resources, ArgoCD Applications

```terraform
resource "k8sconnect_object" "argocd_app" {
  yaml_body = file("argocd-application.yaml")
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_wait" "argocd_app" {
  object_ref = k8sconnect_object.argocd_app.object_ref
  wait_for   = { condition = "Healthy", timeout = "15m" }
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "dependent_service" {
  yaml_body  = file("service.yaml")
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_wait.argocd_app]
}
```

### field_value - Wait for Specific Values

Wait for field to match exact string value.

**Use for:** Job completion, PVC binding, Pod phases

```terraform
resource "k8sconnect_object" "setup_job" {
  yaml_body = file("setup-job.yaml")
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_wait" "setup_job" {
  object_ref = k8sconnect_object.setup_job.object_ref

  wait_for = {
    field       = "status.succeeded"
    field_value = "1"
    timeout     = "10m"
  }

  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "app" {
  yaml_body  = file("app.yaml")
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_wait.setup_job]
}
```

## Common Patterns

### Extract and Use Value

```terraform
# Create → Wait → Extract → Use
resource "k8sconnect_object" "cert" {
  yaml_body = file("certificate.yaml")
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_wait" "cert" {
  object_ref = k8sconnect_object.cert.object_ref
  wait_for   = { field = "status.conditions[?type=='Ready'].status", timeout = "10m" }
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "ingress" {
  yaml_body = templatefile("ingress.yaml", {
    tls_secret = k8sconnect_wait.cert.result.spec.secretName
  })
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_wait.cert]
}
```

### Sequential Deployment

<!-- runnable-test: wait-sequential-deployment -->
```terraform
# Database → Wait → Migration → Wait → App
resource "k8sconnect_object" "db" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: postgres
      namespace: default
    spec:
      replicas: 1
      selector:
        matchLabels:
          app: postgres
      template:
        metadata:
          labels:
            app: postgres
        spec:
          containers:
          - name: postgres
            image: postgres:14
            env:
            - name: POSTGRES_PASSWORD
              value: testpass
  YAML
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_wait" "db" {
  object_ref = k8sconnect_object.db.object_ref
  wait_for   = { rollout = true, timeout = "5m" }
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "migration" {
  yaml_body = <<-YAML
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: db-migration
      namespace: default
    spec:
      template:
        spec:
          containers:
          - name: migrate
            image: busybox
            command: ["sh", "-c", "echo 'Running migration'; sleep 2; echo 'Migration complete'"]
          restartPolicy: Never
      backoffLimit: 1
  YAML
  cluster_connection = local.cluster_connection

  depends_on = [k8sconnect_wait.db]
}

resource "k8sconnect_wait" "migration" {
  object_ref = k8sconnect_object.migration.object_ref

  wait_for = {
    field_value = {
      "status.succeeded" = "1"
    }
    timeout = "5m"
  }

  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "app" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: app
      namespace: default
    spec:
      replicas: 1
      selector:
        matchLabels:
          app: myapp
      template:
        metadata:
          labels:
            app: myapp
        spec:
          containers:
          - name: app
            image: nginx:1.21
  YAML
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_wait.migration]
}
```
<!-- /runnable-test -->

### Parallel Waits

```terraform
# Deploy multiple services → Wait for all → Deploy LB
resource "k8sconnect_object" "services" {
  for_each = toset(["api", "worker", "scheduler"])
  yaml_body = file("${each.key}.yaml")
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_wait" "services" {
  for_each = k8sconnect_object.services

  object_ref = each.value.object_ref
  wait_for   = { rollout = true, timeout = "5m" }
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "lb" {
  yaml_body  = file("lb.yaml")
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_wait.services]
}
```

## Timeout Guidelines

| Resource Type | Recommended Timeout |
|--------------|---------------------|
| Deployment (simple) | 5m |
| Deployment (complex) | 10-15m |
| LoadBalancer Service | 10-15m |
| Job (simple) | 10m |
| Job (migration) | 30m+ |
| cert-manager Certificate | 10-15m |
| Crossplane resources | 30m+ |

## Timeout Behavior

**Key difference from hashicorp/kubernetes:**

When wait times out:
- ✅ Wait resource fails, NOT tainted
- ✅ Re-running `terraform apply` retries
- ✅ Underlying resource unchanged

```bash
# First apply - timeout
$ terraform apply
Error: Wait timeout

# Fix issue, retry - no recreation
$ terraform apply
Apply complete!
```

## Decision Tree

```
Need to extract a value? → field
Need workload fully deployed? → rollout
Have a K8s condition? → condition
Waiting for specific value? → field_value
```

## Common Mistakes

### Using rollout when you need a value

```terraform
# ❌ BAD: Can't access .result from rollout
resource "k8sconnect_wait" "lb" {
  wait_for = { rollout = true }
}

output "ip" {
  value = k8sconnect_wait.lb.result.status.loadBalancer.ingress[0].ip  # ERROR
}

# ✅ GOOD: Use field wait
resource "k8sconnect_wait" "lb" {
  wait_for = { field = "status.loadBalancer.ingress" }
}

output "ip" {
  value = k8sconnect_wait.lb.result.status.loadBalancer.ingress[0].ip
}
```

### Not waiting for operators

```terraform
# ❌ BAD: CR created before operator is running
resource "k8sconnect_object" "operator" {
  yaml_body = file("operator.yaml")
}

resource "k8sconnect_object" "cr" {
  yaml_body  = file("cr.yaml")
  depends_on = [k8sconnect_object.operator]  # Operator deployed, not ready!
}

# ✅ GOOD: Wait for operator rollout
resource "k8sconnect_wait" "operator" {
  object_ref = k8sconnect_object.operator.object_ref
  wait_for   = { rollout = true }
}

resource "k8sconnect_object" "cr" {
  depends_on = [k8sconnect_wait.operator]
}
```

## Related Resources

- [k8sconnect_wait resource](../resources/wait.md)
- [Bootstrap Patterns guide](bootstrap-patterns.md)

## Summary

- Only `field` waits populate `.result` - use it when you need values
- `rollout`/`condition`/`field_value` use `depends_on` for chaining
- Waits are retriable (not tainted on timeout)
- Always wait for operators before creating CRs
