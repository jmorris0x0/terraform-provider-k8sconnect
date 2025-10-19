# examples/wait-for-pvc-volume/main.tf

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster_connection = var.cluster_connection
}

# Create PVC using k3d's local-path storage class (dynamic provisioning)
# No need to manually create PV - the provisioner creates it automatically
resource "k8sconnect_object" "pvc" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: app-data
      namespace: example
    spec:
      accessModes:
        - ReadWriteOnce
      storageClassName: local-path
      resources:
        requests:
          storage: 5Gi
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

# Wait for PVC to be bound before proceeding with dependent resources
resource "k8sconnect_wait" "pvc" {
  object_ref = k8sconnect_object.pvc.object_ref

  cluster_connection = var.cluster_connection

  wait_for = {
    field_value = { "status.phase" = "Bound" }
    timeout     = "1m"
  }
}

# Dependent resource that requires PVC to be bound
# Uses explicit depends_on since wait ensures binding is complete
resource "k8sconnect_object" "volume_metadata" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: volume-tracking
      namespace: example
    data:
      pvc_name: "app-data"
      capacity: "5Gi"
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_wait.pvc]
}
