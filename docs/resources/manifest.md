---
page_title: "Resource k8sconnect_manifest - terraform-provider-k8sconnect"
subcategory: ""
description: |-
  Applies a single‑document Kubernetes YAML manifest to a cluster, with per‑resource inline or kubeconfig‑based connection settings.
---

# Resource: k8sconnect_manifest

Applies a single‑document Kubernetes YAML manifest to a cluster, with per‑resource inline or kubeconfig‑based connection settings.

## Example Usage - Basic Deployment

```terraform
resource "k8sconnect_manifest" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster_connection = var.cluster_connection
}

resource "k8sconnect_manifest" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: nginx
      namespace: example
    spec:
      replicas: 2
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
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace]
}
```

## Example Usage - Bootstrap EKS Cluster with Workloads

Single terraform apply to create cluster and deploy workloads:

```terraform
resource "aws_eks_cluster" "main" {
  name     = "my-cluster"
  role_arn = aws_iam_role.cluster.arn
  # ... cluster configuration
}

resource "k8sconnect_manifest" "cert_manager" {
  yaml_body = file("cert-manager.yaml")

  cluster_connection = {
    host                   = aws_eks_cluster.main.endpoint
    cluster_ca_certificate = base64decode(aws_eks_cluster.main.certificate_authority[0].data)
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", aws_eks_cluster.main.name]
    }
  }
}
```

## Example Usage - Coexisting with HPA

Use `ignore_fields` to let the HorizontalPodAutoscaler manage replicas:

```terraform
resource "k8sconnect_manifest" "app" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: nginx-with-hpa
      namespace: example
    spec:
      replicas: 2
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

  # Ignore spec.replicas because HPA will modify it
  ignore_fields = ["spec.replicas"]

  cluster_connection = var.cluster_connection
}

resource "k8sconnect_manifest" "hpa" {
  yaml_body = <<-YAML
    apiVersion: autoscaling/v2
    kind: HorizontalPodAutoscaler
    metadata:
      name: nginx-hpa
      namespace: example
    spec:
      scaleTargetRef:
        apiVersion: apps/v1
        kind: Deployment
        name: nginx-with-hpa
      minReplicas: 1
      maxReplicas: 10
      metrics:
      - type: Resource
        resource:
          name: cpu
          target:
            type: Utilization
            averageUtilization: 50
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.app]
}
```

## Example Usage - Wait for LoadBalancer and Use Status (field wait)

Wait for a LoadBalancer to be provisioned and use its IP in other resources. **Only `field` waits populate `.status` for chaining.**

```terraform
resource "k8sconnect_manifest" "loadbalancer_service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: demo-lb
      namespace: example
    spec:
      type: LoadBalancer
      ports:
      - port: 80
        targetPort: 8080
      selector:
        app: demo
  YAML

  wait_for = {
    field   = "status.loadBalancer.ingress"  # Enables .status output
    timeout = "5m"
  }

  cluster_connection = var.cluster_connection
}

# Use the LoadBalancer IP in another resource
resource "k8sconnect_manifest" "endpoint_config" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: external-endpoints
      namespace: example
    data:
      service_endpoint: "${k8sconnect_manifest.loadbalancer_service.status.loadBalancer.ingress[0].ip}:80"
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.loadbalancer_service]
}
```

## Example Usage - Wait for Deployment Rollout (rollout wait)

Wait for a Deployment to fully roll out before continuing:

```terraform
resource "k8sconnect_manifest" "app" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: web-app
      namespace: example
    spec:
      replicas: 3
      selector:
        matchLabels:
          app: web
      template:
        metadata:
          labels:
            app: web
        spec:
          containers:
          - name: nginx
            image: nginx:1.21
            resources:
              requests:
                cpu: 100m
                memory: 128Mi
  YAML

  # Wait for all replicas to be updated and ready
  wait_for = {
    rollout = true
    timeout = "5m"
  }

  cluster_connection = var.cluster_connection
}
```

## Example Usage - Wait for Condition (condition wait)

Wait for a Kubernetes condition to be True:

```terraform
resource "k8sconnect_manifest" "app" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: storage-app
      namespace: example
    spec:
      replicas: 1
      selector:
        matchLabels:
          app: storage
      template:
        metadata:
          labels:
            app: storage
        spec:
          containers:
          - name: app
            image: busybox:latest
            command: ["sh", "-c", "while true; do date; sleep 30; done"]
  YAML

  # Wait for "Available" condition (minimum availability reached)
  wait_for = {
    condition = "Available"
    timeout   = "3m"
  }

  cluster_connection = var.cluster_connection
}
```

## Example Usage - Wait for Field Value (field_value wait)

Wait for specific field values:

```terraform
resource "k8sconnect_manifest" "migration_job" {
  yaml_body = <<-YAML
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: database-migration
      namespace: example
    spec:
      template:
        spec:
          containers:
          - name: migrate
            image: busybox:latest
            command: ["sh", "-c", "echo 'Running migrations...' && sleep 5"]
          restartPolicy: Never
  YAML

  # Wait for exactly 1 successful completion
  wait_for = {
    field_value = {
      "status.succeeded" = "1"
    }
    timeout = "5m"
  }

  cluster_connection = var.cluster_connection
}
```

## Example Usage - Multi-Cluster Deployment

Deploy the same resource to multiple clusters:

```terraform
locals {
  prod_connection = {
    host                   = aws_eks_cluster.prod.endpoint
    cluster_ca_certificate = base64decode(aws_eks_cluster.prod.certificate_authority[0].data)
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", "prod"]
    }
  }

  staging_connection = {
    kubeconfig = file("~/.kube/staging-config")
    context    = "staging"
  }
}

resource "k8sconnect_manifest" "prod_app" {
  yaml_body          = file("app.yaml")
  cluster_connection = local.prod_connection
}

resource "k8sconnect_manifest" "staging_app" {
  yaml_body          = file("app.yaml")
  cluster_connection = local.staging_connection
}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `cluster_connection` (Attributes) Kubernetes cluster connection for this specific resource. Can be different per-resource, enabling multi-cluster deployments without provider aliases. Supports inline credentials (token, exec, client certs) or kubeconfig. (see [below for nested schema](#nestedatt--cluster_connection))
- `yaml_body` (String) UTF-8 encoded, single-document Kubernetes YAML. Multi-doc files will fail validation.

### Optional

- `delete_protection` (Boolean) Prevent accidental deletion of the resource. If set to true, the resource cannot be deleted unless this field is set to false.
- `delete_timeout` (String) How long to wait for a resource to be deleted before considering the deletion failed. Defaults to 300s (5 minutes).
- `force_destroy` (Boolean) Force deletion by removing finalizers. **WARNING:** Unlike other providers, this REMOVES finalizers after timeout. May cause data loss and orphaned cloud resources. Consult documentation before enabling.
- `ignore_fields` (List of String) Field paths to exclude from management. On Create, fields are sent to establish initial state; on Update, they're omitted from the Apply patch, releasing ownership to other controllers and excluding them from drift detection. Supports dot notation (e.g., 'metadata.annotations', 'spec.replicas'), array indices ('webhooks[0].clientConfig.caBundle'), and strategic merge keys ('spec.containers[name=nginx].image'). Use for fields managed by controllers (e.g., HPA modifying replicas) or when operators inject values.
- `wait_for` (Attributes) Wait for resource to reach desired state during apply. Automatically enables status tracking. (see [below for nested schema](#nestedatt--wait_for))

### Read-Only

- `field_ownership` (Map of String) Tracks which controller owns each managed field using Server-Side Apply field management. Shows as a map of 'field.path': 'controller-name'. Only appears in plan diffs when ownership actually changes (e.g., when HPA takes ownership of spec.replicas). Empty/hidden when ownership is unchanged. Critical for understanding SSA conflicts and knowing which controller controls what.
- `id` (String) Unique identifier for this manifest (generated by the provider).
- `managed_state_projection` (Map of String) Field-by-field snapshot of managed state as flat key-value pairs with dotted paths. Shows exactly which fields k8sconnect manages and their current values. Terraform automatically displays only changed keys in diffs for clean, scannable output. When this differs from current cluster state, it indicates drift - someone modified your managed fields outside Terraform. Computed via Server-Side Apply dry-run for accuracy, enabling precise drift detection without false positives.
- `status` (Dynamic) Resource status from the cluster, populated only when using wait_for with field='status.path'. Contains resource-specific runtime information like LoadBalancer IPs, Pod conditions, Deployment replicas. Follows the principle: 'You get only what you wait for' to avoid storing volatile status fields that cause drift. Returns null when wait_for is not configured or uses non-field wait types.

<a id="nestedatt--cluster_connection"></a>
### Nested Schema for `cluster_connection`

Optional:

- `client_certificate` (String, Sensitive) Client certificate for TLS authentication. Accepts PEM format or base64-encoded PEM - automatically detected.
- `client_key` (String, Sensitive) Client certificate key for TLS authentication. Accepts PEM format or base64-encoded PEM - automatically detected.
- `cluster_ca_certificate` (String, Sensitive) Root certificate bundle for TLS authentication. Accepts PEM format or base64-encoded PEM - automatically detected.
- `context` (String) Context to use from the kubeconfig. Optional when kubeconfig contains exactly one context (that context will be used automatically). Required when kubeconfig contains multiple contexts to prevent accidental connection to the wrong cluster. Error will list available contexts if not specified when required.
- `exec` (Attributes, Sensitive) Configuration for exec-based authentication. (see [below for nested schema](#nestedatt--cluster_connection--exec))
- `host` (String) The hostname (in form of URI) of the Kubernetes API server.
- `insecure` (Boolean) Whether server should be accessed without verifying the TLS certificate.
- `kubeconfig` (String, Sensitive) Raw kubeconfig file content.
- `proxy_url` (String) URL of the proxy to use for requests.
- `token` (String, Sensitive) Token to authenticate to the Kubernetes API server.

<a id="nestedatt--cluster_connection--exec"></a>
### Nested Schema for `cluster_connection.exec`

Required:

- `api_version` (String) API version to use when encoding the ExecCredentials resource.
- `command` (String) Command to execute.

Optional:

- `args` (List of String) Arguments to pass when executing the plugin.
- `env` (Map of String) Environment variables to set when executing the plugin.



<a id="nestedatt--wait_for"></a>
### Nested Schema for `wait_for`

Optional:

- `condition` (String) Condition type that must be True. Example: 'Ready'
- `field` (String) JSONPath to field that must exist/be non-empty. Example: 'status.loadBalancer.ingress'
- `field_value` (Map of String) Map of JSONPath to expected value. Example: {'status.phase': 'Running'}
- `rollout` (Boolean) Wait for Deployment/StatefulSet/DaemonSet to complete rollout before considering the resource ready. Optional - rollout waiting is not automatic. Checks that all replicas are updated and available.
- `timeout` (String) Maximum time to wait. Defaults to 10m. Format: '30s', '5m', '1h'
