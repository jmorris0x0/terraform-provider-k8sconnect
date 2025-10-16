// internal/k8sconnect/resource/object/cluster_scoped_test.go
package object_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccObjectResource_ClusterScopedWithInvalidNamespace tests that cluster-scoped
// resources with invalid namespace fields in YAML don't fail projection calculation.
//
// Bug scenario:
// 1. User provides ClusterRoleBinding YAML with invalid `namespace: default` field
// 2. Kubernetes strips namespace during apply (ClusterRoleBindings are cluster-scoped)
// 3. When reading back for projection, we use rc.Object.GetNamespace() (returns "default")
// 4. OLD BUG: Client tries to access cluster-scoped resource WITH namespace → "not found"
// 5. FIX: Client always checks discovery to determine if resource is namespaced
func TestAccObjectResource_ClusterScopedWithInvalidNamespace(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	// Use unique names to avoid conflicts
	saName := fmt.Sprintf("acctest-sa-%d", time.Now().UnixNano()%1000000)
	crbName := fmt.Sprintf("acctest-crb-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigClusterScopedInvalidNamespace(saName, crbName),
				ConfigVariables: config.Variables{
					"raw":      config.StringVariable(raw),
					"sa_name":  config.StringVariable(saName),
					"crb_name": config.StringVariable(crbName),
				},
				Check: resource.ComposeTestCheckFunc(
					// Verify ServiceAccount was created
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_sa", "id"),
					testhelpers.CheckServiceAccountExists(k8sClient, "default", saName),

					// KEY ASSERTION: ClusterRoleBinding with invalid namespace field
					// should NOT fail with "Projection Calculation Failed"
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_crb", "id"),
					testhelpers.CheckClusterRoleBindingExists(k8sClient, crbName),

					// Verify projection was calculated successfully (not empty due to error)
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_crb", "managed_state_projection.%"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckServiceAccountDestroy(k8sClient, "default", saName),
			testhelpers.CheckClusterRoleBindingDestroy(k8sClient, crbName),
		),
	})
}

func testAccManifestConfigClusterScopedInvalidNamespace(saName, crbName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "sa_name" {
  type = string
}
variable "crb_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "test_sa" {
  yaml_body = <<YAML
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: default
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_crb" {
  yaml_body = <<YAML
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: %s
  namespace: default
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: view
subjects:
  - kind: ServiceAccount
    name: %s
    namespace: default
YAML

  cluster_connection = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.test_sa]
}
`, saName, crbName, saName)
}
