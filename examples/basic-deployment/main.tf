# examples/basic-deployment/main.tf

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: nginx
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
  YAML

  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}
