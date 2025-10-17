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
  }
  required_version = ">= 1.6"
}

#############################################
# CLUSTER SETUP
#############################################

resource "kind_cluster" "dogfood" {
  name           = "dogfood"
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

provider "k8sconnect" {}

locals {
  cluster_connection = {
    host                   = kind_cluster.dogfood.endpoint
    cluster_ca_certificate = base64encode(kind_cluster.dogfood.cluster_ca_certificate)
    client_certificate     = base64encode(kind_cluster.dogfood.client_certificate)
    client_key             = base64encode(kind_cluster.dogfood.client_key)
  }
}

#############################################
# DATASOURCE TESTS
#############################################

# Test yaml_split datasource - splits multi-doc YAML
data "k8sconnect_yaml_split" "multi_resources" {
  content = file("${path.module}/multi-resources.yaml")
}

# Test yaml_scoped datasource - separates cluster vs namespaced resources
data "k8sconnect_yaml_scoped" "mixed_scope" {
  content = file("${path.module}/mixed-scope.yaml")
}

#############################################
# NAMESPACE CREATION
#############################################

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: dogfood
      labels:
        purpose: dogfooding
        version: v0.1.0
  YAML
  cluster_connection = local.cluster_connection
}

#############################################
# CLUSTER-SCOPED RESOURCES FROM DATASOURCE
#############################################

# Apply cluster-scoped resources from yaml_scoped datasource
resource "k8sconnect_object" "cluster_scoped" {
  for_each           = data.k8sconnect_yaml_scoped.mixed_scope.cluster_scoped
  yaml_body          = each.value
  cluster_connection = local.cluster_connection
}

#############################################
# NAMESPACE-SCOPED RESOURCES FROM yaml_split
#############################################

# Apply all resources from multi-resources.yaml using yaml_split
resource "k8sconnect_object" "split_resources" {
  for_each           = data.k8sconnect_yaml_split.multi_resources.manifests
  yaml_body          = each.value
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# NAMESPACE-SCOPED RESOURCES FROM yaml_scoped
#############################################

# Apply namespace-scoped resources from yaml_scoped datasource
resource "k8sconnect_object" "namespaced_scoped" {
  for_each           = data.k8sconnect_yaml_scoped.mixed_scope.namespaced
  yaml_body          = each.value
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.cluster_scoped]
}

#############################################
# STORAGE TESTS - PVC with field_value wait
#############################################

# Create a PVC (kind provides local-path storage)
resource "k8sconnect_object" "test_pvc" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: test-pvc
      namespace: dogfood
    spec:
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 100Mi
      storageClassName: standard
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

# Wait for PVC to be bound and extract volume name
resource "k8sconnect_wait" "pvc_bound" {
  object_ref = k8sconnect_object.test_pvc.object_ref
  wait_for = {
    field_value = {
      "status.phase" = "Bound"
    }
    timeout = "60s"
  }
  cluster_connection = local.cluster_connection
}

# Also test field wait (just existence) on the same PVC
resource "k8sconnect_wait" "pvc_volume_name" {
  object_ref = k8sconnect_object.test_pvc.object_ref
  wait_for = {
    field   = "spec.volumeName"
    timeout = "60s"
  }
  cluster_connection = local.cluster_connection
}

#############################################
# JOB TESTS - with condition wait
#############################################

