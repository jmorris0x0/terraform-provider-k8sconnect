# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**terraform-provider-k8sconnect** is an enterprise-quality Kubernetes Terraform provider that enables bootstrapping clusters and workloads in a **single terraform apply** using inline per-resource connections. Key differentiators:

- **Server-Side Apply (SSA)** with field ownership tracking - prevents conflicts with K8s controllers
- **Dry-run projections** - shows accurate diffs before apply (what K8s will actually do, not just what you send)
- **Inline connections** - no provider dependency hell, works in modules, supports multi-cluster
- **Universal CRD support** - no schema translation needed

Read README.md for user-facing context. Read ADRs (especially ADR-001, ADR-005, ADR-011) for deep architectural decisions.

## Build, Test, and Development Commands

### Build and Install
```bash
# Build the provider binary
make build

# Install provider locally for testing with Terraform
make install
# Provider installs to: ~/.terraform.d/plugins/registry.terraform.io/local/k8sconnect/<version>/<os>_<arch>
```

### Testing

```bash
# Unit tests only (fast, no cluster needed)
make test

# Acceptance tests (requires k3d cluster, slower)
make testacc

# If the above has too much output, run like this to get the summary first
make testacc 2> &1 | grep FAIL

# Then run ONE specific acceptance test to zoom in
TEST=TestAccManifestResource_Basic make testacc

# Test runnable examples (dual-purpose acceptance tests, requires k3d cluster)
make test-examples

# Run one runnable examples test (requires k3d cluster)
TEST=TestExamples/yaml-split-dependency-ordering make test-examples

# Coverage report (generates coverage.html)
make coverage
```

**IMPORTANT:** After finishing a substantial feature, run EVERY SINGLE TEST (all three test targets).

**I REPEAT**: The acceptance criteria for a change is ALL tests passing! No assumptions!

### Other Commands

```bash
# Linting and vet
make lint
make vet

# Security scanning
make security-scan

# Generate provider documentation
make docs

# Clean test artifacts
make clean
```

## Architecture Overview

### Core Design Principles

1. **Managed State Projection (ADR-001)**:
   - Store both `yaml_body` (user config) and `managed_state_projection` (filtered K8s state)
   - Use server-side dry-run during plan to predict accurate changes
   - Only track fields we actually manage (via SSA field ownership)

2. **Field Ownership Strategy (ADR-005)**:
   - Parse `managedFields` from K8s resources to determine what we own
   - Project only owned fields to avoid false drift (e.g., HPA changing replicas, K8s adding nodePort)
   - Always use `force=true` during apply to take ownership of conflicted fields

3. **Bootstrap-Aware Projection (ADR-011)**:
   - **Smart CREATE logic**: Do dry-run when cluster exists + values known, fallback to yaml when bootstrapping
   - Handles "unknown after apply" during cluster creation gracefully
   - Detects `${...}` interpolations and sets projection to unknown when YAML unparseable

### Key Files and Their Responsibilities

#### Resource Layer
- **`internal/k8sconnect/resource/manifest/manifest.go`**: Schema definition and resource interface implementation
- **`internal/k8sconnect/resource/manifest/plan_modifier.go`**: ModifyPlan implementation - executes dry-run, computes projection
- **`internal/k8sconnect/resource/manifest/crud.go`**: Create/Read/Update/Delete operations using SSA
- **`internal/k8sconnect/resource/manifest/projection.go`**: Field filtering logic based on managedFields
- **`internal/k8sconnect/resource/manifest/field_ownership.go`**: Parse K8s managedFields (FieldsV1 format)
- **`internal/k8sconnect/resource/manifest/identity_changes.go`**: Detect identity changes requiring replacement (ADR-010)

#### Client and Auth
- **`internal/k8sconnect/common/k8sclient/client.go`**: K8s client wrapper with SSA support
- **`internal/k8sconnect/common/auth/`**: Connection handling (kubeconfig, token, exec, client cert)
- **`internal/k8sconnect/common/factory/factory.go`**: Client factory with connection caching

