# To run this example, define your cluster connection in locals.tf
# See ../README.md for setup instructions

provider "k8sconnect" {}

# Create namespace for the Helm release
resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: nginx-example
  YAML

  cluster = local.cluster
}

# Deploy nginx using Bitnami Helm chart
resource "k8sconnect_helm_release" "nginx" {
  name      = "nginx"
  namespace = "nginx-example"

  # Bitnami nginx chart from Artifact Hub
  repository = "https://charts.bitnami.com/bitnami"
  chart      = "nginx"
  version    = "15.4.4"

  # Configure minimal replicas and ClusterIP service
  values = <<-YAML
    replicaCount: 1
    service:
      type: ClusterIP
  YAML

  cluster            = local.cluster
  create_namespace   = false
  wait               = false  # Don't wait to keep example fast
  dependency_update  = false

  depends_on = [k8sconnect_object.namespace]
}