resource "k8sconnect_object" "migration_job" {
  yaml_body = <<-YAML
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: migration-job
      namespace: dogfood
      labels:
        purpose: migration
    spec:
      template:
        spec:
          containers:
          - name: migrate
            image: busybox:1.28
            command: ["sh", "-c", "echo 'Running migration...' && sleep 5 && echo 'Migration complete'"]
            resources:
              requests:
                memory: "32Mi"
                cpu: "50m"
              limits:
                memory: "64Mi"
                cpu: "100m"
          restartPolicy: Never
      backoffLimit: 2
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

# Wait for job completion using condition wait
resource "k8sconnect_wait" "migration_complete" {
  object_ref = k8sconnect_object.migration_job.object_ref
  wait_for = {
    condition = "Complete"
    timeout   = "120s"
  }
  cluster_connection = local.cluster_connection
}

#############################################
# CRONJOB TEST
#############################################

resource "k8sconnect_object" "backup_cronjob" {
  yaml_body = <<-YAML
    apiVersion: batch/v1
    kind: CronJob
    metadata:
      name: backup-cronjob
      namespace: dogfood
      labels:
        purpose: backup
    spec:
      schedule: "0 2 * * *"
      jobTemplate:
        spec:
          template:
            spec:
              containers:
              - name: backup
                image: busybox:1.28
                command: ["sh", "-c", "echo 'Performing backup...'"]
                resources:
                  requests:
                    memory: "32Mi"
                    cpu: "50m"
                  limits:
                    memory: "64Mi"
                    cpu: "100m"
              restartPolicy: OnFailure
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# REPLICASET TEST (standalone workload)
#############################################

resource "k8sconnect_object" "replicaset" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: ReplicaSet
    metadata:
      name: frontend-replicaset
      namespace: dogfood
      labels:
        app: frontend
        tier: web
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: frontend
      template:
        metadata:
          labels:
            app: frontend
            tier: web
        spec:
          containers:
          - name: nginx
            image: public.ecr.aws/nginx/nginx:1.21
            ports:
            - containerPort: 80
            resources:
              requests:
                memory: "32Mi"
                cpu: "50m"
              limits:
                memory: "64Mi"
                cpu: "100m"
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# STANDALONE POD TEST (workload type)
#############################################

resource "k8sconnect_object" "standalone_pod" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Pod
    metadata:
      name: standalone-pod
      namespace: dogfood
      labels:
        app: standalone
        type: test
    spec:
      containers:
      - name: app
        image: busybox:1.28
        command: ["sh", "-c", "while true; do echo 'Running...'; sleep 300; done"]
        resources:
          requests:
            memory: "16Mi"
            cpu: "25m"
          limits:
            memory: "32Mi"
            cpu: "50m"
      restartPolicy: Always
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

# Wait for pod to be running
resource "k8sconnect_wait" "pod_running" {
  object_ref = k8sconnect_object.standalone_pod.object_ref
  wait_for = {
    field_value = {
      "status.phase" = "Running"
    }
    timeout = "60s"
  }
  cluster_connection = local.cluster_connection
}

#############################################
# DEPLOYMENT TESTS - with rollout wait
#############################################

resource "k8sconnect_object" "web_deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: web-deployment
      namespace: dogfood
      labels:
        app: web
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: web
      template:
        metadata:
          labels:
            app: web
            version: v1
        spec:
          containers:
          - name: nginx
            image: public.ecr.aws/nginx/nginx:1.21
            ports:
            - containerPort: 80
              name: http
            resources:
              requests:
                memory: "64Mi"
                cpu: "50m"
              limits:
                memory: "128Mi"
                cpu: "100m"
            readinessProbe:
              httpGet:
                path: /
                port: 80
              initialDelaySeconds: 5
              periodSeconds: 5
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace, k8sconnect_wait.migration_complete]
}

# Wait for deployment rollout using rollout wait
resource "k8sconnect_wait" "web_rollout" {
  object_ref = k8sconnect_object.web_deployment.object_ref
  wait_for = {
    rollout = true
    timeout = "120s"
  }
  cluster_connection = local.cluster_connection
}

#############################################
# SERVICE TEST
#############################################

resource "k8sconnect_object" "web_service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: web-service
      namespace: dogfood
      labels:
        app: web
    spec:
      type: NodePort
      selector:
        app: web
      ports:
      - port: 80
        targetPort: 80
        nodePort: 30080
        protocol: TCP
        name: http
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# NETWORK POLICY TEST
#############################################

resource "k8sconnect_object" "network_policy" {
  yaml_body = <<-YAML
    apiVersion: networking.k8s.io/v1
    kind: NetworkPolicy
    metadata:
      name: web-network-policy
      namespace: dogfood
    spec:
      podSelector:
        matchLabels:
          app: web
      policyTypes:
      - Ingress
      - Egress
      ingress:
      - from:
        - podSelector:
            matchLabels:
              role: frontend
        ports:
        - protocol: TCP
          port: 80
      egress:
      - to:
        - podSelector:
            matchLabels:
              role: database
        ports:
        - protocol: TCP
          port: 5432
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# STATEFULSET TEST
#############################################

resource "k8sconnect_object" "database_statefulset" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: StatefulSet
    metadata:
      name: postgres
      namespace: dogfood
    spec:
      serviceName: postgres
      replicas: 1
      selector:
        matchLabels:
          app: postgres
      template:
        metadata:
          labels:
            app: postgres
        spec:
          containers:
          - name: postgres
            image: postgres:14-alpine
            ports:
            - containerPort: 5432
              name: postgres
            env:
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: app-secrets
                  key: database.password
            resources:
              requests:
                memory: "128Mi"
                cpu: "100m"
              limits:
                memory: "256Mi"
                cpu: "200m"
            volumeMounts:
            - name: data
              mountPath: /var/lib/postgresql/data
      volumeClaimTemplates:
      - metadata:
          name: data
        spec:
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: 500Mi
  YAML
  cluster_connection = local.cluster_connection
  depends_on = [
    k8sconnect_object.namespace,
    k8sconnect_object.split_resources
  ]
}

