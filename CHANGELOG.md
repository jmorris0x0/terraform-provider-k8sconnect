# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.3] - 2025-11-03

### Fixed

- **Fixed schema descriptions to improve technical accuracy**

## [0.3.2] - 2025-11-03

### Fixed

- **Fixed documentation errors in guides and resource templates**

## [0.3.1] - 2025-11-02

### Fixed

- **Fixed empty YAML validation and error handling**
  - Provider now validates `yaml_body` is not empty or whitespace-only before attempting to parse
- **Fixed error message clarity for missing namespaces**
  - Missing namespace errors now clearly indicate "namespace not found" instead of suggesting CRD issues
- **Fixed CEL validation error categorization for built-in resources**
  - Built-in Kubernetes resources (v1, apps/v1, *.k8s.io) with validation errors now show "Invalid Resource" instead of "CEL Validation Failed"
- **Fixed connection error categorization and messaging**
  - Network/connection errors now properly categorized as "Cluster Connection Failed" instead of "Resource Type Not Found"

### Improved
- **Enhanced error message quality across all resource operations**
  - All error messages now follow consistent UX pattern: clear title, what happened, why it happened, how to fix

## [0.3.0] - 2025-10-31

### BREAKING CHANGES

- **Restored `managed_fields` computed attribute** to `k8sconnect_object` and `k8sconnect_patch` resources
  - This attribute was removed in v0.2.0 and has now been restored with enhanced functionality
  - Tracks all field managers (not just k8sconnect) to support Server-Side Apply shared ownership detection
  - Shows field-level ownership as `map[string]string` where keys are field paths and values are manager names
  - When ownership changes appear in diffs, it indicates another system has taken control of those fields
  - **Migration impact**: State files from v0.2.x will show this attribute appearing during first plan/apply after upgrade
  - Note: `previous_owners` attribute remains removed (as of v0.2.0)

### Security

- **Updated Go runtime to 1.25.3** to address security vulnerability GO-2025-4007

### Fixed

- **Fixed wait resource race condition with condition evaluation timing**
  - Wait operations could incorrectly report timeout when condition was actually met due to watch event delivery lag or select statement timing
  - Added dual-layer condition check: primary check at timeout boundary in watch loop, defense-in-depth check in error builder
  - Wait operations now accurately detect when conditions are met even when watch events are delayed
- **Fixed wait resource initial status reporting**
  - Enhanced wait logic to properly report first status check
  - Added comprehensive timeout test coverage

### Improved

- **Enhanced field ownership tracking for shared ownership scenarios**
  - Refactored internal ownership data structures to track all field managers (not just k8sconnect)
  - Enables proper handling of Server-Side Apply shared ownership (ADR-021 Phase 0)
  - Foundation for future ownership transition messaging improvements

- **Code quality and maintainability**
  - Renamed internal field ownership modules to managed_fields for clarity
  - Reduced code complexity through DRY refactoring
  - Removed dead code and state upgrade files
  - Improved test coverage for wait resources and shared ownership scenarios

## [0.2.2] - 2025-10-28

### Improved

- **Improved error messages**
  - RBAC and field validation

## [0.2.1] - 2025-10-27

### Fixed

- **Fixed for_each replacement race condition timeout**
  - When for_each keys change (e.g., `["old-key"]` → `["new-key"]`), Terraform runs Delete(old-key) and Create(new-key) in parallel
  - If both map to the same Kubernetes object, Create() wins via Server-Side Apply, changing the ownership annotation
  - Previously: Delete() would wait 5 minutes for an object that was already replaced, then timeout
  - Now: Delete() continuously monitors ownership annotations during wait loop, exits gracefully in ~5 seconds when replacement detected
  - This fix applies to any scenario where parallel operations target the same Kubernetes object
- **Fixed namespace defaulting for namespace-scoped resources**
  - Resources without explicit `metadata.namespace` now correctly infer the namespace from cluster connection

## [0.2.0] - 2025-10-27

### BREAKING CHANGES
- **Removed `managed_fields` and `previous_owners` computed attributes** from `k8sconnect_object` and `k8sconnect_patch` resources
  - Field ownership tracking moved to private state to prevent "Provider produced inconsistent result" errors
  - Ownership transitions are now displayed as **warnings during plan** instead of state diffs
  - Note: `k8sconnect_patch` ownership transfer-back logic has been simplified - removed fields now become unmanaged rather than transferred back
- **Renamed `cluster_connection` to `cluster`** across all resources and data sources
  - **Migration required:** Replace `cluster_connection =` with `cluster =` in your Terraform configurations

## [0.1.7] - 2025-10-26

### Fixed
- Fixed `k8sconnect_patch` resource showing formatting-only changes (whitespace, comments) as drift

### Improved
- Enhanced kubeconfig validation with better error messages
- Improved `k8sconnect_patch` drift detection warnings

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
- Dry-run plans for accurate diffs
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

