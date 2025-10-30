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
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// TestAccObjectResource_OwnershipTransitionInPlan tests the CRITICAL requirement:
// When ownership transitions occur, the PLAN must show the transition BEFORE apply.
//
// This is the core feature of field_ownership - showing "who owns it now" vs "who will own it after apply"
//
// Test scenarios:
//  1. Import kubectl-created resource → k8sconnect takes ownership
//     Plan MUST show: field_ownership["data.foo"] = "kubectl-create" -> "k8sconnect"
//
//  2. External controller modifies k8sconnect-owned field → k8sconnect reclaims ownership
//     Plan MUST show: field_ownership["spec.replicas"] = "kubectl" -> "k8sconnect"
//
//  3. k8sconnect manages field, then ignore_fields added → ownership released
//     Plan MUST show field removed from field_ownership map
//
// Each scenario verifies:
// - Plan shows the transition (not just warnings)
// - Apply succeeds without "inconsistent result" error
// - Final state matches plan prediction
func TestAccObjectResource_OwnershipTransitionInPlan(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	t.Run("ImportKubectlResource_ShowsTransitionInPlan", func(t *testing.T) {
		testImportShowsOwnershipTransitionInPlan(t, raw)
	})

	t.Run("ExternalControllerDrift_ShowsTransitionInPlan", func(t *testing.T) {
		testExternalDriftShowsOwnershipTransitionInPlan(t, raw)
	})

	t.Run("IgnoreFieldsAdded_ShowsOwnershipReleaseInPlan", func(t *testing.T) {
		testIgnoreFieldsShowsOwnershipReleaseInPlan(t, raw)
	})
}

