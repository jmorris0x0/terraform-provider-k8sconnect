# examples/manifest-datasource-kubernetes-service/main.tf

provider "k8sconnect" {}

# Read the kubernetes API server service (present in all clusters)
data "k8sconnect_manifest" "kubernetes_api" {
  api_version = "v1"
  kind        = "Service"
  name        = "kubernetes"
  namespace   = "default"

  cluster_connection = var.cluster_connection
}

# Create a namespace for this example
resource "k8sconnect_manifest" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster_connection = var.cluster_connection
}

# Parse the manifest JSON to access fields
locals {
  kubernetes_api = jsondecode(data.k8sconnect_manifest.kubernetes_api.manifest)
}

# Use the datasource output to create a ConfigMap with API endpoint info
resource "k8sconnect_manifest" "api_endpoint_config" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: api-info
      namespace: example
    data:
      cluster_ip: "${local.kubernetes_api.spec.clusterIP}"
      port: "${tostring(local.kubernetes_api.spec.ports[0].port)}"
      endpoint: "https://${local.kubernetes_api.spec.clusterIP}:${tostring(local.kubernetes_api.spec.ports[0].port)}"
      service_name: "${local.kubernetes_api.metadata.name}"
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace]
}

# Output the API endpoint for verification
output "kubernetes_api_endpoint" {
  value       = "https://${local.kubernetes_api.spec.clusterIP}:${tostring(local.kubernetes_api.spec.ports[0].port)}"
  description = "The Kubernetes API server endpoint"
}
