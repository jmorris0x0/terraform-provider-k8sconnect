# examples/multi-resource/main.tf

provider "k8sconnect" {}

data "k8sconnect_yaml_split" "resources" {
  content = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
    ---
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: app-config
      namespace: example
    data:
      key: value
    ---
    apiVersion: v1
    kind: Secret
    metadata:
      name: app-secret
      namespace: example
    type: Opaque
    data:
      password: cGFzc3dvcmQ=
  YAML
}

resource "k8sconnect_object" "resources" {
  for_each = data.k8sconnect_yaml_split.resources.manifests

  yaml_body          = each.value
  cluster = local.cluster
}
