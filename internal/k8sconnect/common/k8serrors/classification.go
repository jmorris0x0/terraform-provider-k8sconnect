package k8serrors

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"k8s.io/apimachinery/pkg/api/errors"
)

// isBuiltInAPIGroup checks if the apiVersion belongs to a built-in Kubernetes API group
// Built-in groups are either:
// 1. Core API (v1, v1alpha1, v1beta1) - no group prefix
// 2. Legacy groups (apps, batch, autoscaling, etc.) - no domain in group name
// 3. Modern K8s groups (*.k8s.io) - official Kubernetes API extensions
// CRDs MUST have a domain (contain a dot) that is NOT .k8s.io
func isBuiltInAPIGroup(apiVersion string) bool {
	parts := strings.Split(apiVersion, "/")

	if len(parts) == 1 {
		// Core API: v1, v1alpha1, v1beta1
		return true
	}

	group := parts[0]

	// CRDs must have a domain. Built-in groups either:
	// 1. Have no dot (legacy: apps, batch, autoscaling, etc.)
	// 2. End with .k8s.io (modern K8s extensions)
	if !strings.Contains(group, ".") {
		return true // Legacy built-in (apps, batch, etc.)
	}

	return strings.HasSuffix(group, ".k8s.io")
}

// ClassifyError categorizes Kubernetes API errors for better user experience
// Returns (severity, title, detail) suitable for Terraform diagnostics
func ClassifyError(err error, operation, resourceDesc, apiVersion string) (severity, title, detail string) {
	switch {
	case errors.IsNotFound(err):
		return "warning", fmt.Sprintf("%s: Resource Not Found", operation),
			fmt.Sprintf("The %s was not found in the cluster. It may have been deleted outside of Terraform.", resourceDesc)

	case errors.IsForbidden(err):
		return "error", fmt.Sprintf("%s: Insufficient Permissions", operation),
			fmt.Sprintf("RBAC permissions insufficient to %s %s. Check that your credentials have the required permissions for this operation. Details: %v",
				operation, resourceDesc, err)

	// Note: SSA conflicts are intentionally prevented by using Force=true (ADR-005)
	// This code path exists for defensive programming in case Force is ever disabled
	// ExtractConflictDetails has defensive unit test coverage despite being unreachable in production
	case errors.IsConflict(err):
		conflictDetails, conflictPaths := ExtractConflictDetailsAndPaths(err)
		ignoreFieldsSuggestion := formatIgnoreFieldsSuggestion(conflictPaths)

		message := fmt.Sprintf("Server-side apply conflict detected for %s.\n"+
			"Another controller is managing one or more fields in this resource.\n\n"+
			"Conflicting fields:\n%s\n\n", resourceDesc, conflictDetails)

		if ignoreFieldsSuggestion != "" {
			message += fmt.Sprintf("To release ownership and allow other controllers to manage these fields, add:\n\n%s\n\n", ignoreFieldsSuggestion)
		} else {
			message += "To resolve this conflict:\n" +
				"1. Add conflicting field paths to 'ignore_fields' to release ownership\n" +
				"2. Remove the conflicting fields from your Terraform configuration\n\n"
		}

		message += fmt.Sprintf("Details: %v", err)

		return "error", fmt.Sprintf("%s: Field Manager Conflict", operation), message

	case errors.IsTimeout(err) || errors.IsServerTimeout(err):
		return "error", fmt.Sprintf("%s: Kubernetes API Timeout", operation),
			fmt.Sprintf("Timeout while performing %s on %s. The cluster may be under heavy load or experiencing connectivity issues. Details: %v",
				operation, resourceDesc, err)

	case errors.IsUnauthorized(err):
		return "error", fmt.Sprintf("%s: Authentication Failed", operation),
			fmt.Sprintf("Authentication failed for %s %s. Check your credentials and ensure they are valid. Details: %v",
				operation, resourceDesc, err)

	// ADR-017: Field validation errors (status 400) - check BEFORE IsInvalid (status 422)
	case IsFieldValidationError(err):
		fieldDetails := ExtractFieldValidationDetails(err)
		return "error", fmt.Sprintf("%s: Field Validation Failed", operation),
			fmt.Sprintf("Field validation failed for %s.\n\n%s",
				resourceDesc, fieldDetails)

	case errors.IsInvalid(err):
		// Check if this is specifically an immutable field error
		// IMPORTANT: Check this FIRST, since CEL immutability errors
		// (e.g., "may not change once set") match both IsCELValidationError and IsImmutableFieldError.
		// Immutable is more specific, so it should take precedence.
		if IsImmutableFieldError(err) {
			immutableFields := ExtractImmutableFields(err)
			return "error", fmt.Sprintf("%s: Immutable Field Changed", operation),
				fmt.Sprintf("Cannot update immutable field(s) %v on %s.\n\n"+
					"Immutable fields cannot be changed after resource creation.\n\n"+
					"To resolve this:\n\n"+
					"Option 1 - Revert the change:\n"+
					"  Restore the original value in your YAML\n\n"+
					"Option 2 - Recreate the resource:\n"+
					"  terraform destroy -target=<resource_address>\n"+
					"  terraform apply\n\n"+
					"Option 3 - Use replace (Terraform 1.5+):\n"+
					"  terraform apply -replace=<resource_address>",
					immutableFields, resourceDesc)
		}

		// Check if this is specifically a CEL validation error
		// IMPORTANT: Only show CEL error for CRDs, not built-in K8s resources
		// Built-in resources (v1, apps/v1, etc.) use OpenAPI schema validation, not CEL
		if IsCELValidationError(err) && !isBuiltInAPIGroup(apiVersion) {
			celDetails := ExtractCELValidationDetails(err)
			return "error", fmt.Sprintf("%s: CEL Validation Failed", operation),
				fmt.Sprintf("CEL validation rule failed for %s.\n\n"+
					"%s\n\n"+
					"CEL (Common Expression Language) validation is defined in the CRD schema.\n"+
					"Fix the field value to satisfy the validation rule or adjust the CRD validation rules.\n\n"+
					"Details: %v",
					resourceDesc, celDetails, err)
		}

		// Check if this IsInvalid error contains field-specific validation details
		// (e.g., enum validation, type validation) - Issue #3
		// This is more generic than CEL, so check it AFTER CEL validation
		if IsInvalidWithFieldDetails(err) {
			fieldDetails := ExtractInvalidFieldDetails(err)
			return "error", fmt.Sprintf("%s: Field Validation Failed", operation),
				fmt.Sprintf("Field validation failed for %s.\n\n%s",
					resourceDesc, fieldDetails)
		}

		// Generic invalid resource error (for non-field-validation, non-CEL, and non-immutable errors)
		return "error", fmt.Sprintf("%s: Invalid Resource", operation),
			fmt.Sprintf("The %s contains invalid fields or values. Review the YAML specification and ensure all required fields are present and correctly formatted. Details: %v",
				resourceDesc, err)

	// Note: "Already Exists" errors are prevented by using Server-Side Apply (SSA)
	// SSA is idempotent - it updates existing resources instead of failing
	// This code path exists for defensive programming in case non-SSA operations are added
	case errors.IsAlreadyExists(err):
		return "error", fmt.Sprintf("%s: Resource Already Exists", operation),
			fmt.Sprintf("The %s already exists in the cluster and cannot be created. Use import to manage existing resources with Terraform. Details: %v",
				resourceDesc, err)

	case IsCRDNotFoundError(err):
		return "error", fmt.Sprintf("%s: Custom Resource Definition Not Found", operation),
			fmt.Sprintf("The Custom Resource Definition (CRD) for %s does not exist in the cluster.\n\n"+
				"This usually means:\n"+
				"1. The CRD hasn't been installed yet\n"+
				"2. The CRD is being created in the same apply (will auto-retry for up to 30 seconds)\n"+
				"3. There's a typo in apiVersion or kind\n\n"+
				"If the CRD is being created in this same Terraform config, the provider will automatically "+
				"retry while the CRD becomes established.",
				resourceDesc)

	default:
		// Check if this is a conversion/schema validation error (Issue #2)
		// These errors contain patterns like "failed to convert" or "quantities must match"
		if IsConversionError(err) {
			fieldDetails := ExtractConversionErrorDetails(err)
			return "error", fmt.Sprintf("%s: Field Validation Failed", operation),
				fmt.Sprintf("Field validation failed for %s.\n\n%s",
					resourceDesc, fieldDetails)
		}

		return "error", fmt.Sprintf("%s: Kubernetes API Error", operation),
			fmt.Sprintf("An unexpected error occurred while performing %s on %s. Details: %v",
				operation, resourceDesc, err)
	}
}

