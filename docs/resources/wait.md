---
page_title: "Resource k8sconnect_wait - terraform-provider-k8sconnect"
subcategory: ""
description: |-
  Waits for a Kubernetes resource to reach a desired state. Use this resource to wait for resources created by k8sconnect_object without risking resource tainting on timeout. Follows the pattern: create resource -> wait for readiness -> use outputs.
---

# Resource: k8sconnect_wait

Waits for a Kubernetes resource to reach a desired state. Use this resource to wait for resources created by k8sconnect_object without risking resource tainting on timeout. Follows the pattern: create resource -> wait for readiness -> use outputs.

The `k8sconnect_wait` resource waits for Kubernetes resources to reach a desired state. It references a `k8sconnect_object` resource via `object_ref` and blocks until the specified wait condition is met.

## Wait Strategies

Choose the right wait strategy based on your use case:

### Field Wait (`field`)
**Use for**: Infrastructure resources that need status values for DNS, outputs, or resource chaining
- LoadBalancer Services, Ingress, cert-manager Certificates, Crossplane resources, Custom CRDs
- **Populates `.result` attribute** for use in other resources
- Only the waited-for field is extracted to prevent drift from volatile fields

### Rollout Wait (`rollout`)
**Use for**: Workloads that need complete deployment confirmation
- Deployments, StatefulSets, DaemonSets
- **Does NOT populate `.result`** - use `depends_on` for sequencing
- Checks replicas, updatedReplicas, readyReplicas, and observedGeneration

### Condition Wait (`condition`)
**Use for**: Resources with Kubernetes conditions (Ready, Available, etc.)
- Deployments (Available, Progressing), Custom CRDs with conditions
- **Does NOT populate `.result`** - use `depends_on` for sequencing
- Waits for condition status to be "True"

### Field Value Wait (`field_value`)
**Use for**: Waiting for specific field values (Job completion, PVC binding, etc.)
- Jobs (status.succeeded), PVCs (status.phase)
- **Does NOT populate `.result`** - use `depends_on` for sequencing
- Checks exact string match for field values

## Example Usage - Wait for LoadBalancer (field wait)

Wait for a LoadBalancer to be provisioned and use its IP in other resources.

<!-- runnable-test: wait-loadbalancer -->
```terraform
resource "k8sconnect_object" "service" {
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

  cluster = local.cluster
}

resource "k8sconnect_wait" "service" {
  object_ref = k8sconnect_object.service.object_ref

  wait_for = {
    field   = "status.loadBalancer.ingress"
    timeout = "5m"
  }

  cluster = local.cluster
}

# Use the LoadBalancer IP in another resource
resource "k8sconnect_object" "endpoint_config" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: external-endpoints
      namespace: example
    data:
      service_endpoint: "${k8sconnect_wait.service.result.status.loadBalancer.ingress[0].ip}:80"
  YAML

  cluster = local.cluster
  depends_on         = [k8sconnect_wait.service]
}
```
<!-- /runnable-test -->

## Example Usage - Wait for Deployment Rollout (rollout wait)

Wait for a Deployment to fully roll out before continuing.

<!-- runnable-test: wait-rollout -->
```terraform
resource "k8sconnect_object" "app" {
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

  cluster = local.cluster
}

# Wait for all replicas to be updated and ready
resource "k8sconnect_wait" "app" {
  object_ref = k8sconnect_object.app.object_ref

  wait_for = {
    rollout = true
    timeout = "5m"
  }

  cluster = local.cluster
}

# Deploy service only after deployment is fully rolled out
resource "k8sconnect_object" "service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: web-svc
      namespace: example
    spec:
      type: ClusterIP
      ports:
      - port: 80
        targetPort: 80
      selector:
        app: web
  YAML

  cluster = local.cluster
  depends_on         = [k8sconnect_wait.app]
}
```
<!-- /runnable-test -->

## Example Usage - Wait for Condition (condition wait)

Wait for a Kubernetes condition to be True.

<!-- runnable-test: wait-condition -->
```terraform
resource "k8sconnect_object" "storage_app" {
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
            image: public.ecr.aws/docker/library/busybox:latest
            command: ["sh", "-c", "while true; do date; sleep 30; done"]
            resources:
              requests:
                cpu: 50m
                memory: 64Mi
  YAML

  cluster = local.cluster
}

# Wait for "Available" condition (minimum availability reached)
resource "k8sconnect_wait" "storage_app" {
  object_ref = k8sconnect_object.storage_app.object_ref

  wait_for = {
    condition = "Available"
    timeout   = "3m"
  }

  cluster = local.cluster
}
```
<!-- /runnable-test -->

## Example Usage - Wait for Field Value (field_value wait)

Wait for specific field values (e.g., Job completion).

