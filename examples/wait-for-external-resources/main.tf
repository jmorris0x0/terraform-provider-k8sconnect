# examples/wait-for-external-resources/main.tf
#
# This example demonstrates using k8sconnect_wait STANDALONE to wait for
# resources you don't manage with Terraform. No k8sconnect_object needed.
#
# The wait resource works with ANY Kubernetes resource regardless of how it
# was created: Helm, kubectl, operators, other Terraform states, etc.

provider "k8sconnect" {}

# Wait for metrics-server (installed via Helm/kubeadm/etc.)
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

# Wait for CoreDNS to be ready (installed by cluster bootstrap)
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

# Wait for local-path-provisioner to be ready (k3d default storage)
resource "k8sconnect_wait" "storage_provisioner" {
  object_ref = {
    api_version = "apps/v1"
    kind        = "Deployment"
    namespace   = "kube-system"
    name        = "local-path-provisioner"
  }

  wait_for = {
    condition = "Available"
    timeout   = "5m"
  }

  cluster = local.cluster
}

# Outputs show the waits succeeded
output "cluster_ready" {
  value = "Cluster infrastructure is ready: metrics-server, coredns, and storage provisioner are all available"
  depends_on = [
    k8sconnect_wait.metrics_server,
    k8sconnect_wait.coredns,
    k8sconnect_wait.storage_provisioner
  ]
}
