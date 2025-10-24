import {
  to = k8sconnect_object.imported_deploy
  id = "kind-kind-validation:import-test:apps/v1/Deployment:test-deploy"
}

resource "k8sconnect_object" "imported_deploy" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: test-deploy
      namespace: import-test
      labels:
        app: test-deploy
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: test-deploy
      template:
        metadata:
          labels:
            app: test-deploy
        spec:
          containers:
          - name: nginx
            image: nginx:1.25
  YAML

  cluster_connection = {
    kubeconfig = file("${path.module}/kind-validation-config")
  }
}