<!-- runnable-test: wait-field-value -->
```terraform
resource "k8sconnect_object" "migration_job" {
  yaml_body = <<-YAML
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: database-migration
      namespace: example
    spec:
      backoffLimit: 1
      completions: 1
      template:
        spec:
          containers:
          - name: migrate
            image: public.ecr.aws/docker/library/busybox:latest
            command: ["sh", "-c", "echo 'Running migrations...' && sleep 5"]
          restartPolicy: Never
  YAML

  cluster = local.cluster
}

# Wait for exactly 1 successful completion
resource "k8sconnect_wait" "migration_job" {
  object_ref = k8sconnect_object.migration_job.object_ref

  wait_for = {
    field_value = {
      "status.succeeded" = "1"
    }
    timeout = "2m"
  }

  cluster = local.cluster
}

# Deploy app only after migrations complete
resource "k8sconnect_object" "app_deployment" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: app-config
      namespace: example
    data:
      database_ready: "true"
      migrations_complete: "true"
  YAML

  cluster = local.cluster
  depends_on         = [k8sconnect_wait.migration_job]
}
```
<!-- /runnable-test -->

## Example Usage - Wait for PVC Binding (field_value wait)

Wait for a PersistentVolumeClaim to be bound to a PersistentVolume.

<!-- runnable-test: wait-pvc -->
```terraform
resource "k8sconnect_object" "pv" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolume
    metadata:
      name: example-pv
    spec:
      capacity:
        storage: 1Gi
      accessModes:
        - ReadWriteOnce
      persistentVolumeReclaimPolicy: Delete
      storageClassName: manual
      hostPath:
        path: /tmp/example-pv
  YAML

  cluster = local.cluster
}

resource "k8sconnect_object" "pvc" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: data-claim
      namespace: example
    spec:
      accessModes:
        - ReadWriteOnce
      storageClassName: manual
      resources:
        requests:
          storage: 1Gi
  YAML

  cluster = local.cluster
  depends_on         = [k8sconnect_object.pv]
}

# Wait for PVC to be bound
resource "k8sconnect_wait" "pvc" {
  object_ref = k8sconnect_object.pvc.object_ref

  wait_for = {
    field_value = {
      "status.phase" = "Bound"
    }
    timeout = "2m"
  }

  cluster = local.cluster
}

# Create deployment that uses the PVC - only after it's bound
resource "k8sconnect_object" "app" {
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
            image: public.ecr.aws/docker/library/busybox:latest
            command: ["sh", "-c", "while true; do date >> /data/log.txt; sleep 30; done"]
            volumeMounts:
            - name: data
              mountPath: /data
            resources:
              requests:
                cpu: 50m
                memory: 64Mi
          volumes:
          - name: data
            persistentVolumeClaim:
              claimName: data-claim
  YAML

  cluster = local.cluster
  depends_on         = [k8sconnect_wait.pvc]
}
```
<!-- /runnable-test -->

## Example Usage - Wait for External Resources (standalone)

Use `k8sconnect_wait` to wait for resources you **don't manage with Terraform** - no `k8sconnect_object` needed.

<!-- runnable-test: wait-external-resources -->
```terraform
# Wait for metrics-server (installed via Helm, kubeadm, etc.)
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

  cluster = local.cluster
}

# Wait for CoreDNS (installed by cluster bootstrap)
resource "k8sconnect_wait" "coredns" {
  object_ref = {
    api_version = "apps/v1"
    kind        = "Deployment"
    namespace   = "kube-system"
    name        = "coredns"
  }

  wait_for = {
    condition = "Available"
    timeout   = "5m"
  }

  cluster = local.cluster
}

output "cluster_ready" {
  value = "Cluster infrastructure is ready"
  depends_on = [
    k8sconnect_wait.metrics_server,
    k8sconnect_wait.coredns
  ]
}
```
<!-- /runnable-test -->

