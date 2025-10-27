# examples/patch-strategic-merge/main.tf

provider "k8sconnect" {}

# Strategic Merge Patch example: Add a label to the coredns deployment
# This demonstrates Server-Side Apply with field ownership tracking
resource "k8sconnect_patch" "coredns_label" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "coredns"
    namespace   = "kube-system"
  }

  patch = jsonencode({
    metadata = {
      labels = {
        "example.com/managed-by" = "terraform"
      }
    }
  })

  cluster = local.cluster
}
