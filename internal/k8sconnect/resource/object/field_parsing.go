package object

import (
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/fieldmanagement"
)

// Type alias for compatibility
type MergeKeyMatcher = fieldmanagement.MergeKeyMatcher

// NewMergeKeyMatcher creates a new MergeKeyMatcher
func NewMergeKeyMatcher() *MergeKeyMatcher {
	return fieldmanagement.NewMergeKeyMatcher()
}
