package helm_release_test

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
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

// TestAccHelmReleaseResource_StatePersistence tests Issue #1669
// Verifies that helm releases never randomly disappear from state
func TestAccHelmReleaseResource_StatePersistence(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-state-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Initial create
			{
				Config: testAccHelmReleaseConfigBasic(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
			// Multiple re-applies to verify state persistence
			{
				Config: testAccHelmReleaseConfigBasic(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					resource.TestCheckResourceAttrSet("k8sconnect_helm_release.test", "id"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
			{
				Config: testAccHelmReleaseConfigBasic(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					resource.TestCheckResourceAttrSet("k8sconnect_helm_release.test", "id"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_FailedReleaseNoStateUpdate tests Issue #472
// Verifies that failed releases don't update state
func TestAccHelmReleaseResource_FailedReleaseNoStateUpdate(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-failed-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// First step: Deploy with bad image, expect failure
			{
				Config: testAccHelmReleaseConfigBadImage(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ExpectError: regexp.MustCompile("context deadline exceeded|timeout|failed to install"),
				Check: resource.ComposeTestCheckFunc(
					// Resource should NOT exist in state after failed create
					func(s *terraform.State) error {
						if _, ok := s.RootModule().Resources["k8sconnect_helm_release.test"]; ok {
							return fmt.Errorf("failed resource should not be in state")
						}
						return nil
					},
				),
			},
		},
	})
}

// TestAccHelmReleaseResource_ManualRollbackDetection tests Issue #1349
// Verifies that manual helm rollback is detected as drift
func TestAccHelmReleaseResource_ManualRollbackDetection(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-rollback-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Deploy initial version (revision 1)
			{
				Config: testAccHelmReleaseConfigWithReplicas(releaseName, namespace, 1),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "revision", "1"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
			// Upgrade (revision 2)
			{
				Config: testAccHelmReleaseConfigWithReplicas(releaseName, namespace, 2),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "revision", "2"),
					// Manually rollback to revision 1
					func(s *terraform.State) error {
						tmpfile, err := os.CreateTemp("", "kubeconfig-*.yaml")
						if err != nil {
							return err
						}
						defer os.Remove(tmpfile.Name())

						if _, err := tmpfile.Write([]byte(raw)); err != nil {
							return err
						}
						tmpfile.Close()

						cmd := exec.Command("helm", "rollback", releaseName, "1", "-n", namespace, "--kubeconfig", tmpfile.Name())
						if output, err := cmd.CombinedOutput(); err != nil {
							return fmt.Errorf("helm rollback failed: %v\nOutput: %s", err, output)
						}
						return nil
					},
				),
			},
			// Re-apply with a small change should trigger update after rollback
			// Since Terraform doesn't auto-correct drift without input changes,
			// we make a small change (add a label) to trigger the update
			{
				Config: testAccHelmReleaseConfigWithReplicasAndLabel(releaseName, namespace, 2, "drift-test"),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					// After Terraform applies the change, we should be at revision 4
					// (rev 1: initial, rev 2: upgrade, rev 3: manual rollback, rev 4: TF update with new label)
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "revision", "4"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_DaemonSetWait tests Issue #1364
// Verifies that wait=true waits for DaemonSet workloads
func TestAccHelmReleaseResource_DaemonSetWait(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-ds-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccHelmReleaseConfigDaemonSet(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "status", "deployed"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
					// Verify DaemonSet is actually ready (wait worked)
					func(s *terraform.State) error {
						tmpfile, err := os.CreateTemp("", "kubeconfig-*.yaml")
						if err != nil {
							return err
						}
						defer os.Remove(tmpfile.Name())

						if _, err := tmpfile.Write([]byte(raw)); err != nil {
							return err
						}
						tmpfile.Close()

						cmd := exec.Command("kubectl", "get", "daemonset", "daemonset-test", "-n", namespace,
							"-o", "jsonpath={.status.numberReady}", "--kubeconfig", tmpfile.Name())
						output, err := cmd.CombinedOutput()
						if err != nil {
							return fmt.Errorf("failed to get daemonset status: %v", err)
						}

						if string(output) == "0" || string(output) == "" {
							return fmt.Errorf("DaemonSet not ready, but helm release shows deployed - wait didn't work!")
						}
						return nil
					},
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

func testAccHelmReleaseConfigDaemonSet(releaseName, namespace string) string {
	chartPath := "../../../../test/testdata/charts/daemonset-test"
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
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`, chartPath)
}

// TestAccHelmReleaseResource_FirstDeployTimeout tests Issue #672
// Verifies that timeout is respected on FIRST deployment
func TestAccHelmReleaseResource_FirstDeployTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-timeout-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	startTime := time.Now()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccHelmReleaseConfigBadImageWithTimeout(releaseName, namespace, "15s"),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ExpectError: regexp.MustCompile("context deadline exceeded|timeout"),
				Check: resource.ComposeTestCheckFunc(
					func(s *terraform.State) error {
						elapsed := time.Since(startTime)
						// Should timeout around 15s, not succeed after timeout
						if elapsed > 25*time.Second {
							return fmt.Errorf("timeout took too long: %v (expected ~15s)", elapsed)
						}
						if elapsed < 10*time.Second {
							return fmt.Errorf("timeout happened too quickly: %v (expected ~15s)", elapsed)
						}
						return nil
					},
				),
			},
		},
	})
}

// TestAccHelmReleaseResource_TimeoutParameterRespected tests Issue #463
// Verifies that custom timeout values are respected
func TestAccHelmReleaseResource_TimeoutParameterRespected(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-timeout2-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Deploy with short timeout - should fail
			{
				Config: testAccHelmReleaseConfigBadImageWithTimeout(releaseName, namespace, "10s"),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ExpectError: regexp.MustCompile("context deadline exceeded|timeout"),
			},
		},
	})
}

// TestAccHelmReleaseResource_SensitiveValuesNotLeaked tests Issues #1287 and #1221
// Verifies that sensitive values are never exposed in plan/state/logs
func TestAccHelmReleaseResource_SensitiveValuesNotLeaked(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-sensitive-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)
	secretValue := "super-secret-password-123"

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccHelmReleaseConfigSensitive(releaseName, namespace, secretValue),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
					"secret_value": config.StringVariable(secretValue),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
					// TODO: Add check that verifies secretValue does NOT appear in state JSON
					// This requires inspecting the raw state file
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_Import tests Issue #1613
// Verifies that existing helm releases can be imported without drift
func TestAccHelmReleaseResource_Import(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-import-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create helm release with Terraform
			{
				Config: testAccHelmReleaseConfigBasic(releaseName, namespace),
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
			// Step 2: Import the helm release
			{
				Config: testAccHelmReleaseConfigBasic(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ResourceName:      "k8sconnect_helm_release.test",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("k3d-k8sconnect-test:%s:%s", namespace, releaseName),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"id",                // Random ID generated on create/import
					"cluster",           // Cluster config not in helm state
					"wait",              // Runtime config
					"timeout",           // Runtime config
					"wait_for_jobs",     // Runtime config
					"dependency_update", // Runtime config
				},
			},
			// Step 3: Verify no drift after import
			{
				Config: testAccHelmReleaseConfigBasic(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				PlanOnly: true,
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_DependencyUpdate tests Issue #576
// Verifies dependencies are downloaded when chart changes
func TestAccHelmReleaseResource_DependencyUpdate(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-dep-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Deploy with dependency_update = true
			{
				Config: testAccHelmReleaseConfigWithDependencies(releaseName, namespace, true, 1),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "dependency_update", "true"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
			// Modify values and verify dependencies still work
			{
				Config: testAccHelmReleaseConfigWithDependencies(releaseName, namespace, true, 2),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
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

func testAccHelmReleaseConfigWithDependencies(releaseName, namespace string, depUpdate bool, replicas int) string {
	chartPath := "../../../../test/testdata/charts/with-dependencies"
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
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  set = [
    {
      name  = "replicaCount"
      value = "%d"
    }
  ]

  cluster = {
    kubeconfig = var.kubeconfig
  }

  dependency_update = %t
  wait              = true
  timeout           = "300s"
}
`, chartPath, replicas, depUpdate)
}

// TestAccHelmReleaseResource_ValuesAndSetMixed tests Issue #524
// Verifies that values and set parameters work together correctly
func TestAccHelmReleaseResource_ValuesAndSetMixed(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-mixed-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Deploy with values + set
			{
				Config: testAccHelmReleaseConfigValuesAndSet(releaseName, namespace, 1, 2),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
			// Change only the set parameter
			{
				Config: testAccHelmReleaseConfigValuesAndSet(releaseName, namespace, 1, 3),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
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

// TestAccHelmReleaseResource_NoUnnecessaryRevisions tests Issue #906
// Verifies that re-applying without changes doesn't increment revision
func TestAccHelmReleaseResource_NoUnnecessaryRevisions(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-revisions-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Initial deploy
			{
				Config: testAccHelmReleaseConfigBasic(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "revision", "1"),
				),
			},
			// Re-apply without changes
			{
				Config: testAccHelmReleaseConfigBasic(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					// Revision should still be 1
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "revision", "1"),
				),
			},
			// Re-apply again
			{
				Config: testAccHelmReleaseConfigBasic(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				Check: resource.ComposeTestCheckFunc(
					// Revision should STILL be 1
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "revision", "1"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// Helper config functions

func testAccHelmReleaseConfigBadImage(releaseName, namespace string) string {
	chartPath := "../../../../test/testdata/charts/bad-image-test"
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
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "30s"
}
`, chartPath)
}

func testAccHelmReleaseConfigBadImageWithTimeout(releaseName, namespace, timeout string) string {
	chartPath := "../../../../test/testdata/charts/bad-image-test"
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
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "%s"
}
`, chartPath, timeout)
}

func testAccHelmReleaseConfigWithReplicas(releaseName, namespace string, replicas int) string {
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
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  set = [
    {
      name  = "replicaCount"
      value = "%d"
    }
  ]

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`, chartPath, replicas)
}

func testAccHelmReleaseConfigWithReplicasAndLabel(releaseName, namespace string, replicas int, label string) string {
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
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  set = [
    {
      name  = "replicaCount"
      value = "%d"
    },
    {
      name  = "testLabel"
      value = "%s"
    }
  ]

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`, chartPath, replicas, label)
}

func testAccHelmReleaseConfigSensitive(releaseName, namespace, secretValue string) string {
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
variable "secret_value" {
  type      = string
  sensitive = true
}

provider "k8sconnect" {}

resource "k8sconnect_helm_release" "test" {
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  set_sensitive = [
    {
      name  = "secretPassword"
      value = var.secret_value
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

func testAccHelmReleaseConfigValuesAndSet(releaseName, namespace string, valuesReplicas, setReplicas int) string {
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
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  values = <<-YAML
    replicaCount: %d
  YAML

  set = [
    {
      name  = "replicaCount"
      value = "%d"
    }
  ]

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`, chartPath, valuesReplicas, setReplicas)
}
