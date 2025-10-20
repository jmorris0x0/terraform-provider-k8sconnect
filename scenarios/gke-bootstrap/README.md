# GKE Bootstrap Test

This test validates the critical bootstrap scenario: creating a GKE cluster and deploying Kubernetes workloads in a **single `terraform apply`**.

## What This Tests

- **Inline cluster connections** with "known after apply" values
- **Bootstrap timing** - provider connecting as soon as API server is ready
- **apply_timeout** handling (when implemented) for GKE startup delays
- **No time_sleep workaround** needed

## Prerequisites

1. Google Cloud SDK (gcloud) installed and authenticated
2. GKE auth plugin installed:
   ```bash
   gcloud components install gke-gcloud-auth-plugin
   ```

3. GCP project with:
   - GKE API enabled
   - Compute Engine API enabled
   - Billing enabled
   - Permissions to create GKE clusters

4. Terraform >= 1.6
5. k8sconnect provider installed locally:
   ```bash
   cd ../..  # Back to project root
   make install
   ```

## Expected Timeline

```
t=0s:       terraform apply starts
t=0-5m:     GKE control plane + node pool creation
t=5-8m:     Cluster becomes ready (typically faster than EKS)
t=5-8m:     k8sconnect_object resources create namespace
t=5-8m+:    Workloads deployed (deployment, configmap, pvc)
t=8-12m:    terraform apply completes
```

## Current Limitation

**With default 2-minute timeout**: This test will likely **FAIL** because the GKE API server takes 5-8 minutes to become ready, but the provider only retries connections for 2 minutes.

**Error you'll see**:
```
Error: Failed to connect to cluster
  Connection refused / timeout after 2 minutes
```

**Once `apply_timeout` is implemented**: Uncomment the `apply_timeout = "10m"` lines in main.tf.

## Running the Test

### Step 1: Set GCP Project

```bash
export TF_VAR_gcp_project="your-project-id"
```

Or create a `terraform.tfvars` file:
```hcl
gcp_project = "your-project-id"
```

### Step 2: Initialize Terraform

```bash
cd scenarios/gke-bootstrap
terraform init
```

### Step 3: Plan (optional but recommended)

```bash
terraform plan
```

You'll see ~6 resources:
- GKE cluster
- Node pool
- 4 k8sconnect_object resources (namespace, deployment, configmap, pvc)

### Step 4: Apply

```bash
terraform apply
```

**Expected result with current code**: FAILURE after ~2 minutes (connection timeout)

**Expected result with apply_timeout implemented**: SUCCESS after ~8-12 minutes

### Step 5: Verify (if successful)

```bash
# Update kubeconfig
gcloud container clusters get-credentials k8sconnect-bootstrap-test \
  --region us-central1 --project your-project-id

# Check namespace
kubectl get ns bootstrap-test

# Check deployment
kubectl get deployment -n bootstrap-test nginx-bootstrap-test

# Check configmap
kubectl get configmap -n bootstrap-test bootstrap-config

# Check PVC
kubectl get pvc -n bootstrap-test test-pvc
```

### Step 6: Cleanup

```bash
terraform destroy
```

**IMPORTANT**: Make sure to destroy the cluster to avoid GCP charges!

## Variables

- `gcp_project` - GCP project ID (REQUIRED)
- `gcp_region` - GCP region (default: us-central1)
- `cluster_name` - GKE cluster name (default: k8sconnect-bootstrap-test)

Override with:
```bash
terraform apply \
  -var="gcp_project=my-project" \
  -var="gcp_region=us-west1" \
  -var="cluster_name=my-test-cluster"
```

## Cost Estimate

- GKE cluster: Free tier available (1 zonal cluster per billing account)
- Compute Engine VMs (2x e2-small): ~$0.03/hour ($24/month)
- **Total for testing**: ~$0.03/hour (or free with free tier)

**Cost for 30-minute test**: < $0.02 (or free)

Always run `terraform destroy` when done!

## Success Criteria

1. `terraform apply` completes without errors
2. All k8sconnect_object resources created
3. `kubectl get ns bootstrap-test` shows namespace
4. `kubectl get deployment -n bootstrap-test` shows nginx deployment with 2 replicas
5. `kubectl get pvc -n bootstrap-test` shows test-pvc in Bound state
6. No false drift on subsequent `terraform plan`

## Troubleshooting

### Connection timeout after 2 minutes

**Expected with current code**. Wait for `apply_timeout` implementation.

**Workaround**: Add time_sleep between cluster and workloads:
```hcl
resource "time_sleep" "wait_for_cluster" {
  depends_on      = [google_container_cluster.main, google_container_node_pool.main]
  create_duration = "5m"
}

resource "k8sconnect_object" "namespace" {
  # ...
  depends_on = [time_sleep.wait_for_cluster]
}
```

### gke-gcloud-auth-plugin not found

Install it:
```bash
gcloud components install gke-gcloud-auth-plugin
```

### GKE API not enabled

Enable it:
```bash
gcloud services enable container.googleapis.com --project=your-project-id
```

### Quota exceeded

Check your GCP quotas:
```bash
gcloud compute project-info describe --project=your-project-id
```

## GKE vs EKS Comparison

| Aspect | GKE | EKS |
|--------|-----|-----|
| Startup time | 5-8 minutes | 5-10 minutes |
| Cost | Lower (e2-small cheaper than t3.small) | Higher |
| Auth method | gke-gcloud-auth-plugin | aws eks get-token |
| Free tier | 1 zonal cluster free | No free tier |
| Default storage | standard-rwo (works immediately) | gp2 (works immediately) |

## What This Proves

If this test passes, it proves:

1. **Bootstrap works** - No need for separate cluster creation + workload deployment
2. **Inline connections work** - Connection values can be "known after apply"
3. **Timing is correct** - Provider waits long enough for API server to be ready
4. **No workarounds needed** - No time_sleep hacks required
5. **Multi-cloud support** - Same provider works with GKE, EKS, kind, k3d, etc.

This is the **killer feature** that differentiates k8sconnect from other providers!
