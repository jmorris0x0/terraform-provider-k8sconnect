# examples/wait-for-deployment-rollout/main.tf

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

# Wait for deployment rollout to complete
# This ensures all replicas are updated and ready before continuing
resource "k8sconnect_manifest" "app" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: web-app
      namespace: example
      labels:
        app: web
        version: v1
    spec:
      replicas: 3
      selector:
        matchLabels:
          app: web
      template:
        metadata:
          labels:
            app: web
            version: v1
        spec:
          containers:
          - name: nginx
            image: public.ecr.aws/nginx/nginx:1.21
            ports:
            - containerPort: 80
            resources:
              requests:
                cpu: 100m
                memory: 128Mi
              limits:
                cpu: 200m
                memory: 256Mi
  YAML

  # Wait for rollout completion - ensures all 3 replicas are updated and ready
  # This checks:
  # - status.replicas == spec.replicas
  # - status.updatedReplicas == spec.replicas
  # - status.readyReplicas == spec.replicas
  # - metadata.generation == status.observedGeneration
  wait_for = {
    rollout = true
    timeout = "5m"
  }

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace]
}

# Deploy service only after deployment is fully rolled out
# This prevents exposing traffic to partially-ready deployments
resource "k8sconnect_manifest" "service" {
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

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.app]
}

# Output confirms deployment completed
output "deployment_ready" {
  value       = true
  description = "Deployment rollout completed - all replicas are updated and ready"
}

output "deployment_name" {
  value       = "web-app"
  description = "Name of the deployed application"
}
