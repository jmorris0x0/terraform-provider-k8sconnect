# ADR-015: Actionable Error Messages and Diagnostic Context

## Status
Accepted

## Summary

All error messages must explain **why** an operation failed and provide **actionable next steps**. When operations timeout or fail, perform additional diagnostic API calls to gather context that helps users understand and resolve the issue. This is a key differentiator from other Kubernetes Terraform providers.

## Context

### The Problem

During dogfooding, we encountered this error:

```
Error: Deletion Stuck Without Finalizers

Resource was marked for deletion but did not complete within 10m0s.
The resource has no finalizers blocking deletion.

Set force_destroy = true to remove finalizers and force deletion.
```

**What this tells us:**
- ✅ Deletion was initiated
- ✅ No finalizers are blocking
- ❌ **Why is it taking so long?**
- ❌ **Is it making progress?**
- ❌ **What should I do differently?**

The namespace contained 74+ resources and Kubernetes was cascading deletion through them. This is **expected behavior** but the error made it seem like something was broken.

### Current State of Kubernetes Provider Errors

**hashicorp/kubernetes provider:**
```
Error: namespaces "test" not found
```
*No context, no suggestions, just the raw K8s error.*

**gavinbunney/kubectl provider:**
```
Error: timeout while waiting for resource to be deleted
```
*Slightly better, but still no explanation or guidance.*

### Why This Matters

1. **User Experience**: Clear errors reduce frustration and support burden
2. **Debugging Time**: Good errors eliminate the "run kubectl manually to figure out what's wrong" step
3. **Competitive Advantage**: This is a concrete, visible improvement over existing providers
4. **Trust**: When errors are helpful, users trust the tool more

### Real-World Examples of Poor Errors

**Example 1: Namespace Deletion Timeout**
```
Error: Deletion timeout
```
*User has to manually run: `kubectl get all -n namespace` to discover 50 resources still cleaning up*

**Example 2: Finalizer Blocking**
```
Error: Resource not deleted
```
*User has to manually inspect managedFields to find that cert-manager finalizer is stuck*

**Example 3: CRD Not Ready**
```
Error: could not find the requested resource
```
*User doesn't know if the CRD doesn't exist or if it's just not ready yet*

## Decision

Implement a **diagnostic-first error strategy**:

### Core Principles

1. **Every error explains WHY, not just WHAT**
2. **Every error provides actionable next steps**
3. **Perform additional diagnostic API calls when it helps**
4. **Tailor errors to the specific resource type and failure mode**
5. **Include relevant kubectl commands for manual investigation**

### Implementation Guidelines

#### 1. Namespace Deletion Timeouts

When a namespace deletion times out, **perform a diagnostic API call** to list remaining resources:

```go
func (r *manifestResource) explainNamespaceDeletionFailure(ctx context.Context, client k8sclient.K8sClient, namespace string) string {
    // Single LIST call to get diagnostic context
    resourceList, err := client.ListNamespaceResources(ctx, namespace)
    if err != nil {
        return "(Could not check namespace contents: check cluster connectivity)"
    }

    if len(resourceList) == 0 {
        return "Namespace is empty but still not deleted. This may indicate an API server issue."
    }

    // Group by kind and identify blockers
    resourceCounts := make(map[string]int)
    var resourcesWithFinalizers []string

    for _, resource := range resourceList {
        resourceCounts[resource.GetKind()]++
        if len(resource.GetFinalizers()) > 0 {
            resourcesWithFinalizers = append(resourcesWithFinalizers,
                fmt.Sprintf("%s/%s (finalizers: %v)",
                    resource.GetKind(), resource.GetName(), resource.GetFinalizers()))
        }
    }

    // Build actionable message
    return formatNamespaceDeletionDiagnostics(resourceCounts, resourcesWithFinalizers)
}
```

