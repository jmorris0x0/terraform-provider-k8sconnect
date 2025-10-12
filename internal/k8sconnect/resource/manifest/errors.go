// internal/k8sconnect/resource/manifest/errors.go
package manifest

import (
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8serrors"
)

// classifyK8sError is a wrapper around the common error classification
func (r *manifestResource) classifyK8sError(err error, operation, resourceDesc string) (severity, title, detail string) {
	return k8serrors.ClassifyError(err, operation, resourceDesc)
}

// isDependencyNotReadyError is a wrapper around the common function
func (r *manifestResource) isDependencyNotReadyError(err error) bool {
	return k8serrors.IsDependencyNotReadyError(err)
}

// isCRDNotFoundError is a wrapper around the common function
func (r *manifestResource) isCRDNotFoundError(err error) bool {
	return k8serrors.IsCRDNotFoundError(err)
}

// isNamespaceNotFoundError is a wrapper around the common function
func (r *manifestResource) isNamespaceNotFoundError(err error) bool {
	return k8serrors.IsNamespaceNotFoundError(err)
}

// isImmutableFieldError is a wrapper around the common function
func (r *manifestResource) isImmutableFieldError(err error) bool {
	return k8serrors.IsImmutableFieldError(err)
}

// extractImmutableFields is a wrapper around the common function
func (r *manifestResource) extractImmutableFields(err error) []string {
	return k8serrors.ExtractImmutableFields(err)
}
