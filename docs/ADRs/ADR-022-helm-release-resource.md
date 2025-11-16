# ADR-022: Helm Release Resource with Inline Cluster Configuration

## Status
Accepted - Implementation Pending (2025-11-14)

## Context

**The bootstrapping problem**: The Helm provider (`hashicorp/helm`) cannot deploy to clusters that don't exist yet because Terraform providers are configured before resources are created. This forces users into workarounds:
- Separate Terraform stacks (breaks single-apply workflow)
- `terraform_remote_state` (tight coupling, complexity)
- Null resources with `local-exec` (fragile, not declarative)

**User demand**: [Issue #127](https://github.com/jmorris0x0/terraform-provider-k8sconnect/issues/127) demonstrates real users trying to bootstrap foundation services (Cilium, cert-manager, ArgoCD) alongside cluster creation.

**The Helm provider's fatal limitation**: Provider configuration happens before resources are created, so you can't reference cluster credentials from a cluster being created in the same apply. This makes bootstrapping impossible.

**Real-world use case from #127**:
```hcl
# User wants to do this in ONE terraform apply:
resource "aws_eks_cluster" "main" { ... }

resource "helm_release" "cilium" {  # FAILS - provider needs cluster config first!
  chart = "cilium"
  repository = "https://helm.cilium.io/"
  # Can't use: aws_eks_cluster.main.endpoint (not available at provider config time)
}
```

This is exactly what k8sconnect is designed for - but the Helm provider's architecture makes it impossible.

## Decision

**Implement `k8sconnect_helm_release` resource that accepts inline cluster configuration, enabling true single-apply bootstrapping while maintaining full Helm release semantics.**

This is NOT a templating-only solution. This is a full Helm release manager that uses the Helm SDK to perform actual `helm install`, `helm upgrade`, and `helm uninstall` operations. The ONLY difference from hashicorp/helm is inline cluster configuration instead of provider-level configuration.

### Core Design Principles

**1. Inline Cluster Configuration** - Unlike hashicorp/helm provider:
- Cluster credentials specified per-resource (like k8sconnect_object)
- Can reference cluster values being created in same apply
- Enables true cluster + workload bootstrapping in single apply
- No provider configuration required

**2. Full Helm Release Semantics** - This is NOT just templating:
- Real Helm releases (shows in `helm list`)
- Helm hooks execute (pre-install, post-upgrade, etc.)
- Helm rollback works from CLI
- Release history maintained in cluster
- Three-way merge strategy (Helm's native behavior)
- Atomic upgrades/rollbacks

**3. Exceed HashiCorp Quality** - Learn from their mistakes:
- Fix state corruption issues
- Proper drift detection
- Correct wait behavior for all workload types
- Secure sensitive value handling
- Better error messages

### Schema

```hcl
resource "k8sconnect_helm_release" "cilium" {
  name         = "cilium"
  namespace    = "kube-system"
  repository   = "https://helm.cilium.io/"
  chart        = "cilium"
  version      = "1.15.5"

  # Values file (optional)
  values = file("${path.module}/cilium-values.yaml")

  # Individual value overrides (list attributes, not blocks)
  set = [
    {
      name  = "ipam.mode"
      value = "kubernetes"
    }
  ]

  set_sensitive = [
    {
      name  = "hubble.relay.tls.ca.cert"
      value = var.ca_cert
    }
  ]

  # THE KEY DIFFERENCE: Inline cluster config
  cluster = {
    host                   = aws_eks_cluster.main.endpoint  # Can reference being created!
    cluster_ca_certificate = base64encode(aws_eks_cluster.main.certificate_authority[0].data)
    token                  = data.aws_eks_cluster_auth.main.token
  }

  # Helm release options
  create_namespace = true
  atomic          = true
  wait            = true
  wait_for_jobs   = true
  timeout         = "600s"

  dependency_update = true
  skip_crds        = false

  # k8sconnect enhancements
  force_destroy    = false  # Require explicit disable to delete PVCs, etc.
}
```

## HashiCorp Helm Provider: Known Issues We Must Address

We're not just replicating hashicorp/helm - we're fixing their fundamental problems. Here are the critical issues currently plaguing their provider:

### Critical State Management Issues

**Issue #1669: Resources Randomly Removed from State (v3 regression)**
- **Problem**: helm_release resources randomly disappear from Terraform state after successful applies
- **Impact**: ~50% failure rate in CI, forces recreation of existing releases
- **Root Cause**: Suspected bug in `resourceReleaseExists` incorrectly pruning state
- **Our Solution**: Different state management architecture, comprehensive state validation tests

**Issue #472: Failed Releases Update State (4+ years old!)**
- **Problem**: When Helm release fails (pod crash, image pull error), state file is updated anyway
- **Impact**: Next `terraform apply` shows "no changes" and doesn't retry failed deployment
- **Root Cause**: Terraform framework saves state even when resource operations fail
- **Our Solution**: Only update state on successful Helm operations, use transaction-like semantics

### Drift Detection Failures

**Issue #1349: No Drift Detection After Manual Rollback**
- **Problem**: Running `helm rollback` from CLI doesn't trigger Terraform drift detection
- **Impact**: Terraform shows "no changes" even though release is at wrong revision
- **Root Cause**: Provider doesn't compare actual release revision with state
- **Our Solution**: Always check release revision, metadata, and computed values during Read

**Issue #1307: OCI Chart Drift Not Detected**
- **Problem**: Charts deployed by digest don't trigger drift when digest changes
- **Impact**: Security vulnerabilities from outdated chart versions
- **Our Solution**: Track both version tags AND digests for OCI charts

### Wait Logic Bugs

**Issue #1364: Doesn't Wait for DaemonSets**
- **Problem**: `wait = true` only waits for Deployments/Jobs, not DaemonSets
- **Impact**: Deployment continues before DaemonSet pods are ready, errors discovered late
- **Root Cause**: Incomplete wait logic implementation
- **Our Solution**: Wait for ALL workload types: Deployments, StatefulSets, DaemonSets, Jobs, ReplicaSets

**Issue #672: First Deploy Always Succeeds (Timeout Ignored)**
- **Problem**: First deployment always succeeds after timeout, subsequent ones fail correctly
- **Impact**: False positive on initial deployment masks real issues
- **Root Cause**: Timeout logic inconsistency between create and update
- **Our Solution**: Consistent timeout enforcement across all operations

**Issue #463: Timeout Parameter Ignored**
- **Problem**: Client times out at 30s despite higher timeout configuration
- **Impact**: Long-running deployments fail unexpectedly
- **Root Cause**: Context deadline not properly set from timeout parameter
- **Our Solution**: Proper context management with user-specified timeouts

### Security Issues

**Issue #1287: Sensitive Values Leaked in Metadata**
- **Problem**: Values marked `sensitive = true` are exposed in plan output metadata field
- **Impact**: Passwords, API keys visible in CI logs, terminal history
- **Root Cause**: Terraform treats all metadata as non-sensitive
- **Our Solution**: Properly propagate sensitivity through all computed fields, never store sensitive values in metadata

**Issue #1221: Sensitive Attribute Not Respected (Regression)**
- **Problem**: Provider stopped respecting `sensitive` attribute in v2.10+
- **Impact**: Credential exposure in logs and state files
- **Our Solution**: Comprehensive sensitive value handling from day one, tested rigorously

### Import and Migration Issues

**Issue #1613: Cannot Import Existing Releases**
- **Problem**: Imported releases have `description` field mismatch causing permanent drift
- **Impact**: Can't adopt existing Helm releases into Terraform management
- **Root Cause**: Auto-generated Kubernetes fields not filtered during import
- **Our Solution**: Smart import that filters server-generated fields, clean state from day one

### OCI and Chart Handling Issues

**Issue #1596: Digest-Based Charts Not Supported**
- **Problem**: Can't use `@sha256:...` digest references for charts
- **Impact**: No immutable chart deployments, supply chain security gap
- **Root Cause**: Validation rejects digest syntax as invalid tag
- **Our Solution**: Full support for both version tags and SHA256 digests

**Issues #1645, #1660, #844: OCI Registry Authentication Failures**
- **Problem**: Repeated auth failures with AWS ECR, Azure ACR (token refresh issues)
- **Impact**: Production deployments fail intermittently
- **Root Cause**: Insufficient credential refresh logic for cloud registries
- **Our Solution**: Robust OCI auth with proper token refresh, tested against major cloud providers

**Issue #782: CRD Empty Key Bug**
- **Problem**: CRDs from dependency charts create empty string keys in manifests map
- **Impact**: Can't iterate over manifests without workarounds
- **Root Cause**: Helm SDK outputs CRDs with malformed source comments
- **Our Solution**: Not applicable - we're doing full releases, not templating

### Dependency Management

**Issue #576: Dependencies Not Downloaded on Local Chart Update**
- **Problem**: `dependency_update = true` doesn't download dependencies when local chart changes
- **Impact**: Stale dependencies deployed with updated chart
- **Root Cause**: Dependency update only runs on certain triggers
- **Our Solution**: Always run dependency update when enabled, comprehensive dependency tracking

### Values Handling

**Issue #524: Values and Set Arguments Mixed, Changes Ignored**
- **Problem**: Using both `values` and `set` causes set changes to be ignored
- **Impact**: Configuration drift, unexpected behavior
- **Root Cause**: Incorrect precedence order implementation
- **Our Solution**: Proper Helm precedence: values.yaml → values file → set/set_sensitive

**Issue #906: Manifest Experiment Always Triggers Recreate**
- **Problem**: Provider increments revision number even without changes
- **Impact**: Unnecessary releases, polluted history
- **Root Cause**: Manifest comparison logic bug
- **Our Solution**: Accurate change detection using proper Helm comparison

## Implementation Plan

### Phase 1: Core Release Management
- Embed Helm SDK (helm.sh/helm/v3)
- Implement Create (helm install), Update (helm upgrade), Delete (helm uninstall)
- Support: repository, chart, version, values, set/set_sensitive list attributes
- Inline cluster configuration (same auth methods as k8sconnect_object)
- OCI registry support with proper authentication
- Digest-based chart references

### Phase 2: Wait and Readiness
- Wait for ALL workload types: Deployment, StatefulSet, DaemonSet, ReplicaSet, Job
- Configurable timeouts per operation
- Proper context management
- Hook execution with timeout handling
- Atomic rollback on failure

### Phase 3: State Management Excellence
- Transaction-like semantics: only update state on success
- Comprehensive drift detection (revision, values, metadata)
- Import support with auto-generated field filtering
- State validation and recovery logic
- Never lose resources from state

### Phase 4: Security and Sensitive Values
- Proper sensitive value propagation through all fields
- Never leak secrets in metadata or logs
- Test coverage for all sensitive scenarios
- Secure credential handling for OCI registries

### Phase 5: Testing
- Unit tests with mocked Helm SDK
- Integration tests against real k3d clusters
- State corruption tests (ensure we never lose state)
- Drift detection tests (manual helm rollback, upgrades, etc.)
- Sensitive value leakage tests
- Example demonstrating EKS + Cilium + cert-manager in single apply

## Critical Implementation Details

**Helm SDK Usage**: Use `helm.sh/helm/v3/pkg/action` package:
- `action.NewInstall()` for Create operations
- `action.NewUpgrade()` for Update operations
- `action.NewUninstall()` for Delete operations
- Proper RESTClientGetter implementation using our cluster config
- No kubeconfig required

**State Management**:
- Store release name, namespace, chart, version, revision
- Store computed values (manifest, status, etc.)
- Only update state after successful Helm operation
- Validate state consistency on Read
- Never remove from state without explicit Delete

**Wait Logic**:
- Use Helm's native wait logic as base
- Extend to cover all workload types
- Configurable timeouts with proper context propagation
- Clear error messages showing current status

**Chart Repository Handling**:
- Support HTTP/HTTPS repositories
- Support OCI registries (oci://)
- Support chart museums / Harbor / Artifactory
- Credential management for private repos
- Chart caching

**Values Merging**: Follow Helm's exact precedence order:
1. Chart's default values.yaml
2. User-supplied values file
3. Individual set values
4. set_sensitive values (highest priority)

**Sensitive Values**:
- Mark ALL computed fields containing sensitive data as sensitive
- Never include sensitive values in metadata
- Propagate sensitivity through entire state
- Test that sensitive values never appear in logs

## Architectural Decisions to Address HashiCorp Issues

### 1. State Management Architecture
**Decision**: Use explicit transaction semantics
- Create/Update/Delete only modify state on success
- Read validates state matches cluster (drift detection)
- Never remove from state unless Delete succeeds
- Test: Simulate failures at every step, ensure state consistency

### 2. Wait Strategy
**Decision**: Comprehensive workload readiness checking
- Detect resource types in rendered manifests
- Wait for each type appropriately (Deployment rollout, DaemonSet ready, Job completion, etc.)
- User-configurable timeout with proper context
- Clear error messages showing current status and waiting resources

### 3. Drift Detection
**Decision**: Compare all Helm metadata, not just values
- Check release revision number
- Compare values hash
- Verify chart version/digest
- Detect manual rollbacks, upgrades, or modifications

### 4. Sensitive Values
**Decision**: Sensitivity propagation from inputs to all outputs
- Mark schema fields as sensitive appropriately
- Never store raw sensitive values in computed metadata
- Test plan/apply output for credential leakage
- Use Terraform's built-in sensitive value handling

### 5. Import Support
**Decision**: Smart field filtering during import
- Auto-detect and filter Kubernetes server-generated fields
- Import existing releases cleanly without permanent drift
- Generate config skeletons showing required vs optional fields

### 6. OCI Authentication
**Decision**: Robust credential refresh for cloud registries
- Support AWS ECR token refresh
- Support Azure ACR token refresh
- Support GCR application default credentials
- Cache credentials with proper TTL
- Retry logic for transient auth failures

### 7. Digest Support
**Decision**: First-class support for immutable chart references
- Accept both `version = "1.0.0"` and `version = "1.0.0@sha256:..."`
- Track digest separately in state
- Detect digest changes as drift
- Support pure digest references without version tag

## Why NOT Just Template?

We considered building `k8sconnect_helm_template` (templating-only) but decided against it because:

**You lose critical Helm semantics:**
- No `helm list`, `helm history`, `helm rollback`
- Hooks don't execute (pre-install, post-upgrade, etc.)
- No atomic upgrades
- No three-way merge
- Can't use helm CLI to inspect/manage releases

**Our helm_release resource gives you:**
- Full Helm compatibility (it's a real release)
- CLI interop (helm list/rollback work)
- Hooks execute correctly
- **PLUS** bootstrapping (inline cluster config)
- **PLUS** fixes for all HashiCorp bugs

## Key Benefits

**For Users**:
- True single-apply cluster bootstrapping
- Real Helm releases (not just templates)
- All Helm CLI features work (list, history, rollback)
- Hooks execute properly
- More reliable than hashicorp/helm (no state corruption, proper drift detection)
- Better security (no sensitive value leaks)

**For k8sconnect**:
- Completes the bootstrapping story
- Maintains Helm ecosystem compatibility
- Differentiates from hashicorp provider (better quality + bootstrapping)
- Natural fit with k8sconnect_object for mixed workloads

**For the Ecosystem**:
- Proves bootstrapping is possible without two-phase applies
- Demonstrates provider-per-resource configuration pattern
- Shows Terraform CAN handle complex Kubernetes workflows reliably

## Relationship to Other ADRs

**ADR-007: CRD Dependency Resolution** - Complementary: helm_release can install charts with CRDs, our auto-retry handles CRD propagation

**ADR-011: Concise Diff Format** - Helm releases show in Terraform diffs, need clear representation

**Issue #127 Fix** - This ADR is the permanent solution to the validation bug. The validator fix allows unknown values from data sources; this provides native Helm support for bootstrapping.

## Migration Path

**From hashicorp/helm**:
```hcl
# Before - provider-level config prevents bootstrapping
provider "helm" {
  kubernetes {
    host = "..."  # Must be known before apply!
  }
}

resource "helm_release" "app" {
  name       = "myapp"
  chart      = "..."
  repository = "..."
}

# After - inline cluster config enables bootstrapping
resource "k8sconnect_helm_release" "app" {
  name       = "myapp"
  chart      = "..."
  repository = "..."

  cluster = {
    host = aws_eks_cluster.main.endpoint  # Can reference being created!
    ...
  }
}
```

**For new users**: Use k8sconnect_helm_release for foundation charts (Cilium, cert-manager, ingress controllers, ArgoCD). After ArgoCD/Flux is running, let GitOps handle application Helm releases.

## Success Criteria

Before declaring this feature complete:

1. **No state corruption**: 1000+ test cycles without losing resources from state
2. **Proper drift detection**: Manual helm rollback/upgrade detected every time
3. **All workload types wait correctly**: Deployments, StatefulSets, DaemonSets, Jobs
4. **No sensitive value leaks**: Comprehensive testing shows zero credential exposure
5. **Successful bootstrapping**: EKS/GKE/AKS + Cilium + cert-manager + ArgoCD in single apply
6. **Import works**: Can adopt existing Helm releases without drift
7. **Digest support**: SHA256 chart references work reliably
8. **Better than HashiCorp**: Users migrate FROM hashicorp/helm TO k8sconnect because of quality

If we can't meet these criteria, we should NOT ship this feature.
