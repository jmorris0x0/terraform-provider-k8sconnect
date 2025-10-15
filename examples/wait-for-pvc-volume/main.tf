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

# Create PVC and wait for volumeName
# Infrastructure resource - need volumeName for monitoring/tracking
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

  # Infrastructure pattern: wait for volumeName to track which PV was bound
  wait_for = {
    field   = "status.volumeName"
    timeout = "1m"
  }

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace, k8sconnect_manifest.pv]
}

# Use the bound PV name in monitoring configuration
resource "k8sconnect_manifest" "volume_metadata" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: volume-tracking
      namespace: example
    data:
      pvc_name: "app-data"
      bound_pv: "${k8sconnect_manifest.pvc.status.volumeName}"
      capacity: "5Gi"
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.pvc]
}

output "bound_volume" {
  value       = k8sconnect_manifest.pvc.status.volumeName
  description = "Name of the PersistentVolume bound to this claim"
}

output "pvc_phase" {
  value       = k8sconnect_manifest.pvc.status.phase
  description = "Current phase of the PVC (should be Bound)"
}
