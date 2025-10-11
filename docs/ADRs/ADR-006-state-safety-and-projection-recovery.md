# ADR-006: State Safety and Projection Recovery

## Status
Accepted

## Context

When creating a Kubernetes resource, projection calculation can fail after the resource is successfully applied (network timeout, API unavailable). The provider must handle this gracefully.

**The Problem**: Network issues during `terraform apply` are **common** (WiFi disconnections, laptop closures, API server restarts, CI/CD network partitions).

**Users expect**: Simple retry succeeds without manual cleanup.

**Without recovery**: Second apply generates new random ID → ownership conflict → requires manual `kubectl delete` + state surgery.

## Alternatives Considered

**Approach 1: Fatal Error (No State Save)** - Don't save state on projection failure.
- Rejected: Second apply generates new ID → ownership conflict → requires manual `kubectl delete`

**Approach 2: Save with Warning** - Save empty projection, exit 0, show warning.
- Rejected: Dangerous for CI/CD (exit 0 → dependent resources create → silent corruption spreads)

**Approach 3: Schema Field** - Add `partially_created` boolean to user-visible schema.
- Rejected: Pollutes API surface, shows in `terraform show`, requires documentation, breaking change to remove

## Decision

**Use Terraform Plugin Framework Private State to track incomplete projections.**

When projection fails during Create, save state with `pending_projection` flag in Private state and return error (exit code 1). During Update/Read, check for flag and retry projection. If successful, clear flag. If still failing, keep flag.

**Recovery Paths**:
1. **Network recovers**: Next apply retries projection → succeeds → flag cleared
2. **Persistent failure**: Multiple applies fail → manual intervention required
3. **Refresh recovery**: Refresh operation opportunistically retries → may succeed

## Why Private State?

| Criterion | Private State | Schema Field | No State (Fatal) |
|-----------|---------------|--------------|------------------|
| **Hidden from users** | ✅ | ❌ | N/A |
| **No schema pollution** | ✅ | ❌ | ✅ |
| **Persisted across applies** | ✅ | ✅ | ❌ |
| **Auto-recovery** | ✅ | ✅ | ❌ |
| **Stops CI/CD** | ✅ | ✅ | ✅ |
| **No manual cleanup** | ✅ | ✅ | ❌ |

**Key insight**: Save state + return error achieves both CI/CD stopping (exit 1) and automatic recovery (same ID on retry). In practice, 99% of projection failures are network timeouts that resolve on retry.

## Benefits

- **Graceful recovery**: Common laptop-closure scenario (apply → error → apply → success) works without manual cleanup
- **CI/CD safe**: Failures stop pipeline (exit 1), prevent cascading failures
- **Clean API**: No user-visible schema fields, can remove flag later without breaking changes
- **Same resource ID**: No ownership conflicts, natural retry flow

## Drawbacks

- **Hidden state**: Users can't inspect flag directly, debugging requires TF_LOG=INFO
- **Drift detection gap**: Between failure and recovery, projection is empty "{}" (acceptable because error is visible)
