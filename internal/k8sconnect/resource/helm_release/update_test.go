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

func TestAccHelmReleaseResource_Update(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-update-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-update-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	// Create namespace before running test
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create with 1 replica
			{
				Config: testAccHelmReleaseConfigVersion(releaseName, namespace, "", "1"),
				ConfigVariables: config.Variables{
					"kubeconfig":    config.StringVariable(raw),
					"release_name":  config.StringVariable(releaseName),
					"namespace":     config.StringVariable(namespace),
					"chart_version": config.StringVariable(""),
					"replicas":      config.StringVariable("1"),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "revision", "1"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
			// Step 2: Update to 2 replicas
			{
				Config: testAccHelmReleaseConfigVersion(releaseName, namespace, "", "2"),
				ConfigVariables: config.Variables{
					"kubeconfig":    config.StringVariable(raw),
					"release_name":  config.StringVariable(releaseName),
					"namespace":     config.StringVariable(namespace),
					"chart_version": config.StringVariable(""),
					"replicas":      config.StringVariable("2"),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "revision", "2"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

func testAccHelmReleaseConfigVersion(releaseName, namespace, version, replicas string) string {
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
variable "chart_version" {
  type = string
}
variable "replicas" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_helm_release" "test" {
  name       = var.release_name
  namespace  = var.namespace
  chart      = "%s"

  set = [
    {
      name  = "replicaCount"
      value = var.replicas
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
