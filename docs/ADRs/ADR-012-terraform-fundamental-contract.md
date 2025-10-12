# ADR-012: Terraform's Fundamental Contract

## Status
Accepted - Foundational Principle

## Decision

**k8sconnect adopts the Pragmatic Interpretation:**

- **State shows managed fields only** (per SSA managedFields), not complete cluster object
- **Drift detection applies to managed fields** - fields the provider is responsible for
- **Framework limitations make strict interpretation technically infeasible**
- **This aligns with Kubernetes SSA semantics** and industry practice

## Terraform's Core Contract

```
HCL (desired state) + State (actual reality) = Truth

When they differ, the user MUST be informed.
```

**Strict Interpretation**: State must contain ALL fields that exist in infrastructure, even if the provider didn't set them. Show drift on all fields.


## Framework Limitation

**Discovery**: terraform-plugin-framework does NOT provide APIs to suppress diffs for subsets of stored state.

**The gap**: Framework has no API for "store field X, but don't show diffs for it if ignore_fields says so."

**Impossible with framework**:
1. Store complete state including all K8s defaults
2. User sets `ignore_fields = ["spec.protocol"]`
3. Field changes in cluster: `protocol: TCP → UDP`
4. **Cannot suppress this diff** - framework will show it
5. User sees noise they explicitly asked to ignore

**Framework facts** (95%+ certainty):
- No equivalent to SDK v2's `DiffSuppressFunc`
- Plan modifiers work at attribute level, not sub-paths
- Semantic equality cannot access other attributes
- Custom types cannot read `ignore_fields` during comparison

## Pragmatic Interpretation for SSA-Based Providers

**Given framework limitations, for providers using Server-Side Apply (SSA):**

**Strict interpretation (ideal, but framework-blocked):**
> "State must contain ALL fields that exist in infrastructure"

**Pragmatic interpretation (framework-compatible):**
> "State must contain all fields the provider is responsible for managing, as determined by the infrastructure's field ownership mechanism (managedFields in K8s)"

**Modified contract:**

```
HCL (desired state) + State (managed fields) = Truth

When managed fields differ, user MUST be informed.
```

**The three parts become:**

1. **HCL declares desired state for managed fields** - What you write is what you want to manage
2. **State reflects actual managed fields** - Shows fields provider manages (per SSA managedFields), not fields other controllers own
3. **Drift detection for managed fields** - When HCL ≠ State for managed fields, show user. When another controller takes a field, show ownership change.

## Why Pragmatic Interpretation Is Acceptable

**Acceptable when:**
1. ✅ Infrastructure has explicit field ownership mechanism (K8s SSA)
2. ✅ User can see ownership information (`field_ownership` attribute)
3. ✅ User can take ownership by explicitly specifying fields
4. ✅ User can release ownership via `ignore_fields`
5. ✅ Framework technically cannot support strict interpretation with clean UX
6. ✅ Documentation clearly explains the behavior
7. ✅ Other mature providers use similar approach (no K8s provider achieves strict interpretation with clean UX)

**Not acceptable when:**
1. ❌ Hiding drift for convenience (not framework limitation)
2. ❌ No way for user to take ownership of hidden fields
3. ❌ No visibility into who owns what
4. ❌ Undocumented or surprising behavior

## Trade-off

**User might miss changes to fields they didn't know existed**, but UX is clean and they can opt-in to managing any field by adding it to their YAML.

**Alternatives**:
- **Strict interpretation**: Overwhelming diff noise for every K8s default (unusable UX)
- **Pragmatic interpretation**: Clean UX, users control via explicit field specifications

**k8sconnect chooses pragmatic** given framework limitations and SSA field ownership model.
