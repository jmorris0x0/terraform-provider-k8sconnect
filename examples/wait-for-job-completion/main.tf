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
            image: public.ecr.aws/docker/library/busybox:latest
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
# Note: field_value waits don't populate .status (only field waits do)
# We use depends_on to ensure this runs after the migration succeeds
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
