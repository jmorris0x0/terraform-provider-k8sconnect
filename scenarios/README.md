# Test Scenarios

This directory contains real-world test scenarios that validate the provider against actual cloud infrastructure.

## What Are Scenarios?

**Scenarios** are end-to-end validation configurations that:
- Test against real clusters (kind, EKS, GKE, etc.)
- Require manual setup and cloud credentials
- Cost money to run (cloud provider charges)
- Cannot run in automated CI/CD
- Validate critical user workflows

## How Are They Different From Examples?

| | Examples (`examples/`) | Scenarios (`scenarios/`) |
|---|---|---|
| **Purpose** | Documentation + minimal runnable tests | Real-world validation |
| **Tested by** | `make test-examples` (automated) | Manual execution |
| **Cluster** | k3d (created by test) | Real cloud clusters (EKS, GKE, kind) |
| **Cost** | Free | Cloud charges apply |
| **Complexity** | Minimal (5-15 resources) | Comprehensive (40+ resources) |
| **Duration** | 30-60 seconds | 10-15 minutes (including cluster creation) |

## Available Scenarios

### `k3d-validation/` - Pre-Release Validation

Comprehensive validation using k3d cluster (local Kubernetes in Docker). Tests ALL features:
- All 3 resources (object, patch, wait)
- All 3 data sources (manifest, yaml_split, yaml_scoped)
- Multiple auth methods
- Ignore fields, field validation, drift detection
- Full lifecycle (create, update, delete)

**Run before every release.**

**Cost:** Free (k3d local cluster)
**Duration:** ~5 minutes

### `eks-bootstrap/` - EKS Bootstrap Scenario

The **killer feature** test: Create EKS cluster + deploy workloads in **single terraform apply**.

Validates:
- Inline connections with "known after apply" values
- `apply_timeout` handling for slow cluster startup
- `aws eks get-token` exec auth
- No time_sleep workarounds needed

**Critical for proving the value proposition.**

**Cost:** ~$0.10 for 30-minute test
**Duration:** 10-15 minutes

### `gke-bootstrap/` - GKE Bootstrap Scenario

Same as EKS, but for GKE. Validates multi-cloud support.

Validates:
- Inline connections with "known after apply" values
- `apply_timeout` handling for slow cluster startup
- `gke-gcloud-auth-plugin` exec auth
- No time_sleep workarounds needed

**Cost:** ~$0.02 for 30-minute test (or free with GCP free tier)
**Duration:** 8-12 minutes

## Running Scenarios

### Prerequisites

1. **Provider installed locally:**
   ```bash
   cd /path/to/terraform-provider-k8sconnect
   make install
   ```

2. **Cloud credentials configured:**
   - EKS: AWS CLI with credentials
   - GKE: gcloud + gke-gcloud-auth-plugin
   - kind/k3d: Docker Desktop

### Basic Workflow

```bash
cd scenarios/<scenario-name>
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars if needed
terraform init
terraform plan
terraform apply
# Validate manually
terraform destroy  # IMPORTANT: Clean up to avoid charges!
```

### When to Run

- **Before every release:** `dogfood/`
- **Before implementing `apply_timeout`:** `eks-bootstrap/` and `gke-bootstrap/` (document failures)
- **After implementing `apply_timeout`:** `eks-bootstrap/` and `gke-bootstrap/` (validate success)
- **When adding cloud-specific features:** Relevant cloud scenario

## Adding New Scenarios

Future scenarios could include:
- `aks-bootstrap/` - Azure AKS bootstrap
- `disaster-recovery/` - Cluster migration, backup/restore
- `multi-cluster/` - Fleet management across regions
- `on-prem/` - Bare-metal K8s
- `upgrade/` - K8s version upgrades with provider

Keep scenarios:
1. **Focused** - Test one critical workflow
2. **Documented** - Clear README with cost/time estimates
3. **Realistic** - Match actual user patterns
4. **Cheap** - Minimize cloud costs where possible

## Cost Management

Always run `terraform destroy` when done! Set a reminder:

```bash
terraform apply && echo "Don't forget: terraform destroy" | at now + 1 hour
```

Or use auto-destroy:
```bash
terraform apply && sleep 1800 && terraform destroy
```
