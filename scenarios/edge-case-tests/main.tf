terraform {
  required_providers {
    kind = {
      source  = "tehcyx/kind"
      version = "~> 0.6"
    }
    k8sconnect = {
      source  = "local/k8sconnect"
      version = "0.1.0"
    }
  }
}

provider "k8sconnect" {}

resource "kind_cluster" "test" {
  name           = "identity-test"
  node_image     = "kindest/node:v1.31.0"
  wait_for_ready = true
  kind_config {
    kind        = "Cluster"
    api_version = "kind.x-k8s.io/v1alpha4"
    node {
      role = "control-plane"
    }
  }
}

locals {
  cluster_connection = {
    host                   = kind_cluster.test.endpoint
    cluster_ca_certificate = base64encode(kind_cluster.test.cluster_ca_certificate)
    client_certificate     = base64encode(kind_cluster.test.client_certificate)
    client_key             = base64encode(kind_cluster.test.client_key)
  }
}

# Test namespace
resource "k8sconnect_object" "test_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: identity-test
  YAML
  cluster_connection = local.cluster_connection
}

# Test resource for identity changes
resource "k8sconnect_object" "test_configmap" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-config
      namespace: identity-test
    data:
      foo: bar
  YAML
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_object.test_namespace]
}

# Test resource for immutable field changes
resource "k8sconnect_object" "test_service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: test-service
      namespace: identity-test
    spec:
      type: ClusterIP
      clusterIP: 10.96.100.100
      selector:
        app: test
      ports:
      - port: 80
        targetPort: 8080
  YAML
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_object.test_namespace]
}

# Test reading external resource (alternative to import)
data "k8sconnect_object" "import_test_read" {
  api_version = "v1"
  kind = "ConfigMap"
  name = "external-import-test"
  namespace = "identity-test"
  cluster_connection = local.cluster_connection
  depends_on = [k8sconnect_object.test_namespace]
}

output "external_resource_data" {
  value = yamldecode(data.k8sconnect_object.import_test_read.yaml_body).data
}
