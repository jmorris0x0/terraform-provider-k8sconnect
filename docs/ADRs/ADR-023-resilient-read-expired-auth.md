# ADR-023: Resilient Read for Expired Cluster Auth Tokens

## Status
Accepted (2026-02-07)

**Related Issues:** [#131 - Support Ephemeral resource for cluster input](https://github.com/jmorris0x0/terraform-provider-k8sconnect/issues/131)

## Context

When the cluster auth token stored in Terraform state expires between runs, Read fails with a hard error and the entire plan crashes. This is a common problem with short-lived tokens (e.g., EKS tokens with ~15 min TTL).

The sequence:
1. First `terraform apply` fetches a fresh token, apply succeeds, token saved to state
2. Next `terraform plan` (hours/days later) tries to refresh using the stale token from state, gets 401

This is not unique to k8sconnect. The official `hashicorp/kubernetes` provider had the same problem for years, which led AWS to build `ephemeral_aws_eks_cluster_auth`.

### The Read Problem

The Terraform plugin framework only passes state (not config) to `Read()`. During `terraform plan`, Read is called first to refresh state. If the token in state is expired, Read fails before ModifyPlan ever runs, even though ModifyPlan would have access to the fresh token from config.

## Decision

**Make Read handle auth failures gracefully.** When Read gets a 401/403, return prior state with a warning instead of crashing. ModifyPlan (which has the fresh token from config) then handles drift detection.

This fixes the problem for all users, requires no schema changes, and works on any Terraform version.

### How It Works

When the token has expired:
```
Read:
  Stale token in state -> 401 -> warning + return prior state + set stale_read flag

ModifyPlan:
  Fresh token from Config -> create K8s client
  Get current object from cluster (already happens for ownership detection)
  stale_read flag set -> refresh projection from Get result
  Dry-run apply -> compare -> detect drift
  Clear stale_read flag
```

When the token is valid (normal case), Read succeeds and ModifyPlan uses Read's projection as-is. No behavior change.

The `stale_read` flag uses private state (`resp.Private.SetKey()` / `req.Private.GetKey()`) for cross-phase communication. It is only set when Read degrades due to auth failure and cleared on successful Read. Using the refreshed projection unconditionally causes regressions during ownership transitions because dry-run paths differ from currentObj paths.

### Auth Error Classification

We cannot distinguish "token expired" from "token was never valid" via the 401 status code. All 401/403 errors during Read are degraded to warnings. This is the right tradeoff: a hard failure on Read blocks all operations, even ones that would succeed with a fresh token. The warning surfaces the issue without blocking. Mutations (Create/Update/Delete) still hard-fail on auth errors.

## Alternatives Considered

**Write-only `token_wo` attribute** - Deferred. Requires TF 1.11+, and write-only attributes are null in state, so Read would have *zero* auth (not just expired). Resilient Read is a prerequisite regardless. Can layer on later for the security benefit of keeping tokens out of state entirely.

**Provider-level ephemeral resource** - Rejected. Would require adding provider-level configuration, which breaks k8sconnect's multi-cluster support (per-resource inline `cluster` blocks). Fundamental architecture change.

**`exec` auth** - Already implemented, zero code changes. Always-fresh tokens. Recommended when the runner environment supports it. The issue reporter said exec doesn't work due to "runner constraints."

**Write-only entire `cluster_wo` block** - Rejected. More confusing UX (two ways to specify cluster config), higher effort, same outcome as `token_wo`.

## Implementation

### Phase 1: Resilient Read

In Read, auth errors (401/403) are downgraded from hard errors to warnings. Prior state is returned intact with a `stale_read` private state flag.

Key files:
- `k8serrors/classification.go` - `IsAuthError()` helper
- `resource/object/errors.go` - `classifyReadGetError()` intercepts auth errors, returns warning severity
- `resource/object/crud.go` - Read uses `classifyReadGetError()`, emits warning and returns prior state. Same pattern for patch and wait.

### Phase 2: Write-only `token_wo` (Deferred)

Future addition for the security benefit of keeping tokens out of state. Foundation from Phase 1 supports this without changes.

### Phase 3: ModifyPlan Drift Detection

ModifyPlan already calls `client.Get()` for ownership transition detection. When the `stale_read` flag is set, that Get result is also used to refresh the managed state projection, replacing the stale projection from Read. No additional API calls.

## Test Coverage

- `read_auth_failure_test.go` - 6 unit tests: 401/403 degrade to warning, non-auth errors still hard-fail, stale_read flag lifecycle, warning message content, state preservation
- `expired_token_test.go` - 2 acceptance tests: create with valid token, invalidate, verify plan/apply succeed with warning
- `classification_test.go` - Auth errors wrapped in discovery messages correctly classified (bug found during QA: `IsConnectionError` string match was catching these before `errors.IsUnauthorized` type check)
- `plan_modifier_drift_test.go` - stale_read flag gating: drift detected when set, no false positives, no regression when not set

## References

- [Terraform Ephemeral Values (1.10)](https://www.hashicorp.com/en/blog/terraform-1-10-improves-handling-secrets-in-state-with-ephemeral-values)
- [Terraform Write-Only Arguments (1.11)](https://www.hashicorp.com/en/blog/terraform-1-11-ephemeral-values-managed-resources-write-only-arguments)
- [Plugin Framework: Write-Only Arguments](https://developer.hashicorp.com/terraform/plugin/framework/resources/write-only-arguments)
- [AWS Provider: ephemeral_aws_eks_cluster_auth](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/ephemeral-resources/eks_cluster_auth)