// AddClassifiedError classifies a K8s error and adds it to diagnostics
// This is a convenience function that combines ClassifyError with adding to diagnostics
func AddClassifiedError(diags *diag.Diagnostics, err error, operation, resourceDesc, apiVersion string) {
	severity, title, detail := ClassifyError(err, operation, resourceDesc, apiVersion)
	if severity == "warning" {
		diags.AddWarning(title, detail)
	} else {
		diags.AddError(title, detail)
	}
}

// IsImmutableFieldError checks if error is due to immutable field modification
func IsImmutableFieldError(err error) bool {
	if statusErr, ok := err.(*errors.StatusError); ok {
		if statusErr.ErrStatus.Code == 422 {
			msg := strings.ToLower(statusErr.ErrStatus.Message)
			return strings.Contains(msg, "immutable") ||
				strings.Contains(msg, "forbidden") ||
				strings.Contains(msg, "cannot be changed") ||
				strings.Contains(msg, "may not be modified") ||
				strings.Contains(msg, "may not change") // K8s 1.25+ CEL validation
		}
	}
	return false
}

// checkErrorContains checks if error message contains given substrings
// Checks both StatusError.Message and plain error string
func checkErrorContains(err error, substrings ...string) bool {
	if statusErr, ok := err.(*errors.StatusError); ok {
		msg := strings.ToLower(statusErr.ErrStatus.Message)
		for _, substr := range substrings {
			if !strings.Contains(msg, substr) {
				return false
			}
		}
		return true
	}
	// Also check plain error messages (for wrapped errors)
	errMsg := strings.ToLower(err.Error())
	for _, substr := range substrings {
		if !strings.Contains(errMsg, substr) {
			return false
		}
	}
	return true
}

