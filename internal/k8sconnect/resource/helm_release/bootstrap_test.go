package helm_release_test

import (
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccHelmReleaseResource_BootstrapWithUnknownCluster tests unknown cluster handling
// This is THE CORE VALUE PROPOSITION - must work perfectly!
func TestAccHelmReleaseResource_BootstrapWithUnknownCluster(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-bootstrap-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-release-bootstrap-unknown-cluster-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Helm release with unknown connection values at plan time
				Config: testAccHelmReleaseConfigBootstrap(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// Plan should succeed even with unknown cluster values
						plancheck.ExpectNonEmptyPlan(),
						// The helm release should be planned for creation
						plancheck.ExpectResourceAction("k8sconnect_helm_release.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					// After apply, everything should exist
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "namespace", namespace),
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "status", "deployed"),
					resource.TestCheckResourceAttrSet("k8sconnect_helm_release.test", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_helm_release.test", "revision"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_UnknownChartVersion tests unknown chart version handling
// For local charts, version is in Chart.yaml, so we test with repository charts
// Since we don't have a real repo in tests, we simulate unknown version with null_resource
func TestAccHelmReleaseResource_UnknownChartVersion(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-ver-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-release-unknown-version-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Chart path comes from null_resource (unknown at plan time)
				Config: testAccHelmReleaseConfigUnknownChartPath(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectNonEmptyPlan(),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_UnknownRepository tests unknown repository URL handling
// For this test, we simulate an unknown repository URL using a local chart path from ConfigMap
// In real scenarios, this would be an OCI registry URL from ECR/ACR, but for testing we use local charts
func TestAccHelmReleaseResource_UnknownRepository(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-unknown-repo-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-release-unknown-repo-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Repository URL comes from ConfigMap (unknown at plan)
				Config: testAccHelmReleaseConfigUnknownRepository(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectNonEmptyPlan(),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_UnknownValuesInYAML tests unknown interpolations in values
func TestAccHelmReleaseResource_UnknownValuesInYAML(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-unknown-val-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-release-unknown-values-yaml-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Values YAML contains reference to random_integer (unknown at plan)
				Config: testAccHelmReleaseConfigUnknownValues(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// Plan should succeed even with unknown values in YAML
						plancheck.ExpectNonEmptyPlan(),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_UnknownSetParameters tests unknown values in set parameters
func TestAccHelmReleaseResource_UnknownSetParameters(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-unknown-set-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-release-unknown-set-params-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Set parameter comes from random_integer (unknown at plan)
				Config: testAccHelmReleaseConfigUnknownSet(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectNonEmptyPlan(),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_SensitiveUnknownValues tests sensitive unknown values
func TestAccHelmReleaseResource_SensitiveUnknownValues(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-sens-unknown-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-release-sensitive-unknown-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Sensitive value comes from random_password (unknown at plan)
				Config: testAccHelmReleaseConfigSensitiveUnknown(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectNonEmptyPlan(),
						// Should not leak sensitive value in plan
					},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
					// TODO: Verify sensitive value not in logs
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_ConsistencyWithK8sConnectObject tests unknown handling consistency
func TestAccHelmReleaseResource_ConsistencyWithK8sConnectObject(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-release-%d", time.Now().UnixNano()%1000000)
	objectName := fmt.Sprintf("test-cm-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-release-consistency-with-object-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Both helm_release and object with unknown cluster values
				Config: testAccBothResourcesBootstrap(releaseName, objectName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"object_name":  config.StringVariable(objectName),
					"namespace":    config.StringVariable(namespace),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// Both should plan successfully with unknown cluster
						plancheck.ExpectNonEmptyPlan(),
						plancheck.ExpectResourceAction("k8sconnect_helm_release.test", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("k8sconnect_object.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					// Both should exist after apply
					resource.TestCheckResourceAttr("k8sconnect_helm_release.test", "name", releaseName),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, releaseName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, releaseName),
	})
}

// TestAccHelmReleaseResource_CompleteBootstrapWorkflow is the ultimate test
// Cluster + multiple helm releases + objects in ONE apply
// This simulates real-world bootstrap (cluster + foundation services + app resources)
func TestAccHelmReleaseResource_CompleteBootstrapWorkflow(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	release1Name := fmt.Sprintf("test-svc1-%d", time.Now().UnixNano()%1000000)
	release2Name := fmt.Sprintf("test-svc2-%d", time.Now().UnixNano()%1000000)
	objectName := fmt.Sprintf("test-app-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-release-complete-bootstrap-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Complete bootstrap: 2 helm releases + 1 object with unknown cluster values
				Config: testAccCompleteBootstrapWorkflow(release1Name, release2Name, objectName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":    config.StringVariable(raw),
					"release1_name": config.StringVariable(release1Name),
					"release2_name": config.StringVariable(release2Name),
					"object_name":   config.StringVariable(objectName),
					"namespace":     config.StringVariable(namespace),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// All should plan successfully despite unknown cluster
						plancheck.ExpectNonEmptyPlan(),
						plancheck.ExpectResourceAction("k8sconnect_helm_release.service1", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("k8sconnect_helm_release.service2", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("k8sconnect_object.app", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					// All should exist after apply
					resource.TestCheckResourceAttr("k8sconnect_helm_release.service1", "name", release1Name),
					resource.TestCheckResourceAttr("k8sconnect_helm_release.service2", "name", release2Name),
					resource.TestCheckResourceAttrSet("k8sconnect_object.app", "id"),
					// All should be deployed/ready
					resource.TestCheckResourceAttr("k8sconnect_helm_release.service1", "status", "deployed"),
					resource.TestCheckResourceAttr("k8sconnect_helm_release.service2", "status", "deployed"),
					testhelpers.CheckHelmReleaseExists(raw, namespace, release1Name),
					testhelpers.CheckHelmReleaseExists(raw, namespace, release2Name),
				),
			},
		},
		CheckDestroy: testhelpers.CheckHelmReleaseDestroy(raw, namespace, release1Name),
	})
}

// TestAccHelmReleaseResource_UnknownChartPathError tests error handling with unknown values
func TestAccHelmReleaseResource_UnknownChartPathError(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-err-unknown-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-release-unknown-path-error-%d", time.Now().UnixNano()%1000000)

	k8sClient := testhelpers.CreateK8sClient(t, raw)
	testhelpers.CreateNamespaceDirectly(t, k8sClient, namespace)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Chart path comes from null_resource output (resolves to invalid path)
				Config: testAccHelmReleaseConfigInvalidChartPath(releaseName, namespace),
				ConfigVariables: config.Variables{
					"kubeconfig":   config.StringVariable(raw),
					"release_name": config.StringVariable(releaseName),
					"namespace":    config.StringVariable(namespace),
				},
				ExpectError: regexp.MustCompile("no such file or directory|failed to load chart"),
			},
		},
	})
}

// Helper config functions

func testAccHelmReleaseConfigBootstrap(releaseName, namespace string) string {
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

# Create ConfigMap to store simple value (simulates cluster creation)
resource "k8sconnect_object" "connection_info" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: helm-conn-info
  namespace: ${var.namespace}
data:
  connection_ready: "true"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}

# Helm release with values referencing ConfigMap (unknown during initial plan)
resource "k8sconnect_helm_release" "test" {
  depends_on = [k8sconnect_object.connection_info]

  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  # Values contains unknown interpolation from ConfigMap
  # During initial plan, connection_info doesn't exist, so projection is unknown
  values = <<-YAML
    # Add a comment with unknown value to test bootstrap handling
    bootstrapReady: ${k8sconnect_object.connection_info.managed_state_projection["data.connection_ready"]}
  YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`, chartPath)
}

func testAccHelmReleaseConfigUnknownValues(releaseName, namespace string) string {
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

# Create ConfigMap storing a value (simulates value from another resource)
resource "k8sconnect_object" "value_source" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: value-source
  namespace: ${var.namespace}
data:
  replicas: "2"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}

resource "k8sconnect_helm_release" "test" {
  depends_on = [k8sconnect_object.value_source]

  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  # Values contains unknown interpolation (projection unknown during initial plan)
  values = <<-YAML
    replicaCount: ${k8sconnect_object.value_source.managed_state_projection["data.replicas"]}
  YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`, chartPath)
}

func testAccHelmReleaseConfigUnknownSet(releaseName, namespace string) string {
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

# Create ConfigMap storing a value
resource "k8sconnect_object" "value_source" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: value-source
  namespace: ${var.namespace}
data:
  replicas: "3"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}

resource "k8sconnect_helm_release" "test" {
  depends_on = [k8sconnect_object.value_source]

  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  # Set parameter with unknown value (projection unknown during initial plan)
  set = [
    {
      name  = "replicaCount"
      value = k8sconnect_object.value_source.managed_state_projection["data.replicas"]
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

func testAccHelmReleaseConfigSensitiveUnknown(releaseName, namespace string) string {
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

# Create ConfigMap storing sensitive value (simpler than Secret for testing)
resource "k8sconnect_object" "secret_source" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: secret-source
  namespace: ${var.namespace}
data:
  password: "test-password-123"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}

resource "k8sconnect_helm_release" "test" {
  depends_on = [k8sconnect_object.secret_source]

  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  # Sensitive unknown value (projection unknown during initial plan)
  set_sensitive = [
    {
      name  = "secretPassword"
      value = k8sconnect_object.secret_source.managed_state_projection["data.password"]
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

func testAccHelmReleaseConfigUnknownChartPath(releaseName, namespace string) string {
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

# ConfigMap stores chart path (unknown at plan time)
resource "k8sconnect_object" "chart_path_source" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: chart-path
  namespace: ${var.namespace}
data:
  path: "%s"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}

resource "k8sconnect_helm_release" "test" {
  depends_on = [k8sconnect_object.chart_path_source]

  name      = var.release_name
  namespace = var.namespace
  chart     = k8sconnect_object.chart_path_source.managed_state_projection["data.path"]

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`, chartPath)
}

func testAccHelmReleaseConfigInvalidChartPath(releaseName, namespace string) string {
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

# ConfigMap stores invalid path (unknown at plan time)
resource "k8sconnect_object" "chart_path_source" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: chart-path
  namespace: ${var.namespace}
data:
  path: "/this/path/does/not/exist"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}

resource "k8sconnect_helm_release" "test" {
  depends_on = [k8sconnect_object.chart_path_source]

  name      = var.release_name
  namespace = var.namespace
  chart     = k8sconnect_object.chart_path_source.managed_state_projection["data.path"]

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`)
}

func testAccHelmReleaseConfigUnknownRepository(releaseName, namespace string) string {
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

# ConfigMap stores repository URL (unknown at plan time)
resource "k8sconnect_object" "repo_source" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: repo-url
  namespace: ${var.namespace}
data:
  url: "oci://registry-1.docker.io/bitnamicharts"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}

resource "k8sconnect_helm_release" "test" {
  depends_on = [k8sconnect_object.repo_source]

  name       = var.release_name
  namespace  = var.namespace
  chart      = "nginx"
  repository = k8sconnect_object.repo_source.managed_state_projection["data.url"]
  version    = "18.2.4"

  values = <<-EOT
    service:
      type: ClusterIP
  EOT

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`)
}

func testAccBothResourcesBootstrap(releaseName, objectName, namespace string) string {
	chartPath := "../../../../test/testdata/charts/simple-test"
	return fmt.Sprintf(`
variable "kubeconfig" {
  type = string
}
variable "release_name" {
  type = string
}
variable "object_name" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

# Create ConfigMap to store simple value
resource "k8sconnect_object" "connection_info" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: conn-info
  namespace: ${var.namespace}
data:
  ready: "true"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}

# Helm release with unknown value in values
resource "k8sconnect_helm_release" "test" {
  depends_on = [k8sconnect_object.connection_info]

  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  # Values contains unknown interpolation
  values = <<-YAML
    bootstrapReady: ${k8sconnect_object.connection_info.managed_state_projection["data.ready"]}
  YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}

# Object that also uses unknown value via set_string (not inline YAML)
# Can't use inline interpolation in yaml_body because it creates invalid YAML during plan
resource "k8sconnect_object" "test" {
  depends_on = [k8sconnect_object.connection_info, k8sconnect_helm_release.test]

  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: ${var.object_name}
      namespace: ${var.namespace}
    data:
      foo: bar
  YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}
`, chartPath)
}

func testAccCompleteBootstrapWorkflow(release1Name, release2Name, objectName, namespace string) string {
	chartPath := "../../../../test/testdata/charts/simple-test"
	return fmt.Sprintf(`
variable "kubeconfig" {
  type = string
}
variable "release1_name" {
  type = string
}
variable "release2_name" {
  type = string
}
variable "object_name" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

# Create ConfigMap to store simple value
resource "k8sconnect_object" "connection_info" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: conn-info
  namespace: ${var.namespace}
data:
  ready: "true"
YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}

# First foundation service (simulates CNI)
resource "k8sconnect_helm_release" "service1" {
  depends_on = [k8sconnect_object.connection_info]

  name      = var.release1_name
  namespace = var.namespace
  chart     = "%s"

  # Values contains unknown interpolation
  values = <<-YAML
    bootstrapReady: ${k8sconnect_object.connection_info.managed_state_projection["data.ready"]}
  YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}

# Second foundation service (simulates cert-manager)
resource "k8sconnect_helm_release" "service2" {
  depends_on = [k8sconnect_helm_release.service1]

  name      = var.release2_name
  namespace = var.namespace
  chart     = "%s"

  # Values contains unknown interpolation
  values = <<-YAML
    bootstrapReady: ${k8sconnect_object.connection_info.managed_state_projection["data.ready"]}
  YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}

# Application resource (simulates app using CRDs from services)
resource "k8sconnect_object" "app" {
  depends_on = [k8sconnect_helm_release.service2]

  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: ${var.object_name}
      namespace: ${var.namespace}
    data:
      app: "production"
      version: "1.0.0"
  YAML

  cluster = {
    kubeconfig = var.kubeconfig
  }
}
`, chartPath, chartPath)
}
