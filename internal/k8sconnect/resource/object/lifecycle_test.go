package object_test

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

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
	testhelpers "github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/test"
)

// Test delete protection functionality
func TestAccObjectResource_DeleteProtection(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("delete-protected-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create resource with delete protection enabled
			{
				Config: testAccManifestConfigDeleteProtectionEnabled(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.test_protected", "delete_protection", "true"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_protected", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Try to destroy - should fail due to protection
			{
				Config: testAccManifestConfigDeleteProtectionProviderOnly(),
				ConfigVariables: config.Variables{
					"raw": config.StringVariable(raw),
				},
				ExpectError: regexp.MustCompile("Delete Protection Enabled"),
			},
			// Step 3: Disable protection
			{
				Config: testAccManifestConfigDeleteProtectionDisabled(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.test_protected", "delete_protection", "false"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 4: Now destroy should succeed
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigDeleteProtectionEnabled(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "test_protected" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  delete_protection = true

  cluster = {
    kubeconfig = var.raw
  }
}
`, namespace)
}

func testAccManifestConfigDeleteProtectionDisabled(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "test_protected" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  delete_protection = false

  cluster = {
    kubeconfig = var.raw
  }
}
`, namespace)
}

func TestAccObjectResource_ConnectionChange(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("conn-change-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create with kubeconfig
			{
				Config: testAccManifestConfigConnectionChange1(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_conn_change", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Change connection method (same cluster) - should succeed now!
			{
				Config: testAccManifestConfigConnectionChange2(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_conn_change", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					// Connection change should succeed - resource still exists
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigConnectionChange1(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "test_conn_change" {
  yaml_body = <<YAML
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

func testAccManifestConfigConnectionChange2(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "test_conn_change" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    kubeconfig = var.raw
    context        = "k3d-k8sconnect-test"  # Explicit context (connection change)
  }
}
`, namespace)
}

func TestAccObjectResource_ForceDestroy(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("force-destroy-ns-%d", time.Now().UnixNano()%1000000)
	pvcName := fmt.Sprintf("force-destroy-pvc-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigForceDestroy(ns, pvcName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"pvc_name":  config.StringVariable(pvcName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.test_force", "force_destroy", "true"),
					resource.TestCheckResourceAttr("k8sconnect_object.test_force", "delete_timeout", "30s"),
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckPVCDestroy(k8sClient, ns, pvcName),
	})
}

func testAccManifestConfigForceDestroy(namespace, pvcName string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}
variable "pvc_name" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "force_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_force" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
YAML

  delete_timeout = "30s"
  force_destroy = true

  cluster = {
    kubeconfig = var.raw
  }
  
  depends_on = [k8sconnect_object.force_namespace]
}
`, namespace, pvcName, namespace)
}

func TestAccObjectResource_DeleteTimeout(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("delete-timeout-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigDeleteTimeout(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("k8sconnect_object.test_timeout", "delete_timeout", "2m"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigDeleteTimeout(namespace string) string {
	return fmt.Sprintf(`
variable "raw" {
  type = string
}
variable "namespace" {
  type = string
}

provider "k8sconnect" {}

resource "k8sconnect_object" "test_timeout" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML

  delete_timeout = "2m"

  cluster = {
    kubeconfig = var.raw
  }
}
`, namespace)
}

func testAccManifestConfigDeleteProtectionProviderOnly() string {
	return `
variable "raw" {
  type = string
}

provider "k8sconnect" {}
`
}

// ADR-010: Test resource identity change triggers replacement (Kind change)
// This test verifies the critical orphan resource bug is fixed
func TestAccObjectResource_IdentityChange_Kind(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("identity-kind-ns-%d", time.Now().UnixNano()%1000000)
	resourceName := fmt.Sprintf("identity-test-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create a Pod
			{
				Config: testAccManifestConfigIdentityKind_Pod(ns, resourceName),
				ConfigVariables: config.Variables{
					"raw":           config.StringVariable(raw),
					"namespace":     config.StringVariable(ns),
					"resource_name": config.StringVariable(resourceName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_identity", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					testhelpers.CheckPodExists(k8sClient, ns, resourceName),
				),
			},
			// Step 2: Change kind to ConfigMap - should trigger replacement
			{
				Config: testAccManifestConfigIdentityKind_ConfigMap(ns, resourceName),
				ConfigVariables: config.Variables{
					"raw":           config.StringVariable(raw),
					"namespace":     config.StringVariable(ns),
					"resource_name": config.StringVariable(resourceName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_identity", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, resourceName),
					// CRITICAL: Old Pod should be DELETED (not orphaned)
					testhelpers.CheckPodDestroy(k8sClient, ns, resourceName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, resourceName),
		),
	})
}

func testAccManifestConfigIdentityKind_Pod(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "resource_name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_identity" {
  yaml_body = <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  containers:
  - name: nginx
    image: nginx:latest
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}
`, namespace, name, namespace)
}

func testAccManifestConfigIdentityKind_ConfigMap(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "resource_name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_identity" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: value
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}
`, namespace, name, namespace)
}

// ADR-010: Test resource identity change triggers replacement (Name change)
func TestAccObjectResource_IdentityChange_Name(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("identity-name-ns-%d", time.Now().UnixNano()%1000000)
	oldName := fmt.Sprintf("old-configmap-%d", time.Now().UnixNano()%1000000)
	newName := fmt.Sprintf("new-configmap-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with old name
			{
				Config: testAccManifestConfigIdentityName(ns, oldName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(oldName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, oldName),
				),
			},
			// Step 2: Change name - should trigger replacement
			{
				Config: testAccManifestConfigIdentityName(ns, newName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"name":      config.StringVariable(newName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, ns, newName),
					// Old resource should be deleted
					testhelpers.CheckConfigMapDestroy(k8sClient, ns, oldName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
			testhelpers.CheckConfigMapDestroy(k8sClient, ns, newName),
		),
	})
}

func testAccManifestConfigIdentityName(namespace, name string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_identity" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: value
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}
`, namespace, name, namespace)
}

// ADR-010: Test resource identity change triggers replacement (Namespace change)
func TestAccObjectResource_IdentityChange_Namespace(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	oldNs := fmt.Sprintf("old-ns-%d", time.Now().UnixNano()%1000000)
	newNs := fmt.Sprintf("new-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("test-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create in old namespace
			{
				Config: testAccManifestConfigIdentityNamespace(oldNs, newNs, cmName, oldNs),
				ConfigVariables: config.Variables{
					"raw":     config.StringVariable(raw),
					"old_ns":  config.StringVariable(oldNs),
					"new_ns":  config.StringVariable(newNs),
					"cm_name": config.StringVariable(cmName),
					"use_ns":  config.StringVariable(oldNs),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, oldNs, cmName),
				),
			},
			// Step 2: Change namespace - should trigger replacement
			{
				Config: testAccManifestConfigIdentityNamespace(oldNs, newNs, cmName, newNs),
				ConfigVariables: config.Variables{
					"raw":     config.StringVariable(raw),
					"old_ns":  config.StringVariable(oldNs),
					"new_ns":  config.StringVariable(newNs),
					"cm_name": config.StringVariable(cmName),
					"use_ns":  config.StringVariable(newNs),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckConfigMapExists(k8sClient, newNs, cmName),
					// Old resource should be deleted
					testhelpers.CheckConfigMapDestroy(k8sClient, oldNs, cmName),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, oldNs),
			testhelpers.CheckNamespaceDestroy(k8sClient, newNs),
		),
	})
}

func testAccManifestConfigIdentityNamespace(oldNs, newNs, cmName, useNs string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "old_ns" { type = string }
variable "new_ns" { type = string }
variable "cm_name" { type = string }
variable "use_ns" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "old_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "new_namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_identity" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key: value
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [
    k8sconnect_object.old_namespace,
    k8sconnect_object.new_namespace
  ]
}
`, oldNs, newNs, cmName, useNs)
}

// ADR-002: Test immutable field changes trigger replacement (PVC storage)
// This test verifies automatic recreation when immutable fields change
func TestAccObjectResource_ImmutableFieldChange_PVCStorage(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("immutable-pvc-ns-%d", time.Now().UnixNano()%1000000)
	pvcName := fmt.Sprintf("test-pvc-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create PVC with 1Gi storage
			{
				Config: testAccManifestConfigImmutablePVC(ns, pvcName, "1Gi"),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"pvc_name":  config.StringVariable(pvcName),
					"storage":   config.StringVariable("1Gi"),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_pvc", "id"),
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
				),
			},
			// Step 2: Try to change storage size to 2Gi - should trigger replacement
			// Note: PVC storage is immutable (cannot be changed in many K8s versions)
			{
				Config: testAccManifestConfigImmutablePVC(ns, pvcName, "2Gi"),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"pvc_name":  config.StringVariable(pvcName),
					"storage":   config.StringVariable("2Gi"),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_pvc", "id"),
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
					// Verify PVC was recreated (new UID)
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
			testhelpers.CheckPVCDestroy(k8sClient, ns, pvcName),
		),
	})
}

func testAccManifestConfigImmutablePVC(namespace, pvcName, storage string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "pvc_name" { type = string }
variable "storage" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_pvc" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}
`, namespace, pvcName, namespace, storage)
}

// ADR-010: Test resource identity change triggers replacement (apiVersion change)
// This completes the 4th identity field test
func TestAccObjectResource_IdentityChange_ApiVersion(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("identity-apiversion-ns-%d", time.Now().UnixNano()%1000000)
	crdName := "widgets.example.com"
	crName := fmt.Sprintf("test-widget-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create CRD with v1
			{
				Config: testAccManifestConfigIdentityApiVersion_CRD_v1(ns, crdName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"crd_name":  config.StringVariable(crdName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_crd", "id"),
					testhelpers.CheckNamespaceExists(k8sClient, ns),
				),
			},
			// Step 2: Create custom resource using v1
			{
				Config: testAccManifestConfigIdentityApiVersion_CR_v1(ns, crdName, crName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"crd_name":  config.StringVariable(crdName),
					"cr_name":   config.StringVariable(crName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_crd", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_identity", "id"),
				),
			},
			// Step 3: Update CRD to add v1beta1
			{
				Config: testAccManifestConfigIdentityApiVersion_CRD_v1_and_v1beta1(ns, crdName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"crd_name":  config.StringVariable(crdName),
					"cr_name":   config.StringVariable(crName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_crd", "id"),
				),
			},
			// Step 4: Change CR to use v1beta1 (apiVersion change) - should trigger replacement
			{
				Config: testAccManifestConfigIdentityApiVersion_CR_v1beta1(ns, crdName, crName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"crd_name":  config.StringVariable(crdName),
					"cr_name":   config.StringVariable(crName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_crd", "id"),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_identity", "id"),
					// CRITICAL: Replacement occurred - new resource ID due to apiVersion change
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigIdentityApiVersion_CRD_v1(namespace, crdName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "crd_name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_crd" {
  yaml_body = <<YAML
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %s
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              color:
                type: string
YAML
  cluster = {
    kubeconfig = var.raw
  }
}
`, namespace, crdName)
}

func testAccManifestConfigIdentityApiVersion_CR_v1(namespace, crdName, crName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "crd_name" { type = string }
variable "cr_name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_crd" {
  yaml_body = <<YAML
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %s
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              color:
                type: string
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_identity" {
  yaml_body = <<YAML
apiVersion: example.com/v1
kind: Widget
metadata:
  name: %s
  namespace: %s
spec:
  color: blue
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [
    k8sconnect_object.namespace,
    k8sconnect_object.test_crd
  ]
}
`, namespace, crdName, crName, namespace)
}

func testAccManifestConfigIdentityApiVersion_CRD_v1_and_v1beta1(namespace, crdName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "crd_name" { type = string }
variable "cr_name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_crd" {
  yaml_body = <<YAML
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %s
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
  scope: Namespaced
  versions:
  - name: v1beta1
    served: true
    storage: false
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              color:
                type: string
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              color:
                type: string
YAML
  cluster = {
    kubeconfig = var.raw
  }
}
`, namespace, crdName)
}

func testAccManifestConfigIdentityApiVersion_CR_v1beta1(namespace, crdName, crName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "crd_name" { type = string }
variable "cr_name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_crd" {
  yaml_body = <<YAML
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %s
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
  scope: Namespaced
  versions:
  - name: v1beta1
    served: true
    storage: false
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              color:
                type: string
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              color:
                type: string
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_identity" {
  yaml_body = <<YAML
apiVersion: example.com/v1beta1
kind: Widget
metadata:
  name: %s
  namespace: %s
spec:
  color: red
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [
    k8sconnect_object.namespace,
    k8sconnect_object.test_crd
  ]
}
`, namespace, crdName, crName, namespace)
}

// ADR-011: Test connection with variable kubeconfig
// This validates that connection parameters work correctly
func TestAccObjectResource_ConnectionWithVariable(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("conn-var-ns-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigConnectionWithVariable(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigConnectionWithVariable(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "test" {
  yaml_body = <<YAML
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

// ADR-011: Test YAML with variable interpolation
// This verifies that YAML with Terraform variables is handled correctly
func TestAccObjectResource_YAMLWithVariableInterpolation(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("yaml-interp-ns-%d", time.Now().UnixNano()%1000000)
	configValue := "test-config-value-123"
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigYAMLInterpolation(ns, configValue),
				ConfigVariables: config.Variables{
					"raw":          config.StringVariable(raw),
					"namespace":    config.StringVariable(ns),
					"config_value": config.StringVariable(configValue),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckNamespaceExists(k8sClient, ns),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigYAMLInterpolation(namespace, configValue string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "config_value" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config
  namespace: %s
data:
  config-value: ${var.config_value}
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}
`, namespace, namespace)
}

// ADR-011: Test ignore_fields with variable value
// This verifies that ignore_fields work correctly when set via variables
func TestAccObjectResource_IgnoreFieldsWithVariable(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("ignore-var-ns-%d", time.Now().UnixNano()%1000000)
	deployName := fmt.Sprintf("test-deploy-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccManifestConfigIgnoreFieldsVariable(ns, deployName),
				ConfigVariables: config.Variables{
					"raw":         config.StringVariable(raw),
					"namespace":   config.StringVariable(ns),
					"deploy_name": config.StringVariable(deployName),
				},
				Check: resource.ComposeTestCheckFunc(
					testhelpers.CheckDeploymentExists(k8sClient, ns, deployName),
					resource.TestCheckResourceAttrSet("k8sconnect_object.test", "id"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
			testhelpers.CheckDeploymentDestroy(k8sClient, ns, deployName),
		),
	})
}

func testAccManifestConfigIgnoreFieldsVariable(namespace, deployName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "deploy_name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test" {
  yaml_body = <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 3
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: nginx
        image: public.ecr.aws/nginx/nginx:1.21
        ports:
        - containerPort: 80
YAML

  ignore_fields = ["spec.replicas"]

  cluster = {
    kubeconfig = var.raw
  }

  depends_on = [k8sconnect_object.namespace]
}
`, namespace, deployName, namespace, deployName, deployName)
}

// Test deletion timeout when resource has stuck finalizer
// This exercises the deletion timeout and finalizer explanation code paths
func TestAccObjectResource_DeleteWithStuckFinalizer(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("finalizer-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("stuck-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	// Clean up the stuck finalizer after the test
	t.Cleanup(func() {
		testhelpers.CleanupFinalizer(t, k8sClient, ns, cmName)
		testhelpers.CleanupNamespace(t, k8sClient, ns)
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create ConfigMap with custom finalizer and short timeout
			{
				Config: testAccManifestConfigStuckFinalizer(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_cm", "id"),
					testhelpers.CheckConfigMapExists(k8sClient, ns, cmName),
				),
			},
			// Step 2: Remove the resource - should timeout waiting for deletion
			// The finalizer will block deletion and we have a 2s timeout
			{
				Config: testAccManifestConfigStuckFinalizerEmpty(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				ExpectError: regexp.MustCompile("timed out waiting for resource to be deleted|deletion timeout|finalizer|Deletion Blocked"),
			},
			// Step 3: Clean up the finalizer, then remove the resource successfully
			// This prevents the test framework's automatic cleanup from hitting the finalizer
			{
				PreConfig: func() {
					testhelpers.CleanupFinalizer(t, k8sClient, ns, cmName)
				},
				Config: testAccManifestConfigStuckFinalizerEmpty(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: resource.ComposeTestCheckFunc(
					// ConfigMap should now be successfully deleted
					testhelpers.CheckConfigMapDestroy(k8sClient, ns, cmName),
				),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigStuckFinalizer(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "cm_name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_cm" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
  finalizers:
  - k8sconnect.test/blocking-finalizer
data:
  test: value
YAML

  delete_timeout = "2s"

  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}
`, namespace, cmName, namespace)
}

func testAccManifestConfigStuckFinalizerEmpty(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
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

// Test creating a resource that already exists (exercises IsAlreadyExists error path)
// This test verifies that the provider properly handles the case where a resource
// already exists in the cluster (without using Terraform import)
func TestAccObjectResource_CreateAlreadyExists(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("already-exists-ns-%d", time.Now().UnixNano()%1000000)
	cmName := fmt.Sprintf("existing-cm-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create namespace first
			{
				Config: testAccManifestConfigAlreadyExistsNamespace(ns),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
				},
				Check: testhelpers.CheckNamespaceExists(k8sClient, ns),
			},
			// Step 2: Pre-create ConfigMap directly with K8s client, then try to create with Terraform
			{
				PreConfig: func() {
					testhelpers.CreateConfigMapDirectly(t, k8sClient, ns, cmName, map[string]string{"pre-created": "true"})
				},
				Config: testAccManifestConfigAlreadyExistsConfigMap(ns, cmName),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"cm_name":   config.StringVariable(cmName),
				},
				// Should error - resource exists without k8sconnect ownership (must use import)
				ExpectError: regexp.MustCompile(`(?i)Resource Already Exists`),
			},
		},
		CheckDestroy: testhelpers.CheckNamespaceDestroy(k8sClient, ns),
	})
}

func testAccManifestConfigAlreadyExistsNamespace(namespace string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
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

func testAccManifestConfigAlreadyExistsConfigMap(namespace, cmName string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "cm_name" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_cm" {
  yaml_body = <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  managed: "true"
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}
`, namespace, cmName, namespace)
}

// ADR-002: Test update triggering immutable field recreation
// This test verifies that when both mutable (labels) and immutable (storage) fields change
// in the same apply, the resource is properly recreated with both changes applied
func TestAccObjectResource_UpdateTriggeringImmutableRecreation(t *testing.T) {
	t.Parallel()

	raw := os.Getenv("TF_ACC_KUBECONFIG")
	if raw == "" {
		t.Fatal("TF_ACC_KUBECONFIG must be set")
	}

	ns := fmt.Sprintf("immutable-update-ns-%d", time.Now().UnixNano()%1000000)
	pvcName := fmt.Sprintf("update-pvc-%d", time.Now().UnixNano()%1000000)
	k8sClient := testhelpers.CreateK8sClient(t, raw)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			// Step 1: Create PVC with 1Gi storage and env=dev label
			{
				Config: testAccManifestConfigImmutableUpdate(ns, pvcName, "1Gi", "dev"),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"pvc_name":  config.StringVariable(pvcName),
					"storage":   config.StringVariable("1Gi"),
					"env_label": config.StringVariable("dev"),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_pvc", "id"),
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
					testhelpers.CheckPVCHasLabel(k8sClient, ns, pvcName, "env", "dev"),
				),
			},
			// Step 2: Update storage to 2Gi AND change label to env=prod in same apply
			// Should trigger replacement due to immutable storage field
			// AND apply the mutable label change to the new resource
			{
				Config: testAccManifestConfigImmutableUpdate(ns, pvcName, "2Gi", "prod"),
				ConfigVariables: config.Variables{
					"raw":       config.StringVariable(raw),
					"namespace": config.StringVariable(ns),
					"pvc_name":  config.StringVariable(pvcName),
					"storage":   config.StringVariable("2Gi"),
					"env_label": config.StringVariable("prod"),
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("k8sconnect_object.test_pvc", "id"),
					testhelpers.CheckPVCExists(k8sClient, ns, pvcName),
					// Verify new PVC has updated label (mutable change applied to recreated resource)
					testhelpers.CheckPVCHasLabel(k8sClient, ns, pvcName, "env", "prod"),
					// Verify storage is 2Gi (immutable change triggered recreation)
					testhelpers.CheckPVCStorage(k8sClient, ns, pvcName, "2Gi"),
				),
			},
		},
		CheckDestroy: resource.ComposeTestCheckFunc(
			testhelpers.CheckNamespaceDestroy(k8sClient, ns),
			testhelpers.CheckPVCDestroy(k8sClient, ns, pvcName),
		),
	})
}

func testAccManifestConfigImmutableUpdate(namespace, pvcName, storage, envLabel string) string {
	return fmt.Sprintf(`
variable "raw" { type = string }
variable "namespace" { type = string }
variable "pvc_name" { type = string }
variable "storage" { type = string }
variable "env_label" { type = string }
provider "k8sconnect" {}

resource "k8sconnect_object" "namespace" {
  yaml_body = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
}

resource "k8sconnect_object" "test_pvc" {
  yaml_body = <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
  labels:
    env: %s
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: %s
YAML
  cluster = {
    kubeconfig = var.raw
  }
  depends_on = [k8sconnect_object.namespace]
}
`, namespace, pvcName, namespace, envLabel, storage)
}

// NOTE: for_each key change test removed - Terraform testing framework doesn't support for_each in test configurations
// BUG #2 (Resource replacement deletion timeout) is tested via manual soak test in scenarios/kind-validation/
// See SOAKTEST.md for test steps and expected behavior