// checkErrorContainsAny checks if error message contains any of given substrings
func checkErrorContainsAny(err error, substrings ...string) bool {
	if statusErr, ok := err.(*errors.StatusError); ok {
		msg := strings.ToLower(statusErr.ErrStatus.Message)
		for _, substr := range substrings {
			if strings.Contains(msg, substr) {
				return true
			}
		}
		return false
	}
	// Also check plain error messages
	errMsg := strings.ToLower(err.Error())
	for _, substr := range substrings {
		if strings.Contains(errMsg, substr) {
			return true
		}
	}
	return false
}

// IsCRDNotFoundError detects when a Custom Resource Definition doesn't exist yet
func IsCRDNotFoundError(err error) bool {
	// Kubernetes returns these messages when the CRD doesn't exist
	return checkErrorContainsAny(err, "no matches for kind", "could not find the requested resource")
}

// IsNamespaceNotFoundError detects when a namespace doesn't exist yet
// This can happen when namespace and namespaced resources are created in parallel
func IsNamespaceNotFoundError(err error) bool {
	// Kubernetes returns "namespaces 'xyz' not found" when namespace doesn't exist
	// Use "namespaces" (plural, the K8s resource type) to be specific
	return checkErrorContains(err, "namespaces", "not found")
}

// IsDependencyNotReadyError detects temporary errors due to dependencies not being ready yet
// This includes both CRD not found and namespace not found errors
func IsDependencyNotReadyError(err error) bool {
	return IsCRDNotFoundError(err) || IsNamespaceNotFoundError(err)
}

