# To run this example, define your cluster connection in locals.tf
# See ../README.md for setup instructions

provider "k8sconnect" {}

# Merge Patch example (RFC 7386): Add an annotation to the kube-dns service
# Merge Patch is simpler than JSON Patch - just specify the fields to merge
resource "k8sconnect_patch" "kube_dns_annotation" {
  target = {
    api_version = "v1"
    kind        = "Service"
    name        = "kube-dns"
    namespace   = "kube-system"
  }

  merge_patch = jsonencode({
    metadata = {
      annotations = {
        "example.com/managed-by" = "terraform"
        "example.com/patch-type" = "merge-patch"
      }
    }
  })

  cluster = local.cluster
}
