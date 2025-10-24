package object

import (
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	OwnershipAnnotation = "k8sconnect.terraform.io/terraform-id"
	CreatedAtAnnotation = "k8sconnect.terraform.io/created-at"
)

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