// ExtractImmutableFields extracts field names from immutable field errors
func ExtractImmutableFields(err error) []string {
	// Simple extraction - just look for field names in the error
	var fields []string
	if statusErr, ok := err.(*errors.StatusError); ok {
		msg := statusErr.ErrStatus.Message

		// Look for patterns like "spec.storageClassName: Forbidden"
		if strings.Contains(msg, "spec.") {
			fields = append(fields, "spec fields")
		} else {
			fields = append(fields, "(see error details)")
		}
	}
	return fields
}

// ExtractConflictDetailsAndPaths parses conflict error and returns both formatted details and field paths
func ExtractConflictDetailsAndPaths(err error) (string, []string) {
	// The Kubernetes error message typically contains field paths and managers
	// Example: "conflict with \"kubectl\" with subresource \"scale\" using apps/v1: .spec.replicas"
	errStr := err.Error()

	// Parse out the conflicts
	var details []string
	var paths []string

	// Look for patterns like: conflict with "manager" ... : .field.path
	conflictPattern := regexp.MustCompile(`conflict with "([^"]+)".*?: ([\.\w\[\]]+)`)
	matches := conflictPattern.FindAllStringSubmatch(errStr, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			manager := match[1]
			rawFieldPath := match[2]
			// Remove leading dot if present (.spec.replicas -> spec.replicas)
			fieldPath := strings.TrimPrefix(rawFieldPath, ".")
			details = append(details, fmt.Sprintf("  - %s (managed by \"%s\")", fieldPath, manager))
			paths = append(paths, fieldPath)
		}
	}

	if len(details) == 0 {
		// Fallback if we can't parse
		return "- Multiple field ownership conflicts detected", nil
	}

	return strings.Join(details, "\n"), paths
}

// ExtractConflictDetails parses conflict error details (backward compatibility wrapper)
func ExtractConflictDetails(err error) string {
	details, _ := ExtractConflictDetailsAndPaths(err)
	return details
}

// formatIgnoreFieldsSuggestion creates a ready-to-use ignore_fields configuration from conflict paths
func formatIgnoreFieldsSuggestion(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	if len(paths) == 1 {
		return fmt.Sprintf("  ignore_fields = [\"%s\"]", paths[0])
	}

	// Multiple paths - format as multi-line for readability
	var lines []string
	lines = append(lines, "  ignore_fields = [")
	for i, path := range paths {
		if i < len(paths)-1 {
			lines = append(lines, fmt.Sprintf("    \"%s\",", path))
		} else {
			lines = append(lines, fmt.Sprintf("    \"%s\"", path))
		}
	}
	lines = append(lines, "  ]")
	return strings.Join(lines, "\n")
}

// IsFieldValidationError checks if error is due to field validation (unknown/duplicate field)
// Field validation errors occur when FieldValidation="Strict" and YAML contains fields
// not present in the resource's OpenAPI schema
func IsFieldValidationError(err error) bool {
	// Nil check to prevent panic
	if err == nil {
		return false
	}

	if statusErr, ok := err.(*errors.StatusError); ok {
		// Field validation errors are status code 400 (Bad Request)
		// vs CEL/immutable which are 422 (Unprocessable Entity)
		if statusErr.ErrStatus.Code == 400 {
			msg := strings.ToLower(statusErr.ErrStatus.Message)

			// Primary indicators of field validation errors
			if strings.Contains(msg, "unknown field") ||
				strings.Contains(msg, "duplicate field") {
				return true
			}

			// K8s also uses these message formats for field validation
			if strings.Contains(msg, "strict decoding error") ||
				strings.Contains(msg, "field not declared in schema") {
				return true
			}
		}
		// IMPORTANT: Return false for StatusError with other status codes (like 404, 422, etc.)
		// Don't fall through to wrapped error check for StatusError types
		return false
	}

	// For non-StatusError types, check if error message contains field validation indicators
	// This handles wrapped errors that don't expose the underlying StatusError
	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "field not declared in schema") ||
		strings.Contains(errMsg, "unknown field") ||
		strings.Contains(errMsg, "duplicate field") {
		return true
	}

	return false
}

