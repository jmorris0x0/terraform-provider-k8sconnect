# ADR-023: Read Fails When Cluster Auth Token Expires Between Runs

**Status:** Accepted
**Date:** 2026-02-07
**Decision Date:** 2026-02-07
**Related ADRs:** ADR-001 (managed state projection), ADR-005 (field ownership), ADR-021 (ownership transitions)
**Related Issues:** [#131 - Support Ephemeral resource for cluster input](https://github.com/jmorris0x0/terraform-provider-k8sconnect/issues/131)

## Summary

Read fails when the cluster auth token stored in Terraform state expires between runs. A community user reported this as a request for ephemeral resource support, but the underlying problem is simpler: **the provider's Read operation has no way to handle expired credentials gracefully**.

**Decision:** Implement **Option E (resilient Read)** with **drift detection preserved via ModifyPlan**. Read gracefully handles auth failures (401/403) by returning prior state with a warning instead of crashing. ModifyPlan — which already has the fresh token from Config and already fetches current state from the cluster — refreshes the projection and performs drift detection when Read was degraded. The `token_wo` write-only attribute (Option A) is **deferred** to a future release — Option E alone solves the user's core problem for all auth methods without requiring schema changes or Terraform 1.11+.

## Context

### The Problem

The user's exact words:
> Currently we're using this provider to bootstrap EKS clusters where cluster attribute is passed down from the EKS module call. And this works well for the first run, however any sequential run fails with invalid authentication, and our suspicion is it's due to expired token as we're unable to utilise `exec` mechanism due to runner constrains.

This is a **token expiry problem**, not primarily a security concern. Here's the sequence:

1. **First `terraform apply`** — data source fetches a fresh EKS token (~15 min TTL) → token is valid → apply succeeds → **token saved to state**
2. **Next `terraform plan`** (minutes/hours/days later) — provider reads state → tries to refresh resource using **stale token from state** → 401 auth failure

The token in state is always stale by the next run. The security benefit of not storing tokens in state is a nice side effect, but the user's pain is that **subsequent runs break**.

This is not unique to k8sconnect — the official `hashicorp/kubernetes` provider had the same problem for years, which is why AWS built `ephemeral_aws_eks_cluster_auth`.

### Why This Is Tricky: The Read Problem

In the terraform-plugin-framework, the resource lifecycle methods receive different data:

| Method | Gets Config? | Gets State? | Gets Plan? |
|--------|:---:|:---:|:---:|
| Create | Yes | No | Yes |
| Read (refresh) | **No** | Yes | No |
| Update | Yes | Yes | Yes |
| Delete | Yes | Yes | No |
| ModifyPlan | Yes | Yes | Yes |

**The critical gap:** `Read` only receives state. It does NOT have access to the current configuration. This means:

- During `terraform plan`, Terraform calls Read to refresh state
- Read gets the prior state, which contains the stored `cluster` config
- If the token is stored in state → it's expired → Read fails
- If the token is write-only → it's null in state → Read has no auth at all

**Any solution that removes the token from state must also handle Read having no auth.**

### How ADR-023 Actually Solves This (Plain English)

> **This section is the canonical explanation.** If you're trying to understand how this works, read this first.

When a user runs `terraform plan`, the framework does two things internally:

1. **Refresh (Read)** — the framework only passes `req.State` (old token). This is the framework's design, we can't change it.
2. **Plan (ModifyPlan)** — the framework passes `req.Config` (new token).

**Before ADR-023:** Read uses old token → 401 → **HARD ERROR** → plan crashes, ModifyPlan never runs. User sees "plan always fails."

**After ADR-023:** Read uses old token → 401 → **WARNING** (graceful degradation) → stale state preserved → ModifyPlan runs → uses **fresh token from Config** → does a live `Get()` from the cluster → produces correct plan. **Plan succeeds.**

So the user's `terraform plan` works with the new token. The plan itself uses the new config — that's the whole point of plan. We just have to survive the automatic refresh step first, which is what Phase 1 (resilient Read) does. The warning is just informational: "your old token expired, but I'm using your new one for the actual plan."

The two-phase approach:
- **Phase 1 (Resilient Read):** Don't crash during refresh — downgrade auth error to warning
- **Phase 3 (Refreshed Projection):** Actually use the new token during planning — ModifyPlan fetches live state with config credentials and detects drift

### What We Know vs What We're Assuming

**High confidence (confirmed by code analysis):**
- The core problem is stale tokens in state causing Read to fail on subsequent runs. Both reporters describe this: User 1 says "sequential run fails with invalid authentication", User 2 says "once the token expires... plan always fails."
- The error path: `Read()` → `rc.Client.Get()` → 401 → `classifyK8sError()` returns `"error"` severity → Terraform fails.
- User 2's "cache" comment likely refers to the stale-state problem, not the `CachedClientFactory`. The factory creates a fresh instance per Terraform run, and within a run the cache key includes the token value, so a different token gets a different client.

**Open questions (need to verify by testing):**
- **Does the failure also occur during Create/Update with a stale plan?** If a user runs `terraform plan -out=plan.tfplan` and applies 20+ minutes later, the token baked into the plan is expired too. Option E (resilient Read) only fixes the Read/refresh path — it wouldn't help with a stale token during apply from a saved plan. The users describe "plan fails" (which is Read), but we should confirm Create/Update also fail and whether that needs handling.
- **Does the `CachedClientFactory` contribute to the problem?** If a token expires mid-run (unlikely for short TTL tokens in a single plan, but possible for large deployments with many resources), the cached client would keep using the stale token for all resources sharing that connection. We should verify.
- **What are User 1's "runner constraints" for exec auth?** We don't know if this is fundamental (e.g., serverless CI with no exec support) or solvable (e.g., just needs AWS CLI installed). We should ask.

### Failure Scenarios to Test

Before implementing a fix, we need to reproduce and understand each failure mode:

| # | Scenario | Operation | Current Behavior | Fix |
|---|----------|-----------|-----------------|-----|
| 1 | Token in state expired, next `terraform plan` | Read | Hard error: "Authentication Failed" | Phase 1: Read warns, returns prior state. Phase 3: ModifyPlan detects drift with fresh token. |
| 2 | Token in state expired, next `terraform apply` (no saved plan) | Read then Update | Read fails first (same as #1) | Phase 1: Read warns. Update uses fresh token from Config. |
| 3 | Saved plan with expired token, `terraform apply plan.tfplan` | Create/Update | Hard error during apply | Out of scope — saved plans bake in config values. Users should avoid long delays between plan and apply. |
| 4 | Token expires mid-run (large deployment, many resources) | Various | Cached client uses stale token | Phase 1 helps for Read. Create/Update would still fail (correct — need valid auth for mutations). |
| 5 | Wrong token (misconfiguration, not expiry) | Read | Hard error: "Authentication Failed" | Phase 1: Read warns (can't distinguish from expired via 401). Warning message tells user to check config. |
| 6 | 403 Forbidden (valid token, wrong RBAC) | Read | Hard error: "Insufficient Permissions" | Phase 1: Read warns (stale RBAC is similar to stale token). Create/Update still hard-fail. |

Scenarios 1-2 are the confirmed user reports. Scenarios 5-6 degrade gracefully during Read but remain hard errors during mutations — the warning tells users to investigate if the issue persists.

**Key distinction:** We cannot distinguish "token was valid but expired" from "token was never valid" via the 401 status code. We degrade ALL 401/403s during Read to warnings. This is the right tradeoff: a hard failure on Read blocks all operations, even ones that would succeed with a fresh token. The warning surfaces the issue without blocking.

### What Are Terraform Ephemeral Resources?

Introduced in **Terraform 1.10** (November 2024). Ephemeral resources are declared with the `ephemeral` block:

```hcl
ephemeral "aws_eks_cluster_auth" "cluster" {
  name = aws_eks_cluster.example.name
}
```

Core guarantee: **Terraform never persists ephemeral resource data in plan or state files.** They exist only during the current Terraform operation.

Three-phase lifecycle:
1. **Open** — Terraform "opens" the resource to fetch/generate data when its values are needed
2. **Renew** — For time-limited resources (tokens, leases), Terraform can periodically refresh them during long operations
3. **Close** — Explicit cleanup when no longer needed (e.g., revoking a Vault lease)

**Terraform 1.11** (early 2025) added **write-only arguments** which complement ephemeral resources by allowing ephemeral values to be passed into managed resource attributes without being stored in state.

### The Architectural Constraint

Ephemeral values **cannot be passed to regular resource attributes**. They can only flow into:
- Provider configuration blocks
- Other ephemeral resources
- Write-only arguments (Terraform 1.11+)
- Provisioner/connection blocks
- Locals (which then become ephemeral)

Since k8sconnect uses **per-resource inline `cluster` blocks** (not provider-level config), a user cannot simply do:

```hcl
# THIS WILL NOT WORK — Terraform rejects ephemeral values in regular attributes
resource "k8sconnect_object" "example" {
  cluster = {
    token = ephemeral.aws_eks_cluster_auth.example.token
  }
}
```

### Framework Support

The `hashicorp/terraform-plugin-framework` supports:
- **Ephemeral resources:** GA since v1.14.0 (Feb 2025)
- **Write-only attributes:** `WriteOnly: true` field on schema attributes, GA in v1.14.0+

We are on **v1.17.0** — no framework upgrade needed for either feature.

### Reference Implementation: AWS `ephemeral_aws_eks_cluster_auth`

The AWS provider's implementation is remarkably simple:
- Schema: Takes a `name` (the EKS cluster name), returns a `token` (computed, sensitive)
- Open: Uses STS client to generate a presigned token
- No Renew or Close needed (stateless 15-minute TTL tokens)
- Test: Uses `tfversion.SkipBelow(tfversion.Version1_10_0)`

**Key difference:** The AWS provider uses this ephemeral token in the **provider block** (which is evaluated fresh every run). k8sconnect uses per-resource inline config (which is stored in resource state). This is why the same pattern doesn't directly translate.

## Options

### Option A: Write-Only `token_wo` + Skip Refresh When No Auth

Add a write-only `token_wo` attribute to the `cluster` block. When Read finds no auth in state (because `token_wo` is null in state), **skip the refresh and return prior state as-is** instead of failing.

```hcl
ephemeral "aws_eks_cluster_auth" "cluster" {
  name = aws_eks_cluster.example.name
}

resource "k8sconnect_object" "example" {
  cluster = {
    host                   = aws_eks_cluster.example.endpoint
    cluster_ca_certificate = aws_eks_cluster.example.certificate_authority[0].data
    token_wo               = ephemeral.aws_eks_cluster_auth.cluster.token
  }
  yaml_body = "..."
}
```

**How it handles the Read problem:** During Read, `token_wo` is null in state. The provider detects "no auth available" and returns the existing state without contacting the cluster. Create/Update/Delete still work because they receive the fresh token from config.

**How drift detection is preserved:** ModifyPlan already has the fresh token (via Config/Plan) and already creates a K8s client. For UPDATE operations, ModifyPlan already calls `client.Get()` to fetch current cluster state for ownership transition detection (`plan_modifier.go:292`). By also using this Get result to refresh the managed state projection, drift detection works even when Read can't authenticate. Zero additional API calls — we just extract more value from a call that's already happening.

The two-phase drift detection flow with `token_wo`:
1. **Read** — no token in state → returns prior state (stale projection)
2. **ModifyPlan** — fresh token from Config → `Get` fetches current state → refreshes projection → dry-run predicts result → compares → drift detected if any external changes

**Pros:**
- Backward compatible — existing `token` attribute unchanged
- Solves the user's actual problem (subsequent runs don't fail)
- **Drift detection preserved** — ModifyPlan handles it with fresh token from Config
- No `token_wo_version` needed — k8sconnect re-authenticates on every CRUD operation anyway, and versioning is for forcing re-reads of state, which we're skipping
- No noisy diffs — token never in state, nothing to diff on rotation

**Cons:**
- Requires Terraform 1.11+
- Only covers `token` — other fields (client_certificate, client_key) would need separate `_wo` variants if requested later
- `terraform refresh` / `terraform plan -refresh-only` won't refresh projection (Read can't auth), but ModifyPlan compensates during normal plan/apply cycles

**Effort:** Medium

### Option B: Create `ephemeral "k8sconnect_cluster_connection"`

Create an ephemeral resource that produces the entire cluster connection config, then feed it to provider-level config.

```hcl
ephemeral "k8sconnect_cluster_connection" "eks" {
  host                   = aws_eks_cluster.example.endpoint
  token                  = ephemeral.aws_eks_cluster_auth.cluster.token
  cluster_ca_certificate = aws_eks_cluster.example.certificate_authority[0].data
}

provider "k8sconnect" {
  cluster = ephemeral.k8sconnect_cluster_connection.eks
}
```

**Pros:**
- Clean user experience
- Entire connection is ephemeral, not just token
- Provider-level config is the intended target for ephemeral values
- Read gets auth because provider-level config is evaluated every run

**Cons:**
- Requires adding provider-level configuration (currently k8sconnect has none)
- **Breaks multi-cluster support** — provider blocks are static, can't vary per-resource (would need provider aliases)
- Major architectural change to k8sconnect's core design
- Could use provider aliases, but that's clunky for many clusters

**Effort:** High — fundamental architecture change

### Option C: Lean Into `exec` Auth (Already Implemented)

k8sconnect already supports `exec` blocks in the cluster config. For EKS:

```hcl
resource "k8sconnect_object" "example" {
  cluster = {
    host                   = aws_eks_cluster.example.endpoint
    cluster_ca_certificate = aws_eks_cluster.example.certificate_authority[0].data
    exec = {
      api_version = "client.authentication.k8s.io/v1beta1"
      command     = "aws"
      args        = ["eks", "get-token", "--cluster-name", aws_eks_cluster.example.name]
    }
  }
  yaml_body = "..."
}
```

**Pros:**
- Already implemented, zero new code
- Works with any Terraform version
- Token is always fresh (exec runs at operation time, including Read)
- Full drift detection works
- No Read problem — exec generates fresh token on every call

**Cons:**
- The issue reporter said exec doesn't work due to "runner constraints" (unclear what this means)
- Requires the CLI tool (e.g., `aws`) to be available on the runner
- Not all environments can run exec (serverless CI, restricted containers)

**Open question:** We should ask the user what "runner constraints" means. If it's solvable (e.g., they just need to install the AWS CLI in their CI image), this is the best option with zero code changes.

**Effort:** None (already exists)

### Option D: Write-Only Entire `cluster_wo` Block

Similar to Option A but makes the entire cluster block write-only.

```hcl
resource "k8sconnect_object" "example" {
  cluster_wo = {
    host                   = aws_eks_cluster.example.endpoint
    cluster_ca_certificate = aws_eks_cluster.example.certificate_authority[0].data
    token                  = ephemeral.aws_eks_cluster_auth.cluster.token
  }
  yaml_body = "..."
}
```

**Pros:**
- Entire connection config is ephemeral in one shot
- No per-field `_wo` proliferation
- Backward compatible

**Cons:**
- Requires Terraform 1.11+
- Same Read problem as Option A (no auth in state during refresh)
- Two ways to specify cluster config (`cluster` vs `cluster_wo`) is confusing
- When `SingleNestedAttribute` has `WriteOnly: true`, all child attributes must also be `WriteOnly: true` — this means the entire block is all-or-nothing
- Higher effort than Option A

**Effort:** Medium-High

### Option E: Resilient Read — Graceful Auth Failure Handling

Instead of changing the schema, make Read **handle auth failures gracefully**. If Read gets a 401 Unauthorized when trying to refresh from the cluster, return the prior state with a warning diagnostic instead of failing.

No schema changes. No new attributes. No Terraform version requirement. Fixes the problem for all users.

```
# No HCL changes needed — existing configs just stop breaking on subsequent runs
```

**How it works:**
1. Read attempts to refresh from the cluster as it does today
2. If the connection succeeds → normal drift detection, no behavior change
3. If the connection fails with **401/403 (auth failure)** → return prior state + emit a warning: "Could not refresh resource state: cluster authentication failed. Using prior state. This typically means the stored token has expired. Consider using exec auth for automatic token refresh."
4. All other errors (network timeout, 404, server errors) → fail as they do today

**Pros:**
- **Fixes the problem for all users** — not just those who adopt a new attribute
- **Zero schema changes** — no `token_wo`, no new attributes, no migration
- **Works on any Terraform version** — no TF 1.11+ requirement
- Preserves drift detection for users with working auth (exec, long-lived tokens, client certs)
- Graceful degradation — warns instead of failing
- Does not preclude adding `token_wo` later for the security benefit (tokens out of state)

**Cons:**
- No drift detection when auth is expired (same tradeoff as Option A, but implicit rather than explicit)
- Silently swallowing auth errors could theoretically mask real auth misconfigurations — mitigated by the warning diagnostic
- Need to distinguish auth failures (401/403) from other connection errors reliably

**Implementation scope:**
- Resource Read methods (object, patch, wait) — wrap the cluster connection attempt, catch auth errors, return prior state with warning
- Possibly a shared helper in the auth or factory package: `IsAuthError(err) bool`

**Effort:** Low

## Analysis

### The Core Tension

The fundamental tension is between k8sconnect's architecture (per-resource inline auth stored in state) and ephemeral values (which by definition are not stored in state). Every option that removes the token from state creates a gap during Read/refresh when the provider has no auth.

### Comparison Matrix

| | Token Expiry Fixed | Drift Detection | Breaking Change | TF Version | Effort | Scope |
|---|:---:|:---:|:---:|:---:|:---:|:---:|
| **A+E: `token_wo` + resilient Read** | Yes | Yes (via ModifyPlan) | No | 1.11+ | Medium | Opt-in |
| **E alone: Resilient Read** | Yes | Degrades gracefully | No | Any | Low | All users |
| **B: Provider-level ephemeral** | Yes | Yes | Yes | 1.10+ | High | Opt-in |
| **C: `exec` auth** | Yes | Yes | No | Any | None | Opt-in |
| **D: `cluster_wo` block** | Yes | Yes (via ModifyPlan) | No | 1.11+ | Medium-High | Opt-in |

### What's NOT Needed

- **`token_wo_version`**: The HashiCorp pattern uses version attributes to force re-reads of write-only values. k8sconnect doesn't need this because it re-authenticates on every CRUD operation. The token is consumed at operation time, not cached.
- **`client_certificate_wo` / `client_key_wo`**: Nobody asked for it. Certs don't expire the same way tokens do. Can add later if requested.
- **Data source schema changes**: Data sources don't support write-only attributes and ephemeral values can't flow to data sources anyway.

## Decision

**Implement Option E (resilient Read), with drift detection via ModifyPlan.**

### What we're building

1. **Resilient Read (Option E)** — when Read gets a 401/403 auth failure, return prior state with a warning instead of crashing. This fixes the core problem for ALL users regardless of auth method — tokens that expire between runs no longer break subsequent plans.

2. **ModifyPlan drift detection** — ModifyPlan already has the fresh token from Config, already creates a K8s client, and already calls `client.Get()` for UPDATE operations. When Read was degraded (stale_read flag set), ModifyPlan uses its Get result to refresh the managed state projection, preserving drift detection. When Read succeeded normally, ModifyPlan uses the existing projection from Read (no change from prior behavior).

### Why Option E alone (token_wo deferred)

During implementation, we determined that Option E alone solves the user's core problem:

- **Works for all users** — not just those who adopt a new attribute
- **No schema changes** — no `token_wo`, no migration, no version conflicts
- **Works on any Terraform version** — no TF 1.11+ requirement
- **Drift detection preserved** — ModifyPlan compensates when Read is degraded
- **Backward compatible** — zero behavior change for users with working auth

The `token_wo` attribute (Option A) remains a good future addition for the security benefit of keeping tokens out of state entirely, but it's not needed to fix the reported problem. It can be added in a future release without any changes to the resilient Read foundation.

### Why not the other options

- **Option A (`token_wo`):** Deferred. Requires TF 1.11+, adds schema complexity, and Option E already solves the user's problem. Can layer on later for the security benefit.
- **Option B (provider-level ephemeral):** Breaks multi-cluster support, requires fundamental architecture change. Too large.
- **Option C (`exec` auth):** Already works, zero code changes. Should ask the reporter about "runner constraints." Good recommendation regardless.
- **Option D (`cluster_wo` block):** More confusing UX, higher effort, same result as Option A.

## Implementation Plan

### Phase 1: Resilient Read (Option E) — building block

The error path has been traced:

1. `Read()` calls `prepareContext()` which creates a K8s client with the stale token — **no error here** (client creation is lazy)
2. `Read()` calls `rc.Client.Get()` at `crud.go:130` — **this is where the 401 happens**
3. `classifyK8sError()` hits the `errors.IsUnauthorized(err)` case at `classification.go:136`, returns `"error"` severity with title `"Read: Authentication Failed"`
4. Read adds it as an error diagnostic and returns — Terraform fails

The fix: `classifyReadGetError()` (already stubbed in `errors.go`) degrades auth errors to warnings during Read. When Get returns 401/403, Read returns prior state with a warning instead of failing.

**Files to change:**

1. **`k8serrors/classification.go`** — `IsAuthError()` already added (checks `errors.IsUnauthorized || errors.IsForbidden`)
2. **`resource/object/errors.go`** — Implement `classifyReadGetError()` (currently delegates to `classifyK8sError`; needs to intercept auth errors and return "warning" severity)
3. **`resource/object/crud.go`** — In Read, use `classifyReadGetError()` instead of `classifyK8sError()` for Get errors. When severity is "warning", emit warning diagnostic and return prior state. Same pattern for patch and wait resources.

**Already done:**
- `IsAuthError()` helper in `classification.go` with tests
- `classifyReadGetError()` stub in `errors.go`
- Failing tests in `read_auth_failure_test.go` (TDD — tests assert desired behavior, currently fail)

### Phase 2: `token_wo` attribute (Option A) — DEFERRED

> **Status: Deferred to future release.** Option E alone solves the core user problem. `token_wo` adds the security benefit of keeping tokens out of state entirely and can be layered on later.

When implemented, the plan is:
1. **`auth/schema.go`** — Add `token_wo` StringAttribute with `WriteOnly: true, Optional: true, Sensitive: true`
2. **`auth/connection.go`** — Add `TokenWO types.String` to `ClusterModel`. In `configureAuth()`, prefer `TokenWO` over `Token` when non-null. In `IsConnectionReady()`, add `TokenWO` unknown check.
3. **`auth/converter.go`** — Add `token_wo` to `ObjectToConnectionModel()`, `ConnectionToObject()`, and `GetConnectionAttributeTypes()`
4. **`auth/validation.go`** — Update `hasAuthentication()` to recognize `TokenWO`

### Phase 3: ModifyPlan drift detection (stale_read gated)

ModifyPlan already does a `client.Get()` for UPDATE operations for ownership transition detection. The change:

1. **`resource/object/plan_modifier.go`** — When the `stale_read` private state flag is set (meaning Read was degraded due to auth failure), use the Get result from ModifyPlan to refresh the managed state projection. This replaces the stale projection from Read with a fresh one, enabling drift detection even when Read couldn't authenticate.

2. **When `stale_read` is NOT set** (normal Read succeeded), the existing projection from Read is used as-is. This is critical — unconditionally refreshing the projection causes regressions during ownership transitions because dry-run paths differ from currentObj paths.

3. **No additional API calls.** The Get is already happening for ownership transition detection. We just use the result for projection refresh when the stale_read flag indicates Read was degraded.

4. **The `stale_read` flag** is set in Read when auth fails (warning path) and cleared on successful Read. It uses `resp.Private.SetKey()` / `req.Private.GetKey()` for cross-phase communication.

Flow when token has expired:
```
Read: stale token in state → 401 → warning + return prior state + set stale_read flag
ModifyPlan:
  1. Fresh token from Config → create K8s client
  2. Get current object from cluster (already happens for ownership)
  3. stale_read flag set → project managed fields from Get result → refresh projection baseline
  4. Dry-run apply → predict result → project predicted fields
  5. Compare refreshed baseline vs dry-run prediction → detect drift
  6. Clear stale_read flag
```

Flow when token is valid (normal case):
```
Read: valid token → successful Get → fresh projection → clear stale_read flag
ModifyPlan:
  1. stale_read flag NOT set → use Read's projection as-is (no change from prior behavior)
  2. Normal ownership transition detection continues as before
```

### Testing Strategy

**Resilient Read tests (Phase 1):**
- `read_auth_failure_test.go` — 6 unit test scenarios:
  - 401 Unauthorized on Read → warning, returns prior state, sets stale_read flag
  - 403 Forbidden on Read → warning, returns prior state, sets stale_read flag
  - Non-auth errors (404, network) → still hard error (unchanged behavior)
  - Successful Read → clears stale_read flag
  - Warning message content — includes token expired guidance and exec auth suggestion
  - State preservation — prior state returned intact (projection, yaml_body, etc.)
- `expired_token_test.go` — 2 acceptance tests (full integration with token invalidation):
  - Create resource with valid token → invalidate token → plan succeeds with warning
  - Create resource with valid token → invalidate token → apply succeeds (reverts to prior state)

**Error classification tests:**
- `classification_test.go` — tests that auth errors wrapped in discovery messages (e.g., `"failed to get resource info for v1/ConfigMap: Unauthorized"`) are correctly classified as auth errors, not connection errors. Bug found during QA: `IsConnectionError` string match caught these before `errors.IsUnauthorized` type check. Fix: auth checks moved before connection checks in ClassifyError switch.

**ModifyPlan drift detection tests (Phase 3):**
- `plan_modifier_drift_test.go` — unit tests:
  - stale_read flag set + external drift → drift detected via refreshed projection
  - stale_read flag set + no external drift → no false positive
  - stale_read flag NOT set → uses Read's projection (no regression)

**`token_wo` tests (Phase 2) — DEFERRED:**
- When implemented: acceptance tests gated with `tfversion.SkipBelow(tfversion.Version1_11_0)`
- Test matrix: token only, token_wo only, both set (token_wo wins), neither set (error)

## Open Items

- **Ask issue #131 reporter** what "runner constraints" prevent exec auth. If solvable, exec is a zero-code fix they can use immediately.
- **Reply to discussion #146** (flux bootstrap on EKS) — this is the same token expiry problem, `token_wo` solves it.
- **PR #132** (Copilot-generated write-only stub) — superseded by this ADR. Close or use as starting point.

## Maintenance Context (2026-02-07)

This ADR was created during a project maintenance catch-up.

### Open PRs
| # | What | Status |
|---|------|--------|
| **#133** | Helm v4 resource | Open, acceptance tests failing |
| **#132** | Write-only cluster attribute (Copilot) | Draft, essentially a stub — superseded by this ADR |

### Unanswered Community
- **Discussion #146:** How to bootstrap flux with k8sconnect on EKS (no replies since Jan 20)
- **Issue #131:** This ephemeral resource request (user willing to contribute)

## References

- [Terraform 1.10 Ephemeral Values Blog Post](https://www.hashicorp.com/en/blog/terraform-1-10-improves-handling-secrets-in-state-with-ephemeral-values)
- [Terraform Ephemeral Block Reference](https://developer.hashicorp.com/terraform/language/block/ephemeral)
- [Terraform 1.11 Write-Only Arguments](https://www.hashicorp.com/en/blog/terraform-1-11-ephemeral-values-managed-resources-write-only-arguments)
- [Plugin Framework: Write-Only Arguments](https://developer.hashicorp.com/terraform/plugin/framework/resources/write-only-arguments)
- [Plugin Framework: Ephemeral Resources](https://developer.hashicorp.com/terraform/plugin/framework/ephemeral-resources)
- [AWS Provider: ephemeral_aws_eks_cluster_auth](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/ephemeral-resources/eks_cluster_auth)
- [Plugin Framework StringAttribute source](https://github.com/hashicorp/terraform-plugin-framework/blob/main/resource/schema/string_attribute.go) — `WriteOnly bool` field confirmed in v1.17.0