**Enhanced Error Message:**
```
Error: Namespace Deletion Timeout

Namespace "oracle" did not delete within 10m0s

Namespace still contains 47 resources:
  - 12 Pods
  - 8 ConfigMaps
  - 5 Deployments
  - 3 PersistentVolumeClaims
  - 19 other resources

Resources with finalizers (may be blocking):
  - PersistentVolumeClaim/data-postgres-0 (finalizers: [kubernetes.io/pvc-protection])
  - Pod/stuck-pod (finalizers: [custom.io/cleanup])

WHY: Kubernetes is cascading deletion through these resources. This is normal
for namespaces with many resources, but can take time depending on finalizers
and dependent resources.

WHAT TO DO:
• To wait longer:
    delete_timeout = "20m"

• To force immediate removal (WARNING: may orphan resources):
    force_destroy = true

• To manually investigate:
    kubectl get all -n oracle
    kubectl get pvc -n oracle
    kubectl api-resources --verbs=list --namespaced -o name | xargs -n 1 kubectl get -n oracle
```

**Cost Analysis:**
- **Performance**: Single LIST API call, only on timeout (rare)
- **Value**: Eliminates need for manual kubectl investigation
- **Verdict**: **Worth it** - dramatic UX improvement for minimal cost

#### 2. Finalizer Explanations

Provide context-specific explanations for **built-in Kubernetes finalizers only**. We use a conservative approach: only include finalizers that are officially documented and stable across K8s versions.

```go
type FinalizerInfo struct {
    Explanation string
    Source      string // Documentation link for verification
}

// Only include built-in Kubernetes finalizers that are:
// 1. Officially documented in kubernetes.io/docs
// 2. Stable for 2+ years
// 3. Guaranteed backward compatible
var knownFinalizers = map[string]FinalizerInfo{
    "kubernetes.io/pvc-protection": {
        Explanation: "Volume is still attached to a pod",
        Source:      "https://kubernetes.io/docs/concepts/storage/persistent-volumes/#storage-object-in-use-protection",
    },
    "kubernetes.io/pv-protection": {
        Explanation: "PersistentVolume is still bound to a claim",
        Source:      "https://kubernetes.io/docs/concepts/storage/persistent-volumes/#storage-object-in-use-protection",
    },
    "kubernetes": {
        Explanation: "Namespace is deleting all contained resources",
        Source:      "https://kubernetes.io/docs/concepts/overview/working-with-objects/namespaces/#automatic-deletion",
    },
    "foregroundDeletion": {
        Explanation: "Waiting for owned resources to delete first",
        Source:      "https://kubernetes.io/docs/concepts/architecture/garbage-collection/#foreground-deletion",
    },
    "orphan": {
        Explanation: "Dependents will be orphaned (not deleted)",
        Source:      "https://kubernetes.io/docs/concepts/architecture/garbage-collection/#orphan-dependents",
    },
}

func explainFinalizer(finalizer string) string {
    if info, ok := knownFinalizers[finalizer]; ok {
        return fmt.Sprintf("  • %s: %s\n    (See: %s)", finalizer, info.Explanation, info.Source)
    }
    return fmt.Sprintf("  • %s (custom finalizer - check controller logs)", finalizer)
}
```

**Adding New Finalizers:**

Only add a finalizer if ALL of these are true:
- ✅ Documented in official K8s or operator documentation
- ✅ Stable for 2+ years across versions
- ✅ Widely used (encountered during dogfooding)
- ✅ Behavior is well-defined and won't change

**Do NOT add:**
- ❌ Operator-specific finalizers unless widely used (e.g., cert-manager, ArgoCD)
- ❌ Custom finalizers from specific deployments
- ❌ Undocumented finalizers
- ❌ Version-specific behaviors

**Graceful Fallback:**
Unknown finalizers get a generic message that's still helpful:
```
• custom.company.io/cleanup (custom finalizer - check controller logs)
```

This approach ensures we're 100% accurate while still being helpful.

**Before:**
```
Error: Deletion blocked by finalizers
Finalizers: [kubernetes.io/pvc-protection]
```

