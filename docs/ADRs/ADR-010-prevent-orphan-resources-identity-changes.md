# ADR-010: Preventing Orphan Resources on Identity Changes

## Status
Accepted - Implemented (2025-10-06)

## Context

**Critical bug**: Changing resource identity fields (kind, apiVersion, metadata.name, metadata.namespace) in `yaml_body` creates orphan resources.

**Problem**: User changes Pod → ConfigMap. Update applies ConfigMap YAML, Kubernetes creates ConfigMap (different types can have same name), Terraform ID stays same now pointing to ConfigMap, **Pod is orphaned** - still running but untracked.

**Security implication**: ServiceAccount in kube-system later changed to ConfigMap → ServiceAccount orphaned with elevated privileges.

**Root cause** (ADR-003): Random UUIDs for Terraform IDs enable multi-cluster but create gap for identity changes - no detection mechanism existed.

**Other providers**: kubectl uses `ForceNew` schema attribute (only available in legacy SDK). HashiCorp uses `RequiresReplace` in low-level protocol. We use terraform-plugin-framework which doesn't support ForceNew.

## Decision

**Use ModifyPlan with RequiresReplace in Terraform Framework.**

In ModifyPlan, detect identity changes (kind, apiVersion, metadata.name, metadata.namespace) by comparing state and plan YAML. If changes detected, set `resp.RequiresReplace` with warning diagnostic showing old → new values. Terraform handles destroy/create automatically.

Implemented in identity_changes.go with `checkResourceIdentityChanges()` and `detectIdentityChanges()`.

## Per-Field Analysis

**metadata.name**: MUST trigger replacement - name is fundamental resource identity

**metadata.namespace**: MUST trigger replacement - namespace is part of identity for namespaced resources. Cluster-scoped resources (both return "") correctly detected as no change.

**kind**: MUST trigger replacement - different Kinds are different resource types, can coexist with same name (Pod → ConfigMap creates orphan)

**apiVersion**: MUST trigger replacement - safer to replace than risk duplicates. Aligns with kubectl/kubernetes providers. Even if API server would alias versions, explicit recreation is clearer.

**NOT checked**: labels, annotations (not part of identity), uid (read-only), spec/status (handled by drift detection)


## Relationship to ADR-002

**ADR-002** (Immutable Fields): Same resource, immutable field change → K8s rejects with 422 error

**ADR-010** (Identity Changes): Different resource identity → K8s accepts, creates new resource → triggers replacement

