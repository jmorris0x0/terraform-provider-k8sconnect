# examples/wait-for-job-completion/main.tf

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

resource "k8sconnect_manifest" "migration_job" {
  yaml_body = <<-YAML
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: database-migration
      namespace: example
    spec:
      backoffLimit: 1
      completions: 1
      template:
        spec:
          containers:
          - name: migrate
            image: busybox:1.28
            command: 
            - sh
            - -c
            - echo "Running migrations..." && sleep 5 && echo "Complete!"
          restartPolicy: Never
  YAML

  wait_for = {
    field_value = {
      "status.succeeded" = "1" # Wait for exactly 1 successful completion
    }
    timeout = "2m"
  }

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace]
}

# Deploy app only after migrations complete
# Note: Since field_value doesn't populate status, we just rely on depends_on
resource "k8sconnect_manifest" "app_deployment" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: app-config
      namespace: example
    data:
      database_ready: "true"
      migrations_complete: "true"
  YAML

  cluster_connection = var.cluster_connection
  depends_on         = [k8sconnect_manifest.migration_job]
}

output "job_deployed" {
  value       = true
  description = "Job has completed successfully (waited for status.succeeded = 1)"
}
