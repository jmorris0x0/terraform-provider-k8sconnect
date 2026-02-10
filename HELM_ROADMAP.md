# Helm Release Roadmap

Goal: world-class `helm_release` resource that covers 99% of enterprise use cases. Achieve parity with hashicorp/helm where it matters, exceed their quality everywhere, and don't over-engineer.

## Already Ahead of HashiCorp

These are solved problems in k8sconnect that remain open issues in hashicorp/helm:

- **Failed release recovery** (their #472): We detect failed releases and clean them up before retrying. They corrupt state.
- **Wait logic** (their #672): We use Helm v4's kstatus watcher. Their v2 wait is broken for DaemonSets and StatefulSets.
- **Per-resource cluster config**: Their provider-level config breaks bootstrapping. We can install Cilium before kube-proxy is ready.
- **SSA by default**: We use Server-Side Apply with force-conflicts. No field ownership surprises.
- **Write-only attributes** (their #1753): We handle Terraform 1.11+ write-only correctly. They lose values on update.
- **ADR-015 error messages**: Diagnostic API calls on failure, actionable suggestions, kubectl commands. They return raw Go errors.
- **Import UX**: Format examples, non-existent release suggestions, post-import guidance. Theirs is bare.

## Tier 1: Must Ship (blocks enterprise adoption)

### 1.1 OCI Credential Helper Support

**Status**: DONE
**Impact**: Blocks ECR, GCR, ACR, GHCR users who rely on `docker login` or credential helpers

Added `registry_config_path` attribute. When omitted, Helm v4 automatically uses the default Docker credential chain (~/.docker/config.json, platform native stores, credential helpers). Explicit `repository_username`/`repository_password` takes precedence. See detailed spec below.

### 1.2 `max_history`

**Status**: DONE
**Impact**: Long-lived releases accumulate Secrets in etcd. Production clusters hit etcd size limits.
**HashiCorp**: Has it, defaults to 0 (unlimited, a bad default)

Added `max_history` (int, default 10). Smart default of 10 is better than HashiCorp's 0. Set on Upgrade actions. Most users never need more than 10 revisions, and the ones who hit etcd limits don't know this knob exists until it's too late.

### 1.3 `pass_credentials`

**Status**: DONE
**Impact**: Blocks users with Artifactory, Nexus, or any chart repo behind a redirect proxy
**HashiCorp**: Has it, defaults to false

Added `pass_credentials` bool (default false). Maps to `ChartPathOptions.PassCredentialsAll`.

### 1.4 Plan-Time Validation (`ModifyPlan`)

**Status**: DONE
**Impact**: Errors that could be caught at `terraform plan` only surface during `apply`
**HashiCorp**: Does not have this (we exceed them here)

Implemented `ResourceWithModifyPlan` validating:
- Local chart path exists and contains Chart.yaml
- OCI charts have explicit version set
- Timeout string parses correctly
- Registry config path exists if specified

## Tier 2: Should Ship (common workflows)

### 2.1 `reuse_values`

**Status**: DONE
**Impact**: Users upgrading charts that set values during install need `reuse_values`
**HashiCorp**: Has both `reuse_values` and `reset_values`, defaults to false

Added `reuse_values` bool (default false). `reset_values` is the implicit default behavior and doesn't need a separate attribute.

### 2.2 Value Drift Detection

**Status**: Deprioritized (build only if users request)
**Impact**: Manual `helm upgrade --set key=val` is invisible to Terraform
**HashiCorp**: Their #372, still open after years

SSA already provides the real protection here. Manual `helm upgrade` on a k8sconnect-managed release hits field ownership conflicts, preventing silent overrides. The target use case (bootstrapping) doesn't involve mixed Terraform/manual Helm workflows.

Additionally, this is genuinely hard to implement without false positives. Helm stores user-supplied values separately from chart defaults, and distinguishing "user set replicaCount=3" from "chart default is 3" requires comparing against the chart's values.yaml, which changes between versions. HashiCorp has had #372 open for years for this reason.

If we build it: Only detect drift for values Terraform explicitly manages. Keys not in the Terraform config are ignored.

### 2.3 `description`

**Status**: DONE
**Impact**: Minor, but useful for release metadata
**HashiCorp**: Has it

Added `description` string attribute. Maps to `Install.Description` / `Upgrade.Description`.

## Tier 3: Nice to Have (diminishing returns)

### 3.1 `postrender`

**Status**: Missing
**Impact**: Niche. Kustomize post-rendering used by some GitOps teams.
**HashiCorp**: Has it (`postrender { binary_path, args }`)

Only implement if users request it. The target use case (bootstrapping) rarely needs post-rendering.

### 3.2 `lint`

**Status**: Missing
**Impact**: Useful for CI/CD but users can run `helm lint` directly
**HashiCorp**: Has it (defaults to false)

Low priority. `terraform plan` with our ModifyPlan validation covers most of the same ground.

### 3.3 `data.k8sconnect_helm_release`

**Status**: Missing
**Impact**: Reading existing releases without managing them
**HashiCorp**: Doesn't have this either

Would be useful for referencing release metadata in other resources. Low priority since `helm_release` computed attributes already expose this for managed releases.

---

## Detailed Spec: OCI Credential Helper Support

### Problem

The current `loadOCIChart()` calls `registry.NewClient()` with no options, then does explicit `Login()` only when username/password are set. This means:

1. Docker credential helpers (`docker-credential-ecr-login`, `docker-credential-gcr`, etc.) are not used
2. Existing `docker login` sessions are not respected
3. Users who authenticate via credential helpers (common on developer machines and some CI systems) cannot pull from private OCI registries

### Key Insight

Helm v4's registry client (via ORAS v2) already has full credential helper support built in. The `NewClient()` function:
- Reads `~/.docker/config.json` by default
- Supports `credHelpers` (per-registry credential helpers)
- Supports `credsStore` (default credential store)
- Falls back to plaintext `auths` entries
- Auto-detects platform native stores (osxkeychain, pass, wincred)

We just need to stop blocking this by passing the right options.

### Design

Three auth paths, in priority order:

**Path 1: Explicit credentials (CI/CD, automation)**
```hcl
# ECR example
data "aws_ecr_authorization_token" "token" {}

resource "k8sconnect_helm_release" "app" {
  repository          = "oci://123456.dkr.ecr.us-east-1.amazonaws.com"
  repository_username = data.aws_ecr_authorization_token.token.user_name
  repository_password = data.aws_ecr_authorization_token.token.password
  chart               = "my-app"
  version             = "1.0.0"
  # ...
}
```
This already works today. Username/password are passed to `registryClient.Login()`.

**Path 2: Docker config with credential helpers (developer machines, some CI)**
```hcl
resource "k8sconnect_helm_release" "app" {
  repository = "oci://123456.dkr.ecr.us-east-1.amazonaws.com"
  chart      = "my-app"
  version    = "1.0.0"
  # No username/password needed - uses docker-credential-ecr-login
  # ...
}
```
When no explicit credentials are provided, Helm v4 will automatically use the Docker credential chain. This is the zero-config path.

**Path 3: Custom config file (non-standard Docker config location)**
```hcl
resource "k8sconnect_helm_release" "app" {
  repository           = "oci://registry.internal.corp"
  registry_config_path = "/etc/helm/registry.json"
  chart                = "my-app"
  version              = "1.0.0"
  # ...
}
```
New attribute for users whose Docker config is not in the default location.

### Schema Change

Add one attribute:

```go
"registry_config_path": schema.StringAttribute{
    Optional:    true,
    Description: "Path to Docker/OCI registry config file for credential helper authentication. " +
        "Defaults to ~/.docker/config.json. Only needed when your config is in a non-standard location. " +
        "When repository_username and repository_password are set, they take precedence over this file.",
},
```

### Implementation Change

In `loadOCIChart()`, change `registry.NewClient()` to pass credential options:

```go
// Build registry client options
var registryOpts []registry.ClientOption

// Custom credentials file if specified
if !data.RegistryConfigPath.IsNull() {
    registryOpts = append(registryOpts, registry.ClientOptCredentialsFile(data.RegistryConfigPath.ValueString()))
}

registryClient, err := registry.NewClient(registryOpts...)
```

When `registry.NewClient()` is called without `ClientOptCredentialsFile`, Helm v4 automatically reads the default Docker config. So:

- No `registry_config_path`, no username/password: uses default Docker credential chain (zero config)
- No `registry_config_path`, with username/password: explicit Login() as today
- With `registry_config_path`, no username/password: uses custom config file
- With `registry_config_path`, with username/password: custom config + explicit Login() override

### Precedence

Explicit `repository_username`/`repository_password` always wins. If both are set alongside a credential helper, the explicit Login() call overrides whatever the credential helper returns. This matches Helm CLI behavior.

### What This Covers

| Registry | Auth Method | Works Today? | After Change |
|----------|-------------|-------------|--------------|
| ECR | `aws_ecr_authorization_token` data source | Yes | Yes |
| ECR | `docker-credential-ecr-login` helper | No | Yes |
| GCR/GAR | `google_client_config` data source | Yes | Yes |
| GCR/GAR | `docker-credential-gcloud` helper | No | Yes |
| ACR | `azurerm_container_registry` data source | Yes | Yes |
| ACR | `docker-credential-acr-env` helper | No | Yes |
| GHCR | PAT via username/password | Yes | Yes |
| Docker Hub | username/password | Yes | Yes |
| Harbor | username/password or robot account | Yes | Yes |
| Artifactory | username/password or API key | Yes | Yes |
| Any OCI | `docker login` (default config) | No | Yes |

### Testing Plan

1. Unit test: `classifyHelmError` correctly identifies OCI auth failures
2. Acceptance test: Pull from local OCI registry (use `zot` or `distribution` in k3d)
3. Manual QA: Test with ECR credential helper (requires AWS account)
4. Document: Add examples for ECR, GCR, ACR, GHCR, Docker Hub in provider docs

### Non-Goals

- **No built-in cloud auth** (IAM roles, workload identity, etc.). Users should use their cloud provider's Terraform data sources to get tokens. This is the Terraform way and avoids duplicating the AWS/GCP/Azure provider auth logic.
- **No `registries` provider block** (HashiCorp's approach). Per-resource config is our paradigm.
- **No credential caching across resources**. Each resource creates its own registry client. This is correct for per-resource auth and avoids cross-resource state leakage.
