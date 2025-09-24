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
	config := &rest.Config{
		Host: conn.Host.ValueString(),
	}

	// Configure TLS
	if err := configureTLS(config, conn); err != nil {
		return nil, err
	}

	// Configure authentication
	if err := configureAuth(config, conn); err != nil {
		return nil, err
	}

	// Configure proxy if present
	if err := configureProxy(config, conn); err != nil {
		return nil, err
	}

	return config, nil
}

// configureTLS handles all TLS configuration
func configureTLS(config *rest.Config, conn ClusterConnectionModel) error {
	// Handle insecure mode
	if !conn.Insecure.IsNull() && conn.Insecure.ValueBool() {
		config.TLSClientConfig.Insecure = true
		return nil
	}

	// Handle CA certificate
	if !conn.ClusterCACertificate.IsNull() {
		caData, err := base64.StdEncoding.DecodeString(conn.ClusterCACertificate.ValueString())
		if err != nil {
			return fmt.Errorf("failed to decode cluster_ca_certificate: %w", err)
		}
		config.TLSClientConfig.CAData = caData
		return nil
	}

	// Neither insecure nor CA cert provided
	return fmt.Errorf("cluster_ca_certificate is required for secure connections (or set insecure=true)")
}

// configureAuth handles all authentication methods
func configureAuth(config *rest.Config, conn ClusterConnectionModel) error {
	authMethods := 0

	// Token auth
	if !conn.Token.IsNull() {
		config.BearerToken = conn.Token.ValueString()
		authMethods++
	}

	// Client certificate auth
	if err := configureClientCerts(config, conn); err != nil {
		return err
	} else if config.TLSClientConfig.CertData != nil {
		authMethods++
	}

	// Exec auth
	if err := configureExecAuth(config, conn); err != nil {
		return err
	} else if config.ExecProvider != nil {
		authMethods++
	}

	if authMethods == 0 {
		return fmt.Errorf("no authentication method specified: provide token, client certificates, or exec configuration")
	}

	return nil
}

// configureClientCerts handles client certificate authentication
func configureClientCerts(config *rest.Config, conn ClusterConnectionModel) error {
	// Both must be present or both absent
	hasCert := !conn.ClientCertificate.IsNull()
	hasKey := !conn.ClientKey.IsNull()

	if hasCert != hasKey {
		return fmt.Errorf("client_certificate and client_key must both be provided for client cert auth")
	}

	if !hasCert {
		return nil // No client cert auth
	}

	certData, err := base64.StdEncoding.DecodeString(conn.ClientCertificate.ValueString())
	if err != nil {
		return fmt.Errorf("failed to decode client_certificate: %w", err)
	}

	keyData, err := base64.StdEncoding.DecodeString(conn.ClientKey.ValueString())
	if err != nil {
		return fmt.Errorf("failed to decode client_key: %w", err)
	}

	config.TLSClientConfig.CertData = certData
	config.TLSClientConfig.KeyData = keyData
	return nil
}

// configureExecAuth handles exec authentication
func configureExecAuth(config *rest.Config, conn ClusterConnectionModel) error {
	if conn.Exec == nil || conn.Exec.APIVersion.IsNull() {
		return nil // No exec auth
	}

	// Build args array
	args := make([]string, len(conn.Exec.Args))
	for i, arg := range conn.Exec.Args {
		args[i] = arg.ValueString()
	}

	// Build env vars array
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

	return nil
}

// configureProxy sets up proxy configuration
func configureProxy(config *rest.Config, conn ClusterConnectionModel) error {
	if !conn.ProxyURL.IsNull() {
		proxyURL, err := url.Parse(conn.ProxyURL.ValueString())
		if err != nil {
			return fmt.Errorf("failed to parse proxy_url: %w", err)
		}
		config.Proxy = http.ProxyURL(proxyURL)
	}
	return nil
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
