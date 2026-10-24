package wait_test

import (
	"context"
	"fmt"
	"os"
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

// TestAccWaitResource_ExpiredToken_PlanSucceeds is the ADR-023 reproduction for
// k8sconnect_wait.
//
// The wait's Read discovers the GVR before it calls Client.Get. Both use the
// token stored in state, so with an expired token the discovery call is the one
// that fails. Before the fix, that failure was a hard "Failed to Discover GVR"
// error, so every plan after the token expired failed, even though the object
// resource already degrades the same failure to a warning.
//
// Flow:
// 1. Create a namespace and a wait on it with token1 (stored in state)
// 2. Delete SA1 (invalidates token1)
// 3. Plan with token2 from SA2
// 4. BEFORE: Read fails GVR discovery with 401 → plan fails
// 5. AFTER: Read warns and keeps prior state, plan succeeds
func TestAccWaitResource_ExpiredToken_PlanSucceeds(t *testing.T) {
	t.Parallel()

	host := os.Getenv("TF_ACC_K8S_HOST")
	ca := os.Getenv("TF_ACC_K8S_CA")
	raw := os.Getenv("TF_ACC_KUBECONFIG")

	if host == "" || ca == "" || raw == "" {
		t.Skip("TF_ACC_K8S_HOST, TF_ACC_K8S_CA, and TF_ACC_KUBECONFIG must be set")
	}

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ctx := context.Background()

	ns := fmt.Sprintf("wait-expired-tok-ns-%d", time.Now().UnixNano()%1000000)
	saName1 := fmt.Sprintf("wait-expired-tok-sa1-%d", time.Now().UnixNano()%1000000)
	saName2 := fmt.Sprintf("wait-expired-tok-sa2-%d", time.Now().UnixNano()%1000000)

	token1 := testhelpers.CreateServiceAccountWithToken(t, k8sClient, ctx, saName1)
	token2 := testhelpers.CreateServiceAccountWithToken(t, k8sClient, ctx, saName2)

	variables := func(token string) config.Variables {
		return config.Variables{
			"host":      config.StringVariable(host),
			"ca":        config.StringVariable(ca),
			"token":     config.StringVariable(token),
			"namespace": config.StringVariable(ns),
		}
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace and wait with token1, which gets stored in state
			{
				Config:          testAccWaitExpiredTokenConfig(ns),
				ConfigVariables: variables(token1),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_wait.expired_token_test", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Invalidate token1, then plan with token2. Read still uses token1
			// from state; its GVR discovery must degrade to a warning.
			{
				PreConfig: func() {
					testhelpers.DeleteServiceAccount(t, k8sClient, ctx, saName1)
					// Make sure the API server rejects token1 before planning, otherwise
					// the auth-error path is not exercised
					testhelpers.WaitForTokenRejection(t, host, ca, token1)
				},
				Config:             testAccWaitExpiredTokenConfig(ns),
				ConfigVariables:    variables(token2),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // Token changed (token1→token2), plan shows cluster update
			},
			// Step 3: Apply with token2 so state has valid credentials for destroy
			{
				Config:          testAccWaitExpiredTokenConfig(ns),
				ConfigVariables: variables(token2),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_wait.expired_token_test", "id"),
				),
			},
		},
		CheckDestroy: func(s *terraform.State) error {
			testhelpers.DeleteServiceAccount(t, k8sClient, ctx, saName1)
			testhelpers.DeleteServiceAccount(t, k8sClient, ctx, saName2)
			return testhelpers.CheckNamespaceDestroy(k8sClient, ns)(s)
		},
	})
}

func testAccWaitExpiredTokenConfig(namespace string) string {
	return fmt.Sprintf(`
variable "host" { type = string }
variable "ca" { type = string }
variable "token" { type = string }
variable "namespace" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "expired_token_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    token                  = var.token
  }
}

resource "k8sconnect_wait" "expired_token_test" {
  object_ref = k8sconnect_object.expired_token_namespace.object_ref

  wait_for = {
    field_value = {
      "status.phase" = "Active"
    }
    timeout = "1m"
  }

  cluster = {
    host                   = var.host
    cluster_ca_certificate = var.ca
    token                  = var.token
  }
}
`, namespace)
}