# Wait for StatefulSet to be ready
resource "k8sconnect_wait" "postgres_ready" {
  object_ref = k8sconnect_object.database_statefulset.object_ref
  wait_for = {
    rollout = true
    timeout = "180s"
  }
  cluster_connection = local.cluster_connection
}

# StatefulSet headless service
resource "k8sconnect_object" "postgres_service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: postgres
      namespace: dogfood
    spec:
      clusterIP: None
      selector:
        app: postgres
      ports:
      - port: 5432
        targetPort: 5432
        name: postgres
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# DAEMONSET TEST
#############################################

resource "k8sconnect_object" "log_collector" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: DaemonSet
    metadata:
      name: log-collector
      namespace: dogfood
    spec:
      selector:
        matchLabels:
          name: log-collector
      template:
        metadata:
          labels:
            name: log-collector
        spec:
          containers:
          - name: fluentd
            image: fluent/fluentd:v1.14-1
            resources:
              requests:
                memory: "64Mi"
                cpu: "50m"
              limits:
                memory: "128Mi"
                cpu: "100m"
            volumeMounts:
            - name: varlog
              mountPath: /var/log
              readOnly: true
          volumes:
          - name: varlog
            hostPath:
              path: /var/log
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# INGRESS TEST
#############################################

resource "k8sconnect_object" "web_ingress" {
  yaml_body = <<-YAML
    apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: web-ingress
      namespace: dogfood
      annotations:
        nginx.ingress.kubernetes.io/rewrite-target: /
    spec:
      ingressClassName: nginx
      rules:
      - host: dogfood.local
        http:
          paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: web-service
                port:
                  number: 80
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.web_service]
}

#############################################
# POD DISRUPTION BUDGET TEST
#############################################

resource "k8sconnect_object" "web_pdb" {
  yaml_body = <<-YAML
    apiVersion: policy/v1
    kind: PodDisruptionBudget
    metadata:
      name: web-pdb
      namespace: dogfood
    spec:
      minAvailable: 1
      selector:
        matchLabels:
          app: web
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.web_deployment]
}

#############################################
# CLUSTER-SCOPED RBAC (ClusterRole, ClusterRoleBinding)
#############################################

resource "k8sconnect_object" "metrics_reader_role" {
  yaml_body = <<-YAML
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRole
    metadata:
      name: dogfood-metrics-reader
    rules:
    - apiGroups: ["metrics.k8s.io"]
      resources: ["pods", "nodes"]
      verbs: ["get", "list"]
    - apiGroups: [""]
      resources: ["pods", "nodes"]
      verbs: ["get", "list"]
  YAML
  cluster_connection = local.cluster_connection
}

resource "k8sconnect_object" "metrics_reader_binding" {
  yaml_body = <<-YAML
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: dogfood-metrics-reader-binding
    subjects:
    - kind: ServiceAccount
      name: app-service-account
      namespace: dogfood
    roleRef:
      kind: ClusterRole
      name: dogfood-metrics-reader
      apiGroup: rbac.authorization.k8s.io
  YAML
  cluster_connection = local.cluster_connection
  depends_on = [
    k8sconnect_object.metrics_reader_role,
    k8sconnect_object.split_resources
  ]
}

#############################################
# STORAGE CLASS TEST (cluster-scoped)
#############################################

resource "k8sconnect_object" "fast_storage" {
  yaml_body = <<-YAML
    apiVersion: storage.k8s.io/v1
    kind: StorageClass
    metadata:
      name: fast-storage
    provisioner: rancher.io/local-path
    volumeBindingMode: WaitForFirstConsumer
    reclaimPolicy: Delete
  YAML
  cluster_connection = local.cluster_connection
}

#############################################
# PRIORITY CLASS TEST (cluster-scoped)
#############################################