**After:**
```
Error: Deletion Blocked by Finalizers

PersistentVolumeClaim "data-postgres-0" cannot be deleted due to finalizers:
  • kubernetes.io/pvc-protection: Volume is still attached to a pod
    (See: https://kubernetes.io/docs/concepts/storage/persistent-volumes/#storage-object-in-use-protection)

WHY: Kubernetes prevents deleting volumes that are still in use to avoid data loss.

WHAT TO DO:
• Delete or scale down pods using this volume:
    kubectl get pods -o json | jq '.items[] | select(.spec.volumes[]?.persistentVolumeClaim.claimName=="data-postgres-0") | .metadata.name'

• If pods are already gone but finalizer is stuck:
    kubectl patch pvc data-postgres-0 -p '{"metadata":{"finalizers":null}}'
    (WARNING: Only do this if you're sure the volume is unused)
```

#### 3. CRD Availability Errors

Distinguish between "doesn't exist" and "not ready yet":

```go
func (r *manifestResource) classifyCRDError(err error, crdName string) (severity, title, detail string) {
    if isDiscoveryError(err) {
        // Perform diagnostic: check if CRD exists
        crd, checkErr := client.GetCRD(ctx, crdName)

        if checkErr == nil && crd != nil {
            // CRD exists but not yet in discovery
            return "warning", "CRD Not Ready",
                fmt.Sprintf("CRD '%s' exists but is not yet available in API discovery.\n\n"+
                    "WHY: CRDs take 5-15 seconds to become available after creation.\n\n"+
                    "WHAT TO DO:\n"+
                    "• This resource will be created automatically during apply (auto-retry)\n"+
                    "• To check CRD status: kubectl get crd %s -o yaml\n"+
                    "• To see conditions: kubectl get crd %s -o jsonpath='{.status.conditions}'",
                    crdName, crdName, crdName)
        }

        // CRD truly doesn't exist
        return "error", "CRD Not Found",
            fmt.Sprintf("CustomResourceDefinition '%s' does not exist.\n\n"+
                "WHY: The API server has no definition for this resource type.\n\n"+
                "WHAT TO DO:\n"+
                "• Install the CRD: kubectl apply -f crd-definition.yaml\n"+
                "• Check if operator is running: kubectl get pods -A | grep operator\n"+
                "• List available CRDs: kubectl get crds",
                crdName)
    }

    return "error", "Resource Not Found", err.Error()
}
```

#### 4. Network and API Server Errors

```go
func classifyK8sError(err error, operation string) (severity, title, detail string) {
    switch {
    case isNetworkError(err):
        return "error", "Cluster Connection Failed",
            fmt.Sprintf("Failed to connect to Kubernetes API server during %s.\n\n"+
                "WHY: Network connectivity or authentication issue.\n\n"+
                "WHAT TO DO:\n"+
                "• Check cluster is running: kubectl cluster-info\n"+
                "• Verify credentials: kubectl auth can-i get pods\n"+
                "• Check network connectivity to API server\n"+
                "• Error details: %v", operation, err)

    case isRateLimitError(err):
        return "warning", "API Rate Limited",
            "Kubernetes API server is rate limiting requests. Retrying with backoff.\n\n"+
                "WHY: Too many requests to the API server.\n\n"+
                "If this persists:\n"+
                "• Reduce number of resources in single apply\n"+
                "• Check for other tools making API requests"

    case isServerError(err):
        return "error", "API Server Error",
            fmt.Sprintf("Kubernetes API server returned an error during %s.\n\n"+
                "WHY: Internal server error or API server instability.\n\n"+
                "WHAT TO DO:\n"+
                "• Retry: terraform apply\n"+
                "• Check API server logs: kubectl logs -n kube-system kube-apiserver-*\n"+
                "• Check cluster health: kubectl get componentstatuses\n"+
                "• Error details: %v", operation, err)
    }

    return "error", "Unknown Error", err.Error()
}
```

