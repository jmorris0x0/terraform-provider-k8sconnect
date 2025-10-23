resource "k8sconnect_object" "imported_configmap" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-import
      namespace: import-test
      labels:
        created-by: kubectl
    data:
      key1: value1
      key2: value2
  YAML

  cluster_connection = {
    kubeconfig = file("${path.module}/kind-validation-config")
  }
}
