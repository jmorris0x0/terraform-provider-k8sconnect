# Contributing to terraform-provider-k8sconnect

Thank you for your interest in contributing! This document provides guidelines and instructions for contributing to the project.

## Table of Contents

- [Before You Start](#before-you-start)
- [Getting Started](#getting-started)
- [Development Workflow](#development-workflow)
- [Testing Requirements](#testing-requirements)
- [Code Quality Standards](#code-quality-standards)
- [Architecture & Design Principles](#architecture--design-principles)
- [Pull Request Process](#pull-request-process)
- [Questions or Problems?](#questions-or-problems)

---

## Before You Start

### Open an Issue First

**Please open an issue before starting work on any non-trivial changes.** This helps us:
- Discuss the approach before you invest time coding
- Ensure the change aligns with project goals
- Avoid duplicate work
- Identify any architectural constraints

Small fixes (typos, documentation clarifications, obvious bugs) don't require an issue, but feel free to open one anyway if you'd like feedback.

### What We're Looking For

We welcome contributions including:
- Bug fixes
- Documentation improvements
- Test coverage improvements
- Performance optimizations
- New features (discuss in issue first)
- Examples and usage patterns

---

## Getting Started

### Prerequisites

You'll need the following tools installed:

- **Go**: Version specified in `go.mod` (currently 1.23+)
- **Terraform**: >= 1.0.11 (we recommend using [tfenv](https://github.com/tfutils/tfenv) for version management)
- **k3d**: For running acceptance tests ([installation guide](https://k3d.io/stable/#installation))
- **Docker**: Required by k3d for running test clusters
- **kubectl**: Kubernetes CLI tool
- **make**: Build automation (installed by default on macOS/Linux)

### Initial Setup

1. **Fork the repository** on GitHub

2. **Clone your fork**:
   ```bash
   git clone https://github.com/YOUR_USERNAME/terraform-provider-k8sconnect.git
   cd terraform-provider-k8sconnect
   ```

3. **Add upstream remote**:
   ```bash
   git remote add upstream https://github.com/jmorris0x0/terraform-provider-k8sconnect.git
   ```

4. **Install dependencies**:
   ```bash
   go mod download
   ```

5. **Build the provider**:
   ```bash
   make build
   ```

6. **Install locally for testing**:
   ```bash
   make install
   ```
   This installs to `~/.terraform.d/plugins/registry.terraform.io/local/k8sconnect/`

---

## Development Workflow

### 1. Create a Branch

```bash
git checkout -b feature/your-feature-name
# or
git checkout -b fix/issue-number-description
```

### 2. Make Your Changes

- Write clear, readable code
- Follow existing code style and patterns
- Add tests for new functionality
- Update documentation as needed

### 3. Test Your Changes

**This is critical**: All tests must pass before your PR can be merged.

```bash
# Run unit tests (fast, no cluster required)
make test

# Run acceptance tests (slower, requires k3d cluster)
make testacc

# Run example tests
make test-examples

# Run all linting checks
make lint

# Run security scans
make security-scan
```

See [Testing Requirements](#testing-requirements) for details.

### 4. Commit Your Changes

- Write clear, descriptive commit messages
- Reference issue numbers when applicable (e.g., "Fix drift detection for HPA replicas (#123)")
- Commits can be squashed during merge, so don't worry too much about commit granularity

### 5. Push and Create Pull Request

```bash
git push origin your-branch-name
```

Then open a pull request on GitHub.

---

## Testing Requirements

### Why Testing Matters

This provider manages critical infrastructure. **All tests must pass** before any change is merged. No exceptions.

### Test Types

#### Unit Tests
Fast tests that don't require a Kubernetes cluster. Run frequently during development.

```bash
make test
```

**When to add unit tests:**
- Pure functions (parsing, validation, transformation)
- Error handling logic
- Utility functions

#### Acceptance Tests
Integration tests against a real k3d Kubernetes cluster. These verify the provider works end-to-end.

```bash
make testacc

# Run a specific test
TEST=TestAccObjectResource_Basic make testacc

# Reduce output noise
make testacc 2>&1 | grep FAIL
```

**When to add acceptance tests:**
- New resource features
- Bug fixes involving Kubernetes API interactions
- Field ownership or drift detection changes
- Connection/authentication methods

#### Example Tests
Tests that verify our runnable examples work correctly. These serve as both tests and documentation.

```bash
make test-examples

# Run a specific example test
TEST=TestExamples/yaml-split-dependency-ordering make test-examples
```

**When to add example tests:**
- New usage patterns
- Complex multi-resource scenarios
- Common user workflows

### Coverage

Generate a coverage report:
```bash
make coverage
# Opens coverage.html in your browser
```

We don't have a strict coverage percentage requirement, but significant new features should include comprehensive tests.

### Test Environment

Acceptance tests automatically:
- Create a k3d cluster with OIDC configuration
- Set up test certificates and service accounts
- Configure environment variables
- Clean up after completion

You can manually set up the test environment:
```bash
make oidc-setup
```

And clean it up:
```bash
make clean
```

---

## Code Quality Standards

### Linting

We use `golangci-lint` with automatic fixes enabled:

```bash
make lint
```

This will:
- Run `go vet`
- Run `golangci-lint` with auto-fix
- Install `golangci-lint` automatically if missing

### Security Scanning

Run security checks before submitting:

```bash
make security-scan
```

This runs:
- `gosec` - Security-focused static analysis
- `govulncheck` - Known vulnerability detection

### Code Style

- Follow standard Go conventions
- Use meaningful variable and function names
- Add comments for non-obvious logic
- Keep functions focused and reasonably sized (cyclomatic complexity < 15)

Check complexity:
```bash
make complexity
```

Try to keep complexity under 20, and under 15 if you can.

### Documentation

When adding new features:

- **Update resource documentation**: Edit schema descriptions in `manifest.go`
- **Regenerate docs**: Run `make docs` to update generated documentation
- **Update README**: Add examples for significant new features
- **Consider ADRs**: For architectural decisions, create an ADR in `docs/ADRs/`

---

## Architecture & Design Principles

### Core Priorities

When proposing changes, these priorities guide decisions (in order):

1. **Accurate diffs** - No false positives, plan must match apply
2. **Clean UX** - Minimal confusion, intuitive behavior
3. **Universal CRD support** - No hardcoded schemas, works with any CRD

If your change requires trade-offs between these, discuss in the issue first.

### Key Architectural Concepts

- **Server-Side Apply (SSA)**: All resource operations use SSA with field ownership tracking
- **Dry-run projections**: Plan phase uses dry-run to predict exact Kubernetes behavior
- **Field ownership parsing**: We parse `managedFields` to determine what we manage vs. external controllers
- **Bootstrap handling**: Smart logic for cluster-creation scenarios where connections are "known after apply"

### Architecture Decision Records (ADRs)

Read these for deep context:

- **[ADR-001](docs/ADRs/ADR-001-managed-state-projection.md)**: Managed state projection core design
- **[ADR-005](docs/ADRs/ADR-005-field-ownership-strategy.md)**: Field ownership strategy (why we parse managedFields)
- **[ADR-011](docs/ADRs/ADR-011-concise-diff-format.md)**: Bootstrap handling and YAML fallback rejection
- **[ADR-012](docs/ADRs/ADR-012-terraform-fundamental-contract.md)**: Terraform contract and framework limitations

Other ADRs document specific features like CRD retry, immutable resources, identity changes, etc.

### File Organization

- **Resource layer**: `internal/k8sconnect/resource/object/`
  - `manifest.go` - Schema definition
  - `plan_modifier.go` - ModifyPlan with dry-run
  - `crud.go` - CRUD operations
  - `projection.go` - Field filtering
  - `field_ownership.go` - Parse managedFields
  - `identity_changes.go` - Detect identity changes

- **Client/Auth**: `internal/k8sconnect/common/`
  - `k8sclient/` - K8s client wrapper
  - `auth/` - Connection handling
  - `factory/` - Client factory with caching

- **Data sources**: `internal/k8sconnect/datasource/`

---

## Pull Request Process

### Before Submitting

- [ ] All tests pass (`make test`, `make testacc`, `make test-examples`)
- [ ] Linting passes (`make lint`)
- [ ] Security scans pass (`make security-scan`)
- [ ] Documentation updated if needed
- [ ] ADR created if making architectural decisions

### PR Description

Include:
- **Summary**: What does this PR do?
- **Motivation**: Why is this change needed?
- **Testing**: What tests did you add/run?
- **Breaking changes**: Any backward compatibility concerns?
- **Related issues**: Link to issue(s)

### Review Process

- Jonathan ([@jmorris0x0](https://github.com/jmorris0x0)) will review all PRs
- Expect feedback and iteration - this is normal!
- CI must pass (unit tests, acceptance tests, linting)
- Once approved, your PR will be squash-merged to `main`

### After Merge

- Your contribution will be included in the next release
- You'll be credited in the release notes
- Thank you for making k8sconnect better! 🎉

---

## Questions or Problems?

- **Bugs**: Open an issue with reproduction steps
- **Feature ideas**: Open an issue to discuss before implementing
- **Questions**: Open an issue or discussion
- **Security issues**: Report privately via [GitHub Security Advisories](https://github.com/jmorris0x0/terraform-provider-k8sconnect/security/advisories/new)

---

## License

By contributing to terraform-provider-k8sconnect, you agree that your contributions will be licensed under the Apache License 2.0.

See [LICENSE](LICENSE) for details.
