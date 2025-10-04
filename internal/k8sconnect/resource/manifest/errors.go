// internal/k8sconnect/resource/manifest/errors.go
package manifest

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
)

// classifyK8sError categorizes Kubernetes API errors for better user experience
func (r *manifestResource) classifyK8sError(err error, operation, resourceDesc string) (severity, title, detail string) {
	switch {
	case errors.IsNotFound(err):
		return "warning", fmt.Sprintf("%s: Resource Not Found", operation),
			fmt.Sprintf("The %s was not found in the cluster. It may have been deleted outside of Terraform.", resourceDesc)

	case errors.IsForbidden(err):
		return "error", fmt.Sprintf("%s: Insufficient Permissions", operation),
			fmt.Sprintf("RBAC permissions insufficient to %s %s. Check that your credentials have the required permissions for this operation. Details: %v",
				operation, resourceDesc, err)

	case errors.IsConflict(err):
		return "error", fmt.Sprintf("%s: Field Manager Conflict", operation),
			fmt.Sprintf("Server-side apply conflict detected for %s.\n"+
				"Another controller is managing one or more fields in this resource.\n\n"+
				"To resolve this conflict do one of the following:\n"+
				"1. Add conflicting field paths to 'ignore_fields' to release ownership to the other controller\n"+
				"2. Remove the conflicting fields from your Terraform configuration\n"+
				"3. Set 'force_conflicts = true' to override (may cause persistent conflicts)\n"+
				"4. Ensure only one controller manages these fields\n\n"+
				"Details: %v",
				resourceDesc, err)

	case errors.IsTimeout(err) || errors.IsServerTimeout(err):
		return "error", fmt.Sprintf("%s: Kubernetes API Timeout", operation),
			fmt.Sprintf("Timeout while performing %s on %s. The cluster may be under heavy load or experiencing connectivity issues. Details: %v",
				operation, resourceDesc, err)

	case errors.IsUnauthorized(err):
		return "error", fmt.Sprintf("%s: Authentication Failed", operation),
			fmt.Sprintf("Authentication failed for %s %s. Check your credentials and ensure they are valid. Details: %v",
				operation, resourceDesc, err)

	case errors.IsInvalid(err):
		// Check if this is specifically an immutable field error
		if r.isImmutableFieldError(err) {
			immutableFields := r.extractImmutableFields(err)
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

		// Generic invalid resource error (for non-immutable field errors)
		return "error", fmt.Sprintf("%s: Invalid Resource", operation),
			fmt.Sprintf("The %s contains invalid fields or values. Review the YAML specification and ensure all required fields are present and correctly formatted. Details: %v",
				resourceDesc, err)

	case errors.IsAlreadyExists(err):
		return "error", fmt.Sprintf("%s: Resource Already Exists", operation),
			fmt.Sprintf("The %s already exists in the cluster and cannot be created. Use import to manage existing resources with Terraform. Details: %v",
				resourceDesc, err)

	default:
		return "error", fmt.Sprintf("%s: Kubernetes API Error", operation),
			fmt.Sprintf("An unexpected error occurred while performing %s on %s. Details: %v",
				operation, resourceDesc, err)
	}
}

func (r *manifestResource) isImmutableFieldError(err error) bool {
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

func (r *manifestResource) extractImmutableFields(err error) []string {
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

func extractConflictDetails(err error) string {
	// The Kubernetes error message typically contains field paths and managers
	// Example: "conflict with \"kubectl\" with subresource \"scale\" using apps/v1: .spec.replicas"
	errStr := err.Error()

	// Parse out the conflicts - this is a simplified version
	// You might need to adjust based on actual error format
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
