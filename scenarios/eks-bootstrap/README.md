# EKS Bootstrap Test

This test validates the critical bootstrap scenario: creating an EKS cluster and deploying Kubernetes workloads in a **single `terraform apply`**.

## What This Tests

- **Inline cluster connections** with "known after apply" values
- **Bootstrap timing** - provider connecting as soon as API server is ready
- **apply_timeout** handling (when implemented) for EKS startup delays
- **No time_sleep workaround** needed

## Prerequisites

1. AWS CLI configured with credentials
2. AWS account with permissions to create:
   - EKS clusters
   - VPC resources
   - IAM roles and policies
   - EC2 instances (for node group)

3. Terraform >= 1.6
4. k8sconnect provider installed locally:
   ```bash
   cd ../..  # Back to project root
   make install
   ```

## Expected Timeline

```
t=0s:       terraform apply starts
t=0-30s:    EKS control plane creation begins
t=30s:      AWS API returns (endpoint available, cluster still provisioning)
t=2-10m:    API server becomes ready (varies by cluster size)
t=2-10m:    k8sconnect_object resources create namespace
t=2-10m+:   Workloads deployed (deployment, configmap)
t=10-15m:   terraform apply completes
```

## Current Limitation

**With default 2-minute timeout**: This test will likely **FAIL** because the EKS API server takes 5-10 minutes to become ready, but the provider only retries connections for 2 minutes.

**Error you'll see**:
```
Error: Failed to connect to cluster
  Connection refused / timeout after 2 minutes
```

**Once `apply_timeout` is implemented**: Uncomment the `apply_timeout = "10m"` lines in main.tf.

## Running the Test

### Step 1: Initialize Terraform

```bash
cd scenarios/eks-bootstrap
terraform init
```

### Step 2: Plan (optional but recommended)

```bash
terraform plan
```

You'll see ~10 resources:
- VPC lookup (data source)
- Subnets lookup (data source)
- 2 IAM roles + 4 policy attachments
- EKS cluster
- EKS node group
- 3 k8sconnect_object resources (namespace, deployment, configmap)

### Step 3: Apply

```bash
terraform apply
```

**Expected result with current code**: FAILURE after ~2 minutes (connection timeout)

**Expected result with apply_timeout implemented**: SUCCESS after ~10-15 minutes

### Step 4: Verify (if successful)

```bash
# Update kubeconfig
aws eks update-kubeconfig --region us-west-2 --name k8sconnect-bootstrap-test

# Check namespace
kubectl get ns bootstrap-test

# Check deployment
kubectl get deployment -n bootstrap-test nginx-bootstrap-test

# Check configmap
kubectl get configmap -n bootstrap-test bootstrap-config
```

### Step 5: Cleanup

```bash
terraform destroy
```

**IMPORTANT**: Make sure to destroy the cluster to avoid AWS charges!

## Variables

- `aws_region` - AWS region (default: us-west-2)
- `cluster_name` - EKS cluster name (default: k8sconnect-bootstrap-test)

Override with:
```bash
terraform apply -var="aws_region=us-east-1" -var="cluster_name=my-test-cluster"
```

## Cost Estimate

- EKS control plane: ~$0.10/hour ($73/month)
- EC2 instances (2x t3.small): ~$0.04/hour ($30/month)
- **Total for testing**: ~$0.14/hour

**Cost for 30-minute test**: < $0.10

Always run `terraform destroy` when done!

## Success Criteria

1. `terraform apply` completes without errors
2. All k8sconnect_object resources created
3. `kubectl get ns bootstrap-test` shows namespace
4. `kubectl get deployment -n bootstrap-test` shows nginx deployment with 2 replicas
5. No false drift on subsequent `terraform plan`

## Troubleshooting

### Connection timeout after 2 minutes

**Expected with current code**. Wait for `apply_timeout` implementation.

**Workaround**: Add time_sleep between cluster and workloads:
```hcl
resource "time_sleep" "wait_for_cluster" {
  depends_on      = [aws_eks_cluster.main, aws_eks_node_group.main]
  create_duration = "5m"
}

resource "k8sconnect_object" "namespace" {
  # ...
  depends_on = [time_sleep.wait_for_cluster]
}
```

### IAM permissions errors

Ensure your AWS credentials have:
- `eks:*`
- `iam:CreateRole`, `iam:AttachRolePolicy`
- `ec2:CreateVpc`, `ec2:CreateSubnet`, etc.

### VPC quota exceeded

Use existing VPC instead of default VPC.

## What This Proves

If this test passes, it proves:

1. **Bootstrap works** - No need for separate cluster creation + workload deployment
2. **Inline connections work** - Connection values can be "known after apply"
3. **Timing is correct** - Provider waits long enough for API server to be ready
4. **No workarounds needed** - No time_sleep hacks required

This is the **killer feature** that differentiates k8sconnect from other providers!
