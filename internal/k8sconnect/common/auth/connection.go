package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ClusterConnectionModel represents the connection configuration for a Kubernetes cluster.
// This model is used by both the provider and resources to establish cluster connections.
type ClusterConnectionModel struct {
	Host                 types.String   `tfsdk:"host"`
	ClusterCACertificate types.String   `tfsdk:"cluster_ca_certificate"`
	Kubeconfig           types.String   `tfsdk:"kubeconfig"`
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
// It determines the appropriate method (inline or kubeconfig) and returns
// a configured rest.Config ready for creating a Kubernetes client.
func CreateRESTConfig(ctx context.Context, conn ClusterConnectionModel) (*rest.Config, error) {
	// Determine which connection method to use
	if !conn.Host.IsNull() {
		// Inline configuration
		return createInlineConfig(conn)
	} else if !conn.Kubeconfig.IsNull() {
		// Kubeconfig (raw content, use file() function to load from file)
		return createKubeconfigConfig(conn)
	}

	return nil, fmt.Errorf("no connection configuration provided")
}

// createInlineConfig creates a REST config from inline connection settings
func createInlineConfig(conn ClusterConnectionModel) (*rest.Config, error) {
	config := &rest.Config{
		Host: conn.Host.ValueString(),
	}

	// Set up warning handler to collect K8s API deprecation warnings
	config.WarningHandler = k8sclient.NewWarningCollector()

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

	// Handle CA certificate with auto-detection
	if !conn.ClusterCACertificate.IsNull() {
		caData, err := AutoDecodePEM(conn.ClusterCACertificate.ValueString(), "cluster_ca_certificate")
		if err != nil {
			return fmt.Errorf("failed to process cluster_ca_certificate: %w", err)
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

	// Auto-decode certificate
	certData, err := AutoDecodePEM(conn.ClientCertificate.ValueString(), "client_certificate")
	if err != nil {
		return fmt.Errorf("failed to process client_certificate: %w", err)
	}

	// Auto-decode key
	keyData, err := AutoDecodePEM(conn.ClientKey.ValueString(), "client_key")
	if err != nil {
		return fmt.Errorf("failed to process client_key: %w", err)
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

// createKubeconfigConfig creates a REST config from kubeconfig data
func createKubeconfigConfig(conn ClusterConnectionModel) (*rest.Config, error) {
	kubeconfigData := []byte(conn.Kubeconfig.ValueString())

	// Load kubeconfig to inspect contexts
	clientConfig, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	if !conn.Context.IsNull() {
		// Context explicitly provided - use it
		context := conn.Context.ValueString()
		if _, exists := clientConfig.Contexts[context]; !exists {
			return nil, fmt.Errorf("context %q not found in kubeconfig", context)
		}

		clientConfig.CurrentContext = context
		config, err := clientcmd.NewDefaultClientConfig(*clientConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, err
		}

		// Set up warning handler to collect K8s API deprecation warnings
		config.WarningHandler = k8sclient.NewWarningCollector()
		return config, nil
	}

	// No context provided - validate kubeconfig has safe defaults
	contextCount := len(clientConfig.Contexts)

	if contextCount == 0 {
		return nil, fmt.Errorf("kubeconfig contains no contexts")
	}

	if contextCount == 1 {
		// Only one context - safe to use it automatically
		for contextName := range clientConfig.Contexts {
			clientConfig.CurrentContext = contextName
			config, err := clientcmd.NewDefaultClientConfig(*clientConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
			if err != nil {
				return nil, err
			}

			// Set up warning handler to collect K8s API deprecation warnings
			config.WarningHandler = k8sclient.NewWarningCollector()
			return config, nil
		}
	}

	// Multiple contexts - require explicit selection for safety
	contextNames := make([]string, 0, len(clientConfig.Contexts))
	for name := range clientConfig.Contexts {
		contextNames = append(contextNames, name)
	}

	return nil, fmt.Errorf("kubeconfig contains %d contexts - you must explicitly specify which one to use via 'context' attribute.\n\nAvailable contexts:\n  - %s",
		contextCount, strings.Join(contextNames, "\n  - "))
}

// IsConnectionReady checks if the connection has all values known (not unknown).
// This determines if we can attempt to contact the cluster (for operations like dry-run).
// Null values are OK (means not using that auth method), but unknown values
// (like "known after apply" during bootstrap) mean we cannot connect yet.
func IsConnectionReady(obj types.Object) bool {
	// First check if the object itself is null/unknown
	if obj.IsNull() || obj.IsUnknown() {
		return false
	}

	// Convert to connection model to check individual fields
	conn, err := ObjectToConnectionModel(context.Background(), obj)
	if err != nil {
		return false
	}

	// Check all string fields - null is OK, unknown is not
	if conn.Host.IsUnknown() ||
		conn.ClusterCACertificate.IsUnknown() ||
		conn.Kubeconfig.IsUnknown() ||
		conn.Context.IsUnknown() ||
		conn.Token.IsUnknown() ||
		conn.ClientCertificate.IsUnknown() ||
		conn.ClientKey.IsUnknown() ||
		conn.ProxyURL.IsUnknown() {
		return false
	}

	// Check bool field
	if conn.Insecure.IsUnknown() {
		return false
	}

	// Check exec auth if present
	if conn.Exec != nil {
		if conn.Exec.APIVersion.IsUnknown() ||
			conn.Exec.Command.IsUnknown() {
			return false
		}

		// Check args array
		for _, arg := range conn.Exec.Args {
			if arg.IsUnknown() {
				return false
			}
		}

		// Check env vars map
		if conn.Exec.Env != nil {
			for _, value := range conn.Exec.Env {
				if value.IsUnknown() {
					return false
				}
			}
		}
	}

	// All fields are known (or null) - connection is ready
	return true
}
