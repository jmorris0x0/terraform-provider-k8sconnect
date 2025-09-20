provider "k8sconnect" {}

# Load manifests that include CRDs
data "k8sconnect_yaml_split" "all" {
  content = file("${path.module}/manifests.yaml")
}

# Apply CRDs first
resource "k8sconnect_manifest" "crds" {
  for_each = {
    for key, yaml in data.k8sconnect_yaml_split.all.manifests :
    key => yaml
    if can(regex("(?i)kind:\\s*CustomResourceDefinition", yaml))
  }

  yaml_body          = each.value
  cluster_connection = var.cluster_connection
}

# Apply namespaces next
resource "k8sconnect_manifest" "namespaces" {
  for_each = {
    for key, yaml in data.k8sconnect_yaml_split.all.manifests :
    key => yaml
    if can(regex("(?i)kind:\\s*Namespace", yaml))
  }

  yaml_body          = each.value
  cluster_connection = var.cluster_connection
}

# Apply custom resources last
resource "k8sconnect_manifest" "custom_resources" {
  for_each = {
    for key, yaml in data.k8sconnect_yaml_split.all.manifests :
    key => yaml
    if !can(regex("(?i)kind:\\s*CustomResourceDefinition", yaml)) &&
    !can(regex("(?i)kind:\\s*Namespace", yaml))
  }

  yaml_body          = each.value
  cluster_connection = var.cluster_connection

  depends_on = [
    k8sconnect_manifest.crds,
    k8sconnect_manifest.namespaces
  ]
}
