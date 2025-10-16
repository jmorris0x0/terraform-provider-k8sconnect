# examples/wait-for-ingress/main.tf

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

# Create backend service
resource "k8sconnect_manifest" "backend" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: backend-svc
      namespace: example
    spec:
      ports:
      - port: 8080
        targetPort: 8080
      selector:
        app: backend
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace]
}

# Create Ingress
resource "k8sconnect_manifest" "ingress" {
  yaml_body = <<-YAML
    apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: api-ingress
      namespace: example
    spec:
      rules:
      - host: api.example.com
        http:
          paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: backend-svc
                port:
                  number: 8080
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.backend]
}

# Wait for Ingress controller to assign hostname/IP
# Infrastructure pattern: use field wait to get status for DNS
resource "k8sconnect_wait" "ingress" {
  object_ref = k8sconnect_manifest.ingress.object_ref

  cluster_connection = var.cluster_connection

  wait_for = {
    field   = "status.loadBalancer.ingress"
    timeout = "2m"
  }
}

# Use the Ingress hostname in DNS configuration
resource "k8sconnect_manifest" "dns_config" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: dns-config
      namespace: example
    data:
      ingress_hostname: "${try(k8sconnect_wait.ingress.status.loadBalancer.ingress[0].hostname, k8sconnect_wait.ingress.status.loadBalancer.ingress[0].ip)}"
      external_url: "https://api.example.com"
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_wait.ingress]
}

output "ingress_endpoint" {
  value       = try(k8sconnect_wait.ingress.status.loadBalancer.ingress[0].hostname, k8sconnect_wait.ingress.status.loadBalancer.ingress[0].ip)
  description = "Ingress controller assigned hostname or IP"
}
