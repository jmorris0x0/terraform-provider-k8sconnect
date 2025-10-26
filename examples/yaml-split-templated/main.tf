provider "k8sconnect" {}

# Use templatefile for dynamic content
data "k8sconnect_yaml_split" "templated" {
  content = templatefile("${path.module}/app-template.yaml", {
    app_name    = "demo"
    environment = "test"
    replicas    = 2
  })
}

resource "k8sconnect_object" "templated_app" {
  for_each = data.k8sconnect_yaml_split.templated.manifests

  yaml_body          = each.value
  cluster = local.cluster
}
