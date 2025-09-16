terraform {
  required_providers {
    kind = {
      source  = "tehcyx/kind"
      version = "~> 0.6"
    }
    k8sconnect = {
      source  = "local/k8sconnect"
      version = ">= 0.1.0"
    }
    kubectl = {
      source  = "alekc/kubectl"
      version = "~> 2.1"
    }
  }
  required_version = ">= 1.6"
}
# Create the Kind cluster
resource "kind_cluster" "test" {
  name           = "provider-comparison"
  node_image     = "kindest/node:v1.31.0"
  wait_for_ready = true
  kind_config {
    kind        = "Cluster"
    api_version = "kind.x-k8s.io/v1alpha4"
    node {
      role = "control-plane"
    }
    node {
      role = "worker"
    }
  }
}
# Configure the kubectl provider - this one works with dynamic config
provider "kubectl" {
  host                   = kind_cluster.test.endpoint
  cluster_ca_certificate = kind_cluster.test.cluster_ca_certificate
  client_certificate     = kind_cluster.test.client_certificate
  client_key             = kind_cluster.test.client_key
  load_config_file       = false
}
# k8sconnect provider doesn't need configuration
provider "k8sconnect" {}
# Local for the connection details
locals {
  cluster_connection = {
    host                   = kind_cluster.test.endpoint
    cluster_ca_certificate = base64encode(kind_cluster.test.cluster_ca_certificate)
    client_certificate     = base64encode(kind_cluster.test.client_certificate)
    client_key             = base64encode(kind_cluster.test.client_key)
  }
}
# Create TWO namespaces - one for each provider's resources
resource "k8sconnect_manifest" "namespace_k8sconnect" {
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: k8sconnect-test
      labels:
        provider: k8sconnect
  YAML
  cluster_connection = local.cluster_connection
}
resource "k8sconnect_manifest" "namespace_kubectl" {
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: kubectl-test
      labels:
        provider: kubectl
  YAML
  cluster_connection = local.cluster_connection
}
#############################################
# Deploy SAME manifests with different providers
# We'll use string replacement to change namespace
#############################################
locals {
  # Original YAML files use placeholder namespace
  deployment_yaml = file("${path.module}/nginx-deployment.yaml")
  service_yaml    = file("${path.module}/nginx-service.yaml")
  # Replace namespace for each provider
  deployment_k8sconnect = replace(local.deployment_yaml, "provider-comparison", "k8sconnect-test")
  service_k8sconnect    = replace(local.service_yaml, "provider-comparison", "k8sconnect-test")

  deployment_kubectl = replace(local.deployment_yaml, "provider-comparison", "kubectl-test")
  # Also change the nodePort for kubectl to avoid conflicts
  service_kubectl = replace(
    replace(local.service_yaml, "provider-comparison", "kubectl-test"),
    "nodePort: 30080",
    "nodePort: 30081"
  )
}
# Using k8sconnect - with inline cluster connection
resource "k8sconnect_manifest" "nginx_deployment" {
  yaml_body          = local.deployment_k8sconnect
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace_k8sconnect]
}
resource "k8sconnect_manifest" "nginx_service" {
  yaml_body          = local.service_k8sconnect
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_manifest.namespace_k8sconnect]
}
# Using kubectl provider - requires provider configuration
resource "kubectl_manifest" "nginx_deployment" {
  yaml_body  = local.deployment_kubectl
  depends_on = [k8sconnect_manifest.namespace_kubectl]
}
resource "kubectl_manifest" "nginx_service" {
  yaml_body  = local.service_kubectl
  depends_on = [k8sconnect_manifest.namespace_kubectl]
}
