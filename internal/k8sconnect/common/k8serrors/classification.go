// internal/k8sconnect/common/k8serrors/classification.go
package k8serrors

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"k8s.io/apimachinery/pkg/api/errors"
)

// ClassifyError categorizes Kubernetes API errors for better user experience
// Returns (severity, title, detail) suitable for Terraform diagnostics
func ClassifyError(err error, operation, resourceDesc string) (severity, title, detail string) {
	switch {
	case errors.IsNotFound(err):
		return "warning", fmt.Sprintf("%s: Resource Not Found", operation),
			fmt.Sprintf("The %s was not found in the cluster. It may have been deleted outside of Terraform.", resourceDesc)

	case errors.IsForbidden(err):
		return "error", fmt.Sprintf("%s: Insufficient Permissions", operation),
			fmt.Sprintf("RBAC permissions insufficient to %s %s. Check that your credentials have the required permissions for this operation. Details: %v",
				operation, resourceDesc, err)

	case errors.IsConflict(err):
		conflictDetails := ExtractConflictDetails(err)
		return "error", fmt.Sprintf("%s: Field Manager Conflict", operation),
			fmt.Sprintf("Server-side apply conflict detected for %s.\n"+
				"Another controller is managing one or more fields in this resource.\n\n"+
				"Conflicting fields:\n%s\n\n"+
				"To resolve this conflict do one of the following:\n"+
				"1. Add conflicting field paths to 'ignore_fields' to release ownership to the other controller\n"+
				"2. Remove the conflicting fields from your Terraform configuration\n"+
				"3. Ensure only one controller manages these fields\n\n"+
				"Details: %v",
				resourceDesc, conflictDetails, err)

	case errors.IsTimeout(err) || errors.IsServerTimeout(err):
		return "error", fmt.Sprintf("%s: Kubernetes API Timeout", operation),
			fmt.Sprintf("Timeout while performing %s on %s. The cluster may be under heavy load or experiencing connectivity issues. Details: %v",
				operation, resourceDesc, err)

	case errors.IsUnauthorized(err):
		return "error", fmt.Sprintf("%s: Authentication Failed", operation),
			fmt.Sprintf("Authentication failed for %s %s. Check your credentials and ensure they are valid. Details: %v",
				operation, resourceDesc, err)

	case errors.IsInvalid(err):
		// Check if this is specifically a CEL validation error
		if IsCELValidationError(err) {
			celDetails := ExtractCELValidationDetails(err)
			return "error", fmt.Sprintf("%s: CEL Validation Failed", operation),
				fmt.Sprintf("CEL validation rule failed for %s.\n\n"+
					"%s\n\n"+
					"CEL (Common Expression Language) validation is defined in the CRD schema.\n"+
					"Fix the field value to satisfy the validation rule or adjust the CRD validation rules.\n\n"+
					"Details: %v",
					resourceDesc, celDetails, err)
		}

		// Check if this is specifically an immutable field error
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

		// Generic invalid resource error (for non-immutable and non-CEL field errors)
		return "error", fmt.Sprintf("%s: Invalid Resource", operation),
			fmt.Sprintf("The %s contains invalid fields or values. Review the YAML specification and ensure all required fields are present and correctly formatted. Details: %v",
				resourceDesc, err)

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
		return "error", fmt.Sprintf("%s: Kubernetes API Error", operation),
			fmt.Sprintf("An unexpected error occurred while performing %s on %s. Details: %v",
				operation, resourceDesc, err)
	}
}

// AddClassifiedError classifies a K8s error and adds it to diagnostics
// This is a convenience function that combines ClassifyError with adding to diagnostics
func AddClassifiedError(diags *diag.Diagnostics, err error, operation, resourceDesc string) {
	severity, title, detail := ClassifyError(err, operation, resourceDesc)
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
				strings.Contains(msg, "may not be modified")
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

// ExtractConflictDetails parses conflict error details
func ExtractConflictDetails(err error) string {
	// The Kubernetes error message typically contains field paths and managers
	// Example: "conflict with \"kubectl\" with subresource \"scale\" using apps/v1: .spec.replicas"
	errStr := err.Error()

	// Parse out the conflicts - this is a simplified version
	var details []string

	// Look for patterns like: conflict with "manager" ... : .field.path
	conflictPattern := regexp.MustCompile(`conflict with "([^"]+)".*?: ([\.\w\[\]]+)`)
	matches := conflictPattern.FindAllStringSubmatch(errStr, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			manager := match[1]
			fieldPath := match[2]
			details = append(details, fmt.Sprintf("- %s: managed by \"%s\"", fieldPath, manager))
		}
	}

	if len(details) == 0 {
		// Fallback if we can't parse - just show we detected conflicts
		return "- Multiple field ownership conflicts detected"
	}

	return strings.Join(details, "\n")
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
