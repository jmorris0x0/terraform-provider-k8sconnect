---
page_title: "Resource k8sconnect_patch - terraform-provider-k8sconnect"
subcategory: ""
description: |-
  Applies targeted patches to existing Kubernetes resources using Server-Side Apply.
  IMPORTANT: This resource forcefully takes ownership of fields from other controllers.
  Appropriate use cases:
  Cloud provider system resources (AWS EKS, GCP GKE, Azure AKS defaults)Operator-managed resources (cert-manager, nginx-ingress, etc.)Helm chart deployments requiring customizationResources created and managed by other tools
  NOT appropriate for:
  Resources managed by k8sconnect_object in the same stateResources requiring full lifecycle controlResources you could manage with k8sconnect_object instead
  Destroy behavior:
  When you destroy a patch resource, ownership is released but patched values remain on the target resource. Values are not reverted to their original state.
---

# Resource: k8sconnect_patch

Applies targeted patches to existing Kubernetes resources using Server-Side Apply.

**IMPORTANT:** This resource forcefully takes ownership of fields from other controllers.

**Appropriate use cases:**
- Cloud provider system resources (AWS EKS, GCP GKE, Azure AKS defaults)
- Operator-managed resources (cert-manager, nginx-ingress, etc.)
- Helm chart deployments requiring customization
- Resources created and managed by other tools

**NOT appropriate for:**
- Resources managed by k8sconnect_object in the same state
- Resources requiring full lifecycle control
- Resources you could manage with k8sconnect_object instead

**Destroy behavior:**
When you destroy a patch resource, ownership is released but patched values remain on the target resource. Values are not reverted to their original state.

## Example Usage - Strategic Merge Patch

Strategic Merge Patch uses Kubernetes-native merge semantics with array merge keys. This is the recommended patch type for most use cases as it supports Server-Side Apply with field ownership tracking and accurate dry-run projections.

<!-- runnable-test: patch-strategic-merge -->
```terraform
resource "k8sconnect_patch" "coredns_label" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "coredns"
    namespace   = "kube-system"
  }

  patch = jsonencode({
    metadata = {
      labels = {
        "example.com/managed-by" = "terraform"
      }
    }
  })

  cluster_connection = var.cluster_connection
}
```
<!-- /runnable-test -->

## Example Usage - JSON Patch (RFC 6902)

JSON Patch provides precise control with operations like `add`, `remove`, `replace`, `move`, `copy`, and `test`. Use this when you need exact control over array elements or want to perform conditional operations.

<!-- runnable-test: patch-json-patch -->
```terraform
resource "k8sconnect_patch" "kubernetes_svc_label" {
  target = {
    api_version = "v1"
    kind        = "Service"
    name        = "kubernetes"
    namespace   = "default"
  }

  # Note: Use "~1" to escape "/" in JSON Pointer paths
  json_patch = jsonencode([
    {
      op    = "add"
      path  = "/metadata/labels/example.com~1patched-by"
      value = "terraform-json-patch"
    }
  ])

  cluster_connection = var.cluster_connection
}
```
<!-- /runnable-test -->

## Example Usage - Merge Patch (RFC 7386)

Merge Patch is the simplest patch type - just specify the fields to merge. Note that it replaces entire arrays rather than merging them.

<!-- runnable-test: patch-merge-patch -->
```terraform
resource "k8sconnect_patch" "kube_dns_annotation" {
  target = {
    api_version = "v1"
    kind        = "Service"
    name        = "kube-dns"
    namespace   = "kube-system"
  }

  merge_patch = jsonencode({
    metadata = {
      annotations = {
        "example.com/managed-by" = "terraform"
        "example.com/patch-type" = "merge-patch"
      }
    }
  })

  cluster_connection = var.cluster_connection
}
```
<!-- /runnable-test -->

## Example Usage - Patching EKS AWS Node DaemonSet

A common real-world use case is modifying cloud provider system resources:

```terraform
resource "k8sconnect_patch" "aws_node_env" {
  target = {
    api_version = "apps/v1"
    kind        = "DaemonSet"
    name        = "aws-node"
    namespace   = "kube-system"
  }

  patch = yamlencode({
    spec = {
      template = {
        spec = {
          containers = [{
            name = "aws-node"
            env = [{
              name  = "ENABLE_PREFIX_DELEGATION"
              value = "true"
            }]
          }]
        }
      }
    }
  })


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

## Choosing a Patch Type

| Patch Type          | When to Use                                                                 | Pros                                                     | Cons                                      |
|---------------------|-----------------------------------------------------------------------------|----------------------------------------------------------|-------------------------------------------|
| Strategic Merge     | Most use cases, especially with arrays of objects                           | SSA field ownership, dry-run projections, merge keys     | Only works with resources that have merge strategies |
| JSON Patch          | Precise array operations, conditional changes, when you need exact control  | Explicit operations, works with any resource             | No SSA, no dry-run, more verbose          |
| Merge Patch         | Simple field updates, resources without strategic merge support             | Simplest syntax, works with any resource                 | No SSA, no dry-run, replaces entire arrays|

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `cluster_connection` (Attributes) Kubernetes cluster connection for this specific patch. Can be different per-resource, enabling multi-cluster deployments without provider aliases. Supports inline credentials (token, exec, client certs) or kubeconfig. (see [below for nested schema](#nestedatt--cluster_connection))
- `target` (Attributes) Identifies the Kubernetes resource to patch. The resource must already exist. Changes to target require replacement. (see [below for nested schema](#nestedatt--target))

### Optional

- `json_patch` (String) JSON Patch (RFC 6902) operations as JSON array. Use for precise operations like adding/removing specific array elements. Example: `[{"op":"add","path":"/metadata/labels/foo","value":"bar"}]`.
- `merge_patch` (String) JSON Merge Patch (RFC 7386) content. Simple key-value merges, replaces entire arrays. Least powerful but simplest patch type.
- `patch` (String) Strategic merge patch content (YAML or JSON). This is the recommended patch type for most use cases. Uses Kubernetes strategic merge semantics with merge keys for arrays.

### Read-Only

- `field_ownership` (Map of String) Map of field paths to their current owner (field manager). Shows which controller owns each patched field. After patch application, patched fields should show this patch's field manager as owner.
- `id` (String) Unique identifier for this patch (generated by the provider).
- `managed_fields` (String) JSON representation of only the fields managed by this patch. Used for drift detection.
- `managed_state_projection` (Map of String) Flattened projection of fields that will be patched. Shows the predicted state after patch application, including any Kubernetes defaults. Only available for strategic merge patches (SSA). Non-SSA patches (json_patch, merge_patch) do not provide projection.
- `previous_owners` (Map of String) Map of field paths to their owners BEFORE this patch was applied. Useful for understanding which controllers were managing fields before takeover. Only populated during initial patch creation.

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



<a id="nestedatt--target"></a>
### Nested Schema for `target`

Required:

- `api_version` (String) API version of the target resource (e.g., 'apps/v1', 'v1'). Changes require replacement.
- `kind` (String) Kind of the target resource (e.g., 'DaemonSet', 'Deployment'). Changes require replacement.
- `name` (String) Name of the target resource. Changes require replacement.

Optional:

- `namespace` (String) Namespace of the target resource. Omit for cluster-scoped resources. Changes require replacement.
