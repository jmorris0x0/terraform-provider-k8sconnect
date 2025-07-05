package manifest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Ownership annotation constants
const (
	OwnershipAnnotation = "k8sinline.terraform.io/id" // Keep existing annotation key
	CreatedAtAnnotation = "k8sinline.terraform.io/created-at"
	DefaultFieldManager = "k8sinline"
)

// generateID creates a 12-character random hex ID (like Docker)
func (r *manifestResource) generateID() string {
	b := make([]byte, 6) // 6 bytes = 12 hex chars
	rand.Read(b)
	return hex.EncodeToString(b)
}

// setOwnershipAnnotation marks a Kubernetes resource as managed by this Terraform resource
func (r *manifestResource) setOwnershipAnnotation(obj *unstructured.Unstructured, terraformID string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[OwnershipAnnotation] = terraformID
	annotations[CreatedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)
	obj.SetAnnotations(annotations)
}

// validateOwnership checks if we have permission to manage this Kubernetes resource
func (r *manifestResource) validateOwnership(liveObj *unstructured.Unstructured, expectedID string) error {
	annotations := liveObj.GetAnnotations()
	if annotations == nil {
		return fmt.Errorf("resource exists but has no ownership annotations - use 'terraform import' to adopt")
	}

	existingID := annotations[OwnershipAnnotation]
	if existingID == "" {
		return fmt.Errorf("resource exists but not managed by k8sinline - use 'terraform import' to adopt")
	}

	if existingID != expectedID {
		return fmt.Errorf("resource managed by different k8sinline resource (Terraform ID: %s)", existingID)
	}

	return nil
}

// getOwnershipID extracts the Terraform resource ID from Kubernetes annotations
func (r *manifestResource) getOwnershipID(obj *unstructured.Unstructured) string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return ""
	}
	return annotations[OwnershipAnnotation]
}