// IsCELValidationError checks if error is due to CEL validation rule failure
func IsCELValidationError(err error) bool {
	if statusErr, ok := err.(*errors.StatusError); ok {
		msg := statusErr.ErrStatus.Message

		// CEL validation errors can appear in several formats:
		// 1. "failed rule: <expression>: <message>" (some K8s versions)
		// 2. "x-kubernetes-validations" (explicit CEL reference)
		// 3. "Invalid value: <value>: <custom-cel-message>" (common format)
		//
		// To distinguish CEL from other validation errors, we check for:
		// - Presence of "Invalid value" with specific CRD field patterns
		// - The error is on a CRD resource (contains ".example.com" or similar)
		// - Has a custom message after the value (CEL messages are user-defined)

		msgLower := strings.ToLower(msg)

		// Direct CEL indicators
		if strings.Contains(msgLower, "failed rule:") ||
			strings.Contains(msgLower, "x-kubernetes-validations") {
			return true
		}

		// Check for CRD + Invalid value pattern (likely CEL)
		// CEL errors on CRDs typically show as: "ResourceName.group.example.com ... Invalid value ..."
		// Multiple errors shown as: [field: Invalid value: "val": msg1, field: Invalid value: "val": msg2]
		if strings.Contains(msg, ".") && // Has group/domain in resource name (CRD pattern)
			strings.Contains(msgLower, "invalid value:") {

			// Try to distinguish CEL from built-in validation
			// CEL messages are after the value: "field: Invalid value: \"value\": <custom-message>"
			// Built-in K8s errors typically are: "field: Required value" or "field: Invalid value: must be ..."

			// If message contains a bracketed list of errors, it's likely multiple CEL violations
			if strings.Contains(msg, "[") && strings.Contains(msg, "]") {
				return true
			}

			// Single error: check it's not a built-in validation error
			// Built-in errors: "Required value", "Invalid value: must be", "Invalid value: required"
			if !strings.Contains(msgLower, "required value") &&
				!strings.Contains(msgLower, "invalid value: must be") &&
				!strings.Contains(msgLower, "invalid value: required") {
				return true
			}
		}
	}
	return false
}

// ExtractFieldValidationDetails parses field validation error for user-friendly display
// Handles both single and multiple field validation errors
// Also handles wrapped errors where the StatusError is nested inside another error
func ExtractFieldValidationDetails(err error) string {
	// Try to get the message - either from StatusError or plain error
	var msg string
	if statusErr, ok := err.(*errors.StatusError); ok {
		msg = statusErr.ErrStatus.Message
	} else {
		// Not a StatusError - try to extract from wrapped error message
		msg = err.Error()
	}

	// Field validation errors can have several formats:
	// 1. "unknown field \"spec.replica\""
	// 2. "duplicate field \"spec.replicas\""
	// 3. Multiple errors in a list (may be bracketed or newline-separated)
	// 4. "strict decoding error: unknown field \"spec.replica\", unknown field \"spec.container\""

	var details []string
	foundErrors := 0

	// Extract content between brackets if present (multiple errors format)
	msgToParse := msg
	if strings.Contains(msg, "[") && strings.Contains(msg, "]") {
		start := strings.Index(msg, "[")
		end := strings.LastIndex(msg, "]")
		if start >= 0 && end > start {
			msgToParse = msg[start+1 : end]
		}
	}

	// Pattern 1: unknown field "path" or duplicate field "path"
	// Also handles: strict decoding error: unknown field "path"
	fieldPattern1 := regexp.MustCompile(`(unknown field|duplicate field)\s*"([^"]+)"`)
	matches1 := fieldPattern1.FindAllStringSubmatch(msgToParse, -1)

	// Pattern 2: .spec.replica: field not declared in schema
	fieldPattern2 := regexp.MustCompile(`([\w\[\]\.]+):\s*field not declared in schema`)
	matches2 := fieldPattern2.FindAllStringSubmatch(msgToParse, -1)

	// Combine both patterns
	var matches [][]string
	for _, match := range matches1 {
		if len(match) >= 3 {
			matches = append(matches, match)
		}
	}
	for _, match := range matches2 {
		if len(match) >= 2 {
			// Reformat to match pattern 1 structure: [full, "unknown field", "path"]
			matches = append(matches, []string{match[0], "field not declared in schema", match[1]})
		}
	}

	if len(matches) > 0 {
		for _, match := range matches {
			if len(match) >= 3 {
				errorType := match[1] // "unknown field" or "duplicate field"
				fieldPath := match[2] // e.g., "spec.replica"

				if foundErrors > 0 {
					details = append(details, "") // Blank line between errors
				}
				details = append(details, fmt.Sprintf("Field: %s", fieldPath))
				details = append(details, fmt.Sprintf("Error: %s", errorType))
				foundErrors++
			}
		}

		if foundErrors > 0 {
			if foundErrors > 1 {
				details = append([]string{fmt.Sprintf("Found %d field validation errors:", foundErrors)}, details...)
			}
			return strings.Join(details, "\n")
		}
	}

	// Fallback: couldn't parse structured details, show the full error
	return fmt.Sprintf("Field validation failed. Check the error message below:\n\n%s", msg)
}

