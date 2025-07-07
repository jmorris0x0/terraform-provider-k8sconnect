// internal/k8sinline/resource/manifest/connection.go
package manifest

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/jmorris0x0/terraform-provider-k8sinline/internal/k8sinline/k8sclient"
)

// CreateK8sClientFromConnection creates a K8sClient from connection model (exported for provider use)
func CreateK8sClientFromConnection(conn ClusterConnectionModel) (k8sclient.K8sClient, error) {
	r := &manifestResource{}
	return r.createK8sClient(conn)
}

// createK8sClient creates a K8sClient from cluster connection configuration
func (r *manifestResource) createK8sClient(conn ClusterConnectionModel) (k8sclient.K8sClient, error) {
	// Determine connection mode
	hasInline := !conn.Host.IsNull() || !conn.ClusterCACertificate.IsNull()
	hasFile := !conn.KubeconfigFile.IsNull()
	hasRaw := !conn.KubeconfigRaw.IsNull()

	modeCount := 0
	if hasInline {
		modeCount++
	}
	if hasFile {
		modeCount++
	}
	if hasRaw {
		modeCount++
	}

	if modeCount == 0 {
		return nil, fmt.Errorf("must specify exactly one of: inline connection, kubeconfig_file, or kubeconfig_raw")
	}
	if modeCount > 1 {
		return nil, fmt.Errorf("cannot specify multiple connection modes")
	}

	// Create REST config
	var config *rest.Config
	var err error

	switch {
	case hasInline:
		config, err = r.createInlineConfig(conn)
	case hasFile:
		config, err = r.createFileConfig(conn)
	case hasRaw:
		config, err = r.createRawConfig(conn)
	default:
		return nil, fmt.Errorf("no valid connection mode specified")
	}

	if err != nil {
		return nil, err
	}

	// Use simple dynamic client
	return k8sclient.NewDynamicK8sClient(config)
}

// createInlineConfig creates a REST config from inline connection settings
func (r *manifestResource) createInlineConfig(conn ClusterConnectionModel) (*rest.Config, error) {
	if conn.Host.IsNull() {
		return nil, fmt.Errorf("host is required for inline connection")
	}

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
func (r *manifestResource) createFileConfig(conn ClusterConnectionModel) (*rest.Config, error) {
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
func (r *manifestResource) createRawConfig(conn ClusterConnectionModel) (*rest.Config, error) {
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

// convertObjectToConnectionModel converts types.Object to ClusterConnectionModel
func (r *manifestResource) convertObjectToConnectionModel(ctx context.Context, obj types.Object) (ClusterConnectionModel, error) {
	if obj.IsNull() {
		return ClusterConnectionModel{}, fmt.Errorf("cluster connection is null")
	}

	if obj.IsUnknown() {
		return ClusterConnectionModel{}, fmt.Errorf("cluster connection contains unknown values")
	}

	var conn ClusterConnectionModel
	diags := obj.As(ctx, &conn, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return ClusterConnectionModel{}, fmt.Errorf("failed to convert cluster connection: %s", diags)
	}

	return conn, nil
}

// isConnectionReady checks if the connection object is ready (not null/unknown)
func (r *manifestResource) isConnectionReady(obj types.Object) bool {
	return !obj.IsNull() && !obj.IsUnknown()
}

// convertConnectionModelToObject converts ClusterConnectionModel to types.Object
func (r *manifestResource) convertConnectionModelToObject(ctx context.Context, conn ClusterConnectionModel) (types.Object, error) {
	// Define the object type based on our schema
	objectType := types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"host":                   types.StringType,
			"cluster_ca_certificate": types.StringType,
			"kubeconfig_file":        types.StringType,
			"kubeconfig_raw":         types.StringType,
			"context":                types.StringType,
			"token":                  types.StringType,
			"client_certificate":     types.StringType,
			"client_key":             types.StringType,
			"insecure":               types.BoolType,
			"proxy_url":              types.StringType,
			"exec": types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"api_version": types.StringType,
					"command":     types.StringType,
					"args":        types.ListType{ElemType: types.StringType},
					"env":         types.MapType{ElemType: types.StringType},
				},
			},
		},
	}

	obj, diags := types.ObjectValueFrom(ctx, objectType.AttrTypes, conn)
	if diags.HasError() {
		return types.ObjectNull(objectType.AttrTypes), fmt.Errorf("failed to convert connection model to object: %s", diags)
	}

	return obj, nil
}
