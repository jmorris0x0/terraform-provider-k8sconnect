// internal/k8sconnect/common/auth/connection.go
package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ClusterConnectionModel represents the connection configuration for a Kubernetes cluster.
// This model is used by both the provider and resources to establish cluster connections.
type ClusterConnectionModel struct {
	Host                 types.String   `tfsdk:"host"`
	ClusterCACertificate types.String   `tfsdk:"cluster_ca_certificate"`
	KubeconfigFile       types.String   `tfsdk:"kubeconfig_file"`
	KubeconfigRaw        types.String   `tfsdk:"kubeconfig_raw"`
	Context              types.String   `tfsdk:"context"`
	Token                types.String   `tfsdk:"token"`
	ClientCertificate    types.String   `tfsdk:"client_certificate"`
	ClientKey            types.String   `tfsdk:"client_key"`
	Insecure             types.Bool     `tfsdk:"insecure"`
	ProxyURL             types.String   `tfsdk:"proxy_url"`
	Exec                 *ExecAuthModel `tfsdk:"exec"`
}

// ExecAuthModel represents exec-based authentication configuration
type ExecAuthModel struct {
	APIVersion types.String            `tfsdk:"api_version"`
	Command    types.String            `tfsdk:"command"`
	Args       []types.String          `tfsdk:"args"`
	Env        map[string]types.String `tfsdk:"env"`
}

// CreateRESTConfig creates a Kubernetes REST config from the connection model.
// It determines the appropriate method (inline, file, or raw kubeconfig) and returns
// a configured rest.Config ready for creating a Kubernetes client.
func CreateRESTConfig(ctx context.Context, conn ClusterConnectionModel) (*rest.Config, error) {
	// Determine which connection method to use
	if !conn.Host.IsNull() {
		// Inline configuration
		return createInlineConfig(conn)
	} else if !conn.KubeconfigFile.IsNull() {
		// File-based kubeconfig
		return createFileConfig(conn)
	} else if !conn.KubeconfigRaw.IsNull() {
		// Raw kubeconfig
		return createRawConfig(conn)
	}

	return nil, fmt.Errorf("no connection configuration provided")
}

// createInlineConfig creates a REST config from inline connection settings
func createInlineConfig(conn ClusterConnectionModel) (*rest.Config, error) {
	// Build REST config
	config := &rest.Config{
		Host: conn.Host.ValueString(),
	}

	// Handle TLS/CA certificate
	if !conn.ClusterCACertificate.IsNull() {
		caData, err := base64.StdEncoding.DecodeString(conn.ClusterCACertificate.ValueString())
		if err != nil {
			return nil, fmt.Errorf("failed to decode cluster_ca_certificate: %w", err)
		}
		config.TLSClientConfig.CAData = caData
	}

	// Handle insecure mode
	if !conn.Insecure.IsNull() && conn.Insecure.ValueBool() {
		config.TLSClientConfig.Insecure = true
	}

	// Validate CA cert or insecure
	if conn.ClusterCACertificate.IsNull() && (conn.Insecure.IsNull() || !conn.Insecure.ValueBool()) {
		return nil, fmt.Errorf("cluster_ca_certificate is required for secure connections (or set insecure=true)")
	}

	// Handle authentication methods

	// Bearer token
	if !conn.Token.IsNull() {
		config.BearerToken = conn.Token.ValueString()
	}

	// Client certificate authentication
	if !conn.ClientCertificate.IsNull() && !conn.ClientKey.IsNull() {
		certData, err := base64.StdEncoding.DecodeString(conn.ClientCertificate.ValueString())
		if err != nil {
			return nil, fmt.Errorf("failed to decode client_certificate: %w", err)
		}

		keyData, err := base64.StdEncoding.DecodeString(conn.ClientKey.ValueString())
		if err != nil {
			return nil, fmt.Errorf("failed to decode client_key: %w", err)
		}

		config.TLSClientConfig.CertData = certData
		config.TLSClientConfig.KeyData = keyData
	}

	// Handle proxy
	if !conn.ProxyURL.IsNull() {
		proxyURL, err := url.Parse(conn.ProxyURL.ValueString())
		if err != nil {
			return nil, fmt.Errorf("failed to parse proxy_url: %w", err)
		}
		config.Proxy = http.ProxyURL(proxyURL)
	}

	// Add exec provider if specified
	if conn.Exec != nil && !conn.Exec.APIVersion.IsNull() {
		args := make([]string, len(conn.Exec.Args))
		for i, arg := range conn.Exec.Args {
			args[i] = arg.ValueString()
		}

		var envVars []clientcmdapi.ExecEnvVar
		if conn.Exec.Env != nil {
			for name, value := range conn.Exec.Env {
				if !value.IsNull() {
					envVars = append(envVars, clientcmdapi.ExecEnvVar{
						Name:  name,
						Value: value.ValueString(),
					})
				}
			}
		}

		config.ExecProvider = &clientcmdapi.ExecConfig{
			APIVersion:      conn.Exec.APIVersion.ValueString(),
			Command:         conn.Exec.Command.ValueString(),
			Args:            args,
			Env:             envVars,
			InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
		}
	}

	// Validate we have some authentication
	hasAuth := !conn.Token.IsNull() ||
		(!conn.ClientCertificate.IsNull() && !conn.ClientKey.IsNull()) ||
		(conn.Exec != nil && !conn.Exec.APIVersion.IsNull())

	if !hasAuth {
		return nil, fmt.Errorf("no authentication method specified: provide token, client certificates, or exec configuration")
	}

	return config, nil
}

// createFileConfig creates a REST config from kubeconfig file
func createFileConfig(conn ClusterConnectionModel) (*rest.Config, error) {
	kubeconfigPath := conn.KubeconfigFile.ValueString()
	context := ""
	if !conn.Context.IsNull() {
		context = conn.Context.ValueString()
	}

	if context != "" {
		// Load kubeconfig file and set context
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
			&clientcmd.ConfigOverrides{CurrentContext: context},
		)
		return clientConfig.ClientConfig()
	}

	return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
}

// createRawConfig creates a REST config from raw kubeconfig data
func createRawConfig(conn ClusterConnectionModel) (*rest.Config, error) {
	kubeconfigData := []byte(conn.KubeconfigRaw.ValueString())

	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	if !conn.Context.IsNull() {
		context := conn.Context.ValueString()
		// Load kubeconfig and set context
		clientConfig, err := clientcmd.Load(kubeconfigData)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
		}

		if _, exists := clientConfig.Contexts[context]; !exists {
			return nil, fmt.Errorf("context %q not found in kubeconfig", context)
		}

		clientConfig.CurrentContext = context
		return clientcmd.NewDefaultClientConfig(*clientConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	}

	return config, nil
}
