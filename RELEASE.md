# Release Pipeline

## Overview

This repository uses GitHub Actions for CI/CD with:
- Automated testing using Kind clusters
- Multi-version Terraform/OpenTofu testing
- Security scanning (gosec, govulncheck)
- Automated releases with GoReleaser
- Dependabot for dependency updates

## Workflows

### Test (`test.yml`)
- **Triggers**: Every push and PR
- **Jobs**:
  - Unit tests
  - Multi-OS builds (Linux, macOS, Windows)
  - Acceptance tests with Kind + OIDC
  - Linting and vetting
- **Matrix**: Tests against Terraform 1.8.0 and 1.9.x

### Release (`release.yml`)
- **Triggers**: Git tags (v*)
- **Actions**: 
  - Builds binaries for multiple platforms
  - Signs with GPG
  - Creates GitHub release (draft during alpha)

### Security (`security.yml`)
- **Triggers**: PRs, pushes, weekly schedule
- **Scans**: gosec, govulncheck
- **Reports**: SARIF format to GitHub Security tab

### Nightly (`nightly.yml`)
- **Triggers**: Daily at 2 AM UTC (or manual)
- **Tests**: Latest Terraform and OpenTofu versions
- **Purpose**: Early detection of compatibility issues

## Setup Requirements

### 1. GPG Keys (for releases)
```bash
# Generate keys for signing
./scripts/setup-gpg-keys.sh

# Add to GitHub Secrets:
# - GPG_PRIVATE_KEY
# - GPG_PASSPHRASE
```

### 2. Optional Secrets
- `CODECOV_TOKEN`: For coverage reports

## Local Testing

```bash
# Test the release process locally
make release-dry-run

# Run linting
make lint

# Run security scans
make security-scan
```

## Creating a Release

1. Update version in code if needed
2. Create and push a tag:
   ```bash
   git tag v0.1.0
   git push origin v0.1.0
   ```
3. GitHub Actions will automatically:
   - Build binaries for all platforms
   - Create a draft release
   - Sign artifacts

## Cost Controls

- Acceptance tests only run on main branch or same-repo PRs
- Nightly builds are opt-in via workflow_dispatch
- Matrix testing is limited to essential versions
- Kind clusters are cleaned up after each run

## Terraform Registry

Publishing is disabled during alpha. When ready:
1. Remove `draft: true` from `.goreleaser.yml`
2. Upload public GPG key to registry.terraform.io
3. Follow HashiCorp's publishing documentation

