# ADR-007: Automatic CRD Dependency Resolution Without Configuration

## Status
Implemented (2025-10-06)

## Context

When applying Terraform configurations with both CRDs and CRs, a race condition occurs. Even with `depends_on`, the CRD may not be fully established when the CR is applied (Kubernetes eventual consistency).

**Industry Context**: This has been unsolved for 3+ years. HashiCorp provider requires two-phase deployment (Issue #1367: 362+ 👍). Kubectl provider has `apply_retry_count` parameter but still often needs two applies. Users resort to `time_sleep` resources or `local-exec` provisioners.

## Decision

**Implement automatic apply-time retry with zero configuration.**

When "no matches for kind" error occurs during apply, automatically retry with exponential backoff (100ms → 500ms → 1s → 2s → 5s → 10s → 10s, ~30s total). Succeed if CRD becomes available. Fail with actionable error if truly missing.

**Design Principles**: It Must Just Work™ (single apply succeeds), zero configuration, fast happy path (<100ms overhead when CRDs exist), clear failure modes.

**Apply-time only**: We don't modify plan-time behavior initially to keep early validation for genuine configuration errors.

## Alternatives Considered

**Configuration-Based Retry** (kubectl approach) - Rejected: Requires user configuration, violates zero-config principle

**Time-Based Delays** (`time_sleep` resource) - Rejected: Unreliable, wastes time, poor UX

**Skip Plan-Time Validation** - Rejected: Loses early error detection for genuine mistakes

**Two-Phase Module Structure** - Rejected: Poor DX, doesn't "just work"

**External Dependency Detection** (analyze plan graph) - Rejected: May not have access to full plan graph, overly complex

**User-Configured Validation Mode** - Rejected: Configuration option violates zero-config principle

## Implementation

Implemented in `applyWithCRDRetry()` (crud_common.go) and `isCRDNotFoundError()` (errors.go).

Detection checks for "no matches for kind" or "could not find the requested resource" in error messages. Retry only occurs for CRD missing errors - other errors fail immediately. Respects context cancellation for graceful shutdown. Logs retry attempts at debug level for troubleshooting.

## Benefits

- **Single apply works** - CRD and CR deployed together without configuration
- **Better than competition** - Solves what HashiCorp/kubectl providers haven't in 3+ years
- **Fast happy path** - <100ms overhead when CRDs exist
- **Clear errors** - Actionable guidance when genuinely broken

## Drawbacks

- **30-second worst case** - May wait up to 30s for truly missing CRDs
- **Apply-time discovery** - Problems not caught during plan (keeps early validation though)

## Test Coverage

`TestAccManifestResource_CRDAndCRTogether` proves CRD + CR work in single apply (~24s) without configuration.
