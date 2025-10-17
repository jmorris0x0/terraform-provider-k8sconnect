# ADR-015: Actionable Error Messages and Diagnostic Context

## Status
Implemented

## Summary

All error messages must explain **why** an operation failed and provide **actionable next steps**. When operations timeout or fail, perform diagnostic API calls to gather context. This is a key differentiator from other Kubernetes Terraform providers.

## Context

During dogfooding, namespace deletion timeouts showed unhelpful errors that didn't explain the namespace was cascading through 74+ resources (expected behavior). Other providers give raw K8s errors with no context or suggestions.

**The problem with existing providers:**
- `hashicorp/kubernetes`: `Error: namespaces "test" not found` (no context)
- `gavinbunney/kubectl`: `Error: timeout while waiting for resource to be deleted` (no explanation)

Good errors eliminate the "run kubectl manually to figure out what's wrong" step.

## Decision

### Core Principles

1. **Every error explains WHY, not just WHAT**
2. **Every error provides actionable next steps**
3. **Perform diagnostic API calls when it helps** (e.g., list resources in stuck namespace)
4. **Be specific**: Include actual field names, resource counts, specific values
5. **Include kubectl commands only when directly helpful**

### Error Message Template

```
Error: [Descriptive Title]

[Brief description of what happened - 1-2 sentences]

[Actionable steps as bulleted list]
• Action 1 with example if helpful
• Action 2
• Investigation command: kubectl ...

[Optional: Details: %v for raw error]
```

### Style Guidelines

- Keep total message under ~10 lines when possible
- Lead with what happened, not why
- **Be specific**: Include actual field names, resource counts, or specific values (e.g., "Fields modified: data.key1, spec.replicas" not "Some fields were modified")
- Assume users are technical and don't need verbose explanations
- Focus on actionable next steps
- **Cluster-scoped resources**: Always conditionally include `-n` flags in kubectl commands only when namespace is non-empty (ClusterRole, PersistentVolume, Namespace, etc. have no namespace)

### When to Perform Diagnostic API Calls

**DO** when:
- ✅ The error is ambiguous without context (namespace deletion timeout)
- ✅ Cost is one additional API call per error occurrence
- ✅ Users would otherwise need to run kubectl manually
- ✅ The failure is rare (timeouts, not validation errors)

**DON'T** when:
- ❌ The error is already clear (validation errors)
- ❌ Would require multiple or expensive API calls
- ❌ Happens frequently (adds latency to common operations)

### Implementation Patterns

**Namespace deletion timeouts:** Perform diagnostic LIST call to show remaining resources and finalizers

**Finalizer explanations:** Only explain built-in K8s finalizers (`kubernetes.io/pvc-protection`, `kubernetes`, `foregroundDeletion`, etc.). Unknown finalizers get generic message: `(custom finalizer - check controller logs)`

**CRD errors:** Distinguish "doesn't exist" from "not ready yet" by checking if CRD resource exists

Search the repo for existing examples in `internal/k8sconnect/resource/*/crud.go` and error handling code.

## Example

**Before:**
```
Error: Deletion Stuck Without Finalizers

Resource was marked for deletion but did not complete within 10m0s.
```

**After:**
```
Error: Namespace Deletion Timeout

Namespace "oracle" did not delete within 10m0s

Namespace still contains 47 resources:
  - 12 Pods
  - 8 ConfigMaps
  - 5 Deployments
  - 3 PersistentVolumeClaims (2 with finalizers)

This is normal for namespaces with many resources, but can take time.

To resolve:
• Increase timeout: delete_timeout = "20m"
• Force removal (may orphan resources): force_destroy = true
• Investigate: kubectl get all -n oracle
```

## Consequences

**Positive:**
- Users can act on errors immediately without running kubectl
- Competitive advantage over existing providers
- Reduced support burden

**Negative:**
- Each error type needs thoughtful design
- Diagnostic API calls add slight latency on errors (acceptable - only on rare failures)

## Maintenance

**Finalizers:** Conservative list of 5 built-in K8s finalizers only. Add new ones only if officially documented, stable 2+ years, and widely encountered.

**Error patterns:** Use dynamic resource listing (no hardcoded types). Trust dogfooding to surface issues.