### Error Message Template

All errors should follow this structure:

```
Error: [DESCRIPTIVE TITLE]

[WHAT HAPPENED - 1-2 sentences]

[CURRENT STATE - key facts about the resource]

WHY: [ROOT CAUSE EXPLANATION]

WHAT TO DO:
• [PRIMARY ACTION with example command]
• [SECONDARY ACTION with example command]
• [INVESTIGATION STEPS with kubectl commands]

[Optional: RELATED DOCS link]
```

### When to Perform Diagnostic API Calls

**DO perform diagnostic calls when:**
- ✅ The error is ambiguous without context (namespace deletion timeout)
- ✅ The cost is one additional API call per error occurrence
- ✅ Users would otherwise need to run kubectl manually
- ✅ The failure is rare (timeouts, not every validation error)

**DON'T perform diagnostic calls when:**
- ❌ The error is already clear (validation errors)
- ❌ Would require multiple or expensive API calls
- ❌ Happens frequently (would add latency to common operations)
- ❌ Information is already available in the error

## Consequences

### Positive

1. **Dramatically Better UX**: Users can act on errors immediately without running kubectl
2. **Competitive Advantage**: Visible, concrete improvement over existing providers
3. **Reduced Support Burden**: Self-service troubleshooting reduces questions
4. **Builds Trust**: Helpful errors make the tool feel more reliable
5. **Faster Debugging**: Context is provided automatically, not discovered manually

### Negative

1. **Implementation Effort**: Each error type needs thoughtful design
2. **Slight Performance Cost**: Diagnostic API calls add latency on errors (acceptable - only on rare failures)

### Neutral

1. **Message Verbosity**: Longer errors, but that's the point
2. **Kubectl Dependency**: Messages assume users have kubectl (reasonable)

## What Is and Isn't Brittle

### NOT Brittle (Zero Maintenance)

**Resource listing and counting:**
- Uses standard K8s discovery + dynamic client
- Returns whatever exists in the cluster
- No hardcoded resource types
- Groups and counts dynamically

**Error message structure:**
- Template-based formatting
- No assumptions about specific resources
- Generic fallbacks for unknown cases

### Minimally Brittle (Rare Updates)

**Built-in K8s finalizer explanations:**
- Only 5 core K8s finalizers included
- All are officially documented and stable for years
- Backward compatible guarantees from K8s project
- Unknown finalizers get generic message (no breakage)
- Expected maintenance: Maybe 1 addition per year when encountering new common ones

**Common error patterns:**
- Network errors, API errors, timeouts
- These patterns rarely change
- Expected maintenance: Almost never

### Testing Strategy

**DO test (Unit tests):**
- ✅ Error message formatting (pure functions)
- ✅ Resource grouping/counting logic (pure functions)
- ✅ Finalizer explanation lookup (simple map access)

**DON'T test (Brittle, caught in dogfooding):**
- ❌ Actual error paths in CRUD operations
- ❌ Exact error message wording
- ❌ Integration with real clusters
- ❌ Timing-dependent failures (timeouts)

**Trust dogfooding:**
- Real usage surfaces real issues
- Manual testing during development catches obvious bugs
- Acceptance tests already cover happy paths

### Maintenance Expectations

**Annual review:**
- Check if any K8s finalizers were deprecated (extremely rare)
- Add new finalizers encountered during dogfooding (1-2 per year max)
- Verify documentation links still work

**No ongoing maintenance needed for:**
- Resource listing (dynamic from API)
- Error structure and formatting
- Generic fallback messages

## Implementation Plan

### Phase 1: Critical Deletion Errors (Immediate)
- ✅ Enhanced namespace deletion timeout errors with resource listing
- ✅ Finalizer explanations for 5 built-in K8s finalizers (100% accurate, documented)
- ✅ Better "stuck deletion" diagnostics with actionable next steps

### Phase 2: Common Failure Modes (Near-term)
- CRD availability classification
- Network error explanations
- API server error classification
- Rate limiting guidance

