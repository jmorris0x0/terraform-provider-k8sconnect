# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.6] - 2025-10-25

### Fixed
- Fixed field ownership prediction accuracy when using Server-Side Apply with `force=true` (ADR-019)

## [0.1.5] - 2025-10-23

### Fixed
- Improved import `yaml_body` cleaning to remove more server-generated fields

### Changed
- Enhanced error messages with more actionable suggestions for field conflicts and validation errors
- Harmonized resource ID format across all resources for consistency

## [0.1.4] - 2025-10-21

### Fixed
- Fixed import causing "Provider produced inconsistent result after apply" errors
- Fixed `ignore_fields` JSONPath predicates not matching array elements by field value
  - Example: `spec.template.spec.containers[?(@.name=='app')].env[?(@.name=='EXTERNAL_VAR')].value`

## [0.1.3] - 2025-10-20

### Changed
- **Breaking**: Renamed `k8sconnect_wait` resource output attribute from `.status` to `.result` for semantic accuracy
  - Previous: `k8sconnect_wait.example.status.loadBalancer.ingress[0].ip`
  - Now: `k8sconnect_wait.example.result.status.loadBalancer.ingress[0].ip`
  - This better reflects that the attribute contains extracted field data, not just status

### Fixed
- Fixed provider crash when CRD is deleted before custom resource instances during `terraform destroy`
  - Kubernetes cascade-deletes CRs when their CRD is removed
  - Provider now gracefully handles missing resource types during delete and read operations
  - Delete succeeds idempotently when resource type no longer exists
  - Read removes resource from state when type is not discoverable

## [0.1.2] - 2025-10-19

### Added
- Server-side field validation to catch typos and invalid fields (ADR-017)
  - Detects common mistakes like `replica` instead of `replicas` during plan phase
  - Leverages Kubernetes 1.27+ strict field validation
  - Clear error messages showing which fields are invalid
  - See [KEP-2579: Field Validation for Server Side Apply](https://github.com/kubernetes/enhancements/tree/master/keps/sig-api-machinery/2579-psp-replacement) for details on the upstream feature

- Enhanced error formatting for CEL (Common Expression Language) validation failures from CustomResourceDefinitions
  - Support for displaying multiple CEL validation errors in a single failure
  - Clear, structured error messages showing field paths and validation messages
  - CEL validation is available in Kubernetes 1.25+ (beta) and 1.29+ (GA)
  - See [KEP-2876: CRD Validation Expression Language](https://github.com/kubernetes/enhancements/tree/master/keps/sig-api-machinery/2876-crd-validation-expression-language) for details on the upstream feature

## [0.1.1] - 2025-10-18

### Changed
- Code cleanup and linting improvements
- Build and release process refinements

## [0.1.0] - 2025-10-18

### Added
- Initial release of terraform-provider-k8sconnect
- Server-Side Apply (SSA) support with field ownership tracking
- Dry-run projections for accurate diffs
- Inline per-resource cluster connections (no provider-level configuration required)
- Universal CRD support via dynamic client discovery
- `k8sconnect_object` resource for managing any Kubernetes resource
- `k8sconnect_patch` resource for patching existing resources
- `k8sconnect_wait` resource for waiting on field conditions
- `k8sconnect_manifest` data source for reading Kubernetes resources
- `k8sconnect_yaml_split` data source for splitting multi-document YAML files
- `k8sconnect_yaml_scoped` data source for filtering YAML by scope
- Support for multiple cluster connections in a single configuration
- Automatic retry logic for CRD establishment (ADR-007)
- Bootstrap support for creating clusters and workloads in single apply (ADR-011)
- Field ownership-aware drift detection
- Managed state projection to avoid false positives
- Ignore fields support for coexisting with other controllers (e.g., HPA)
- Import support for existing Kubernetes resources
- Multiple authentication methods: kubeconfig, token, exec, client certificates, OIDC

