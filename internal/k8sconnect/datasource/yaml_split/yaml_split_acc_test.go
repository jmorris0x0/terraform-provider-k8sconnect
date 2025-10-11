// internal/k8sconnect/datasource/yaml_split/yaml_split_acc_test.go
package yaml_split_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect"
)

func TestAccYamlSplitDataSource_Basic(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccYamlSplitConfigInline,
				Check: resource.ComposeTestCheckFunc(
					// id should be set because the provider hashes the input content
					resource.TestCheckResourceAttrSet("data.k8sconnect_yaml_split.test", "id"),

					// map should contain exactly two entries
					resource.TestCheckResourceAttr("data.k8sconnect_yaml_split.test", "manifests.%", "2"),

					// verify each manifest's content
					resource.TestCheckResourceAttr(
						"data.k8sconnect_yaml_split.test",
						"manifests.namespace.acctest-ns",
						testNamespaceManifest,
					),
					resource.TestCheckResourceAttr(
						"data.k8sconnect_yaml_split.test",
						"manifests.configmap.acctest-ns.example-config",
						testConfigMapManifest,
					),
				),
			},
		},
	})
}

func TestAccYamlSplitDataSource_Pattern(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config: testAccYamlSplitConfigPattern,
				Check: resource.ComposeTestCheckFunc(
					// id should be set
					resource.TestCheckResourceAttrSet("data.k8sconnect_yaml_split.test", "id"),

					// Should find manifests from the pattern
					// Note: Exact count depends on files in examples/yaml-split-files/manifests/
					resource.TestCheckResourceAttrSet("data.k8sconnect_yaml_split.test", "manifests.%"),
				),
			},
		},
	})
}

func TestAccYamlSplitDataSource_Errors(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"k8sconnect": providerserver.NewProtocol6WithError(k8sconnect.New()),
		},
		Steps: []resource.TestStep{
			{
				Config:      testAccYamlSplitConfigBothParams,
				ExpectError: regexp.MustCompile("Exactly one of 'content' or 'pattern' must be specified"),
			},
			{
				Config:      testAccYamlSplitConfigNeitherParam,
				ExpectError: regexp.MustCompile("Either 'content' or 'pattern' must be specified"),
			},
			{
				Config:      testAccYamlSplitConfigNoFiles,
				ExpectError: regexp.MustCompile("No files matched pattern"),
			},
			{
				Config:      testAccYamlSplitConfigDuplicates,
				ExpectError: regexp.MustCompile("duplicate resource ID"),
			},
		},
	})
}

const testNamespaceManifest = `apiVersion: v1
kind: Namespace
metadata:
  name: acctest-ns`

const testConfigMapManifest = `apiVersion: v1
kind: ConfigMap
metadata:
  name: example-config
  namespace: acctest-ns
data:
  foo: bar`

const testAccYamlSplitConfigInline = `
data "k8sconnect_yaml_split" "test" {
  content = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: acctest-ns
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-config
  namespace: acctest-ns
data:
  foo: bar
YAML
}
`

const testAccYamlSplitConfigPattern = `
data "k8sconnect_yaml_split" "test" {
  pattern = "../../../examples/yaml-split-files/manifests/*.yaml"
}
`

const testAccYamlSplitConfigBothParams = `
data "k8sconnect_yaml_split" "test" {
  content = "apiVersion: v1\nkind: Namespace"
  pattern = "*.yaml"
}
`

const testAccYamlSplitConfigNeitherParam = `
data "k8sconnect_yaml_split" "test" {
}
`

const testAccYamlSplitConfigNoFiles = `
data "k8sconnect_yaml_split" "test" {
  pattern = "/nonexistent/path/*.yaml"
}
`

const testAccYamlSplitConfigDuplicates = `
data "k8sconnect_yaml_split" "test" {
  content = <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: duplicate
---
apiVersion: v1
kind: Namespace
metadata:
  name: duplicate
YAML
}
`
