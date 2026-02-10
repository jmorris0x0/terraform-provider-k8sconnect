package object

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
)

// classifyK8sError is a wrapper around the common error classification
func (r *objectResource) classifyK8sError(err error, operation, resourceDesc, apiVersion string) (severity, title, detail string) {
	return k8serrors.ClassifyError(err, operation, resourceDesc, apiVersion)
}

// isDependencyNotReadyError is a wrapper around the common function
func (r *objectResource) isDependencyNotReadyError(err error) bool {
	return k8serrors.IsDependencyNotReadyError(err)
}

// isCRDNotFoundError is a wrapper around the common function
func (r *objectResource) isCRDNotFoundError(err error) bool {
	return k8serrors.IsCRDNotFoundError(err)
}

// isNamespaceNotFoundError is a wrapper around the common function
func (r *objectResource) isNamespaceNotFoundError(err error) bool {
	return k8serrors.IsNamespaceNotFoundError(err)
}

// isImmutableFieldError is a wrapper around the common function
func (r *objectResource) isImmutableFieldError(err error) bool {
	return k8serrors.IsImmutableFieldError(err)
}

// extractImmutableFields is a wrapper around the common function
func (r *objectResource) extractImmutableFields(err error) []string {
	return k8serrors.ExtractImmutableFields(err)
}

// isFieldValidationError is a wrapper around the common function
func (r *objectResource) isFieldValidationError(err error) bool {
	return k8serrors.IsFieldValidationError(err)
}

// addClassifiedError is a wrapper around the common function
func (r *objectResource) addClassifiedError(diags *diag.Diagnostics, err error, operation, resourceDesc, apiVersion string) {
	k8serrors.AddClassifiedError(diags, err, operation, resourceDesc, apiVersion)
}

// classifyReadGetError classifies errors from Client.Get during Read.
// ADR-023: Degrades auth errors (401/403) to warnings during Read,
// since the token in state may have expired between runs. Returning prior state
// with a warning is better than hard-failing terraform plan.
func (r *objectResource) classifyReadGetError(err error, resourceDesc, apiVersion string) (severity, title, detail string) {
	if k8serrors.IsAuthError(err) {
		return "warning", "Read: Using Prior State — Authentication Failed",
			fmt.Sprintf("Could not refresh %s from cluster: authentication failed. "+
				"Using prior state. This typically means the stored token has expired "+
				"between Terraform runs. If this persists, check your cluster authentication "+
				"configuration. Details: %v", resourceDesc, err)
	}
	return r.classifyK8sError(err, "Read", resourceDesc, apiVersion)
}
