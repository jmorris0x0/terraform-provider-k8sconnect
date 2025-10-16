# examples/wait-for-condition/main.tf

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

# Create PersistentVolume for this example
resource "k8sconnect_manifest" "pv" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolume
    metadata:
      name: example-pv
    spec:
      capacity:
        storage: 1Gi
      accessModes:
        - ReadWriteOnce
      persistentVolumeReclaimPolicy: Delete
      storageClassName: manual
      hostPath:
        path: /tmp/example-pv
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace]
}

# Create PVC
resource "k8sconnect_manifest" "pvc" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: data-claim
      namespace: example
    spec:
      accessModes:
        - ReadWriteOnce
      storageClassName: manual
      resources:
        requests:
          storage: 1Gi
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.pv]
}

# Wait for PVC to be bound
# Note: PVCs don't have a "Ready" condition - they just have status.phase
# This example shows waiting for phase via field_value instead
resource "k8sconnect_wait" "pvc" {
  object_ref = k8sconnect_manifest.pvc.object_ref

  cluster_connection = var.cluster_connection

  wait_for = {
    field_value = {
      "status.phase" = "Bound"
    }
    timeout = "2m"
  }
}

# Create deployment that uses the PVC
# Deployments have status.conditions including "Available" and "Progressing"
resource "k8sconnect_manifest" "app" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: storage-app
      namespace: example
    spec:
      replicas: 1
      selector:
        matchLabels:
          app: storage
      template:
        metadata:
          labels:
            app: storage
        spec:
          containers:
          - name: app
            image: public.ecr.aws/docker/library/busybox:latest
            command: ["sh", "-c", "while true; do date >> /data/log.txt; sleep 30; done"]
            volumeMounts:
            - name: data
              mountPath: /data
            resources:
              requests:
                cpu: 50m
                memory: 64Mi
          volumes:
          - name: data
            persistentVolumeClaim:
              claimName: data-claim
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_wait.pvc]
}

# Wait for "Available" condition to be True
# This means the deployment has minimum availability (at least 1 replica ready)
# Available conditions: "Available", "Progressing", "ReplicaFailure"
resource "k8sconnect_wait" "app" {
  object_ref = k8sconnect_manifest.app.object_ref

  cluster_connection = var.cluster_connection

  wait_for = {
    condition = "Available"
    timeout   = "3m"
  }
}

output "pvc_bound" {
  value       = true
  description = "PersistentVolumeClaim is bound and ready"
}

output "deployment_available" {
  value       = true
  description = "Deployment has Available=True condition (minimum availability reached)"
}
