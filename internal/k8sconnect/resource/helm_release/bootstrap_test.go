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

	clusterName := fmt.Sprintf("test-bootstrap-%d", time.Now().UnixNano()%1000000)
	releaseName := fmt.Sprintf("test-release-%d", time.Now().UnixNano()%1000000)
	namespace := "default"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Cluster + Helm release in same apply
				// Cluster endpoint/certs are unknown at plan time
				Config: testAccHelmReleaseConfigBootstrap(clusterName, releaseName, namespace),
				ConfigVariables: config.Variables{
					"cluster_name": config.StringVariable(clusterName),
					"release_name": config.StringVariable(releaseName),
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
				),
			},
		},
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
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

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
// For this test, we simulate an unknown repository URL using a local chart path from null_resource
// In real scenarios, this would be an OCI registry URL from ECR/ACR, but for testing we use local charts
func TestAccHelmReleaseResource_UnknownRepository(t *testing.T) {
	// This test is redundant with UnknownChartVersion above
	// In practice, repository + chart are used together for remote charts
	// For local charts (what we test with), the path is what matters
	// The UnknownChartVersion test already covers this scenario
	t.Skip("Covered by UnknownChartVersion test - repository is for remote charts only")
}

// TestAccHelmReleaseResource_UnknownValuesInYAML tests unknown interpolations in values
func TestAccHelmReleaseResource_UnknownValuesInYAML(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	releaseName := fmt.Sprintf("test-unknown-val-%d", time.Now().UnixNano()%1000000)
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

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
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

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
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

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

	clusterName := fmt.Sprintf("test-consistency-%d", time.Now().UnixNano()%1000000)
	releaseName := fmt.Sprintf("test-release-%d", time.Now().UnixNano()%1000000)
	objectName := fmt.Sprintf("test-cm-%d", time.Now().UnixNano()%1000000)
	namespace := "default"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Both helm_release and object with unknown cluster
				Config: testAccBothResourcesBootstrap(clusterName, releaseName, objectName, namespace),
				ConfigVariables: config.Variables{
					"cluster_name": config.StringVariable(clusterName),
					"release_name": config.StringVariable(releaseName),
					"object_name":  config.StringVariable(objectName),
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
					resource.TestCheckResourceAttr("k8sconnect_object.test", "yaml_body_parsed.metadata.name", objectName),
				),
			},
		},
	})
}

// TestAccHelmReleaseResource_CompleteBootstrapWorkflow is the ultimate test
// Cluster + multiple helm releases + objects in ONE apply
// This simulates real-world bootstrap (cluster + foundation services + app resources)
func TestAccHelmReleaseResource_CompleteBootstrapWorkflow(t *testing.T) {
	t.Parallel()

	clusterName := fmt.Sprintf("test-full-boot-%d", time.Now().UnixNano()%1000000)
	release1Name := fmt.Sprintf("test-svc1-%d", time.Now().UnixNano()%1000000)
	release2Name := fmt.Sprintf("test-svc2-%d", time.Now().UnixNano()%1000000)
	objectName := fmt.Sprintf("test-app-%d", time.Now().UnixNano()%1000000)
	namespace := "default"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				// Complete bootstrap: cluster + 2 helm releases + 1 object
				// All in single apply, all with unknown cluster values
				Config: testAccCompleteBootstrapWorkflow(clusterName, release1Name, release2Name, objectName, namespace),
				ConfigVariables: config.Variables{
					"cluster_name":  config.StringVariable(clusterName),
					"release1_name": config.StringVariable(release1Name),
					"release2_name": config.StringVariable(release2Name),
					"object_name":   config.StringVariable(objectName),
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
					resource.TestCheckResourceAttr("k8sconnect_object.app", "yaml_body_parsed.metadata.name", objectName),
					// All should be deployed/ready
					resource.TestCheckResourceAttr("k8sconnect_helm_release.service1", "status", "deployed"),
					resource.TestCheckResourceAttr("k8sconnect_helm_release.service2", "status", "deployed"),
				),
			},
		},
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
	namespace := fmt.Sprintf("helm-test-%d", time.Now().UnixNano()%1000000)

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