#### Critical Concepts

**Projection Flow:**
1. **Plan phase** (plan_modifier.go): Parse yaml → dry-run → extract owned fields → store projection
2. **Apply phase** (crud.go): SSA apply → read back → extract owned fields → update state
3. **Read phase** (crud.go): Get current state → extract owned fields → detect drift

**Field Ownership Parsing:**
- K8s stores ownership in `managedFields[].fieldsV1` as nested JSON like `{"f:spec":{"f:replicas":{}}}`
- We parse this to determine which exact fields we own vs other controllers
- Projection only includes our owned fields

**Connection Ready Check:**
- During bootstrap, `cluster_connection.host` may be "known after apply"
- `isConnectionReady()` detects this and triggers yaml fallback instead of dry-run
- Once cluster exists, dry-run produces accurate projections with K8s defaults

## Critical Implementation Details

### Why Field-Level Ownership Matters

Without SSA field ownership tracking, resources like LoadBalancer Services show false drift:
- User specifies `port: 80`
- K8s adds `nodePort: 32156` (random)
- Dry-run predicts `nodePort: 32769` (different random)
- Provider sees drift on every plan

Solution: Only track fields owned by `fieldManager: "k8sconnect"` - ignore server-added fields.

### The Bootstrap Problem (ADR-011)

**Problem:** When creating cluster + resources in one apply, connection values are unknown during plan:
```hcl
cluster_connection = {
  host = aws_eks_cluster.main.endpoint  # "known after apply"
}
```

**Solution:** Smart projection logic in `plan_modifier.go`:
- Check if `yaml_body` is parseable (no `${...}` interpolations)
- Check if `cluster_connection` is ready (host + auth known)
- Check if `ignore_fields` is known
- **If all known**: Do dry-run → accurate projection
- **If any unknown**: Use yaml fallback or unknown → graceful degradation

### Testing Patterns

**Acceptance tests** (`*_test.go` in manifest/):
- Use `resource.Test` with `TestStep` sequences
- Environment vars set by `make testacc` (TF_ACC_K8S_HOST, TF_ACC_KUBECONFIG, etc.)
- Follow existing patterns in `basic_test.go`, `drift_test.go`, etc.

**Unit tests** (`.../unit_test.go`, `*_test.go` for non-acceptance):
- Use table-driven tests where applicable
- Mock K8s client interactions via `common/test/ssa_client.go`

### Platform-Specific Notes

**You are on macOS**, so:
- Use macOS command flags (e.g., `base64 -D` not `base64 -d`)
- k3d cluster runs in Docker Desktop
- tfenv manages Terraform versions

## Collaboration Guidelines

**When proposing solutions involving trade-offs or UX changes, ASK FIRST.** This codebase prioritizes:
1. Accurate diffs (no false positives)
2. Clean UX (minimal confusion)
3. Universal CRD support (no hardcoded schemas)

If a solution sacrifices any of these, discuss before implementing.

**Don't make commits.** The user handles git operations.

## Important Context

### Current Work (from bootstrap-changes-implementation-plan.md)

We are implementing "smart projection for CREATE" to fix:
- Bug: `plan_modifier.go:248-252` always sets projection to unknown for CREATE
- Goal: Do dry-run when cluster exists, show accurate projection including K8s defaults
- Requires: Enhanced `isConnectionReady()` to detect when cluster EXISTS vs doesn't exist

See `docs/bootstrap-changes-implementation-plan.md` for full context.

### ADRs You Should Read

- **ADR-001**: Managed state projection core design
- **ADR-005**: Field ownership strategy (why we parse managedFields)
- **ADR-011**: Concise diff format and bootstrap handling
- **ADR-012**: Terraform fundamental contract (why we can't hide ignored fields from state)

Other ADRs cover specific features (CRD retry, immutable resources, etc.) - read as needed.