// ExtractCELValidationDetails parses CEL validation error for user-friendly display
// Handles both single and multiple validation errors
func ExtractCELValidationDetails(err error) string {
	statusErr, ok := err.(*errors.StatusError)
	if !ok {
		return "Unable to parse CEL validation error details"
	}

	msg := statusErr.ErrStatus.Message

	// CEL errors can have several formats:
	// 1. "field.path: failed rule: <expression>: <custom message>"
	// 2. "ResourceName.group.domain ... field: Invalid value: <value>: <custom message>"
	// Multiple errors may be separated by newlines, semicolons, or "* field:" bullets

	var details []string
	foundErrors := 0

	// Pattern 1: field.path: ... failed rule: expression: message (may have multiple)
	celPattern1 := regexp.MustCompile(`(?i)([a-z0-9._\[\]]+).*?failed rule:\s*([^:]+):\s*(.+?)(?:;|\n|$)`)
	matches1 := celPattern1.FindAllStringSubmatch(msg, -1)
	if len(matches1) > 0 {
		for _, match := range matches1 {
			if len(match) >= 4 {
				fieldPath := match[1]
				rule := strings.TrimSpace(match[2])
				message := strings.TrimSpace(match[3])

				if foundErrors > 0 {
					details = append(details, "") // Blank line between errors
				}
				details = append(details, fmt.Sprintf("Field: %s", fieldPath))
				details = append(details, fmt.Sprintf("Rule: %s", rule))
				details = append(details, fmt.Sprintf("Message: %s", message))
				foundErrors++
			}
		}
		if foundErrors > 0 {
			if foundErrors > 1 {
				details = append([]string{fmt.Sprintf("Found %d CEL validation errors:", foundErrors)}, details...)
			}
			return strings.Join(details, "\n")
		}
	}

	// Pattern 2: field: Invalid value: "value": custom-message (may have multiple)
	// K8s formats multiple errors as: [error1, error2, ...]
	// First, check if we have a bracketed list and extract it
	msgToParse := msg
	if strings.Contains(msg, "[") && strings.Contains(msg, "]") {
		// Extract content between brackets
		start := strings.Index(msg, "[")
		end := strings.LastIndex(msg, "]")
		if start >= 0 && end > start {
			msgToParse = msg[start+1 : end]
		}
	}

	// Now parse individual errors (separated by ", " in the bracketed list)
	celPattern2 := regexp.MustCompile(`([a-z0-9._\[\]]+):\s*Invalid value:\s*"[^"]*":\s*([^,\n]+)`)
	matches2 := celPattern2.FindAllStringSubmatch(msgToParse, -1)
	if len(matches2) > 0 {
		for _, match := range matches2 {
			if len(match) >= 3 {
				fieldPath := match[1]
				message := strings.TrimSpace(match[2])

				if foundErrors > 0 {
					details = append(details, "") // Blank line between errors
				}
				details = append(details, fmt.Sprintf("Field: %s", fieldPath))
				details = append(details, fmt.Sprintf("Validation message: %s", message))
				foundErrors++
			}
		}
		if foundErrors > 0 {
			details = append(details, "")
			if foundErrors > 1 {
				details = append(details, "These errors come from CEL validation rules defined in the CustomResourceDefinition.")
				details = append([]string{fmt.Sprintf("Found %d CEL validation errors:", foundErrors)}, details...)
			} else {
				details = append(details, "This error comes from a CEL validation rule defined in the CustomResourceDefinition.")
			}
			return strings.Join(details, "\n")
		}
	}

	// Fallback: extract any useful information
	return fmt.Sprintf("CEL validation rule failed.\n\nFull error: %s", msg)
}

