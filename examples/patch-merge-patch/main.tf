# examples/patch-merge-patch/main.tf

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

  take_ownership     = true
  cluster_connection = var.cluster_connection
}

# Output the managed fields
output "managed_fields" {
  value       = k8sconnect_patch.kube_dns_annotation.managed_fields
  description = "Managed fields metadata for this patch"
}
