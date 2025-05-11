// internal/k8sinline/kubeconfig/builder.go
package kubeconfig

import (
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ExecAuth holds exec‑auth configuration for dynamic credentials
// inline mode (API version, command, args).
type ExecAuth struct {
	APIVersion string
	Command    string
	Args       []string
}

// GenerateKubeconfigFromInline constructs a kubeconfig that
// targets the given host, trusts the provided CA data, and
// uses an exec block for authentication.
func GenerateKubeconfigFromInline(host string, caData []byte, exec ExecAuth) ([]byte, error) {
	config := clientcmdapi.NewConfig()

	config.Clusters["default"] = &clientcmdapi.Cluster{
		Server:                   host,
		CertificateAuthorityData: caData,
	}

	config.AuthInfos["default"] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion: exec.APIVersion,
			Command:    exec.Command,
			Args:       exec.Args,
		},
	}

	config.Contexts["default"] = &clientcmdapi.Context{
		Cluster:  "default",
		AuthInfo: "default",
	}
	config.CurrentContext = "default"

	return clientcmd.Write(*config)
}