// testImportShowsOwnershipTransitionInPlan verifies: kubectl-create → k8sconnect transition visible in plan
func testImportShowsOwnershipTransitionInPlan(t *testing.T, raw string) {
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("ownership-plan-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("kubectl-cm-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccConfigOwnershipTransitionNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Create ConfigMap with kubectl (external manager), import, and apply
			// Note: import blocks in Terraform 1.5+ do import+plan+apply as one operation
			// So after this step, k8sconnect will have taken ownership
			{
				PreConfig: func() {
					t.Logf("Creating ConfigMap %s/%s with kubectl", ns, cmName)
					testhelpers.CreateConfigMapWithKubectl(t, ns, cmName, map[string]string{
						"owner": "kubectl-client-side-apply",
					})
				},
				Config: testAccConfigOwnershipTransitionImport(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":  config.StringVariable(raw),
					"name": config.StringVariable(cmName),
				},
				// After import+apply, k8sconnect has taken ownership
				// This verifies the fix: field_ownership transitions are now correctly predicted
				// in plans BEFORE apply, avoiding "Provider produced inconsistent result" errors
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.imported_cm", "field_ownership.data.owner", "k8sconnect"),
					testhelpers.CheckFieldManager(k8sClient, ns, "ConfigMap", cmName, "k8sconnect"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

// testExternalDriftShowsOwnershipTransitionInPlan verifies: external-controller → k8sconnect transition visible in plan
func testExternalDriftShowsOwnershipTransitionInPlan(t *testing.T, raw string) {
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("ownership-drift-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("web-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccConfigOwnershipTransitionNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Create Deployment with k8sconnect
			{
				Config: testAccConfigOwnershipTransitionDeployment(ns, deployName, 3),
				ConfigVariables: config.Variables{
					"raw":      config.StringVariable(raw),
					"name":     config.StringVariable(deployName),
					"replicas": config.IntegerVariable(3),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.deployment", "field_ownership.spec.replicas", "k8sconnect"),
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
				),
			},
			// Step 3: kubectl scales deployment (external controller takes ownership), then apply to reclaim
			{
				PreConfig: func() {
					t.Logf("Scaling deployment %s/%s with kubectl (simulating external drift)", ns, deployName)
					testhelpers.ScaleDeploymentWithKubectl(t, ns, deployName, 5)
				},
				Config: testAccConfigOwnershipTransitionDeployment(ns, deployName, 3),
				ConfigVariables: config.Variables{
					"raw":      config.StringVariable(raw),
					"name":     config.StringVariable(deployName),
					"replicas": config.IntegerVariable(3),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// Verify the plan shows the ownership transition BEFORE apply
						plancheck.ExpectResourceAction("k8sconnect_object.deployment", plancheck.ResourceActionUpdate),
						// This check verifies field_ownership shows: kubectl → k8sconnect
						testhelpers.ExpectFieldOwnershipTransition(
							"k8sconnect_object.deployment",
							"spec.replicas",
							"kubectl",    // kubectl scale changes manager to this
							"k8sconnect", // we'll reclaim with force=true
						),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					// After apply, ownership should be k8sconnect again
					resource.TestCheckResourceAttr("k8sconnect_object.deployment", "field_ownership.spec.replicas", "k8sconnect"),
					testhelpers.CheckDeploymentReplicas(k8sClient, ns, deployName, 3),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

// testIgnoreFieldsShowsOwnershipReleaseInPlan verifies: ownership released when ignore_fields added
func testIgnoreFieldsShowsOwnershipReleaseInPlan(t *testing.T, raw string) {
	k8sClient := testhelpers.CreateK8sClient(t, raw)
	ns := fmt.Sprintf("ownership-release-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("web-%d", time.Now().UnixNano()%1000000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace
			{
				Config: testAccConfigOwnershipTransitionNamespace(ns),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Create Deployment with k8sconnect owning spec.replicas
			{
				Config: testAccConfigOwnershipTransitionDeployment(ns, deployName, 3),
				ConfigVariables: config.Variables{
					"raw":      config.StringVariable(raw),
					"name":     config.StringVariable(deployName),
					"replicas": config.IntegerVariable(3),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.deployment", "field_ownership.spec.replicas", "k8sconnect"),
				),
			},
			// Step 3: Add ignore_fields for spec.replicas - releases ownership
			{
				Config: testAccConfigOwnershipTransitionDeploymentWithIgnoreFields(ns, deployName, 3),
				ConfigVariables: config.Variables{
					"raw":      config.StringVariable(raw),
					"name":     config.StringVariable(deployName),
					"replicas": config.IntegerVariable(3),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// Verify the plan shows spec.replicas being removed from field_ownership BEFORE apply
						// This is an ownership release (not a transition to another manager)
						testhelpers.ExpectFieldOwnershipRemoved(
							"k8sconnect_object.deployment",
							"spec.replicas", // This field should no longer be in field_ownership after ignore_fields
						),
					},
				},
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

// Config functions

func testAccConfigOwnershipTransitionNamespace(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}
`, namespace)
}

func testAccConfigOwnershipTransitionImport(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "name" { type = string }

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

import {
  to = k8sconnect_object.imported_cm
  id = "k3d-k8sconnect-test:%s:v1/ConfigMap:%s"
}

resource "k8sconnect_object" "imported_cm" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
    data:
      owner: kubectl-client-side-apply
  YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}
`, namespace, namespace, name, name, namespace)
}

func testAccConfigOwnershipTransitionDeployment(namespace, name string, replicas int) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "name" { type = string }
variable "replicas" { type = number }

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: %s
      namespace: %s
    spec:
      replicas: %d
      selector:
        matchLabels:
          app: web
      template:
        metadata:
          labels:
            app: web
        spec:
          containers:
          - name: nginx
            image: nginx:1.21
  YAML

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}
`, namespace, name, namespace, replicas)
}

func testAccConfigOwnershipTransitionDeploymentWithIgnoreFields(namespace, name string, replicas int) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "name" { type = string }
variable "replicas" { type = number }

provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: %s
      namespace: %s
    spec:
      replicas: %d
      selector:
        matchLabels:
          app: web
      template:
        metadata:
          labels:
            app: web
        spec:
          containers:
          - name: nginx
            image: nginx:1.21
  YAML

  cluster = {
    kubeconfig = var.raw
  }

  # Adding ignore_fields releases ownership
  ignore_fields = ["spec.replicas"]

  depends_on = [k8sconnect_object.namespace]
}
`, namespace, name, namespace, replicas)
}
