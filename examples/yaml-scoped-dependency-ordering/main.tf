provider "k8sconnect" {}

# Load and categorize manifests by scope
data "k8sconnect_yaml_scoped" "all" {
  content = file("${path.module}/manifests.yaml")
}

# Apply CRDs first - must exist before custom resources can be created
resource "k8sconnect_object" "crds" {
  for_each = data.k8sconnect_yaml_scoped.all.crds

  yaml_body          = each.value
  cluster_connection = var.cluster_connection
}

# Apply cluster-scoped resources second (Namespaces, ClusterRoles, etc.)
resource "k8sconnect_object" "cluster_scoped" {
  for_each = data.k8sconnect_yaml_scoped.all.cluster_scoped

  yaml_body          = each.value
  cluster_connection = var.cluster_connection

  depends_on = [k8sconnect_object.crds]
}

# Apply namespaced resources last (Deployments, Services, ConfigMaps, Custom Resources, etc.)
resource "k8sconnect_object" "namespaced" {
  for_each = data.k8sconnect_yaml_scoped.all.namespaced

  yaml_body          = each.value
  cluster_connection = var.cluster_connection

  depends_on = [
    k8sconnect_object.crds,
    k8sconnect_object.cluster_scoped
  ]
}

# Example outputs showing what was applied in each category
output "crd_count" {
  value       = length(data.k8sconnect_yaml_scoped.all.crds)
  description = "Number of CRDs applied"
}

output "cluster_scoped_count" {
  value       = length(data.k8sconnect_yaml_scoped.all.cluster_scoped)
  description = "Number of cluster-scoped resources applied"
}

output "namespaced_count" {
  value       = length(data.k8sconnect_yaml_scoped.all.namespaced)
  description = "Number of namespaced resources applied"
}
