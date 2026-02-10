apiVersion: v1
kind: ConfigMap
metadata:
  name: template-test
  namespace: ${namespace}
data:
  cluster_host: "${cluster_host}"
