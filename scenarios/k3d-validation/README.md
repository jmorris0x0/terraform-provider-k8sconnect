# k3d Validation Scenario

This is a comprehensive validation scenario using k3d (local Kubernetes in Docker). It exercises all provider functionality with a wide range of Kubernetes objects to validate the provider before releases.

## What This Tests

### Resources & Datasources
- **k8sconnect_object**: 15+ different K8s resource types
- **k8sconnect_wait**: All 4 wait types (field, field_value, condition, rollout)
- **k8sconnect_patch**: All 3 patch types (strategic, json, merge)
- **k8sconnect_yaml_split**: Multi-document YAML splitting
- **k8sconnect_yaml_scoped**: Cluster vs namespace scoped separation
- **k8sconnect_object datasource**: Reading existing resources

### Kubernetes Objects Tested
- Namespace
- ServiceAccount
- Role & RoleBinding
- ConfigMap & Secret
- PersistentVolumeClaim
- Job
- CronJob
- Deployment
- StatefulSet
- DaemonSet
- Service
- NetworkPolicy
- ResourceQuota & LimitRange
- HorizontalPodAutoscaler

### Wait Types
1. **Field wait** (`spec.volumeName`): Wait for field to exist
2. **Field value wait** (`status.phase=Bound`): Wait for specific value
3. **Condition wait** (Job `Complete`): Wait for condition
4. **Rollout wait** (Deployment, StatefulSet): Wait for rollout completion

### Patch Types
1. **Strategic merge**: Deployment annotations + replica scaling
2. **JSON patch**: ConfigMap add operations
3. **Merge patch**: Service labels

### Special Tests
- **External resource wait**: Wait on deployment NOT managed by k8sconnect (created via kubectl)
- **HPA with ignore_fields**: Tests field ownership filtering
- **Datasource chaining**: yaml_split → k8sconnect_object (for_each)
- **Cross-resource dependencies**: Job completion gates deployment creation

## Resource Requirements

Low resource usage - laptop friendly:
- 2 Kind nodes (1 control-plane, 1 worker)
- Small container images (nginx, busybox, postgres-alpine, fluentd)
- Minimal CPU/memory requests (50m CPU, 32-128Mi RAM typical)
- Small storage (100Mi-500Mi PVCs)

## Usage

### Initial Plan (before cluster exists)
Should show correct plan with all connection values "known after apply":

```bash
cd scenarios/k3d-validation
terraform init
terraform plan
```

Expected: Clean plan, no errors, projections handle bootstrap correctly.

### Apply
Create cluster and all resources:

```bash
terraform apply
```

Expected: All resources created, all waits complete, all patches applied.

### Verify Outputs
Check that datasources and waits captured data correctly:

```bash
terraform output
```

Expected outputs:
- `cluster_endpoint`: Kind cluster API server
- `namespace_uid`: Namespace UID from object datasource
- `pvc_volume_name`: Volume name from field wait
- `pvc_phase`: "Bound" from field_value wait
- `configmap_data`: ConfigMap with patched keys
- `yaml_split_count`: 5 documents
- `yaml_scoped_cluster_count`: 2 cluster-scoped resources
- `yaml_scoped_namespaced_count`: 3 namespaced resources

### Test Updates
Modify resources to test updates (e.g., change replica count, add labels):

```bash
# Edit main.tf - change something like deployment replicas
terraform plan
terraform apply
```

Expected: Clean updates, no false drift.

### Destroy
Clean teardown:

```bash
terraform destroy
```

Expected: All resources destroyed cleanly, no stuck finalizers.

## Files

- `main.tf`: Comprehensive test configuration
- `multi-resources.yaml`: Multi-document YAML for yaml_split test
- `mixed-scope.yaml`: Mixed cluster/namespace resources for yaml_scoped test
- `nginx-deployment.yaml`: Legacy from ux_comparison (not used)
- `nginx-service.yaml`: Legacy from ux_comparison (not used)

## What We're Testing For

This k3d-validationing test aims to discover:

1. **Bootstrap bugs**: Does plan work correctly before cluster exists?
2. **Drift detection issues**: Do we show false drift on K8s-managed fields?
3. **Wait timeout problems**: Do waits complete or timeout unexpectedly?
4. **Patch conflicts**: Do patches apply cleanly or fight with SSA?
5. **Datasource parsing**: Do yaml_split/yaml_scoped handle edge cases?
6. **Destroy issues**: Do resources clean up or get stuck?
7. **Dependency ordering**: Do depends_on chains work correctly?
8. **Field ownership**: Does ignore_fields work with active controllers (HPA)?
9. **External waits**: Can we wait on resources we don't own?
10. **Cross-resource references**: Do object_ref references work correctly?

## Success Criteria

- ✅ `terraform plan` before cluster exists: Clean plan, no errors
- ✅ `terraform apply`: All resources created successfully
- ✅ All waits complete within timeouts
- ✅ All patches apply without conflicts
- ✅ `terraform plan` after apply: No changes detected (no false drift)
- ✅ Outputs show expected values
- ✅ Updates apply cleanly
- ✅ `terraform destroy`: Complete cleanup, no errors

If all criteria pass, we're confident for v0.1.0 release!
