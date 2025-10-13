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

  take_ownership     = true
  cluster_connection = var.cluster_connection
}

# Output the field ownership information
output "field_ownership" {
  value       = k8sconnect_patch.coredns_label.field_ownership
  description = "Fields owned by this patch (using Server-Side Apply)"
}

output "previous_owners" {
  value       = k8sconnect_patch.coredns_label.previous_owners
  description = "Previous owners of the patched fields"
}