**Use cases:**
- Wait for Helm-installed charts (cert-manager, ingress controllers, etc.)
- Wait for operator-managed resources
- Wait for resources created by kubectl or other tools
- Wait for infrastructure from other Terraform states
- Sequence your resources against existing cluster infrastructure

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `cluster` (Attributes) Kubernetes cluster connection for accessing the resource. Should match the connection used by the k8sconnect_object resource. (see [below for nested schema](#nestedatt--cluster))
- `object_ref` (Attributes) Reference to the Kubernetes object to wait for. Typically populated from k8sconnect_object.resource_name.object_ref output. (see [below for nested schema](#nestedatt--object_ref))
- `wait_for` (Attributes) Conditions to wait for before considering the resource ready. (see [below for nested schema](#nestedatt--wait_for))

### Read-Only

- `id` (String) Unique identifier for this wait operation (generated by the provider).
- `result` (Dynamic) Result of the wait operation containing extracted fields from the Kubernetes resource. The structure preserves the full path from the resource (e.g., field='spec.volumeName' → result.spec.volumeName). Follows ADR-008: 'You get only what you wait for' - only populated for field waits, null for condition/rollout waits.

<a id="nestedatt--cluster"></a>
### Nested Schema for `cluster`

Optional:

- `client_certificate` (String, Sensitive) Client certificate for TLS authentication. Accepts PEM format or base64-encoded PEM - automatically detected.
- `client_key` (String, Sensitive) Client certificate key for TLS authentication. Accepts PEM format or base64-encoded PEM - automatically detected.
- `cluster_ca_certificate` (String, Sensitive) Root certificate bundle for TLS authentication. Accepts PEM format or base64-encoded PEM - automatically detected.
- `context` (String) Context to use from the kubeconfig. Optional when kubeconfig contains exactly one context (that context will be used automatically). Required when kubeconfig contains multiple contexts to prevent accidental connection to the wrong cluster. Error will list available contexts if not specified when required.
- `exec` (Attributes, Sensitive) Configuration for exec-based authentication. (see [below for nested schema](#nestedatt--cluster--exec))
- `host` (String) The hostname (in form of URI) of the Kubernetes API server.
- `insecure` (Boolean) Whether server should be accessed without verifying the TLS certificate.
- `kubeconfig` (String, Sensitive) Raw kubeconfig file content.
- `proxy_url` (String) URL of the proxy to use for requests.
- `token` (String, Sensitive) Token to authenticate to the Kubernetes API server.

<a id="nestedatt--cluster--exec"></a>
### Nested Schema for `cluster.exec`

Required:

- `api_version` (String) API version to use when encoding the ExecCredentials resource.
- `command` (String) Command to execute.

Optional:

- `args` (List of String) Arguments to pass when executing the plugin.
- `env` (Map of String) Environment variables to set when executing the plugin.



<a id="nestedatt--object_ref"></a>
### Nested Schema for `object_ref`

Required:

- `api_version` (String) Kubernetes API version (e.g., 'v1', 'apps/v1')
- `kind` (String) Kubernetes resource kind (e.g., 'Pod', 'Deployment')
- `name` (String) Resource name

Optional:

- `namespace` (String) Resource namespace. Omit for cluster-scoped resources.


<a id="nestedatt--wait_for"></a>
### Nested Schema for `wait_for`

Optional:

- `condition` (String) Condition type that must be True. Example: 'Ready'
- `field` (String) JSONPath to field that must exist/be non-empty. Example: 'status.loadBalancer.ingress'
- `field_value` (Map of String) Map of JSONPath to expected value. Example: {'status.phase': 'Running'}
- `rollout` (Boolean) Wait for Deployment/StatefulSet/DaemonSet to complete rollout. Checks that all replicas are updated and available.
- `timeout` (String) Maximum time to wait. Defaults to 10m. Format: '30s', '5m', '1h'

## Result Output

Only **field waits** populate the `result` attribute. The result contains only the waited-for field to prevent drift from volatile or controller-managed fields.

**Example:**
```terraform
resource "k8sconnect_wait" "service" {
  object_ref = k8sconnect_object.service.object_ref

  wait_for = {
    field = "status.loadBalancer.ingress"
  }

  cluster = local.cluster
}

# Access the extracted field from result
output "loadbalancer_ip" {
  value = k8sconnect_wait.service.result.status.loadBalancer.ingress[0].ip
}
```

**Other wait types** (`rollout`, `condition`, `field_value`) do NOT populate result. Use `depends_on` to sequence resources:

```terraform
resource "k8sconnect_wait" "app" {
  object_ref = k8sconnect_object.app.object_ref

  wait_for = {
    rollout = true
  }

  cluster = local.cluster
}

resource "k8sconnect_object" "next" {
  # ... config ...
  depends_on = [k8sconnect_wait.app]
}
```

## Timeouts

All wait operations support configurable timeouts. The default timeout is 10 minutes if not specified.

```terraform
wait_for = {
  field   = "status.loadBalancer.ingress"
  timeout = "10m"  # Options: "30s", "5m", "1h"
}
```

## JSONPath Syntax

The `field` and `field_value` attributes use **JSONPath** syntax (same as `kubectl get -o jsonpath`):

```hcl
# Simple paths
field = "status.phase"
field = "status.loadBalancer.ingress"

# Positional arrays
field = "status.conditions[0].type"
field = "status.containerStatuses[0].ready"

# JSONPath predicates (select by field value)
field = "status.conditions[?(@.type=='Ready')].status"
field_value = {
  "status.conditions[?(@.type=='Ready')].status" = "True"
}
```

**Common wait patterns:**
- LoadBalancer IP: `status.loadBalancer.ingress[0].ip`
- PVC volume name: `spec.volumeName`
- Job completion: `status.succeeded`
- Pod phase: `status.phase`
- Condition status: `status.conditions[?(@.type=='Ready')].status`
