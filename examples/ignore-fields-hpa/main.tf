provider "k8sconnect" {}

# Create a namespace for this example
resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster_connection = var.cluster_connection
}

# Create a deployment with HPA-managed replicas
resource "k8sconnect_object" "app" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: nginx-with-hpa
      namespace: example
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: nginx
      template:
        metadata:
          labels:
            app: nginx
        spec:
          containers:
          - name: nginx
            image: public.ecr.aws/nginx/nginx:1.21
            resources:
              requests:
                cpu: 100m
                memory: 128Mi
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_object.namespace]

  # Ignore spec.replicas because HPA will modify it
  # Without this, Terraform would constantly try to reset replicas
  ignore_fields = ["spec.replicas"]
}

# Create HPA that will manage replicas
resource "k8sconnect_object" "hpa" {
  depends_on = [k8sconnect_object.app]

  yaml_body = <<-YAML
    apiVersion: autoscaling/v2
    kind: HorizontalPodAutoscaler
    metadata:
      name: nginx-hpa
      namespace: example
    spec:
      scaleTargetRef:
        apiVersion: apps/v1
        kind: Deployment
        name: nginx-with-hpa
      minReplicas: 1
      maxReplicas: 10
      metrics:
      - type: Resource
        resource:
          name: cpu
          target:
            type: Utilization
            averageUtilization: 50
  YAML

  cluster_connection = var.cluster_connection
}
