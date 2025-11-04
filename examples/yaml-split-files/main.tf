# To run this example, define your cluster connection in locals.tf
# See ../README.md for setup instructions

provider "k8sconnect" {}

# Load all YAML files from manifests directory
data "k8sconnect_yaml_split" "configs" {
  pattern = "${path.module}/manifests/*.yaml"
}

resource "k8sconnect_object" "all" {
  for_each = data.k8sconnect_yaml_split.configs.manifests

  yaml_body          = each.value
  cluster = local.cluster
}
