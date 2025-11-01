# EDGE CASE TESTS - Pushing the Provider to Its Limits
# These tests explore corner cases and unusual scenarios

#############################################
# TEST 1: Identity Change (Name Change)
# Should force replacement with clear message
#############################################
resource "k8sconnect_object" "identity_test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: identity-test-v2
      namespace: kind-validation
    data:
      version: "2"
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_object.namespace]
}

#############################################
# TEST 2: Large Data Payload
# ConfigMap with very large data field
#############################################
resource "k8sconnect_object" "large_configmap" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: large-configmap
      namespace: kind-validation
    data:
      large-data: "${join("", [for i in range(1000) : "This is line ${i} with some data to make it large enough to test handling of big payloads. "])}"
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_object.namespace]
}

#############################################
# TEST 3: Empty and Null Values
#############################################
resource "k8sconnect_object" "empty_values" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: empty-values-test
      namespace: kind-validation
    data:
      empty-string: ""
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_object.namespace]
}

#############################################
# TEST 4: Binary Data in Secret
#############################################
resource "k8sconnect_object" "binary_secret" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Secret
    metadata:
      name: binary-secret
      namespace: kind-validation
    type: Opaque
    data:
      binary-file: ${base64encode("This is binary data with special chars: ñ é ü 你好 🚀 \n\t\r")}
      text-file: ${base64encode("Plain text data")}
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_object.namespace]
}

#############################################
# TEST 5: Unicode and Special Characters
#############################################
resource "k8sconnect_object" "unicode_test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: unicode-test
      namespace: kind-validation
      labels:
        emoji: "rocket"
    data:
      english: "Hello World"
      spanish: "Hola Mundo ñ á é í ó ú"
      chinese: "你好世界"
      japanese: "こんにちは世界"
      emoji: "🚀 🎉 💯 ✅ ❌"
      special: "Tab:\t Newline:\n Quote:' Backslash:\\\\"
      json: '{"key": "value with spaces", "number": 123}'
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_object.namespace]
}

#############################################
# TEST 6: Wait on Already-Ready Resource
# Should complete immediately without timeout
#############################################
resource "k8sconnect_wait" "wait_on_ready_namespace" {
  object_ref = k8sconnect_object.namespace.object_ref
  wait_for = {
    field_value = {
      "status.phase" = "Active"
    }
    timeout = "10s"
  }
  cluster = local.cluster
}

#############################################
# TEST 7: Resource with Finalizer
# Test deletion behavior when finalizer blocks
#############################################
resource "k8sconnect_object" "finalizer_test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: finalizer-test
      namespace: kind-validation
      finalizers:
        - kubernetes.io/pvc-protection
    data:
      test: "value"
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_object.namespace]
}

#############################################
# TEST 8: Very Long Resource Name
# K8s has 253 char limit for names
#############################################
resource "k8sconnect_object" "long_name" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: this-is-a-very-long-resource-name-that-tests-the-kubernetes-name-length-limits-and-how-the-provider-handles-them-maximum-is-253-characters-for-dns-subdomain-names-lets-see-what-happens-with-a-name-that-approaches-that-limit-but-stays-valid
      namespace: kind-validation
    data:
      test: "long-name"
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_object.namespace]
}

#############################################
# TEST 9: Deployment with Very Long Container Args
# Tests handling of large array fields
#############################################
resource "k8sconnect_object" "long_args_deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: long-args-test
      namespace: kind-validation
    spec:
      replicas: 1
      selector:
        matchLabels:
          app: long-args
      template:
        metadata:
          labels:
            app: long-args
        spec:
          containers:
          - name: app
            image: public.ecr.aws/docker/library/busybox:latest
            command: ["sh", "-c", "sleep 3600"]
            args:
              - "--option1=value1"
              - "--option2=value2"
              - "--option3=value3"
              - "--option4=value4"
              - "--option5=value5"
              - "--option6=value6"
              - "--option7=value7"
              - "--option8=value8"
              - "--option9=value9"
              - "--option10=value10"
            resources:
              requests:
                memory: "16Mi"
                cpu: "25m"
  YAML
  cluster    = local.cluster
  depends_on = [k8sconnect_object.namespace]
}

#############################################
# TEST 10: Empty Deployment (No Containers)
# Should fail validation - let's see error message
#############################################
# resource "k8sconnect_object" "empty_deployment" {
#   yaml_body = <<-YAML
#     apiVersion: apps/v1
#     kind: Deployment
#     metadata:
#       name: empty-deployment
#       namespace: kind-validation
#     spec:
#       replicas: 1
#       selector:
#         matchLabels:
#           app: empty
#       template:
#         metadata:
#           labels:
#             app: empty
#         spec:
#           containers: []
#   YAML
#   cluster    = local.cluster
#   depends_on = [k8sconnect_object.namespace]
# }
