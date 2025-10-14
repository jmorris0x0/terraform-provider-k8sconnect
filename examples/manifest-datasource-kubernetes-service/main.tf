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

# Use the datasource .object attribute to access fields with dot notation
resource "k8sconnect_manifest" "api_endpoint_config" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: api-info
      namespace: example
    data:
      cluster_ip: "${data.k8sconnect_manifest.kubernetes_api.object.spec.clusterIP}"
      port: "${tostring(data.k8sconnect_manifest.kubernetes_api.object.spec.ports[0].port)}"
      endpoint: "https://${data.k8sconnect_manifest.kubernetes_api.object.spec.clusterIP}:${tostring(data.k8sconnect_manifest.kubernetes_api.object.spec.ports[0].port)}"
      service_name: "${data.k8sconnect_manifest.kubernetes_api.object.metadata.name}"
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace]
}

# Output the API endpoint for verification
output "kubernetes_api_endpoint" {
  value       = "https://${data.k8sconnect_manifest.kubernetes_api.object.spec.clusterIP}:${tostring(data.k8sconnect_manifest.kubernetes_api.object.spec.ports[0].port)}"
  description = "The Kubernetes API server endpoint"
}
