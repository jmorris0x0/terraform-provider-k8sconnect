# examples/loadbalancer-service/main.tf
resource "k8sconnect_manifest" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster_connection = var.cluster_connection
}

resource "k8sconnect_manifest" "service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: web-app
      namespace: example
    spec:
      type: LoadBalancer
      selector:
        app: web
      ports:
      - port: 80
        targetPort: 8080
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace]
}
