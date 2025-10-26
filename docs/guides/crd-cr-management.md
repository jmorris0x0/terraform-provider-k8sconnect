---
page_title: "CRD + CR Management - k8sconnect Provider"
subcategory: "Guides"
description: |-
  How to manage Custom Resource Definitions (CRDs) and Custom Resources (CRs) in a single terraform apply with automatic retry and dependency ordering.
---

# CRD + CR Management

## The Problem

Kubernetes eventual consistency: CRD exists in etcd, but API server hasn't registered the new endpoint yet.

**hashicorp/kubernetes**: Requires two applies (Issue #1367, 362+ 👍)
**gavinbunney/kubectl**: Needs `apply_retry_count` config, still fails often
**k8sconnect**: Single apply, zero config, automatic retry

## Auto-Retry Solution

Retries "no matches for kind" errors: 100ms → 500ms → 1s → 2s → 5s → 10s → 10s (~30s total)

**Only retries CRD-missing errors**. Validation/permission errors fail immediately.

## Usage Patterns

### Pattern 1: Simple CRD + CR (Automatic Retry)

**Use when:** You have a few CRDs and CRs with no complex dependencies.

<!-- runnable-test: crd-cr-auto-retry -->
```terraform
# CRD Definition
resource "k8sconnect_object" "widget_crd" {
  yaml_body = <<-YAML
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: widgets.example.com
    spec:
      group: example.com
      names:
        kind: Widget
        plural: widgets
      scope: Namespaced
      versions:
      - name: v1
        served: true
        storage: true
        schema:
          openAPIV3Schema:
            type: object
            properties:
              spec:
                type: object
                properties:
                  size:
                    type: integer
                  color:
                    type: string
  YAML

  cluster_connection = local.cluster_connection
}

# Custom Resource - will auto-retry if CRD not ready
resource "k8sconnect_object" "widget" {
  yaml_body = <<-YAML
    apiVersion: example.com/v1
    kind: Widget
    metadata:
      name: my-widget
      namespace: default
    spec:
      size: 42
      color: blue
  YAML

  cluster_connection = local.cluster_connection

  # Optional: explicit dependency (good practice but not required)
  depends_on = [k8sconnect_object.widget_crd]
}
```
<!-- /runnable-test -->

**What happens:**
1. Terraform submits CRD
2. Terraform immediately tries to submit CR
3. CR fails (CRD not ready yet)
4. k8sconnect auto-retries with backoff
5. CR succeeds once CRD is established (~2-5 seconds typically)

**No configuration needed!**

### Pattern 2: Multiple CRDs (Scoped Ordering)

**Use when:** You have many resources and want explicit ordering for clarity.

```terraform
# Split manifests by scope
data "k8sconnect_yaml_scoped" "operator" {
  content = file("${path.module}/operator-manifests.yaml")
}

# Stage 1: Apply CRDs first
resource "k8sconnect_object" "crds" {
  for_each = data.k8sconnect_yaml_scoped.operator.crds

  yaml_body          = each.value
  cluster_connection = local.cluster_connection
}

# Stage 2: Cluster-scoped resources (Namespaces, ClusterRoles, etc.)
resource "k8sconnect_object" "cluster_scoped" {
  for_each = data.k8sconnect_yaml_scoped.operator.cluster_scoped

  yaml_body          = each.value
  cluster_connection = local.cluster_connection

  depends_on = [k8sconnect_object.crds]
}

# Stage 3: Namespaced resources (Deployments, Services, CRs)
resource "k8sconnect_object" "namespaced" {
  for_each = data.k8sconnect_yaml_scoped.operator.namespaced

  yaml_body          = each.value
  cluster_connection = local.cluster_connection

  depends_on = [
    k8sconnect_object.crds,
    k8sconnect_object.cluster_scoped
  ]
}
```

**Advantages:**
- Clear staging (CRDs → cluster-scoped → namespaced)
- Easier to debug (know which stage failed)
- Better for large operator deployments
- Still gets auto-retry as backup

**When to use:** Large multi-document YAML files (operators, Helm exports, etc.)

### Pattern 3: CRD with Validation Webhook

**Use when:** Your CRD has a validating webhook that must be deployed first.

```terraform
# Stage 1: Deploy webhook service and deployment
resource "k8sconnect_object" "webhook_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: webhook-system
  YAML

  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "webhook_service" {
  yaml_body = file("${path.module}/webhook-service.yaml")
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_object.webhook_namespace]
}

resource "k8sconnect_object" "webhook_deployment" {
  yaml_body = file("${path.module}/webhook-deployment.yaml")
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_object.webhook_namespace]
}

# Stage 2: Wait for webhook to be ready
resource "k8sconnect_wait" "webhook" {
  object_ref = k8sconnect_object.webhook_deployment.object_ref

  wait_for = {
    rollout = true
    timeout = "5m"
  }

  cluster_connection = local.cluster_connection
}

# Stage 3: Deploy CRD (references webhook for validation)
resource "k8sconnect_object" "widget_crd" {
  yaml_body = <<-YAML
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: widgets.example.com
    spec:
      group: example.com
      names:
        kind: Widget
        plural: widgets
      scope: Namespaced
      versions:
      - name: v1
        served: true
        storage: true
        schema:
          openAPIV3Schema:
            type: object
            properties:
              spec:
                type: object
                properties:
                  size:
                    type: integer
      conversion:
        strategy: Webhook
        webhook:
          clientConfig:
            service:
              namespace: webhook-system
              name: widget-webhook
              path: /convert
          conversionReviewVersions: ["v1"]
  YAML

  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_wait.webhook]
}

# Stage 4: Deploy custom resources
resource "k8sconnect_object" "widgets" {
  for_each = var.widgets

  yaml_body = <<-YAML
    apiVersion: example.com/v1
    kind: Widget
    metadata:
      name: ${each.key}
      namespace: default
    spec:
      size: ${each.value.size}
  YAML

  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.widget_crd]
}
```

**Critical:** Webhook must be ready before CRD is created, or CR creation will fail validation.

### Pattern 4: Operator with CRDs

**Use when:** Deploying an operator that defines CRDs.

```terraform
# Stage 1: Operator namespace and RBAC
resource "k8sconnect_object" "operator_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: cert-manager
  YAML

  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "operator_rbac" {
  for_each = {
    "serviceaccount" = file("${path.module}/cert-manager/serviceaccount.yaml")
    "clusterrole"    = file("${path.module}/cert-manager/clusterrole.yaml")
    "clusterrolebinding" = file("${path.module}/cert-manager/clusterrolebinding.yaml")
  }

  yaml_body          = each.value
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.operator_namespace]
}

# Stage 2: CRDs (cert-manager defines Certificate, Issuer, etc.)
data "k8sconnect_yaml_split" "crds" {
  content = file("${path.module}/cert-manager/crds.yaml")
}

resource "k8sconnect_object" "crds" {
  for_each = data.k8sconnect_yaml_split.crds.documents

  yaml_body          = each.value
  cluster_connection = local.cluster_connection
}

# Stage 3: Operator deployment
resource "k8sconnect_object" "operator" {
  yaml_body          = file("${path.module}/cert-manager/deployment.yaml")
  cluster_connection = local.cluster_connection

  depends_on = [
    k8sconnect_object.operator_rbac,
    k8sconnect_object.crds
  ]
}

# Stage 4: Wait for operator to be ready
resource "k8sconnect_wait" "operator" {
  object_ref = k8sconnect_object.operator.object_ref

  wait_for = {
    rollout = true
    timeout = "5m"
  }

  cluster_connection = local.cluster_connection
}

# Stage 5: Create custom resources (Certificate, Issuer, etc.)
resource "k8sconnect_object" "cluster_issuer" {
  yaml_body = <<-YAML
    apiVersion: cert-manager.io/v1
    kind: ClusterIssuer
    metadata:
      name: letsencrypt-prod
    spec:
      acme:
        server: https://acme-v02.api.letsencrypt.org/directory
        email: admin@example.com
        privateKeySecretRef:
          name: letsencrypt-prod
        solvers:
        - http01:
            ingress:
              class: nginx
  YAML

  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_wait.operator]
}
```

**Ordering:**
1. Namespace + RBAC
2. CRDs
3. Operator deployment
4. **Wait for operator to be ready** (critical!)
5. Custom resources

**Why wait for operator?** The operator controller must be running to reconcile custom resources.

### Pattern 5: Crossplane Provider with ManagedResources

**Use when:** Using Crossplane to provision cloud resources.

```terraform
# Stage 1: Crossplane core (namespace, CRDs, deployment)
data "k8sconnect_yaml_scoped" "crossplane" {
  content = file("${path.module}/crossplane-install.yaml")
}

resource "k8sconnect_object" "crossplane_crds" {
  for_each = data.k8sconnect_yaml_scoped.crossplane.crds

  yaml_body          = each.value
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "crossplane_system" {
  for_each = data.k8sconnect_yaml_scoped.crossplane.namespaced

  yaml_body          = each.value
  cluster_connection = local.cluster_connection

  depends_on = [k8sconnect_object.crossplane_crds]
}

# Stage 2: Install AWS provider (creates more CRDs)
resource "k8sconnect_object" "aws_provider" {
  yaml_body = <<-YAML
    apiVersion: pkg.crossplane.io/v1
    kind: Provider
    metadata:
      name: provider-aws
    spec:
      package: crossplane/provider-aws:v0.39.0
  YAML

  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.crossplane_system]
}

# Stage 3: Wait for AWS provider to install (installs RDS CRDs, etc.)
resource "k8sconnect_wait" "aws_provider" {
  object_ref = k8sconnect_object.aws_provider.object_ref

  wait_for = {
    condition = "Healthy"
    timeout   = "10m"
  }

  cluster_connection = local.cluster_connection
}

# Stage 4: Configure AWS provider credentials
resource "k8sconnect_object" "aws_provider_config" {
  yaml_body = <<-YAML
    apiVersion: aws.crossplane.io/v1beta1
    kind: ProviderConfig
    metadata:
      name: default
    spec:
      credentials:
        source: Secret
        secretRef:
          namespace: crossplane-system
          name: aws-creds
          key: credentials
  YAML

  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_wait.aws_provider]
}

# Stage 5: Create RDS instance (uses CRDs installed by AWS provider)
resource "k8sconnect_object" "rds" {
  yaml_body = <<-YAML
    apiVersion: database.aws.crossplane.io/v1beta1
    kind: RDSInstance
    metadata:
      name: production-db
    spec:
      forProvider:
        region: us-west-2
        dbInstanceClass: db.t3.medium
        engine: postgres
        engineVersion: "14"
        allocatedStorage: 100
        masterUsername: admin
      writeConnectionSecretToRef:
        namespace: production
        name: db-connection
  YAML

  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.aws_provider_config]
}
```

**Crossplane stages:**
1. Crossplane core + CRDs
2. Provider package (installs provider-specific CRDs)
3. **Wait for provider to be ready** (CRDs installed)
4. ProviderConfig
5. Managed resources (RDSInstance, S3Bucket, etc.)

## Understanding Auto-Retry Behavior

### When Auto-Retry Kicks In

```terraform
# This triggers auto-retry if CRD not ready:
resource "k8sconnect_object" "widget" {
  yaml_body = <<-YAML
    apiVersion: example.com/v1
    kind: Widget          # CRD might not exist yet
    metadata:
      name: my-widget
  YAML
}
```

**Error that triggers retry:**
```
Error from server (NotFound): unable to retrieve the complete list of
server APIs: example.com/v1: the server could not find the requested resource
```

**k8sconnect response:**
1. Detects "no matches for kind" error
2. Retries: 100ms → 500ms → 1s → 2s → 5s → 10s → 10s
3. Succeeds once CRD is registered (typically 2-5 seconds)

### Errors That Don't Retry

These fail immediately (no retry):

**Validation errors:**
```
Error: spec.size: Invalid value: "abc": spec.size in body must be of type integer
```

**Permission errors:**
```
Error: widgets.example.com is forbidden: User "system:serviceaccount:..."
cannot create resource "widgets" in API group "example.com"
```

**Invalid YAML:**
```
Error: error parsing YAML: yaml: line 5: could not find expected ':'
```

Only "CRD not found" errors trigger retry.

### Viewing Retry Logs

Enable debug logging to see retry attempts:

```bash
TF_LOG=DEBUG terraform apply
```

**Output:**
```
[DEBUG] k8sconnect: CRD not found, retrying (attempt 1/7): kind=Widget, apiVersion=example.com/v1
[DEBUG] k8sconnect: CRD not found, retrying (attempt 2/7): kind=Widget, apiVersion=example.com/v1
[DEBUG] k8sconnect: CRD ready, create succeeded: kind=Widget, apiVersion=example.com/v1
```

## Best Practices

### 1. Use Auto-Retry for Simple Cases

```terraform
# ✅ GOOD: Simple CRD + CR, let auto-retry handle it
resource "k8sconnect_object" "crd" {
  yaml_body = file("crd.yaml")
}

resource "k8sconnect_object" "cr" {
  yaml_body = file("cr.yaml")
  depends_on = [k8sconnect_object.crd]  # Optional but good practice
}
```

No need for `k8sconnect_yaml_scoped` or explicit waits for simple cases.

### 2. Use Scoped Ordering for Complex Deployments

```terraform
# ✅ GOOD: Large operator install with many CRDs
data "k8sconnect_yaml_scoped" "operator" {
  pattern = "./operator/**/*.yaml"
}

resource "k8sconnect_object" "crds" {
  for_each = data.k8sconnect_yaml_scoped.operator.crds
  yaml_body = each.value
}

resource "k8sconnect_object" "resources" {
  for_each = data.k8sconnect_yaml_scoped.operator.namespaced
  yaml_body = each.value
  depends_on = [k8sconnect_object.crds]
}
```

Makes staging explicit and easier to debug.

### 3. Wait for Operators Before Creating CRs

```terraform
# ✅ GOOD: Ensure operator is running before CRs
resource "k8sconnect_wait" "operator" {
  object_ref = k8sconnect_object.operator.object_ref
  wait_for   = { rollout = true, timeout = "5m" }
}

resource "k8sconnect_object" "custom_resource" {
  yaml_body  = file("cr.yaml")
  depends_on = [k8sconnect_wait.operator]
}
```

The operator must be running to reconcile the CR.

### 4. Don't Over-Engineer

```terraform
# ❌ BAD: Unnecessary wait for CRD (auto-retry handles this)
resource "k8sconnect_object" "crd" {
  yaml_body = file("crd.yaml")
}

resource "k8sconnect_wait" "crd" {  # Not needed!
  object_ref = k8sconnect_object.crd.object_ref
  wait_for   = { condition = "Established", timeout = "5m" }
}

resource "k8sconnect_object" "cr" {
  yaml_body  = file("cr.yaml")
  depends_on = [k8sconnect_wait.crd]
}

# ✅ GOOD: Let auto-retry handle it
resource "k8sconnect_object" "crd" {
  yaml_body = file("crd.yaml")
}

resource "k8sconnect_object" "cr" {
  yaml_body  = file("cr.yaml")
  depends_on = [k8sconnect_object.crd]
}
```

Auto-retry makes CRD waits unnecessary for most cases.

### 5. Handle Multi-Version CRDs Carefully

```terraform
# CRD with multiple versions
resource "k8sconnect_object" "widget_crd" {
  yaml_body = <<-YAML
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: widgets.example.com
    spec:
      group: example.com
      versions:
      - name: v1
        served: true
        storage: true    # v1 is storage version
        schema: { ... }
      - name: v2
        served: true
        storage: false
        schema: { ... }
  YAML
}

# Use storage version in CRs
resource "k8sconnect_object" "widget" {
  yaml_body = <<-YAML
    apiVersion: example.com/v1  # Use storage version
    kind: Widget
  YAML
  depends_on = [k8sconnect_object.widget_crd]
}
```

Always use the **storage version** (`storage: true`) in your CRs to avoid conversion issues.

## Troubleshooting

### CRD Still Not Found After 30s

**Problem:** Auto-retry exhausts all attempts, still fails with "no matches for kind".

**Possible causes:**
1. **CRD not actually created** - Check for errors in CRD resource
2. **API server restart** - Rare, but API server may be restarting
3. **Webhook blocking CRD creation** - Validating webhook rejecting CRD

**Debug steps:**
```bash
# Check if CRD exists
kubectl get crd widgets.example.com

# Check CRD status
kubectl get crd widgets.example.com -o yaml | grep -A 10 status:

# Look for "Established" condition
kubectl get crd widgets.example.com -o jsonpath='{.status.conditions[?(@.type=="Established")].status}'
# Should output: True
```

**Fix:** Check CRD definition for errors, ensure it's actually being created.

### CR Validation Fails

**Problem:** CRD exists, but CR creation fails validation.

```
Error: Widget.example.com "my-widget" is invalid:
spec.size: Invalid value: "abc": spec.size in body must be of type integer
```

**This is NOT a retry error** - it's a validation error in your CR.

**Fix:** Correct the CR spec to match the CRD schema.

### Operator Not Reconciling CR

**Problem:** CR is created successfully, but operator doesn't process it.

**Likely cause:** Operator wasn't running when CR was created.

**Fix:** Add a wait for operator deployment:

```terraform
resource "k8sconnect_wait" "operator" {
  object_ref = k8sconnect_object.operator.object_ref
  wait_for   = { rollout = true, timeout = "5m" }
}

resource "k8sconnect_object" "cr" {
  depends_on = [k8sconnect_wait.operator]
}
```

### Webhook Conversion Failures

**Problem:** Multi-version CRD with webhook conversion fails.

```
Error: conversion webhook for example.com/v1, Kind=Widget failed:
Post "https://webhook.default.svc:443/convert": dial tcp: lookup webhook.default.svc: no such host
```

**Cause:** Conversion webhook service not ready.

**Fix:** Wait for webhook deployment before creating CRD:

```terraform
resource "k8sconnect_wait" "webhook" {
  object_ref = k8sconnect_object.webhook.object_ref
  wait_for   = { rollout = true }
}

resource "k8sconnect_object" "crd_with_webhook" {
  depends_on = [k8sconnect_wait.webhook]
}
```

## Migration from Other Providers

### From hashicorp/kubernetes

**Old (two-phase):**
```terraform
# First apply: Create CRDs
resource "kubernetes_manifest" "widget_crd" {
  manifest = yamldecode(file("crd.yaml"))
}

# Second apply: Create CRs (after CRDs exist)
resource "kubernetes_manifest" "widget" {
  manifest = yamldecode(file("cr.yaml"))
  depends_on = [kubernetes_manifest.widget_crd]
}
```

**New (single apply):**
```terraform
resource "k8sconnect_object" "widget_crd" {
  yaml_body = file("crd.yaml")
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "widget" {
  yaml_body = file("cr.yaml")
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_object.widget_crd]
}
```

**Benefit:** Single `terraform apply` works. No more two-phase deployments.

### From gavinbunney/kubectl

**Old (configuration required):**
```terraform
resource "kubectl_manifest" "widget_crd" {
  yaml_body = file("crd.yaml")
}

resource "kubectl_manifest" "widget" {
  yaml_body          = file("cr.yaml")
  apply_retry_count = 10  # Still often needs two applies
  depends_on         = [kubectl_manifest.widget_crd]
}
```

**New (zero config):**
```terraform
resource "k8sconnect_object" "widget_crd" {
  yaml_body = file("crd.yaml")
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "widget" {
  yaml_body = file("cr.yaml")
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_object.widget_crd]
}
```

**Benefit:** No `apply_retry_count` configuration needed. Just works.

## Related Resources

- [k8sconnect_object resource](../resources/object.md) - Full reference
- [k8sconnect_yaml_scoped data source](../data-sources/yaml_scoped.md) - Scoped ordering
- [ADR-007: CRD Dependency Resolution](../ADRs/ADR-007-crd-dependency-resolution.md) - Technical details
- [Wait Strategies guide](wait-strategies.md) - Waiting for operator readiness

## Summary

**Key takeaways:**

1. **Auto-retry solves CRD + CR** - Zero configuration, single apply works

2. **Two approaches:**
   - Simple cases: Use auto-retry (no extra config)
   - Complex cases: Use `k8sconnect_yaml_scoped` for explicit staging

3. **Common patterns:**
   - Simple CRD + CR: Let auto-retry handle it
   - Operator deployment: CRDs → Operator → Wait → CRs
   - Crossplane: Core → Provider → Wait → ProviderConfig → Managed resources

4. **Don't over-engineer:**
   - No need to wait for CRD "Established" condition
   - Auto-retry handles the race condition automatically
   - Only wait for operator deployments (they must be running)

5. **It just works:**
   - Single `terraform apply` succeeds
   - Faster than competition (2-5s typically)
   - Clear errors when genuinely broken

**The 3-year problem is solved. Deploy CRDs and CRs together. It just works.**
