provider "k8sconnect" {}

# Load all YAML files from manifests directory
data "k8sconnect_yaml_split" "configs" {
  pattern = "${path.module}/manifests/*.yaml"
}

resource "k8sconnect_object" "all" {
  for_each = data.k8sconnect_yaml_split.configs.manifests

  yaml_body          = each.value
  cluster_connection = var.cluster_connection
}
