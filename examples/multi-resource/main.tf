# examples/multi-resource/main.tf
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

resource "k8sconnect_manifest" "resources" {
  for_each = data.k8sconnect_yaml_split.resources.manifests

  yaml_body          = each.value
  cluster_connection = var.cluster_connection
}
