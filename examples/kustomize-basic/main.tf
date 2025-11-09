# To run this example, define your cluster connection in locals.tf
# See ../README.md for setup instructions

provider "k8sconnect" {}

# Build kustomization from production overlay
data "k8sconnect_yaml_split" "app" {
  kustomize_path = "${path.module}/kustomization/overlays/production"
}

# Deploy all resources from kustomization
resource "k8sconnect_object" "app" {
  for_each = data.k8sconnect_yaml_split.app.manifests

  yaml_body = each.value
  cluster   = local.cluster
}
