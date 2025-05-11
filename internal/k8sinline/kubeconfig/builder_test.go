// internal/k8sinline/kubeconfig/builder_test.go
package kubeconfig

import (
	"reflect"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
)

func TestGenerateKubeconfigFromInline(t *testing.T) {
	host := "https://example.com"
	caData := []byte("myCAData")
	execCfg := ExecAuth{
		APIVersion: "client.authentication.k8s.io/v1beta1",
		Command:    "echo",
		Args:       []string{"foo", "bar"},
	}

	kubeBytes, err := GenerateKubeconfigFromInline(host, caData, execCfg)
	if err != nil {
		t.Fatalf("GenerateKubeconfigFromInline returned error: %v", err)
	}

	cfg, err := clientcmd.Load(kubeBytes)
	if err != nil {
		t.Fatalf("error loading generated kubeconfig: %v", err)
	}

	// Validate cluster entry
	cluster, ok := cfg.Clusters["default"]
	if !ok {
		t.Fatalf("expected default cluster entry")
	}
	if cluster.Server != host {
		t.Errorf("cluster.Server = %q; want %q", cluster.Server, host)
	}
	if !reflect.DeepEqual(cluster.CertificateAuthorityData, caData) {
		t.Errorf("cluster.CAData = %v; want %v", cluster.CertificateAuthorityData, caData)
	}

	// Validate auth info exec
	auth, ok := cfg.AuthInfos["default"]
	if !ok {
		t.Fatalf("expected default authinfo entry")
	}
	if auth.Exec == nil {
		t.Fatalf("expected ExecConfig in authinfo")
	}
	if auth.Exec.APIVersion != execCfg.APIVersion {
		t.Errorf("exec.APIVersion = %q; want %q", auth.Exec.APIVersion, execCfg.APIVersion)
	}
	if auth.Exec.Command != execCfg.Command {
		t.Errorf("exec.Command = %q; want %q", auth.Exec.Command, execCfg.Command)
	}
	if !reflect.DeepEqual(auth.Exec.Args, execCfg.Args) {
		t.Errorf("exec.Args = %v; want %v", auth.Exec.Args, execCfg.Args)
	}

	// Validate context
	ctx, ok := cfg.Contexts["default"]
	if !ok {
		t.Fatalf("expected default context entry")
	}
	if ctx.Cluster != "default" {
		t.Errorf("context.Cluster = %q; want %q", ctx.Cluster, "default")
	}
	if ctx.AuthInfo != "default" {
		t.Errorf("context.AuthInfo = %q; want %q", ctx.AuthInfo, "default")
	}

	// Validate current context
	if cfg.CurrentContext != "default" {
		t.Errorf("CurrentContext = %q; want %q", cfg.CurrentContext, "default")
	}
}
