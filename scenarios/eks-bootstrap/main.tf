# EKS Bootstrap Test
#
# This configuration tests the critical bootstrap scenario:
# Creating an EKS cluster and deploying workloads in a SINGLE terraform apply.
#
# This validates:
# - Inline cluster connections work with "known after apply" values
# - apply_timeout (when implemented) handles cluster startup delays
# - Provider can connect to cluster as soon as API server is ready
#
# Expected timeline:
# - t=0s: EKS cluster creation starts
# - t=30s: AWS returns (endpoint available, cluster still provisioning)
# - t=2-10m: API server becomes ready
# - t=2-10m+: Workloads deployed
#
# IMPORTANT: Set apply_timeout appropriately for EKS clusters!
# Default 2m may be insufficient - recommend 10m for EKS bootstrap.

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    k8sconnect = {
      source  = "local/k8sconnect"
      version = ">= 0.1.0"
    }
  }
  required_version = ">= 1.6"
}

provider "aws" {
  region = var.aws_region
}

provider "k8sconnect" {}

variable "aws_region" {
  description = "AWS region for EKS cluster"
  type        = string
  default     = "us-west-2"
}

variable "cluster_name" {
  description = "Name of the EKS cluster"
  type        = string
  default     = "k8sconnect-bootstrap-test"
}

#############################################
# VPC AND NETWORKING
#############################################

# Use default VPC for simplicity (production would use custom VPC)
data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
}

#############################################
# IAM ROLES
#############################################

# EKS cluster role
resource "aws_iam_role" "eks_cluster" {
  name = "${var.cluster_name}-cluster-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "eks.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "eks_cluster_policy" {
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
  role       = aws_iam_role.eks_cluster.name
}

# EKS node group role
resource "aws_iam_role" "eks_node_group" {
  name = "${var.cluster_name}-node-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ec2.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "eks_worker_node_policy" {
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy"
  role       = aws_iam_role.eks_node_group.name
}

resource "aws_iam_role_policy_attachment" "eks_cni_policy" {
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy"
  role       = aws_iam_role.eks_node_group.name
}

resource "aws_iam_role_policy_attachment" "eks_container_registry_policy" {
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
  role       = aws_iam_role.eks_node_group.name
}

#############################################
# EKS CLUSTER
#############################################

resource "aws_eks_cluster" "main" {
  name     = var.cluster_name
  role_arn = aws_iam_role.eks_cluster.arn

  vpc_config {
    subnet_ids = data.aws_subnets.default.ids
  }

  depends_on = [
    aws_iam_role_policy_attachment.eks_cluster_policy,
  ]
}

# Node group - small for testing
resource "aws_eks_node_group" "main" {
  cluster_name    = aws_eks_cluster.main.name
  node_group_name = "${var.cluster_name}-nodes"
  node_role_arn   = aws_iam_role.eks_node_group.arn
  subnet_ids      = data.aws_subnets.default.ids

  scaling_config {
    desired_size = 2
    max_size     = 3
    min_size     = 1
  }

  instance_types = ["t3.small"]

  depends_on = [
    aws_iam_role_policy_attachment.eks_worker_node_policy,
    aws_iam_role_policy_attachment.eks_cni_policy,
    aws_iam_role_policy_attachment.eks_container_registry_policy,
  ]
}

#############################################
# CLUSTER CONNECTION
#############################################

# This is the critical part - inline connection with exec auth
# The endpoint and certificate_authority are "known after apply"
# The provider MUST handle this gracefully during bootstrap
locals {
  cluster_connection = {
    host                   = aws_eks_cluster.main.endpoint
    cluster_ca_certificate = aws_eks_cluster.main.certificate_authority[0].data
    exec = {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "aws"
      args = [
        "eks",
        "get-token",
        "--cluster-name",
        aws_eks_cluster.main.name,
        "--region",
        var.aws_region,
      ]
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
        test: eks-bootstrap
  YAML

  cluster_connection = local.cluster_connection

  # When apply_timeout is implemented, uncomment:
  # apply_timeout = "10m"  # EKS needs longer than default 2m

  depends_on = [
    aws_eks_cluster.main,
    aws_eks_node_group.main,
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
            image: nginx:1.25
            ports:
            - containerPort: 80
  YAML

  cluster_connection = local.cluster_connection

  # When apply_timeout is implemented, uncomment:
  # apply_timeout = "10m"

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
      cluster_type: eks
      test_date: "${timestamp()}"
      provider: k8sconnect
  YAML

  cluster_connection = local.cluster_connection

  depends_on = [k8sconnect_object.namespace]
}

#############################################
# OUTPUTS
#############################################

output "cluster_endpoint" {
  description = "EKS cluster endpoint"
  value       = aws_eks_cluster.main.endpoint
}

output "cluster_name" {
  description = "EKS cluster name"
  value       = aws_eks_cluster.main.name
}

output "cluster_certificate_authority" {
  description = "EKS cluster CA certificate"
  value       = aws_eks_cluster.main.certificate_authority[0].data
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
  value       = "aws eks update-kubeconfig --region ${var.aws_region} --name ${aws_eks_cluster.main.name}"
}
