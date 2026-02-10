# To run this example, define your cluster connection in locals.tf
# See ../README.md for setup instructions

provider "k8sconnect" {}

# Load and categorize manifests by scope
data "k8sconnect_yaml_scoped" "all" {
  content = file("${path.module}/manifests.yaml")
}

# Apply CRDs first - must exist before custom resources can be created
resource "k8sconnect_object" "crds" {
  for_each = data.k8sconnect_yaml_scoped.all.crds

  yaml_body          = each.value
  cluster = local.cluster
}

# Apply cluster-scoped resources second (Namespaces, ClusterRoles, etc.)
resource "k8sconnect_object" "cluster_scoped" {
  for_each = data.k8sconnect_yaml_scoped.all.cluster_scoped

  yaml_body          = each.value
  cluster = local.cluster

  depends_on = [k8sconnect_object.crds]
}

# Apply namespaced resources last (Deployments, Services, ConfigMaps, Custom Resources, etc.)
resource "k8sconnect_object" "namespaced" {
  for_each = data.k8sconnect_yaml_scoped.all.namespaced

  yaml_body          = each.value
  cluster = local.cluster

  depends_on = [
    k8sconnect_object.crds,
    k8sconnect_object.cluster_scoped
  ]
}
