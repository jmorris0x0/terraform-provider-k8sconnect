package helm_release_test

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

func TestAccHelmReleaseResource_Values(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-values-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-values-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	// Create namespace before running test
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccHelmReleaseConfigValues(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "namespace", namespace),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

func testAccHelmReleaseConfigValues(releaseName, namespace string) string {
	chartPath := "../../../../test/testdata/charts/simple-test"
	return fmt.Sprintf(`
variable "kubeconfig" {
  type = string
}
variable "release_name" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_helm_release" "test" {
  name       = var.release_name
  namespace  = var.namespace
  chart      = "%s"

  values = <<-YAML
    replicaCount: 3
  YAML

  set = [
    {
      name  = "replicaCount"
      value = "2"
    }
  ]

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`, chartPath)
}
