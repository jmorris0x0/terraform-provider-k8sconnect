# examples/wait-for-loadbalancer/main.tf

provider "k8sconnect" {}

resource "k8sconnect_manifest" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster_connection = var.cluster_connection
}

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
      - port: 8080
        targetPort: 80
      selector:
        app: demo
  YAML

  wait_for = {
    field   = "status.loadBalancer.ingress"
    timeout = "2m"
  }

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace]
}

# This should work if the feature is working correctly
resource "k8sconnect_manifest" "endpoint_config" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: external-endpoints
      namespace: example
    data:
      service_endpoint: "${k8sconnect_manifest.loadbalancer_service.status.loadBalancer.ingress[0].ip}:8080"
      endpoint_ready: "true"
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.loadbalancer_service]
}

output "loadbalancer_endpoint" {
  value = try(
    "${k8sconnect_manifest.loadbalancer_service.status.loadBalancer.ingress[0].ip}:8080",
    "${k8sconnect_manifest.loadbalancer_service.status.loadBalancer.ingress[0].hostname}:8080",
    "pending"
  )
  description = "The LoadBalancer endpoint for the demo service"
}

