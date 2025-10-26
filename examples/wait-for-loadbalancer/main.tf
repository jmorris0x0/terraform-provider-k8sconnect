# examples/wait-for-loadbalancer/main.tf

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster = local.cluster
}

resource "k8sconnect_object" "loadbalancer_service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: demo-lb
      namespace: example
    spec:
      type: LoadBalancer
      ports:
      - port: 9999
        targetPort: 80
      selector:
        app: demo
  YAML

  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

resource "k8sconnect_wait" "loadbalancer_service" {
  object_ref = k8sconnect_object.loadbalancer_service.object_ref

  cluster = local.cluster

  wait_for = {
    field   = "status.loadBalancer.ingress"
    timeout = "2m"
  }
}

resource "k8sconnect_object" "endpoint_config" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: external-endpoints
      namespace: example
    data:
      service_endpoint: "${k8sconnect_wait.loadbalancer_service.result.status.loadBalancer.ingress[0].ip}:8080"
      endpoint_ready: "true"
  YAML

  cluster = local.cluster
  depends_on         = [k8sconnect_wait.loadbalancer_service]
}

output "loadbalancer_endpoint" {
  value = try(
    "${k8sconnect_wait.loadbalancer_service.result.status.loadBalancer.ingress[0].ip}:8080",
    "${k8sconnect_wait.loadbalancer_service.result.status.loadBalancer.ingress[0].hostname}:8080",
    "pending"
  )
  description = "The LoadBalancer endpoint for the demo service"
}
