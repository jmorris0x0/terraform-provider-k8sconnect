# To run this example, define your cluster connection in locals.tf
# See ../README.md for setup instructions

terraform {
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.0"
    }
    k8sconnect = {
      source = "jmorris0x0/k8sconnect"
    }
  }
}

provider "k8sconnect" {}

# Template a Helm chart without installing it
# The output is a single string with all manifests separated by '---'
data "helm_template" "cert_manager" {
  name       = "cert-manager"
  namespace  = "cert-manager"
  repository = "https://charts.jetstack.io"
  chart      = "cert-manager"
  version    = "v1.13.0"

  set {
    name  = "installCRDs"
    value = "true"
  }
}

# Split and categorize the Helm chart output
# This automatically separates CRDs, cluster-scoped, and namespaced resources
data "k8sconnect_yaml_scoped" "cert_manager" {
  content = data.helm_template.cert_manager.manifest
}

# Create namespace first
resource "k8sconnect_object" "cert_manager_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: cert-manager
  YAML

  cluster = local.cluster
}

# Apply CRDs first
resource "k8sconnect_object" "cert_manager_crds" {
  for_each = data.k8sconnect_yaml_scoped.cert_manager.crds

  yaml_body = each.value
  cluster   = local.cluster

  depends_on = [k8sconnect_object.cert_manager_namespace]
}

# Then cluster-scoped resources (ClusterRoles, ClusterRoleBindings, etc.)
resource "k8sconnect_object" "cert_manager_cluster" {
  for_each = data.k8sconnect_yaml_scoped.cert_manager.cluster_scoped

  yaml_body = each.value
  cluster   = local.cluster

  depends_on = [k8sconnect_object.cert_manager_crds]
}

# Finally namespaced resources (Deployments, Services, etc.)
resource "k8sconnect_object" "cert_manager_app" {
  for_each = data.k8sconnect_yaml_scoped.cert_manager.namespaced

  yaml_body = each.value
  cluster   = local.cluster

  depends_on = [k8sconnect_object.cert_manager_cluster]
}

# Outputs showing what was deployed
output "crds_deployed" {
  value       = length(data.k8sconnect_yaml_scoped.cert_manager.crds)
  description = "Number of CRDs deployed"
}

output "cluster_scoped_resources" {
  value       = length(data.k8sconnect_yaml_scoped.cert_manager.cluster_scoped)
  description = "Number of cluster-scoped resources deployed"
}

output "namespaced_resources" {
  value       = length(data.k8sconnect_yaml_scoped.cert_manager.namespaced)
  description = "Number of namespaced resources deployed"
}
