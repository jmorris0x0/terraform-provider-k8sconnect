# To run this example, define your cluster connection in locals.tf
# See ../README.md for setup instructions

provider "k8sconnect" {}

# JSON Patch example (RFC 6902): Add a label to the kubernetes service
# JSON Patch uses an array of operations (add, remove, replace, test, etc.)
resource "k8sconnect_patch" "kubernetes_svc_label" {
  target = {
    api_version = "v1"
    kind        = "Service"
    name        = "kubernetes"
    namespace   = "default"
  }

  json_patch = jsonencode([
    {
      op    = "add"
      path  = "/metadata/labels/example.com~1patched-by"
      value = "terraform-json-patch"
    }
  ])

  cluster = local.cluster
}