func testAccHelmReleaseConfigBootstrap(clusterName, releaseName, namespace string) string {
	chartPath := "../../../../test/testdata/charts/simple-test"
	return fmt.Sprintf(`
variable "cluster_name" {
  type = string
}
variable "release_name" {
  type = string
}

provider "k8sconnect" {}

# Cluster created in same apply - endpoint/certs unknown at plan time
resource "kind_cluster" "test" {
  name = var.cluster_name
}

# Helm release references unknown cluster values
resource "k8sconnect_helm_release" "test" {
  name      = var.release_name
  namespace = "%s"
  chart     = "%s"

  cluster = {
    host                   = kind_cluster.test.endpoint
    cluster_ca_certificate = base64encode(kind_cluster.test.cluster_ca_certificate)
    client_certificate     = base64encode(kind_cluster.test.client_certificate)
    client_key             = base64encode(kind_cluster.test.client_key)
  }

  wait    = true
  timeout = "300s"
}
`, namespace, chartPath)
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

# Random integer - unknown at plan time
resource "random_integer" "replicas" {
  min = 1
  max = 5
}

resource "k8sconnect_helm_release" "test" {
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  # Values contains unknown interpolation
  values = <<-YAML
    replicaCount: ${random_integer.replicas.result}
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

# Random integer - unknown at plan time
resource "random_integer" "replicas" {
  min = 1
  max = 5
}

resource "k8sconnect_helm_release" "test" {
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  # Set parameter with unknown value
  set = [
    {
      name  = "replicaCount"
      value = tostring(random_integer.replicas.result)
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

# Random password - unknown at plan time, sensitive
resource "random_password" "secret" {
  length  = 16
  special = false
}

resource "k8sconnect_helm_release" "test" {
  name      = var.release_name
  namespace = var.namespace
  chart     = "%s"

  # Sensitive unknown value
  set_sensitive = [
    {
      name  = "secretPassword"
      value = random_password.secret.result
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

# Null resource outputs chart path (unknown at plan time)
resource "null_resource" "chart_path" {
  triggers = {
    path = "%s"
  }
}

resource "k8sconnect_helm_release" "test" {
  name      = var.release_name
  namespace = var.namespace
  chart     = null_resource.chart_path.triggers["path"]

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

# Null resource outputs invalid path (unknown at plan time)
resource "null_resource" "chart_path" {
  triggers = {
    path = "/this/path/does/not/exist"
  }
}

resource "k8sconnect_helm_release" "test" {
  name      = var.release_name
  namespace = var.namespace
  chart     = null_resource.chart_path.triggers["path"]

  cluster = {
    kubeconfig = var.kubeconfig
  }

  wait    = true
  timeout = "300s"
}
`)
}

func testAccBothResourcesBootstrap(clusterName, releaseName, objectName, namespace string) string {
	chartPath := "../../../../test/testdata/charts/simple-test"
	return fmt.Sprintf(`
variable "cluster_name" {
  type = string
}
variable "release_name" {
  type = string
}
variable "object_name" {
  type = string
}

provider "k8sconnect" {}

# Cluster created in same apply - endpoint/certs unknown at plan time
resource "kind_cluster" "test" {
  name = var.cluster_name
}

# Helm release with unknown cluster
resource "k8sconnect_helm_release" "test" {
  name      = var.release_name
  namespace = "%s"
  chart     = "%s"

  cluster = {
    host                   = kind_cluster.test.endpoint
    cluster_ca_certificate = base64encode(kind_cluster.test.cluster_ca_certificate)
    client_certificate     = base64encode(kind_cluster.test.client_certificate)
    client_key             = base64encode(kind_cluster.test.client_key)
  }

  wait    = true
  timeout = "300s"
}

# Object with same unknown cluster
resource "k8sconnect_object" "test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: ${var.object_name}
      namespace: %s
    data:
      foo: bar
  YAML

  cluster = {
    host                   = kind_cluster.test.endpoint
    cluster_ca_certificate = base64encode(kind_cluster.test.cluster_ca_certificate)
    client_certificate     = base64encode(kind_cluster.test.client_certificate)
    client_key             = base64encode(kind_cluster.test.client_key)
  }
}
`, namespace, chartPath, namespace)
}

func testAccCompleteBootstrapWorkflow(clusterName, release1Name, release2Name, objectName, namespace string) string {
	chartPath := "../../../../test/testdata/charts/simple-test"
	return fmt.Sprintf(`
variable "cluster_name" {
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

provider "k8sconnect" {}

# Cluster created in same apply - endpoint/certs unknown at plan time
resource "kind_cluster" "test" {
  name = var.cluster_name
}

locals {
  cluster_config = {
    host                   = kind_cluster.test.endpoint
    cluster_ca_certificate = base64encode(kind_cluster.test.cluster_ca_certificate)
    client_certificate     = base64encode(kind_cluster.test.client_certificate)
    client_key             = base64encode(kind_cluster.test.client_key)
  }
}

# First foundation service (simulates CNI)
resource "k8sconnect_helm_release" "service1" {
  name      = var.release1_name
  namespace = "%s"
  chart     = "%s"

  cluster = local.cluster_config
  wait    = true
  timeout = "300s"
}

# Second foundation service (simulates cert-manager)
resource "k8sconnect_helm_release" "service2" {
  name      = var.release2_name
  namespace = "%s"
  chart     = "%s"

  cluster = local.cluster_config
  wait    = true
  timeout = "300s"

  depends_on = [k8sconnect_helm_release.service1]
}

# Application resource (simulates app using CRDs from services)
resource "k8sconnect_object" "app" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: ${var.object_name}
      namespace: %s
    data:
      app: "production"
      version: "1.0.0"
  YAML

  cluster = local.cluster_config

  depends_on = [k8sconnect_helm_release.service2]
}
`, namespace, chartPath, namespace, chartPath, namespace)
}
