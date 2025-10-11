# ADR-003: Terraform Resource ID Strategy for Kubernetes Providers

## Status
Accepted

## Context

When building a Terraform provider that manages Kubernetes resources with per-resource connection configuration, we must decide how to generate Terraform resource IDs. This decision has significant implications for resource stability, multi-cluster support, and conflict prevention.

The fundamental question is: **Should we use deterministic or non-deterministic (random) resource IDs?**

## The Impossible Requirements Triangle

For deterministic IDs to work safely, we need ALL of the following:

1. **Extreme Stability**: IDs must never change due to configuration modifications, certificate rotations, or infrastructure changes
2. **Determinism**: Same inputs must always produce the same ID to prevent Terraform state collisions
3. **Multi-cluster Isolation**: Resources with identical namespace/kind/name in different clusters must have different IDs

**Why this is impossible:**

**Kubernetes Resource Identity Only:**
```
ID = hash(namespace + kind + name)
```
- ✅ Stable: Never changes
- ✅ Deterministic: Same inputs = same ID
- ❌ Multi-cluster: Same resource in different clusters = collision

**Adding Cluster Information:**
```
ID = hash(clusterIdentifier + namespace + kind + name)
```

All possible cluster identifiers have fatal flaws:

- **Cluster UID**: Requires network call during planning phase (violates Terraform constraints)
- **Server URL**: Changes due to load balancer updates, DNS changes, infrastructure migrations
- **Kubeconfig Content**: Changes with certificate rotations, formatting changes, credential updates
- **Connection Configuration**: Any modification breaks all resource IDs

The planning phase constraint makes cluster-based deterministic IDs impossible - Terraform generates IDs during plan (no network calls allowed), but cluster identity requires querying the cluster.

## Decision

**Use random hex IDs (12 characters) with annotation-based ownership tracking.**

Since deterministic IDs cannot satisfy the impossible requirements triangle, we abandon the deterministic constraint and solve conflict prevention through Kubernetes annotations.

### How It Works

1. **Resource Creation**: Generate 12-character random hex ID (6 bytes) for Terraform resource ID
2. **Ownership Annotation**: Store the Terraform ID in `k8sconnect.terraform.io/terraform-id` annotation
3. **Conflict Detection**: Check annotations during operations to prevent conflicts
4. **Multi-cluster Safety**: Different Terraform resources get different random IDs even for identical Kubernetes resources

During Create, we generate a random ID and set it as an annotation on the Kubernetes resource. During Read/Update, we validate that the annotation matches our expected ID. If mismatched, we error indicating another Terraform resource manages it. During Import, we read the existing annotation or generate a new one if unmanaged.

## Benefits

- **Stability**: Random IDs never change due to configuration changes (kubeconfig rotation, certificate updates, DNS changes)
- **Multi-cluster Support**: Same Kubernetes resource in different clusters gets different Terraform IDs
- **Plan-time Compatible**: No external calls needed during planning - IDs generated from randomness
- **Conflict Prevention**: Ownership annotations prevent management conflicts
- **Follows Kubernetes Patterns**: Mirrors Helm, ArgoCD, and operator patterns using annotations for ownership

## Drawbacks

### ❌ Resource IDs Not Human-Readable
Terraform resource IDs are 12-character hex strings (e.g., `a1b2c3d4e5f6`) instead of meaningful identifiers like `namespace.production`.

### ❌ Conflict Detection at Apply-Time
Conflicts are detected during `terraform apply` rather than `terraform plan`. The plan phase succeeds, but apply fails with ownership conflict error. This is unavoidable - we can't check annotations without contacting the cluster, which violates planning phase constraints.

### ❌ Debugging Requires Annotation Lookup
Troubleshooting requires correlating Terraform hex IDs with Kubernetes annotations:
```bash
kubectl get namespace production -o jsonpath='{.metadata.annotations.k8sconnect\.terraform\.io/terraform-id}'
# Output: a1b2c3d4e5f6
```

### ❌ Dependency on Kubernetes Annotations
Solution relies on annotation storage. Annotations could be stripped by other tools. Import process must handle resources with missing or conflicting annotations.

## Alternatives Considered and Rejected

**User-Provided Cluster Identifiers**: Rejected - adds user complexity and still subject to user error.

**Provider-Level Connection Configuration**: Rejected - eliminates the core value proposition of per-resource connections.

**Resource-Only IDs (hash of namespace/kind/name)**: Rejected - doesn't support multi-cluster scenarios.

## Conclusion

Non-deterministic IDs with annotation-based ownership are the only viable solution that satisfies all core requirements. The approach aligns with Kubernetes ecosystem patterns (Helm, ArgoCD, operators) and provides a foundation for robust multi-cluster resource management.

The trade-off of human-readable IDs for operational safety and stability is justified given the impossible constraints of deterministic ID generation in multi-cluster environments.
