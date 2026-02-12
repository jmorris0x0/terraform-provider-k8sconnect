# Phase 14: Helm Release Testing
# Tests helm_release resource with local charts, repo charts,
# plan-time validation, new features, and error scenarios.

#############################################
# HELM TEST 1: Local Chart (simple-test)
# Basic happy path with local chart
#############################################
resource "k8sconnect_helm_release" "local_chart" {
  name      = "qa-local"
  namespace = "qa-helm"
  chart     = "../../test/testdata/charts/simple-test"

  values = <<-YAML
    replicaCount: 1
  YAML

  cluster          = local.cluster
  create_namespace = true
  wait             = false  # simple-test has no readiness probe
  timeout          = "2m"
  description      = "QA local chart test"
  max_history      = 5

  depends_on = [k8sconnect_object.namespace]
}

#############################################
# HELM TEST 2: OCI Registry Chart (bitnami/nginx)
# Tests OCI repository, wait, set, set_sensitive,
# Docker credential chain (zero-config public OCI)
#############################################
resource "k8sconnect_helm_release" "repo_chart" {
  name       = "qa-nginx"
  namespace  = "qa-helm"
  repository = "oci://registry-1.docker.io/bitnamicharts"
  chart      = "nginx"
  version    = "22.4.7"

  values = <<-YAML
    replicaCount: 1
    service:
      type: ClusterIP
    resources:
      requests:
        memory: "64Mi"
        cpu: "50m"
      limits:
        memory: "128Mi"
        cpu: "100m"
  YAML

  set = [
    {
      name  = "image.pullPolicy"
      value = "IfNotPresent"
    }
  ]

  set_sensitive = [
    {
      name  = "podAnnotations.secret-token"
      value = "s3cret-t0ken-value"
    }
  ]

  cluster          = local.cluster
  create_namespace = true  # namespace already exists from test 1, should be fine
  wait             = true
  timeout          = "10m"
  description      = "QA OCI repo chart test"
  max_history      = 3

  depends_on = [k8sconnect_helm_release.local_chart]
}

#############################################
# HELM TEST 3: Upgrade with reuse_values
# Upgrades local chart with value override
#############################################
# (This test is validated manually by changing values
# and running apply again. reuse_values=true is tested
# in acceptance tests. Here we just confirm the attribute
# is accepted without error.)
resource "k8sconnect_helm_release" "reuse_test" {
  name      = "qa-reuse"
  namespace = "qa-helm"
  chart     = "../../test/testdata/charts/simple-test"

  values = <<-YAML
    replicaCount: 1
  YAML

  cluster          = local.cluster
  create_namespace = true
  wait             = false
  reuse_values     = false  # explicit default
  description      = "QA reuse_values test"

  depends_on = [k8sconnect_helm_release.local_chart]
}

#############################################
# HELM TEST 4: Atomic Install (rollback on failure)
# Uses a valid chart but tests atomic flag acceptance
#############################################
resource "k8sconnect_helm_release" "atomic_test" {
  name      = "qa-atomic"
  namespace = "qa-helm"
  chart     = "../../test/testdata/charts/simple-test"

  cluster          = local.cluster
  create_namespace = true
  wait             = true
  atomic           = true
  timeout          = "2m"

  depends_on = [k8sconnect_helm_release.local_chart]
}

#############################################
# HELM TEST 5: Namespace Creation
# Deploys into a namespace that doesn't exist yet
#############################################
resource "k8sconnect_helm_release" "new_namespace" {
  name      = "qa-newns"
  namespace = "qa-helm-new-namespace"
  chart     = "../../test/testdata/charts/simple-test"

  cluster          = local.cluster
  create_namespace = true
  wait             = false
  description      = "Tests create_namespace with fresh namespace"
}

#############################################
# HELM TEST 6: Disable Hooks
# Verify disable_hooks is accepted
#############################################
resource "k8sconnect_helm_release" "no_hooks" {
  name      = "qa-nohooks"
  namespace = "qa-helm"
  chart     = "../../test/testdata/charts/simple-test"

  cluster          = local.cluster
  create_namespace = true
  wait             = false
  disable_hooks    = true

  depends_on = [k8sconnect_helm_release.local_chart]
}

#############################################
# OUTPUTS - Verify computed attributes
#############################################

output "helm_local_status" {
  description = "Local chart release status"
  value       = k8sconnect_helm_release.local_chart.status
}

output "helm_local_revision" {
  description = "Local chart release revision"
  value       = k8sconnect_helm_release.local_chart.revision
}

output "helm_local_metadata" {
  description = "Local chart release metadata"
  value       = k8sconnect_helm_release.local_chart.metadata
}

output "helm_nginx_status" {
  description = "Nginx repo chart release status"
  value       = k8sconnect_helm_release.repo_chart.status
}

output "helm_nginx_revision" {
  description = "Nginx repo chart release revision"
  value       = k8sconnect_helm_release.repo_chart.revision
}

#############################################
# PLAN-TIME VALIDATION TESTS
# Uncomment one at a time to test error messages
#############################################

# TEST V1: Bad timeout format
# Expected: "Invalid Timeout Format" error at plan time
#
# resource "k8sconnect_helm_release" "bad_timeout" {
#   name      = "qa-bad-timeout"
#   namespace = "qa-helm"
#   chart     = "../../test/testdata/charts/simple-test"
#   timeout   = "five minutes"  # Invalid!
#   cluster   = local.cluster
# }

# TEST V2: OCI chart without version
# Expected: "Version Required for OCI Registry" error at plan time
#
# resource "k8sconnect_helm_release" "oci_no_version" {
#   name       = "qa-oci-noversion"
#   namespace  = "qa-helm"
#   repository = "oci://registry.example.com/charts"
#   chart      = "my-app"
#   # version intentionally omitted
#   cluster    = local.cluster
# }

# TEST V3: Local chart path that doesn't exist
# Expected: "Chart Not Found" error at plan time
#
# resource "k8sconnect_helm_release" "bad_chart_path" {
#   name      = "qa-bad-path"
#   namespace = "qa-helm"
#   chart     = "./nonexistent-chart"
#   cluster   = local.cluster
# }

# TEST V4: Registry config path that doesn't exist
# Expected: "Registry Config Not Found" error at plan time
#
# resource "k8sconnect_helm_release" "bad_registry_config" {
#   name                 = "qa-bad-registry"
#   namespace            = "qa-helm"
#   repository           = "oci://registry.example.com/charts"
#   chart                = "my-app"
#   version              = "1.0.0"
#   registry_config_path = "/nonexistent/docker/config.json"
#   cluster              = local.cluster
# }

#############################################
# ERROR SCENARIO TESTS
# Uncomment to test error message quality
#############################################

# TEST E1: Namespace not found (without create_namespace)
# Expected: Actionable error suggesting create_namespace = true
#
# resource "k8sconnect_helm_release" "ns_not_found" {
#   name      = "qa-nsfail"
#   namespace = "this-namespace-does-not-exist"
#   chart     = "../../test/testdata/charts/simple-test"
#   cluster   = local.cluster
#   create_namespace = false
#   wait      = false
# }

# TEST E2: Timeout scenario (very short timeout with wait)
# Expected: Actionable timeout error with pod diagnostics
#
# resource "k8sconnect_helm_release" "timeout_test" {
#   name       = "qa-timeout"
#   namespace  = "qa-helm"
#   repository = "https://charts.bitnami.com/bitnami"
#   chart      = "nginx"
#   version    = "22.4.7"
#   cluster    = local.cluster
#   create_namespace = true
#   wait       = true
#   timeout    = "5s"  # Too short, will timeout
# }
