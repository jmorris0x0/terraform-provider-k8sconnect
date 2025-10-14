# k8sconnect Data Source: wait_for Support Design

## Context

The `wait-for-lb` bash script hack needs to be replaced with native k8sconnect functionality. This requires enhancing the `k8sconnect_manifest` data source to support waiting for resources and field conditions.

## Current State

**wait-for-lb bash script:**
```bash
#!/bin/bash
# Polls AWS ELBv2 API every 5 seconds for up to 20 minutes
# Finds NLB by tags: elbv2.k8s.aws/cluster, service.k8s.aws/stack
# Returns DNS name once populated
# Never fails plan - just waits during apply
```

**Current k8sconnect_manifest data source:**
```hcl
data "k8sconnect_manifest" "nginx_lb" {
  cluster_connection = local.cluster_connection

  api_version = "v1"
  kind        = "Service"
  namespace   = "ingress-nginx"
  name        = "ingress-nginx-controller"
}

# Issues:
# - No wait_for support
# - Fails if resource doesn't exist yet (bootstrap problem)
```

## Usage Goal: Replace wait-for-lb

**What we want:**
```hcl
data "k8sconnect_manifest" "nginx_lb" {
  cluster_connection = local.cluster_connection

  api_version = "v1"
  kind        = "Service"
  namespace   = "ingress-nginx"
  name        = "ingress-nginx-controller"

  wait_for = {
    "status.loadBalancer.ingress[0].hostname" = { exists = true }
  }
}

# Use in Shield module:
module "shield" {
  app_server_name = data.k8sconnect_manifest.nginx_lb.object.status.loadBalancer.ingress[0].hostname
  # ...
}
```

## The Problem: Data Source vs. Bash Script Semantics

### Semantic Differences

