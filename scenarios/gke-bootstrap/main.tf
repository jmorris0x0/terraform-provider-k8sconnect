# GKE Bootstrap Test
#
# This configuration tests the critical bootstrap scenario:
# Creating a GKE cluster and deploying workloads in a SINGLE terraform apply.
#
# This validates:
# - Inline cluster connections work with "known after apply" values
# - Provider can connect to cluster as soon as API server is ready
# - Node pool dependency ensures workloads can schedule

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    k8sconnect = {
      source  = "local/k8sconnect"
      version = ">= 0.1.0"
    }
  }
  required_version = ">= 1.6"
}

provider "google" {
  project = var.gcp_project
  region  = var.gcp_region
}

provider "k8sconnect" {}

variable "gcp_project" {
  description = "GCP project ID"
  type        = string
}

variable "gcp_region" {
  description = "GCP region for GKE cluster"
  type        = string
  default     = "us-central1"
}

variable "cluster_name" {
  description = "Name of the GKE cluster"
  type        = string
  default     = "k8sconnect-bootstrap-test"
}

#############################################
# GKE CLUSTER
#############################################

resource "google_container_cluster" "main" {
  name     = var.cluster_name
  location = var.gcp_region

  # We can't create a cluster with no node pool defined, but we want to only use
  # separately managed node pools. So we create the smallest possible default
  # node pool and immediately delete it.
  remove_default_node_pool = true
  initial_node_count       = 1

  # Disable legacy features
  networking_mode = "VPC_NATIVE"

  ip_allocation_policy {
    # Use auto-created IP ranges
  }

  # Enable Workload Identity (best practice)
  workload_identity_config {
    workload_pool = "${var.gcp_project}.svc.id.goog"
  }

  # Autopilot would be simpler, but we use standard for flexibility
  # Uncomment below for Autopilot (no node pool needed):
  # enable_autopilot = true
}

# Separately managed node pool
resource "google_container_node_pool" "main" {
  name       = "${var.cluster_name}-nodes"
  location   = var.gcp_region
  cluster    = google_container_cluster.main.name
  node_count = 2

  node_config {
    machine_type = "e2-small"

    # Scopes required for basic cluster operations
    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform"
    ]

    # Enable Workload Identity
    workload_metadata_config {
      mode = "GKE_METADATA"
    }
  }
}

#############################################
# CLUSTER CONNECTION
#############################################

# This is the critical part - inline connection with exec auth
# The endpoint and ca_certificate are "known after apply"
# The provider MUST handle this gracefully during bootstrap
locals {
  cluster_connection = {
    host                   = "https://${google_container_cluster.main.endpoint}"
    cluster_ca_certificate = google_container_cluster.main.master_auth[0].cluster_ca_certificate
    exec = {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "gke-gcloud-auth-plugin"
      args        = []
      env = {
        # The gke-gcloud-auth-plugin reads credentials from gcloud config
        # No additional env vars needed if gcloud is configured
      }
    }
  }
}

#############################################
# BOOTSTRAP WORKLOADS
#############################################

# Create namespace IMMEDIATELY after cluster creation
# This is the critical test - no time_sleep workaround!
resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: bootstrap-test
      labels:
        test: gke-bootstrap
  YAML

  cluster_connection = local.cluster_connection

  depends_on = [
    google_container_cluster.main,
    google_container_node_pool.main,
  ]
}

# Deploy a simple workload to prove cluster is functional
resource "k8sconnect_object" "test_deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: nginx-bootstrap-test
      namespace: bootstrap-test
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: nginx
      template:
        metadata:
          labels:
            app: nginx
        spec:
          containers:
          - name: nginx
            image: public.ecr.aws/nginx/nginx:1.25
            ports:
            - containerPort: 80
  YAML

  cluster_connection = local.cluster_connection

  depends_on = [k8sconnect_object.namespace]
}

# Create a ConfigMap to test basic resource creation
resource "k8sconnect_object" "test_configmap" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: bootstrap-config
      namespace: bootstrap-test
    data:
      cluster_type: gke
      test_date: "${timestamp()}"
      provider: k8sconnect
  YAML

  cluster_connection = local.cluster_connection

  depends_on = [k8sconnect_object.namespace]
}

# Test GKE-specific feature: GCS CSI driver with PVC
# This validates CRD support and storage integration
resource "k8sconnect_object" "test_pvc" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: test-pvc
      namespace: bootstrap-test
    spec:
      accessModes:
        - ReadWriteOnce
      resources:
        requests:
          storage: 1Gi
      storageClassName: standard-rwo
  YAML

  cluster_connection = local.cluster_connection

  depends_on = [k8sconnect_object.namespace]
}

#############################################
# OUTPUTS
#############################################

output "cluster_endpoint" {
  description = "GKE cluster endpoint"
  value       = google_container_cluster.main.endpoint
}

output "cluster_name" {
  description = "GKE cluster name"
  value       = google_container_cluster.main.name
}

output "cluster_location" {
  description = "GKE cluster location"
  value       = google_container_cluster.main.location
}

output "cluster_ca_certificate" {
  description = "GKE cluster CA certificate"
  value       = google_container_cluster.main.master_auth[0].cluster_ca_certificate
  sensitive   = true
}

output "namespace_id" {
  description = "K8sconnect object ID for namespace"
  value       = k8sconnect_object.namespace.id
}

output "deployment_id" {
  description = "K8sconnect object ID for deployment"
  value       = k8sconnect_object.test_deployment.id
}

output "kubeconfig_command" {
  description = "Command to update kubeconfig"
  value       = "gcloud container clusters get-credentials ${google_container_cluster.main.name} --region ${var.gcp_region} --project ${var.gcp_project}"
}
