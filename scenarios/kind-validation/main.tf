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
  required_version = ">= 1.6"
}

#############################################
# CLUSTER SETUP
#############################################

resource "kind_cluster" "kind_validation" {
  name           = "kind-validation"
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
  cluster = {
    host                   = kind_cluster.kind_validation.endpoint
    cluster_ca_certificate = base64encode(kind_cluster.kind_validation.cluster_ca_certificate)
    client_certificate     = base64encode(kind_cluster.kind_validation.client_certificate)
    client_key             = base64encode(kind_cluster.kind_validation.client_key)
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
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: kind-validation
      labels:
        purpose: validation
        version: v0.1.0
  YAML
  cluster = local.cluster
}

#############################################
# CRD + CR AUTO-RETRY TEST (HEADLINE FEATURE)
#############################################

# Create a CRD with non-standard plural (cacti) to test DiscoverGVR
resource "k8sconnect_object" "cactus_crd" {
  yaml_body          = file("${path.module}/cactus-crd.yaml")
  cluster = local.cluster
}

# Create CR immediately - should auto-retry until CRD is ready
# NO depends_on! This proves zero-configuration auto-retry
resource "k8sconnect_object" "my_cactus" {
  yaml_body = <<-YAML
    apiVersion: plants.example.com/v1
    kind: Cactus
    metadata:
      name: prickly-pear
      namespace: kind-validation
    spec:
      height: "6ft"
      species: "Opuntia"
  YAML

  cluster = local.cluster
  # Intentionally NO depends_on - tests auto-retry when CRD doesn't exist yet
}

#############################################
# CLUSTER-SCOPED RESOURCES FROM DATASOURCE
#############################################

# Apply cluster-scoped resources from yaml_scoped datasource
resource "k8sconnect_object" "cluster_scoped" {
  for_each           = data.k8sconnect_yaml_scoped.mixed_scope.cluster_scoped
  yaml_body          = each.value
  cluster = local.cluster
}

#############################################
# NAMESPACE-SCOPED RESOURCES FROM yaml_split
#############################################

# Apply all resources from multi-resources.yaml using yaml_split
resource "k8sconnect_object" "split_resources" {
  for_each           = data.k8sconnect_yaml_split.multi_resources.manifests
  yaml_body          = each.value
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# NAMESPACE-SCOPED RESOURCES FROM yaml_scoped
#############################################

# Apply namespace-scoped resources from yaml_scoped datasource
resource "k8sconnect_object" "namespaced_scoped" {
  for_each           = data.k8sconnect_yaml_scoped.mixed_scope.namespaced
  yaml_body          = each.value
  cluster = local.cluster
  depends_on         = [k8sconnect_object.cluster_scoped]
}

#############################################
# STORAGE TESTS - PVC with field_value wait
#############################################

# Create a PVC (kind provides local-path storage)
resource "k8sconnect_object" "test_pvc" {
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: test-pvc
      namespace: kind-validation
    spec:
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 100Mi
      storageClassName: standard
  YAML
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

# Create a pod that uses the PVC (required for WaitForFirstConsumer binding)
resource "k8sconnect_object" "pvc_consumer_pod" {
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: Pod
    metadata:
      name: pvc-consumer
      namespace: kind-validation
      labels:
        app: pvc-test
    spec:
      containers:
      - name: consumer
        image: public.ecr.aws/docker/library/busybox:latest
        command: ["sh", "-c", "echo 'PVC mounted' && sleep 3600"]
        resources:
          requests:
            memory: "16Mi"
            cpu: "25m"
          limits:
            memory: "32Mi"
            cpu: "50m"
        volumeMounts:
        - name: storage
          mountPath: /data
      volumes:
      - name: storage
        persistentVolumeClaim:
          claimName: test-pvc
  YAML
  cluster = local.cluster
  depends_on         = [k8sconnect_object.test_pvc]
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
  cluster = local.cluster
}

# Also test field wait (just existence) on the same PVC
resource "k8sconnect_wait" "pvc_volume_name" {
  object_ref = k8sconnect_object.test_pvc.object_ref
  wait_for = {
    field   = "spec.volumeName"
    timeout = "60s"
  }
  cluster = local.cluster
}

# Now test that we can extract the volume name from the wait result
output "extracted_volume_name" {
  description = "Volume name extracted from PVC spec via wait resource"
  value       = k8sconnect_wait.pvc_volume_name.result.spec.volumeName
}

#############################################
# JOB TESTS - with condition wait
#############################################

resource "k8sconnect_object" "migration_job" {
  yaml_body          = <<-YAML
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: migration-job
      namespace: kind-validation
      labels:
        purpose: migration
    spec:
      template:
        spec:
          containers:
          - name: migrate
            image: public.ecr.aws/docker/library/busybox:latest
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
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

# Wait for job completion using condition wait
resource "k8sconnect_wait" "migration_complete" {
  object_ref = k8sconnect_object.migration_job.object_ref
  wait_for = {
    condition = "Complete"
    timeout   = "120s"
  }
  cluster = local.cluster
}

#############################################
# CRONJOB TEST
#############################################

resource "k8sconnect_object" "backup_cronjob" {
  yaml_body          = <<-YAML
    apiVersion: batch/v1
    kind: CronJob
    metadata:
      name: backup-cronjob
      namespace: kind-validation
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
                image: public.ecr.aws/docker/library/busybox:latest
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
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# REPLICASET TEST (standalone workload)
#############################################

resource "k8sconnect_object" "replicaset" {
  yaml_body          = <<-YAML
    apiVersion: apps/v1
    kind: ReplicaSet
    metadata:
      name: frontend-replicaset
      namespace: kind-validation
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
            image: public.ecr.aws/nginx/nginx:latest
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
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# STANDALONE POD TEST (workload type)
#############################################

resource "k8sconnect_object" "standalone_pod" {
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: Pod
    metadata:
      name: standalone-pod
      namespace: kind-validation
      labels:
        app: standalone
        type: test
    spec:
      containers:
      - name: app
        image: public.ecr.aws/docker/library/busybox:latest
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
  cluster = local.cluster
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
  cluster = local.cluster
}

#############################################
# DEPLOYMENT TESTS - with rollout wait
#############################################

resource "k8sconnect_object" "web_deployment" {
  yaml_body          = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: web-deployment
      namespace: kind-validation
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
            image: public.ecr.aws/nginx/nginx:latest
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
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace, k8sconnect_wait.migration_complete]
}

# Wait for deployment rollout using rollout wait
resource "k8sconnect_wait" "web_rollout" {
  object_ref = k8sconnect_object.web_deployment.object_ref
  wait_for = {
    rollout = true
    timeout = "120s"
  }
  cluster = local.cluster
}

#############################################
# SERVICE TEST
#############################################

resource "k8sconnect_object" "web_service" {
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: web-service
      namespace: kind-validation
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
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# NETWORK POLICY TEST
#############################################

resource "k8sconnect_object" "network_policy" {
  yaml_body          = <<-YAML
    apiVersion: networking.k8s.io/v1
    kind: NetworkPolicy
    metadata:
      name: web-network-policy
      namespace: kind-validation
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
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# STATEFULSET TEST
#############################################

resource "k8sconnect_object" "database_statefulset" {
  yaml_body          = <<-YAML
    apiVersion: apps/v1
    kind: StatefulSet
    metadata:
      name: postgres
      namespace: kind-validation
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
            image: public.ecr.aws/docker/library/postgres:latest
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
              mountPath: /var/lib/postgresql
      volumeClaimTemplates:
      - metadata:
          name: data
        spec:
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: 500Mi
  YAML
  cluster = local.cluster
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
  cluster = local.cluster
}

# StatefulSet headless service
resource "k8sconnect_object" "postgres_service" {
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: postgres
      namespace: kind-validation
    spec:
      clusterIP: None
      selector:
        app: postgres
      ports:
      - port: 5432
        targetPort: 5432
        name: postgres
  YAML
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# DAEMONSET TEST
#############################################

resource "k8sconnect_object" "log_collector" {
  yaml_body          = <<-YAML
    apiVersion: apps/v1
    kind: DaemonSet
    metadata:
      name: log-collector
      namespace: kind-validation
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
            image: public.ecr.aws/docker/library/fluentd:latest
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
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# INGRESS TEST
#############################################

resource "k8sconnect_object" "web_ingress" {
  yaml_body          = <<-YAML
    apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: web-ingress
      namespace: kind-validation
      annotations:
        nginx.ingress.kubernetes.io/rewrite-target: /
    spec:
      ingressClassName: nginx
      rules:
      - host: kind-validation.local
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
  cluster = local.cluster
  depends_on         = [k8sconnect_object.web_service]
}

#############################################
# POD DISRUPTION BUDGET TEST
#############################################

resource "k8sconnect_object" "web_pdb" {
  yaml_body          = <<-YAML
    apiVersion: policy/v1
    kind: PodDisruptionBudget
    metadata:
      name: web-pdb
      namespace: kind-validation
    spec:
      minAvailable: 1
      selector:
        matchLabels:
          app: web
  YAML
  cluster = local.cluster
  depends_on         = [k8sconnect_object.web_deployment]
}

#############################################
# CLUSTER-SCOPED RBAC (ClusterRole, ClusterRoleBinding)
#############################################

resource "k8sconnect_object" "metrics_reader_role" {
  yaml_body          = <<-YAML
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRole
    metadata:
      name: kind-validation-metrics-reader
    rules:
    - apiGroups: ["metrics.k8s.io"]
      resources: ["pods", "nodes"]
      verbs: ["get", "list"]
    - apiGroups: [""]
      resources: ["pods", "nodes"]
      verbs: ["get", "list"]
  YAML
  cluster = local.cluster
}

resource "k8sconnect_object" "metrics_reader_binding" {
  yaml_body          = <<-YAML
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: kind-validation-metrics-reader-binding
    subjects:
    - kind: ServiceAccount
      name: app-service-account
      namespace: kind-validation
    roleRef:
      kind: ClusterRole
      name: kind-validation-metrics-reader
      apiGroup: rbac.authorization.k8s.io
  YAML
  cluster = local.cluster
  depends_on = [
    k8sconnect_object.metrics_reader_role,
    k8sconnect_object.split_resources
  ]
}

#############################################
# STORAGE CLASS TEST (cluster-scoped)
#############################################

resource "k8sconnect_object" "fast_storage" {
  yaml_body          = <<-YAML
    apiVersion: storage.k8s.io/v1
    kind: StorageClass
    metadata:
      name: fast-storage
    provisioner: rancher.io/local-path
    volumeBindingMode: WaitForFirstConsumer
    reclaimPolicy: Delete
  YAML
  cluster = local.cluster
}

#############################################
# PRIORITY CLASS TEST (cluster-scoped)
#############################################

resource "k8sconnect_object" "high_priority" {
  yaml_body          = <<-YAML
    apiVersion: scheduling.k8s.io/v1
    kind: PriorityClass
    metadata:
      name: high-priority
    value: 1000
    globalDefault: false
    description: "High priority workloads"
  YAML
  cluster = local.cluster
}

# Create a deployment using the priority class
resource "k8sconnect_object" "priority_deployment" {
  yaml_body          = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: priority-deployment
      namespace: kind-validation
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
            image: public.ecr.aws/docker/library/busybox:latest
            command: ["sh", "-c", "sleep 3600"]
            resources:
              requests:
                memory: "16Mi"
                cpu: "25m"
              limits:
                memory: "32Mi"
                cpu: "50m"
  YAML
  cluster = local.cluster
  depends_on = [
    k8sconnect_object.namespace,
    k8sconnect_object.high_priority
  ]
}

#############################################
# HORIZONTAL POD AUTOSCALER (HPA) TEST
#############################################

resource "k8sconnect_object" "web_hpa" {
  yaml_body          = <<-YAML
    apiVersion: autoscaling/v2
    kind: HorizontalPodAutoscaler
    metadata:
      name: web-hpa
      namespace: kind-validation
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
  cluster = local.cluster
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
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: Endpoints
    metadata:
      name: external-service
      namespace: kind-validation
    subsets:
    - addresses:
      - ip: 192.168.1.100
      ports:
      - port: 80
        protocol: TCP
  YAML
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

# Service for the custom endpoints
resource "k8sconnect_object" "external_service" {
  yaml_body          = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: external-service
      namespace: kind-validation
    spec:
      type: ClusterIP
      ports:
      - port: 80
        targetPort: 80
        protocol: TCP
  YAML
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

#############################################
# EXTERNAL RESOURCES FOR PATCH TESTING
#############################################

# Create external resources via kubectl that we can patch with k8sconnect_patch
# This demonstrates patching resources NOT managed by k8sconnect_object

# External ConfigMap for JSON Patch testing
resource "null_resource" "external_configmap" {
  depends_on = [k8sconnect_object.namespace]

  provisioner "local-exec" {
    command = <<-EOT
      kubectl --kubeconfig ${kind_cluster.kind_validation.kubeconfig_path} apply -f - <<EOF
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: external-config
        namespace: kind-validation
        labels:
          managed-by: kubectl
      data:
        app.name: "external-app"
        app.version: "1.0.0"
      EOF
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = "kubectl --kubeconfig ${self.triggers.kubeconfig_path} delete configmap external-config -n kind-validation --ignore-not-found=true"
  }

  triggers = {
    kubeconfig_path = kind_cluster.kind_validation.kubeconfig_path
  }
}

# External Service for Merge Patch testing
resource "null_resource" "external_service" {
  depends_on = [k8sconnect_object.namespace]

  provisioner "local-exec" {
    command = <<-EOT
      kubectl --kubeconfig ${kind_cluster.kind_validation.kubeconfig_path} apply -f - <<EOF
      apiVersion: v1
      kind: Service
      metadata:
        name: external-service-patch-test
        namespace: kind-validation
        labels:
          managed-by: kubectl
      spec:
        type: ClusterIP
        selector:
          app: external
        ports:
        - port: 8080
          targetPort: 8080
          protocol: TCP
      EOF
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = "kubectl --kubeconfig ${self.triggers.kubeconfig_path} delete service external-service-patch-test -n kind-validation --ignore-not-found=true"
  }

  triggers = {
    kubeconfig_path = kind_cluster.kind_validation.kubeconfig_path
  }
}

# External Deployment for Strategic Merge Patch testing
resource "null_resource" "external_deployment_strategic" {
  depends_on = [k8sconnect_object.namespace]

  provisioner "local-exec" {
    command = <<-EOT
      kubectl --kubeconfig ${kind_cluster.kind_validation.kubeconfig_path} apply -f - <<EOF
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: external-deployment-strategic
        namespace: kind-validation
        labels:
          app: external-strategic
          managed-by: kubectl
      spec:
        replicas: 1
        selector:
          matchLabels:
            app: external-strategic
        template:
          metadata:
            labels:
              app: external-strategic
          spec:
            containers:
            - name: app
              image: public.ecr.aws/docker/library/busybox:latest
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
    command = "kubectl --kubeconfig ${self.triggers.kubeconfig_path} delete deployment external-deployment-strategic -n kind-validation --ignore-not-found=true"
  }

  triggers = {
    kubeconfig_path = kind_cluster.kind_validation.kubeconfig_path
  }
}

#############################################
# PATCH RESOURCE TESTS
#############################################

# All patches target EXTERNAL resources (created via kubectl, not k8sconnect_object)
# This is the correct use case for k8sconnect_patch

# Test 1: Strategic Merge Patch on External Deployment
resource "k8sconnect_patch" "deployment_strategic" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "external-deployment-strategic"
    namespace   = "kind-validation"
  }
  patch = jsonencode({
    metadata = {
      annotations = {
        "patched-by"    = "k8sconnect"
        "patch-type"    = "strategic-merge"
        "patch-version" = "1"
      }
    }
    spec = {
      replicas = 2 # Scale up from 1 to 2
    }
  })
  cluster = local.cluster
  depends_on         = [null_resource.external_deployment_strategic]
}

# Test 2: JSON Patch on External ConfigMap
resource "k8sconnect_patch" "configmap_json" {
  target = {
    api_version = "v1"
    kind        = "ConfigMap"
    name        = "external-config"
    namespace   = "kind-validation"
  }
  json_patch = jsonencode([
    {
      op    = "add"
      path  = "/data/cache.enabled"
      value = "true"
    },
    {
      op    = "add"
      path  = "/data/cache.ttl"
      value = "3600"
    }
  ])
  cluster = local.cluster
  depends_on         = [null_resource.external_configmap]
}

# Test 3: Merge Patch on External Service
resource "k8sconnect_patch" "service_merge" {
  target = {
    api_version = "v1"
    kind        = "Service"
    name        = "external-service-patch-test"
    namespace   = "kind-validation"
  }
  merge_patch = jsonencode({
    metadata = {
      labels = {
        "patched"     = "true"
        "patch-type"  = "merge"
        "environment" = "kind-validation"
      }
    }
  })
  cluster = local.cluster
  depends_on         = [null_resource.external_service]
}

# Test 4: Patch on external-app Deployment (already existed)
resource "k8sconnect_patch" "external_deployment_patch" {
  target = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "external-app"
    namespace   = "kind-validation"
  }
  patch = jsonencode({
    metadata = {
      annotations = {
        "patched-externally" = "true"
        "patch-timestamp"    = "v1"
      }
    }
  })
  cluster = local.cluster
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
      kubectl --kubeconfig ${kind_cluster.kind_validation.kubeconfig_path} apply -f - <<EOF
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: external-app
        namespace: kind-validation
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
              image: public.ecr.aws/docker/library/busybox:latest
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
    command = "kubectl --kubeconfig ${self.triggers.kubeconfig_path} delete deployment external-app -n kind-validation --ignore-not-found=true"
  }

  triggers = {
    kubeconfig_path = kind_cluster.kind_validation.kubeconfig_path
  }
}

# Wait for the external deployment to roll out
resource "k8sconnect_wait" "external_app_rollout" {
  object_ref = {
    api_version = "apps/v1"
    kind        = "Deployment"
    name        = "external-app"
    namespace   = "kind-validation"
  }
  wait_for = {
    rollout = true
    timeout = "120s"
  }
  cluster = local.cluster
  depends_on         = [null_resource.external_deployment]
}

#############################################
# OBJECT DATASOURCE TEST
#############################################

# Read back a deployed resource using object datasource
data "k8sconnect_object" "namespace_info" {
  api_version        = "v1"
  kind               = "Namespace"
  name               = "kind-validation"
  cluster = local.cluster
  depends_on         = [k8sconnect_object.namespace]
}

# Read the ConfigMap we manage to verify it exists
data "k8sconnect_object" "app_config" {
  api_version        = "v1"
  kind               = "ConfigMap"
  name               = "app-config"
  namespace          = "kind-validation"
  cluster = local.cluster
  depends_on         = [k8sconnect_object.split_resources]
}

# Read back patched external resources to verify patches were applied
data "k8sconnect_object" "external_config_patched" {
  api_version        = "v1"
  kind               = "ConfigMap"
  name               = "external-config"
  namespace          = "kind-validation"
  cluster = local.cluster
  depends_on         = [k8sconnect_patch.configmap_json]
}

data "k8sconnect_object" "external_service_patched" {
  api_version        = "v1"
  kind               = "Service"
  name               = "external-service-patch-test"
  namespace          = "kind-validation"
  cluster = local.cluster
  depends_on         = [k8sconnect_patch.service_merge]
}

data "k8sconnect_object" "external_deployment_patched" {
  api_version        = "apps/v1"
  kind               = "Deployment"
  name               = "external-deployment-strategic"
  namespace          = "kind-validation"
  cluster = local.cluster
  depends_on         = [k8sconnect_patch.deployment_strategic]
}

#############################################
# OUTPUTS
#############################################

output "cluster_endpoint" {
  description = "Kind cluster API endpoint"
  value       = kind_cluster.kind_validation.endpoint
}

output "namespace_uid" {
  description = "UID of the kind-validation namespace (from datasource)"
  value       = yamldecode(data.k8sconnect_object.namespace_info.yaml_body).metadata.uid
}

output "pvc_volume_name" {
  description = "PVC volume name wait completed successfully"
  value       = k8sconnect_wait.pvc_volume_name.id
}

output "pvc_phase" {
  description = "PVC bound wait completed successfully"
  value       = k8sconnect_wait.pvc_bound.id
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

output "external_config_data" {
  description = "External ConfigMap data after JSON patch (should include cache fields)"
  value       = yamldecode(data.k8sconnect_object.external_config_patched.yaml_body).data
}

output "external_service_labels" {
  description = "External Service labels after merge patch (should include patched=true)"
  value       = yamldecode(data.k8sconnect_object.external_service_patched.yaml_body).metadata.labels
}

output "external_deployment_replicas" {
  description = "External Deployment replicas after strategic merge patch (should be 2)"
  value       = yamldecode(data.k8sconnect_object.external_deployment_patched.yaml_body).spec.replicas
}

output "cactus_custom_resource" {
  description = "Custom resource with non-standard plural (proves CRD auto-retry + DiscoverGVR)"
  value       = k8sconnect_object.my_cactus.object_ref
}

#############################################
# FIELD VALIDATION TEST (UNCOMMENT TO TEST)
#############################################
#
# This resource intentionally contains typos to demonstrate field validation.
# Uncomment to see validation errors during plan:
#
# resource "k8sconnect_object" "field_validation_test" {
#   yaml_body = <<-YAML
#     apiVersion: apps/v1
#     kind: Deployment
#     metadata:
#       name: validation-test
#       namespace: kind-validation
#     spec:
#       replica: 1  # TYPO! Should be "replicas"
#       selector:
#         matchLabels:
#           app: test
#       template:
#         metadata:
#           labels:
#             app: test
#         spec:
#           containers:
#           - name: nginx
#             image: nginx:1.21
#             imagePullPolice: Always  # TYPO! Should be "imagePullPolicy"
#   YAML
#
#   cluster = local.cluster
#   depends_on         = [k8sconnect_object.namespace]
# }
#
# Expected error during terraform plan:
# Error: Plan: Field Validation Failed
#   .spec.replica: field not declared in schema
#     (Should be .spec.replicas)

#############################################
# FORMATTING TEST
#############################################

resource "k8sconnect_object" "formatting_test" {
  yaml_body = <<YAML
# This is a comment
apiVersion: v1
kind: ConfigMap
metadata:
  name: formatting-test-cm  # resource name
  namespace: kind-validation
data:
  key1: value1  # first key
  key2: value2  # second key
YAML

  cluster = local.cluster
}
