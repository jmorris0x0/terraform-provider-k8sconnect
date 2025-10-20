// internal/k8sconnect/resource/object/errors.go
package object

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
)

// classifyK8sError is a wrapper around the common error classification
func (r *objectResource) classifyK8sError(err error, operation, resourceDesc string) (severity, title, detail string) {
	return k8serrors.ClassifyError(err, operation, resourceDesc)
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
func (r *objectResource) addClassifiedError(diags *diag.Diagnostics, err error, operation, resourceDesc string) {
	k8serrors.AddClassifiedError(diags, err, operation, resourceDesc)
}
