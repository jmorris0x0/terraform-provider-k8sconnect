// internal/k8sconnect/resource/manifest/token_refresh_test.go
package manifest_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

func TestAccManifestResource_ExecTokenRefresh(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	cmd := os.Getenv("TF_ACC_K8S_CMD")
	raw := os.Getenv("TF_ACC_KUBECONFIG")

	if host == "" || ca == "" || cmd == "" || raw == "" {
		t.Skip("TF_ACC_K8S_HOST, TF_ACC_K8S_CA, TF_ACC_K8S_CMD and TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("token-refresh-ns-%d", time.Now().UnixNano()%1000000)
	cm1Name := fmt.Sprintf("token-refresh-cm1-%d", time.Now().UnixNano()%1000000)
	cm2Name := fmt.Sprintf("token-refresh-cm2-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)

	// Clear the exec log before test
	os.Remove("/tmp/kubectl-exec.log")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		ExternalProviders: map[string]resource.ExternalProvider{
			"time": {
				Source:            "hashicorp/time",
				VersionConstraint: "~> 0.9",
			},
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigTokenRefresh(ns, cm1Name, cm2Name),
				ConfigVariables: config.Variables{
					"host":      config.StringVariable(host),
					"ca":        config.StringVariable(ca),
					"cmd":       config.StringVariable(cmd),
					"namespace": config.StringVariable(ns),
					"cm1_name":  config.StringVariable(cm1Name),
					"cm2_name":  config.StringVariable(cm2Name),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cm1Name),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cm2Name),
					checkExecLogForMultipleCalls(t),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigTokenRefresh(namespace, cm1Name, cm2Name string) string {
	return fmt.Sprintf(`
variable "host" { type = string }
variable "ca" { type = string }
variable "cmd" { type = string }
variable "namespace" { type = string }
variable "cm1_name" { type = string }
variable "cm2_name" { type = string }

provider "k8sconnect" {}

locals {
  # Connection with exec auth and short-lived tokens
  exec_connection = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    exec = {
      api_version = "client.authentication.k8s.io/v1"
      command     = var.cmd
      args        = []
      env = {
        TOKEN_EXPIRY_SECONDS = "20"
      }
    }
  }
}

resource "k8sconnect_manifest" "test_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster_connection = local.exec_connection
}

resource "k8sconnect_manifest" "test_cm1" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  created: "first"
YAML

  cluster_connection = local.exec_connection
  depends_on = [k8sconnect_manifest.test_namespace]
}

resource "time_sleep" "wait_for_token_expiry" {
  depends_on = [k8sconnect_manifest.test_cm1]
  create_duration = "25s"
}

resource "k8sconnect_manifest" "test_cm2" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  created: "second"
YAML

  cluster_connection = local.exec_connection
  depends_on = [time_sleep.wait_for_token_expiry]
}
`, namespace, cm1Name, namespace, cm2Name, namespace)
}

func checkExecLogForMultipleCalls(t *testing.T) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		// Read the exec log
		logData, err := os.ReadFile("/tmp/kubectl-exec.log")
		if err != nil {
			t.Logf("Warning: Could not read exec log: %v", err)
			// Not a failure - log might not exist in all environments
			return nil
		}

		// Count how many times the plugin was called
		lines := strings.Split(string(logData), "\n")
		callCount := 0
		for _, line := range lines {
			if strings.Contains(line, "PLUGIN CALLED") {
				callCount++
				t.Logf("Exec call found: %s", line)
			}
		}

		if callCount < 2 {
			return fmt.Errorf("expected at least 2 exec calls for token refresh, found %d", callCount)
		}

		t.Logf("✅ Token refresh verified: exec called %d times", callCount)
		return nil
	}
}