### Phase 3: Resource-Specific Errors (Future)
- PVC attachment diagnostics
- Service LoadBalancer provisioning failures
- Deployment rollout failures
- Admission webhook rejections

## Examples

### Example 1: Before and After - Namespace Timeout

**Before:**
```
Error: Deletion Stuck Without Finalizers

Resource was marked for deletion but did not complete within 10m0s.
```

**After:**
```
Error: Namespace Deletion Timeout

Namespace "oracle" did not delete within 10m0s

Namespace still contains 47 resources including:
  - 12 Pods
  - 8 ConfigMaps
  - 5 Deployments (including 1 with finalizers)
  - 3 PersistentVolumeClaims (2 still attached to pods)

WHY: Kubernetes is cascading deletion through these resources. With 74 total
resources, this can take 10+ minutes depending on finalizers and dependencies.

WHAT TO DO:
• Increase timeout for large namespaces:
    delete_timeout = "20m"

• To investigate what's blocking:
    kubectl get all -n oracle
    kubectl get pvc -n oracle -o json | jq '.items[] | select(.status.phase!="Bound")'

• Force removal (WARNING: may orphan cloud resources):
    force_destroy = true
```

### Example 2: Before and After - PVC Finalizer

**Before:**
```
Error: timeout waiting for deletion
```

**After:**
```
Error: PersistentVolumeClaim Deletion Blocked

PVC "data-postgres-0" cannot be deleted due to finalizer:
  • kubernetes.io/pvc-protection: Volume is still attached to a pod
    (See: https://kubernetes.io/docs/concepts/storage/persistent-volumes/#storage-object-in-use-protection)

WHY: Kubernetes prevents deleting volumes while they're in use to avoid data loss.

WHAT TO DO:
• Find pods using this volume:
    kubectl get pods -n default -o json | \
      jq '.items[] | select(.spec.volumes[]?.persistentVolumeClaim.claimName=="data-postgres-0") | .metadata.name'

• Delete the pod first, then the PVC will delete automatically

• If pod is already gone but finalizer stuck (check pod list first!):
    kubectl patch pvc data-postgres-0 -p '{"metadata":{"finalizers":null}}'
    (WARNING: Only do this if you've verified no pods are using it)
```

## Alternatives Considered

### Alternative 1: Minimal Errors Like Other Providers
**Rejected** because poor errors are a known pain point. We have the opportunity to do better.

### Alternative 2: Always Perform Diagnostic Calls
**Rejected** because it would add latency to every error, including simple validation failures.

### Alternative 3: External Diagnostic Tool
**Rejected** because requiring a separate tool reduces accessibility. Better to integrate.

### Alternative 4: Log-Based Diagnostics
**Rejected** because users would need to enable verbose logging and search logs. Direct errors are better UX.

## References

- [DOGFOODING.md](../DOGFOODING.md) - Real-world namespace deletion timeout experience
- [DELETION_ANALYSIS.md](../DELETION_ANALYSIS.md) - Deep dive into deletion mechanics
- [Kubernetes API Server Errors](https://kubernetes.io/docs/reference/using-api/api-concepts/#api-responses)
- [kubectl Source Code](https://github.com/kubernetes/kubectl) - Examples of good CLI error messages

## Metrics for Success

We'll know this is working when:

1. **User feedback**: Fewer "how do I debug this?" questions
2. **GitHub issues**: Fewer issues filed due to confusing errors
3. **Documentation**: Error messages are self-documenting, reducing docs burden
4. **Competitive positioning**: Error quality becomes a documented differentiator

## Future Enhancements

1. **Error Codes**: Add machine-readable error codes for programmatic handling
2. **Error Remediation**: Suggest Terraform config changes (e.g., "add `delete_timeout = '20m'` to this resource")
3. **Progressive Enhancement**: As we learn common failure modes, add more specific messages
4. **Telemetry**: Track which errors are most common to prioritize improvements
