# To run this example, define your cluster connection in locals.tf
# See ../README.md for setup instructions

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: example
  YAML

  cluster = local.cluster
}

# Create PVC using k3d's local-path storage class (dynamic provisioning)
# No need to manually create PV - the provisioner creates it automatically
# Note: local-path uses WaitForFirstConsumer binding mode, so PVC won't bind
# until a pod references it
resource "k8sconnect_object" "pvc" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: data-claim
      namespace: example
    spec:
      accessModes:
        - ReadWriteOnce
      storageClassName: local-path
      resources:
        requests:
          storage: 1Gi
  YAML

  cluster    = local.cluster
  depends_on = [k8sconnect_object.namespace]
}

# Create deployment that uses the PVC
# The PVC will automatically bind when this deployment's pod starts
# Deployments have status.conditions including "Available" and "Progressing"
resource "k8sconnect_object" "app" {
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

  cluster    = local.cluster
  depends_on = [k8sconnect_object.pvc]
}

# Wait for "Available" condition to be True
# This means the deployment has minimum availability (at least 1 replica ready)
# Available conditions: "Available", "Progressing", "ReplicaFailure"
resource "k8sconnect_wait" "app" {
  object_ref = k8sconnect_object.app.object_ref

  cluster = local.cluster

  wait_for = {
    condition = "Available"
    timeout   = "5m"
  }
}
