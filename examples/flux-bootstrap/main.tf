# Bootstrap Flux CD without the Flux Terraform provider.
#
# The Flux provider has the same provider-level dependency problem as
# hashicorp/kubernetes: it needs cluster credentials at plan time, but
# those don't exist until the cluster is created during apply.
#
# k8sconnect bypasses this by using inline, per-resource connections.
# Flux's install manifest is just Kubernetes resources (CRDs, Namespaces,
# Deployments, Services), so we apply them directly.
#
# To run this example, define your cluster connection in locals.tf
# See ../README.md for setup instructions

provider "k8sconnect" {}

# Fetch the Flux install manifest from the GitHub release.
# Pin the version to control upgrades through Terraform.
data "http" "flux_install" {
  url = "https://github.com/fluxcd/flux2/releases/download/v2.4.0/install.yaml"
}

# Split and categorize by scope: CRDs -> cluster-scoped -> namespaced.
# The install manifest contains ~30 resources across all three categories.
data "k8sconnect_yaml_scoped" "flux" {
  content = data.http.flux_install.response_body
}

# Stage 1: CRDs (GitRepository, Kustomization, HelmRelease, etc.)
resource "k8sconnect_object" "flux_crds" {
  for_each  = data.k8sconnect_yaml_scoped.flux.crds
  yaml_body = each.value
  cluster   = local.cluster
}

# Stage 2: Cluster-scoped resources (flux-system Namespace, ClusterRoles)
resource "k8sconnect_object" "flux_cluster" {
  for_each   = data.k8sconnect_yaml_scoped.flux.cluster_scoped
  yaml_body  = each.value
  cluster    = local.cluster
  depends_on = [k8sconnect_object.flux_crds]
}

# Stage 3: Namespaced resources (controller Deployments, Services, etc.)
resource "k8sconnect_object" "flux_namespaced" {
  for_each   = data.k8sconnect_yaml_scoped.flux.namespaced
  yaml_body  = each.value
  cluster    = local.cluster
  depends_on = [k8sconnect_object.flux_crds, k8sconnect_object.flux_cluster]
}

# Wait for source-controller before creating sync resources.
# source-controller must be running to reconcile GitRepository objects.
resource "k8sconnect_wait" "source_controller" {
  object_ref = k8sconnect_object.flux_namespaced["apps.deployment.flux-system.source-controller"].object_ref
  wait_for   = { rollout = true, timeout = "5m" }
  cluster    = local.cluster
}

# Point Flux at your fleet repo. Flux will sync everything under the
# configured path, making it the GitOps source of truth after bootstrap.
resource "k8sconnect_object" "flux_source" {
  yaml_body = <<-YAML
    apiVersion: source.toolkit.fluxcd.io/v1
    kind: GitRepository
    metadata:
      name: flux-system
      namespace: flux-system
    spec:
      interval: 1m
      url: ssh://git@github.com/your-org/fleet-infra
      ref:
        branch: main
      secretRef:
        name: flux-system
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_wait.source_controller]
}

resource "k8sconnect_object" "flux_kustomization" {
  yaml_body = <<-YAML
    apiVersion: kustomize.toolkit.fluxcd.io/v1
    kind: Kustomization
    metadata:
      name: flux-system
      namespace: flux-system
    spec:
      interval: 10m
      sourceRef:
        kind: GitRepository
        name: flux-system
      path: ./clusters/my-cluster
      prune: true
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_wait.source_controller]
}
