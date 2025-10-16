# examples/wait-for-pvc-volume/main.tf

provider "k8sconnect" {}

resource "k8sconnect_manifest" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster_connection = var.cluster_connection
}

# Create PersistentVolume
resource "k8sconnect_manifest" "pv" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolume
    metadata:
      name: data-pv
    spec:
      capacity:
        storage: 5Gi
      accessModes:
        - ReadWriteOnce
      persistentVolumeReclaimPolicy: Delete
      storageClassName: manual
      hostPath:
        path: /tmp/data
  YAML

  cluster_connection = var.cluster_connection
}

# Create PVC and wait for it to be bound
# Infrastructure resource - wait for binding to complete before proceeding
resource "k8sconnect_manifest" "pvc" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: app-data
      namespace: example
    spec:
      accessModes:
        - ReadWriteOnce
      storageClassName: manual
      resources:
        requests:
          storage: 5Gi
  YAML

  # Wait for PVC to be bound before proceeding with dependent resources
  wait_for = {
    field_value = { "status.phase" = "Bound" }
    timeout     = "1m"
  }

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace, k8sconnect_manifest.pv]
}

# Dependent resource that requires PVC to be bound
# Uses explicit depends_on since wait ensures binding is complete
resource "k8sconnect_manifest" "volume_metadata" {
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
  depends_on         = [k8sconnect_manifest.pvc]
}