// IsConversionError detects conversion/schema validation errors (Issue #2)
// These errors occur when K8s cannot convert the object to the proper version
// Often caused by invalid quantity formats, type mismatches, etc.
func IsConversionError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())

	// Conversion error patterns
	if strings.Contains(errMsg, "failed to convert") {
		return true
	}

	// Schema validation patterns
	if strings.Contains(errMsg, "quantities must match") {
		return true
	}

	if strings.Contains(errMsg, "unable to convert unstructured object") {
		return true
	}

	return false
}

// ExtractConversionErrorDetails extracts field-specific details from conversion errors
func ExtractConversionErrorDetails(err error) string {
	errMsg := err.Error()

	// Try to extract the validation message (e.g., "quantities must match the regular expression...")
	if idx := strings.Index(errMsg, "quantities must match"); idx != -1 {
		validationMsg := errMsg[idx:]
		// Clean up the message
		validationMsg = strings.TrimSpace(validationMsg)

		return fmt.Sprintf("Invalid quantity format.\n\n%s\n\nValid examples: \"100m\", \"1\", \"500m\", \"2.5\", \"1Gi\", \"512Mi\"", validationMsg)
	}

	// Try to extract other conversion patterns
	if strings.Contains(errMsg, "failed to convert") {
		// Try to find what field it's about
		return fmt.Sprintf("Type conversion error.\n\nDetails: %s\n\nEnsure field values match the expected types (string, number, boolean, etc.)", errMsg)
	}

	// Fallback
	return fmt.Sprintf("Schema validation error.\n\nDetails: %s", errMsg)
}

// IsInvalidWithFieldDetails checks if an IsInvalid error contains field-specific validation details (Issue #3)
// These are Status 422 errors that show specific field paths and validation messages
func IsInvalidWithFieldDetails(err error) bool {
	if statusErr, ok := err.(*errors.StatusError); ok {
		msg := statusErr.ErrStatus.Message

		// Look for field-specific validation patterns
		// Examples: "field: Unsupported value", "field: Required value", "field: Invalid value"
		msgLower := strings.ToLower(msg)

		if strings.Contains(msgLower, "unsupported value:") ||
			strings.Contains(msgLower, "required value") ||
			strings.Contains(msgLower, "must be") {
			// Check if there's a field path (contains a dot or bracket indicating nested fields)
			if strings.Contains(msg, ".") || strings.Contains(msg, "[") {
				return true
			}
		}
	}

	return false
}

// ExtractInvalidFieldDetails extracts field-specific validation details from IsInvalid errors
func ExtractInvalidFieldDetails(err error) string {
	if statusErr, ok := err.(*errors.StatusError); ok {
		msg := statusErr.ErrStatus.Message

		// Try to parse field validation details
		// Example: "Deployment.apps \"name\" is invalid: spec.template.spec.containers[0].imagePullPolicy: Unsupported value: \"Invalid\": supported values: \"Always\", \"IfNotPresent\", \"Never\""

		// Find the field path and error details after "is invalid:"
		if idx := strings.Index(msg, "is invalid:"); idx != -1 {
			details := msg[idx+len("is invalid:"):]
			details = strings.TrimSpace(details)

			// Try to extract field path and validation message
			// Format: "field.path: Error type: details"
			parts := strings.SplitN(details, ":", 2)
			if len(parts) >= 2 {
				fieldPath := strings.TrimSpace(parts[0])
				validationMsg := strings.TrimSpace(parts[1])

				return fmt.Sprintf("Field: %s\n%s", fieldPath, validationMsg)
			}

			return details
		}

		// Fallback: return the full message
		return msg
	}

	// Fallback for non-StatusError
	return err.Error()
}