resource "k8sconnect_object" "high_priority" {
  yaml_body = <<-YAML
    apiVersion: scheduling.k8s.io/v1
    kind: PriorityClass
    metadata:
      name: high-priority
    value: 1000
    globalDefault: false
    description: "High priority workloads"
  YAML
  cluster_connection = local.cluster_connection
}

# Create a deployment using the priority class
resource "k8sconnect_object" "priority_deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: priority-deployment
      namespace: dogfood
    spec:
      replicas: 1
      selector:
        matchLabels:
          app: priority-test
      template:
        metadata:
          labels:
            app: priority-test
        spec:
          priorityClassName: high-priority
          containers:
          - name: app
            image: busybox:1.28
            command: ["sh", "-c", "sleep 3600"]
            resources:
              requests:
                memory: "16Mi"
                cpu: "25m"
              limits:
                memory: "32Mi"
                cpu: "50m"
  YAML
  cluster_connection = local.cluster_connection
  depends_on = [
    k8sconnect_object.namespace,
    k8sconnect_object.high_priority
  ]
}

#############################################
# HORIZONTAL POD AUTOSCALER (HPA) TEST
#############################################

resource "k8sconnect_object" "web_hpa" {
  yaml_body = <<-YAML
    apiVersion: autoscaling/v2
    kind: HorizontalPodAutoscaler
    metadata:
      name: web-hpa
      namespace: dogfood
    spec:
      scaleTargetRef:
        apiVersion: apps/v1
        kind: Deployment
        name: web-deployment
      minReplicas: 2
      maxReplicas: 5
      metrics:
      - type: Resource
        resource:
          name: cpu
          target:
            type: Utilization
            averageUtilization: 80
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.web_deployment]
  ignore_fields = [
    "spec.replicas" # HPA will manage this
  ]
}

#############################################
# ENDPOINT SLICE TEST (networking)
#############################################

# Note: EndpointSlices are typically auto-created by K8s from Services,
# but we can create custom ones for testing
resource "k8sconnect_object" "custom_endpoints" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Endpoints
    metadata:
      name: external-service
      namespace: dogfood
    subsets:
    - addresses:
      - ip: 192.168.1.100
      ports:
      - port: 80
        protocol: TCP
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

# Service for the custom endpoints
resource "k8sconnect_object" "external_service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: external-service
      namespace: dogfood
    spec:
      type: ClusterIP
      ports:
      - port: 80
        targetPort: 80
        protocol: TCP
  YAML
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# PATCH RESOURCE TESTS
#############################################

# Test 1: Strategic Merge Patch on Deployment WE OWN (DISABLED - cannot patch own resources)
# The provider correctly prevents patching resources we already manage with k8sconnect_object
# resource "k8sconnect_patch" "deployment_strategic" {
#   target = {
#     api_version = "apps/v1"
#     kind        = "Deployment"
#     name        = "web-deployment"
#     namespace   = "dogfood"
#   }
#   patch = jsonencode({
#     metadata = {
#       annotations = {
#         "patched-by"    = "k8sconnect"
#         "patch-type"    = "strategic-merge"
#         "patch-version" = "1"
#       }
#     }
#     spec = {
#       replicas = 3 # Scale up from 2 to 3
#     }
#   })
#   cluster_connection = local.cluster_connection
#   depends_on         = [k8sconnect_wait.web_rollout]
# }

# Test 2: JSON Patch on ConfigMap WE OWN (DISABLED - cannot patch own resources)
# The provider correctly prevents patching resources we already manage with k8sconnect_object
# resource "k8sconnect_patch" "configmap_json" {
#   target = {
#     api_version = "v1"
#     kind        = "ConfigMap"
#     name        = "app-config"
#     namespace   = "dogfood"
#   }
#   json_patch = jsonencode([
#     {
#       op    = "add"
#       path  = "/data/cache.enabled"
#       value = "true"
#     },
#     {
#       op    = "add"
#       path  = "/data/cache.ttl"
#       value = "3600"
#     }
#   ])
#   cluster_connection = local.cluster_connection
#   depends_on         = [k8sconnect_object.split_resources]
# }

