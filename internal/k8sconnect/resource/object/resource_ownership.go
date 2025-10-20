package object

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	OwnershipAnnotation = "k8sconnect.terraform.io/terraform-id"
	CreatedAtAnnotation = "k8sconnect.terraform.io/created-at"
)

// generateID creates a random 12-character hex ID for Terraform resource identification
func (r *objectResource) generateID() string {
	bytes := make([]byte, 6) // 6 bytes = 12 hex chars
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based ID if random fails
		return fmt.Sprintf("%x", time.Now().UnixNano())[:12]
	}
	return hex.EncodeToString(bytes)
}

// setOwnershipAnnotation marks a Kubernetes resource as managed by this Terraform resource
func (r *objectResource) setOwnershipAnnotation(obj *unstructured.Unstructured, terraformID string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[OwnershipAnnotation] = terraformID
	annotations[CreatedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)
	obj.SetAnnotations(annotations)
}

// getOwnershipID extracts the Terraform resource ID from Kubernetes annotations
func (r *objectResource) getOwnershipID(obj *unstructured.Unstructured) string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return ""
	}
	return annotations[OwnershipAnnotation]
}
