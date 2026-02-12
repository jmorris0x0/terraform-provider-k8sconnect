package helm_release

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// TestManifestAttributeIsSensitive verifies the manifest attribute is marked
// Sensitive so that set_sensitive values rendered into the Helm template
// are not exposed in terraform show or terraform state show output.
func TestManifestAttributeIsSensitive(t *testing.T) {
	r := &helmReleaseResource{}
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)

	attr, ok := resp.Schema.Attributes["manifest"]
	if !ok {
		t.Fatal("schema missing 'manifest' attribute")
	}

	strAttr, ok := attr.(schema.StringAttribute)
	if !ok {
		t.Fatal("manifest attribute is not a StringAttribute")
	}

	if !strAttr.Sensitive {
		t.Error("manifest attribute must be Sensitive to prevent set_sensitive values from leaking in terraform show")
	}
}