| Aspect | Bash Script | Standard Data Source | Needed Behavior |
|--------|-------------|---------------------|-----------------|
| **Plan phase** | N/A (doesn't run) | Reads resource, fails if missing | Show "known after apply" if missing |
| **Apply phase** | Polls until success or timeout | Single read, fails if condition unmet | Poll until success or timeout |
| **Resource doesn't exist** | Waits for it to appear | Fails immediately | Wait for it to appear |
| **Field not populated** | Waits for it to populate | Returns null/empty | Wait for it to populate |
| **Timeout** | 20 minutes total | No timeout concept | Configurable timeouts |

### Critical Issues to Solve

**Issue 1: Bootstrap Scenario**

During `terraform plan`:
- nginx Service doesn't exist yet (will be created during apply)
- Standard data source tries to read it → fails
- Plan fails before we even get to apply

**Need:** Data source must gracefully handle "resource doesn't exist YET"

**Issue 2: Field Population Delay**

During `terraform apply`:
- nginx Service exists immediately after creation
- But `status.loadBalancer.ingress[0].hostname` is empty initially
- AWS load balancer controller takes 2-5 minutes to provision NLB and populate status
- Data source must wait for field to be populated (like bash script does)

**Issue 3: Timeout Management**

Questions:
- How long to wait for resource to exist? (could be instant, could be minutes)
- How long to wait for field to populate? (NLB creation = 2-5 min typically, but could be longer)
- Should these be separate timeouts?
- What are reasonable defaults?
- Should default be finite or infinite?

## Design Options

### Option A: Single Timeout for Everything

```hcl
data "k8sconnect_manifest" "nginx_lb" {
  cluster_connection = local.cluster_connection

  api_version = "v1"
  kind        = "Service"
  namespace   = "ingress-nginx"
  name        = "ingress-nginx-controller"

  wait_for = {
    "status.loadBalancer.ingress[0].hostname" = { exists = true }
  }

  wait_timeout = "15m"  # Total time for everything
}
```

**Behavior:**
- **Plan phase:**
  - If connection unknown OR resource doesn't exist → all outputs "known after apply"
  - If connection known and resource exists → read actual values
- **Apply phase:**
  - Poll for resource to exist
  - Once exists, poll for condition
  - Fail if 15min total exceeded

**Pros:**
- Simple configuration
- Single timeout to think about
- Easy to understand

**Cons:**
- Can't distinguish "resource taking forever to exist" from "NLB taking forever to provision"
- 15 minutes for both might be too much or too little
- No granular control over different phases

**Error messages:**
```
Error: Timeout waiting for Service ingress-nginx/ingress-nginx-controller
Condition 'status.loadBalancer.ingress[0].hostname exists' not met within 15m0s
```

### Option B: Separate Timeouts (RECOMMENDED)

```hcl
data "k8sconnect_manifest" "nginx_lb" {
  cluster_connection = local.cluster_connection

  api_version = "v1"
  kind        = "Service"
  namespace   = "ingress-nginx"
  name        = "ingress-nginx-controller"

  # Wait up to 5 minutes for resource to exist
  existence_timeout = "5m"

  # After resource exists, wait for conditions
  wait_for = {
    "status.loadBalancer.ingress[0].hostname" = {
      exists = true
      timeout = "10m"  # Per-condition timeout
    }
  }

  poll_interval = "5s"  # How often to check (default: 2s)
}
```

**Behavior:**
- **Plan phase:** Same as Option A
- **Apply phase:**
  1. Poll for resource to exist (up to `existence_timeout`, default 5m)
  2. If resource appears, start polling `wait_for` conditions
  3. Poll each condition (up to its `timeout`, default 10m)
  4. Return data once all conditions met

**Pros:**
- More granular control
- Better error messages ("resource never appeared" vs "NLB never got DNS name")
- Can tune timeouts based on what's slow
- Matches real-world timing (resource creation vs. field population)

**Cons:**
- More complex configuration
- Two timeout concepts to understand
- Could be overkill for simple cases

**Error messages:**
```
Error: Service ingress-nginx/ingress-nginx-controller did not appear within 5m0s
Check if the resource is being created or if there are cluster issues.
```

```
Error: Condition timeout for Service ingress-nginx/ingress-nginx-controller
Condition 'status.loadBalancer.ingress[0].hostname exists' not met within 10m0s
Resource exists but field was not populated. Check controller logs.
```

### Option C: Infinite Wait with Circuit Breaker

```hcl
data "k8sconnect_manifest" "nginx_lb" {
  cluster_connection = local.cluster_connection

  api_version = "v1"
  kind        = "Service"
  namespace   = "ingress-nginx"
  name        = "ingress-nginx-controller"

  # Wait forever by default, but allow override
  wait_timeout = null  # null = infinite (like bash script)

  wait_for = {
    "status.loadBalancer.ingress[0].hostname" = { exists = true }
  }
}
```

**Behavior:**
- Polls indefinitely until condition met
- User can Ctrl+C to interrupt
- Respects context cancellation

**Pros:**
- Matches bash script behavior exactly (wait forever)
- User can Ctrl+C if they want to bail
- Never fails with timeout error

**Cons:**
- **Dangerous in CI/CD** - could hang forever
- User loses explicit control
- No clear feedback about how long to expect
- Could hide real problems (misconfiguration, cluster issues)

**Verdict:** ❌ Bad idea for Terraform - timeouts should be explicit

### Option D: Smart Defaults with Optional Overrides (RECOMMENDED)

```hcl
# Minimal usage - relies on defaults
data "k8sconnect_manifest" "nginx_lb" {
  cluster_connection = local.cluster_connection

  api_version = "v1"
  kind        = "Service"
  namespace   = "ingress-nginx"
  name        = "ingress-nginx-controller"

  wait_for = {
    # Just specify what to wait for, use default timeouts
    "status.loadBalancer.ingress[0].hostname" = { exists = true }
  }
}

# Advanced usage - explicit control
data "k8sconnect_manifest" "slow_resource" {
  cluster_connection = local.cluster_connection

  api_version = "v1"
  kind        = "Service"
  namespace   = "ingress-nginx"
  name        = "ingress-nginx-controller"

  existence_timeout = "10m"  # Override default 5m
  poll_interval     = "10s"  # Override default 2s

  wait_for = {
    "status.loadBalancer.ingress[0].hostname" = {
      exists  = true
      timeout = "20m"  # Override default 10m
    }
  }
}
```

**Defaults:**
```go
const (
    DefaultExistenceTimeout = 5 * time.Minute
    DefaultConditionTimeout = 10 * time.Minute
    DefaultPollInterval     = 2 * time.Second
)
```

**Pros:**
- Simple for common cases (use defaults)
- Powerful for edge cases (override everything)
- Sensible defaults based on real-world usage
- Progressive disclosure of complexity

**Cons:**
- Need to choose good defaults
- Could still be surprising if defaults don't match use case

## Recommended Design: Option D (Smart Defaults)

### Schema Definition

```hcl
data "k8sconnect_manifest" "example" {
  # Required: cluster connection
  cluster_connection = {
    host                   = "https://..."
    cluster_ca_certificate = "..."
    exec = { ... }
  }

  # Required: resource identity
  api_version = "v1"
  kind        = "Service"
  namespace   = "default"  # Optional, omit for cluster-scoped
  name        = "my-service"

  # Optional: wait for resource to exist before reading
  # Default: 5m, set to "0s" to disable (fail immediately if missing)
  existence_timeout = "5m"

  # Optional: poll interval for all waiting operations
  # Default: 2s
  poll_interval = "2s"

  # Optional: conditions to wait for
  wait_for = {
    "status.loadBalancer.ingress[0].hostname" = {
      exists  = true           # Required: condition type
      timeout = "10m"          # Optional: per-condition timeout (default: 10m)
    }

    "status.phase" = {
      equals  = "Ready"        # Alternative condition type
      timeout = "5m"
    }
  }
}

# Outputs:
# - id              (string): Resource identifier
# - manifest        (string): Full YAML manifest
# - object          (dynamic): Parsed Kubernetes object (supports dot notation)
# - yaml_body       (string): YAML representation
# - api_version     (string): Actual apiVersion
# - kind            (string): Actual kind
# - namespace       (string): Actual namespace
# - name            (string): Actual name
```

### Behavior Specification

#### Plan Phase

1. **Connection unknown** (`host = (known after apply)`):
   - All outputs show "known after apply"
   - No API calls made
   - No errors

2. **Connection known, resource doesn't exist:**
   - If `wait_for` or `existence_timeout > 0`: outputs show "known after apply"
   - If `existence_timeout = 0`: fail immediately with error
   - Log: "Resource doesn't exist yet, will wait during apply"

3. **Connection known, resource exists, conditions not met:**
   - If `wait_for` specified: outputs show "known after apply"
   - Log: "Resource exists but conditions not met, will wait during apply"

4. **Connection known, resource exists, all conditions met:**
   - Read actual values
   - Show in plan output
   - No waiting needed during apply

#### Apply Phase

**Step 1: Wait for resource existence** (if `existence_timeout > 0`)
```go
deadline := time.Now().Add(existenceTimeout)
ticker := time.NewTicker(pollInterval)

for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-ticker.C:
        obj, err := client.Get(...)
        if err == nil {
            // Resource exists! Proceed to step 2
            break
        }
        if !errors.IsNotFound(err) {
            // Non-NotFound error - log and continue
            tflog.Warn(ctx, "Error checking resource existence", ...)
        }
        if time.Now().After(deadline) {
            return fmt.Errorf("resource did not appear within %v", existenceTimeout)
        }
    }
}
```

**Step 2: Wait for conditions** (if `wait_for` specified)
```go
for path, condition := range waitFor {
    deadline := time.Now().Add(condition.Timeout)

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            obj, err := client.Get(...)
            if err != nil {
                return fmt.Errorf("resource disappeared while waiting: %w", err)
            }

            if conditionMet(obj, path, condition) {
                break  // This condition met, check next
            }

            if time.Now().After(deadline) {
                return fmt.Errorf("condition '%s %s' not met within %v",
                    path, condition.Type, condition.Timeout)
            }
        }
    }
}
```

**Step 3: Return data**
- Populate all output attributes
- Save to state

### Condition Types

Support multiple condition types:

```hcl
wait_for = {
  # Field exists (any non-null value)
  "status.loadBalancer.ingress[0].hostname" = {
    exists = true
  }

  # Field equals specific value
  "status.phase" = {
    equals = "Ready"
  }

  # Field matches regex
  "metadata.annotations.custom" = {
    matches = "^[0-9]+"
  }

  # Field not equal (useful for waiting for deletion timestamp)
  "metadata.deletionTimestamp" = {
    not_exists = true
  }

  # Multiple conditions (AND logic)
  "status.conditions[?(@.type=='Ready')].status" = {
    equals = "True"
  }
}
```

### Error Messages

**Resource never appeared:**
```
Error: Resource not found

Resource Service "ingress-nginx/ingress-nginx-controller" did not appear within 5m0s

This could mean:
• Resource is not being created
• Resource name or namespace is incorrect
• Cluster connection issues

To wait longer, increase existence_timeout:
  existence_timeout = "10m"

To fail immediately if missing, set:
  existence_timeout = "0s"
```

**Condition timeout:**
```
Error: Wait condition timeout

Service "ingress-nginx/ingress-nginx-controller" condition not met within 10m0s:
  status.loadBalancer.ingress[0].hostname exists

Current value: <null>

This could mean:
• Load balancer provisioning is slow
• Load balancer controller has errors
• Condition path is incorrect

Check controller logs:
  kubectl logs -n kube-system -l app=aws-load-balancer-controller

To wait longer:
  wait_for = {
    "status.loadBalancer.ingress[0].hostname" = {
      exists  = true
      timeout = "20m"
    }
  }
```

**Resource disappeared:**
```
Error: Resource disappeared during wait

Service "ingress-nginx/ingress-nginx-controller" was deleted while waiting for conditions

This indicates the resource is being recreated or there are cluster issues.
```

### Progress Logging

```
data.k8sconnect_manifest.nginx_lb: Reading...
data.k8sconnect_manifest.nginx_lb: Resource not found, waiting for creation...
data.k8sconnect_manifest.nginx_lb: Still waiting... (30s elapsed)
data.k8sconnect_manifest.nginx_lb: Resource found, waiting for conditions...
data.k8sconnect_manifest.nginx_lb: Condition 'status.loadBalancer.ingress[0].hostname exists' not yet met (1m30s elapsed)
data.k8sconnect_manifest.nginx_lb: Condition met! (2m45s elapsed)
data.k8sconnect_manifest.nginx_lb: Read complete after 2m45s [id=v1/Service/ingress-nginx/ingress-nginx-controller]
```

## Default Timeout Values

### Question: What are reasonable defaults?

**Considerations:**
- NLB creation typically takes 2-5 minutes
- But could be longer under load or in edge cases
- Too short: frequent false failures
- Too long: slow feedback on real errors

### Proposed Defaults

```go
const (
    DefaultExistenceTimeout = 5 * time.Minute   // Resource creation
    DefaultConditionTimeout = 10 * time.Minute  // Field population
    DefaultPollInterval     = 2 * time.Second   // Check frequency
)
```

**Rationale:**

**Existence timeout (5m):**
- Most resources appear within seconds
- 5 minutes covers slow cases (heavy load, large resources)
- If resource doesn't appear in 5 minutes, likely a real problem
- User can override if legitimately slower

**Condition timeout (10m):**
- NLB creation: typically 2-5 minutes
- 10 minutes gives comfortable buffer
- Covers 99% of cases
- Matches wait-for-lb script's 20 minute total (5m existence + 10m condition + retries ≈ 20m)

**Poll interval (2s):**
- Fast enough for responsive feedback
- Not so fast it hammers the API
- Standard for Kubernetes controllers
- 150 attempts over 5 minutes (existence)
- 300 attempts over 10 minutes (condition)
- Reasonable API load

### Comparison to wait-for-lb

**wait-for-lb bash script:**
- Total timeout: 20 minutes
- Poll interval: 5 seconds
- Total attempts: 240

**Proposed defaults:**
- Existence: 5 minutes @ 2s = 150 attempts
- Condition: 10 minutes @ 2s = 300 attempts
- Total: 15 minutes, 450 attempts max
- More granular, better error messages

**Verdict:** Proposed defaults are reasonable. Users can override for edge cases.

## Implementation Checklist

### Phase 1: Fix object Attribute ✅ COMPLETED
- ✅ Implement proper parsing of `manifest` to `object` attribute
- ✅ Handle all Kubernetes API types correctly
- ✅ Return null for fields that don't exist
- ✅ Test with Services, Deployments, ConfigMaps, CRDs
- ✅ Update all documentation and examples

### Phase 2: Add Existence Waiting
- [ ] Add `existence_timeout` attribute to schema
- [ ] Implement polling loop for resource existence
- [ ] Handle "known after apply" in plan phase
- [ ] Add progress logging
- [ ] Test bootstrap scenario

### Phase 3: Add Condition Waiting
- [ ] Add `wait_for` map attribute to schema
- [ ] Implement condition evaluation:
  - [ ] `exists` condition
  - [ ] `equals` condition
  - [ ] `matches` (regex) condition
  - [ ] `not_exists` condition
- [ ] Implement per-condition timeout
- [ ] Add progress logging per condition
- [ ] Test with various field paths (nested, arrays, JSONPath-like)

### Phase 4: Error Handling & UX
- [ ] Descriptive error messages with troubleshooting hints
- [ ] Progress updates every 30s during waiting
- [ ] Context cancellation support (Ctrl+C)
- [ ] Detailed logging for debugging

### Phase 5: Documentation & Examples
- [ ] Complete provider documentation
- [ ] Migration guide from wait-for-lb
- [ ] Example: replacing wait-for-lb hack
- [ ] Add to DOGFOODING.md
- [ ] Update runnable examples

## Open Questions

1. **Timeout strategy:** Separate timeouts (Option B/D) or single timeout (Option A)?
   - **Decision:** Option D (separate with smart defaults)

2. **Default timeouts:** Are 5m/10m reasonable?
   - **Decision:** Yes, matches real-world usage

3. **Bootstrap handling:** Should it show "known after apply" during plan if resource doesn't exist?
   - **Decision:** Yes, matches Terraform best practices

4. **Condition syntax:** JSONPath-like or simpler dot notation?
   - **Need to decide:** Balance power vs. complexity

5. **Multiple conditions:** AND logic (all must be met) or OR logic (any can be met)?
   - **Decision:** AND only, user can create multiple data sources for OR

## Success Criteria

**Short term:**
- [ ] Can replace wait-for-lb bash script completely
- [ ] Bootstrap scenario works (resource created in same apply)
- [ ] Clear error messages when timeouts occur
- [ ] No breaking changes to existing functionality

**Long term:**
- [ ] Enables other use cases (waiting for Job completion, Pod ready, etc.)
- [ ] Reduces need for external scripts in Terraform configs
- [ ] Better UX than bash script alternatives
- [ ] Becomes the recommended way to wait for resources
