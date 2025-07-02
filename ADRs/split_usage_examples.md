# Example 1: Split inline multi-document YAML
data "k8sinline_yaml_split" "app_manifests" {
  content = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: my-app
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  namespace: my-app
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
        image: nginx:1.21
---
apiVersion: v1
kind: Service
metadata:
  name: nginx
  namespace: my-app
spec:
  selector:
    app: nginx
  ports:
  - port: 80
    targetPort: 80
YAML
}

# Apply all manifests with for_each (stable IDs!)
resource "k8sinline_manifest" "app" {
  for_each = data.k8sinline_yaml_split.app_manifests.manifests
  
  yaml_body = each.value
  
  cluster_connection = {
    kubeconfig_raw = var.kubeconfig
  }
}

# Example 2: Load from multiple files
data "k8sinline_yaml_split" "all_configs" {
  pattern = "./k8s-configs/*.yaml"
}

resource "k8sinline_manifest" "configs" {
  for_each = data.k8sinline_yaml_split.all_configs.manifests
  
  yaml_body = each.value
  
  cluster_connection = {
    host                   = var.k8s_host
    cluster_ca_certificate = var.k8s_ca
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", var.cluster_name]
    }
  }
}

# Example 3: Different clusters per manifest using locals
locals {
  # Split the manifests
  manifests = data.k8sinline_yaml_split.multi_cluster.manifests
  
  # Define cluster connections
  clusters = {
    prod = {
      kubeconfig_raw = var.prod_kubeconfig
    }
    staging = {
      kubeconfig_raw = var.staging_kubeconfig  
    }
  }
  
  # Map manifests to clusters based on naming convention
  manifest_clusters = {
    for key, yaml in local.manifests : key => (
      strcontains(key, "prod") ? local.clusters.prod : local.clusters.staging
    )
  }
}

resource "k8sinline_manifest" "multi_cluster" {
  for_each = local.manifests
  
  yaml_body = each.value
  
  cluster_connection = local.manifest_clusters[each.key]
}

# Example 4: Dependency ordering with depends_on
resource "k8sinline_manifest" "crds" {
  for_each = {
    for key, yaml in data.k8sinline_yaml_split.all_configs.manifests :
    key => yaml
    if can(regex("(?i)customresourcedefinition", yaml))
  }
  
  yaml_body = each.value
  
  cluster_connection = {
    kubeconfig_raw = var.kubeconfig
  }
}

resource "k8sinline_manifest" "apps" {
  for_each = {
    for key, yaml in data.k8sinline_yaml_split.all_configs.manifests :
    key => yaml
    if !can(regex("(?i)customresourcedefinition", yaml))
  }
  
  yaml_body = each.value
  
  cluster_connection = {
    kubeconfig_raw = var.kubeconfig
  }
  
  depends_on = [k8sinline_manifest.crds]
}

# The stable IDs look like:
# - "namespace.my-app"           (cluster-scoped)
# - "deployment.my-app.nginx"    (namespaced)  
# - "service.my-app.nginx"       (namespaced)
# - "customresourcedefinition.prometheuses.monitoring.coreos.com" (cluster-scoped)

# Example 5: Using with templatefile for dynamic content
data "k8sinline_yaml_split" "templated" {
  content = templatefile("${path.module}/app-template.yaml", {
    app_name    = var.app_name
    environment = var.environment
    replicas    = var.replicas
  })
}

resource "k8sinline_manifest" "templated_app" {
  for_each = data.k8sinline_yaml_split.templated.manifests
  
  yaml_body = each.value
  
  cluster_connection = {
    kubeconfig_file = "~/.kube/config"
    context         = var.kube_context
  }
}
