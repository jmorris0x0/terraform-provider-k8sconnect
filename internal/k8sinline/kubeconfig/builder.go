// internal/k8sinline/kubeconfig/builder.go
package kubeconfig

import (
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ExecAuth holds the bits we need for the user.exec block of the kubeconfig.
type ExecAuth struct {
	APIVersion string
	Command    string
	Args       []string
}

// GenerateKubeconfigFromInline returns a kubeconfig YAML that points at `host`, trusts `caData`,
// and uses an exec block for credentials, with no 'env' field emitted.
func GenerateKubeconfigFromInline(host string, caData []byte, exec ExecAuth) ([]byte, error) {
	cfg := clientcmdapi.NewConfig()
	// Define cluster entry
	cfg.Clusters["default"] = &clientcmdapi.Cluster{
		Server:                   host,
		CertificateAuthorityData: caData,
	}
	// Define user auth with exec, explicitly set empty Env to avoid null
	cfg.AuthInfos["default"] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion:         exec.APIVersion,
			Command:            exec.Command,
			Args:               exec.Args,
			Env:                []clientcmdapi.ExecEnvVar{},
			ProvideClusterInfo: false,
		},
	}
	// Define context
	cfg.Contexts["default"] = &clientcmdapi.Context{
		Cluster:  "default",
		AuthInfo: "default",
	}
	cfg.CurrentContext = "default"

	// Serialize to YAML
	return clientcmd.Write(*cfg)
}