# Test 3: Merge Patch on Service WE OWN (DISABLED - cannot patch own resources)
# The provider correctly prevents patching resources we already manage with k8sconnect_object
# resource "k8sconnect_patch" "service_merge" {
#   target = {
#     api_version = "v1"
#     kind        = "Service"
#     name        = "web-service"
#     namespace   = "dogfood"
#   }
#   merge_patch = jsonencode({
#     metadata = {
#       labels = {
#         "patched"     = "true"
#         "patch-type"  = "merge"
#         "environment" = "dogfood"
#       }
#     }
#   })
#   cluster_connection = local.cluster_connection
# }

# Test 4: Patch EXTERNAL resource (created by kubectl, not k8sconnect_object)
resource "k8sconnect_patch" "external_deployment_patch" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "external-app"
    namespace   = "dogfood"
  }
  patch = jsonencode({
    metadata = {
      annotations = {
        "patched-externally" = "true"
        "patch-timestamp"    = "v1"
      }
    }
  })
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_wait.external_app_rollout]
}

#############################################
# WAIT ON EXTERNAL RESOURCE (one we don't own)
#############################################

# Create a deployment that we will NOT manage via k8sconnect_object
# Instead, we'll use kubectl directly via a local-exec provisioner
resource "null_resource" "external_deployment" {
  depends_on = [k8sconnect_object.namespace]

  provisioner "local-exec" {
    command = <<-EOT
      kubectl --kubeconfig ${kind_cluster.dogfood.kubeconfig_path} apply -f - <<EOF
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: external-app
        namespace: dogfood
        labels:
          app: external
          managed-by: kubectl
      spec:
        replicas: 1
        selector:
          matchLabels:
            app: external
        template:
          metadata:
            labels:
              app: external
          spec:
            containers:
            - name: app
              image: busybox:1.28
              command: ["sh", "-c", "sleep 3600"]
              resources:
                requests:
                  memory: "32Mi"
                  cpu: "50m"
                limits:
                  memory: "64Mi"
                  cpu: "100m"
      EOF
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = "kubectl --kubeconfig ${self.triggers.kubeconfig_path} delete deployment external-app -n dogfood --ignore-not-found=true"
  }

  triggers = {
    kubeconfig_path = kind_cluster.dogfood.kubeconfig_path
  }
}

# Wait for the external deployment to roll out
resource "k8sconnect_wait" "external_app_rollout" {
  object_ref = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "external-app"
    namespace   = "dogfood"
  }
  wait_for = {
    rollout = true
    timeout = "120s"
  }
  cluster_connection = local.cluster_connection
  depends_on         = [null_resource.external_deployment]
}

#############################################
# OBJECT DATASOURCE TEST
#############################################

# Read back a deployed resource using object datasource
data "k8sconnect_object" "namespace_info" {
  api_version        = "v1"
  kind               = "Namespace"
  name               = "dogfood"
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.namespace]
}

# Read the ConfigMap to verify it exists
data "k8sconnect_object" "app_config" {
  api_version        = "v1"
  kind               = "ConfigMap"
  name               = "app-config"
  namespace          = "dogfood"
  cluster_connection = local.cluster_connection
  depends_on         = [k8sconnect_object.split_resources]
}

#############################################
# OUTPUTS
#############################################

output "cluster_endpoint" {
  description = "Kind cluster API endpoint"
  value       = kind_cluster.dogfood.endpoint
}

output "namespace_uid" {
  description = "UID of the dogfood namespace (from datasource)"
  value       = yamldecode(data.k8sconnect_object.namespace_info.yaml_body).metadata.uid
}

output "pvc_volume_name" {
  description = "Volume name assigned to PVC (from field wait)"
  value       = k8sconnect_wait.pvc_volume_name.status.spec.volumeName
}

output "pvc_phase" {
  description = "PVC phase (from field_value wait)"
  value       = k8sconnect_wait.pvc_bound.status.status.phase
}

output "configmap_data" {
  description = "ConfigMap data"
  value       = yamldecode(data.k8sconnect_object.app_config.yaml_body).data
}

output "yaml_split_count" {
  description = "Number of documents from yaml_split datasource"
  value       = length(data.k8sconnect_yaml_split.multi_resources.manifests)
}

output "yaml_scoped_cluster_count" {
  description = "Number of cluster-scoped resources from yaml_scoped"
  value       = length(data.k8sconnect_yaml_scoped.mixed_scope.cluster_scoped)
}

output "yaml_scoped_namespaced_count" {
  description = "Number of namespace-scoped resources from yaml_scoped"
  value       = length(data.k8sconnect_yaml_scoped.mixed_scope.namespaced)
}
