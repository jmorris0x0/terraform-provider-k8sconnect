// internal/k8sinline/common/auth/connection_test.go
package auth

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateRESTConfig_InlineToken(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:                 types.StringValue("https://test.example.com"),
		ClusterCACertificate: types.StringValue(base64.StdEncoding.EncodeToString([]byte("test-ca-cert"))),
		Token:                types.StringValue("test-bearer-token"),
	}

	config, err := CreateRESTConfig(context.Background(), conn)

	require.NoError(t, err)
	assert.Equal(t, "https://test.example.com", config.Host)
	assert.Equal(t, "test-bearer-token", config.BearerToken)
	assert.Equal(t, []byte("test-ca-cert"), config.TLSClientConfig.CAData)
}

func TestCreateRESTConfig_InlineClientCert(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:                 types.StringValue("https://test.example.com"),
		ClusterCACertificate: types.StringValue(base64.StdEncoding.EncodeToString([]byte("test-ca"))),
		ClientCertificate:    types.StringValue(base64.StdEncoding.EncodeToString([]byte("test-cert"))),
		ClientKey:            types.StringValue(base64.StdEncoding.EncodeToString([]byte("test-key"))),
	}

	config, err := CreateRESTConfig(context.Background(), conn)

	require.NoError(t, err)
	assert.Equal(t, []byte("test-cert"), config.TLSClientConfig.CertData)
	assert.Equal(t, []byte("test-key"), config.TLSClientConfig.KeyData)
}

func TestCreateRESTConfig_InlineInsecure(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:     types.StringValue("https://test.example.com"),
		Token:    types.StringValue("test-token"),
		Insecure: types.BoolValue(true),
	}

	config, err := CreateRESTConfig(context.Background(), conn)

	require.NoError(t, err)
	assert.True(t, config.TLSClientConfig.Insecure)
}

func TestCreateRESTConfig_InlineWithProxy(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:     types.StringValue("https://test.example.com"),
		Token:    types.StringValue("test-token"),
		ProxyURL: types.StringValue("http://proxy.example.com:8080"),
		Insecure: types.BoolValue(true),
	}

	config, err := CreateRESTConfig(context.Background(), conn)

	require.NoError(t, err)
	assert.NotNil(t, config.Proxy)
}

func TestCreateRESTConfig_InlineExecAuth(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:                 types.StringValue("https://test.example.com"),
		ClusterCACertificate: types.StringValue(base64.StdEncoding.EncodeToString([]byte("test-ca"))),
		Exec: &ExecAuthModel{
			APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
			Command:    types.StringValue("aws"),
			Args: []types.String{
				types.StringValue("eks"),
				types.StringValue("get-token"),
				types.StringValue("--cluster-name"),
				types.StringValue("test-cluster"),
			},
			Env: map[string]types.String{
				"AWS_PROFILE": types.StringValue("test-profile"),
			},
		},
	}

	config, err := CreateRESTConfig(context.Background(), conn)

	require.NoError(t, err)
	assert.NotNil(t, config.ExecProvider)
	assert.Equal(t, "aws", config.ExecProvider.Command)
	assert.Equal(t, []string{"eks", "get-token", "--cluster-name", "test-cluster"}, config.ExecProvider.Args)
	assert.Len(t, config.ExecProvider.Env, 1)
	assert.Equal(t, "AWS_PROFILE", config.ExecProvider.Env[0].Name)
	assert.Equal(t, "test-profile", config.ExecProvider.Env[0].Value)
}

func TestCreateRESTConfig_KubeconfigRaw(t *testing.T) {
	// Minimal valid kubeconfig
	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test.example.com
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token`

	conn := ClusterConnectionModel{
		KubeconfigRaw: types.StringValue(kubeconfig),
	}

	config, err := CreateRESTConfig(context.Background(), conn)

	require.NoError(t, err)
	assert.NotNil(t, config)
	// The actual values will be set by the kubeconfig parser
}

func TestCreateRESTConfig_NoConnectionMethod(t *testing.T) {
	conn := ClusterConnectionModel{
		// All connection methods are null
	}

	_, err := CreateRESTConfig(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no connection configuration provided")
}

func TestCreateRESTConfig_InlineNoAuth(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:                 types.StringValue("https://test.example.com"),
		ClusterCACertificate: types.StringValue(base64.StdEncoding.EncodeToString([]byte("test-ca"))),
		// No auth method provided
	}

	_, err := CreateRESTConfig(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no authentication method specified")
}

func TestCreateRESTConfig_InlineNoCACert(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:  types.StringValue("https://test.example.com"),
		Token: types.StringValue("test-token"),
		// No CA cert and insecure not set
	}

	_, err := CreateRESTConfig(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster_ca_certificate is required for secure connections")
}

func TestCreateRESTConfig_InvalidBase64(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:                 types.StringValue("https://test.example.com"),
		ClusterCACertificate: types.StringValue("not-valid-base64!@#$"),
		Token:                types.StringValue("test-token"),
	}

	_, err := CreateRESTConfig(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode cluster_ca_certificate")
}

func TestCreateRESTConfig_InvalidProxyURL(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:     types.StringValue("https://test.example.com"),
		Token:    types.StringValue("test-token"),
		ProxyURL: types.StringValue(":::invalid-url"),
		Insecure: types.BoolValue(true),
	}

	_, err := CreateRESTConfig(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse proxy_url")
}

// Validation tests

func TestValidateConnection_NoMode(t *testing.T) {
	conn := ClusterConnectionModel{
		// All modes are null
	}

	err := ValidateConnection(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no cluster connection mode specified")
}

func TestValidateConnection_MultipleModes(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:           types.StringValue("https://test.example.com"),
		KubeconfigFile: types.StringValue("/path/to/kubeconfig"),
	}

	err := ValidateConnection(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple cluster connection modes specified")
}

func TestValidateConnection_InlineValid(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:                 types.StringValue("https://test.example.com"),
		ClusterCACertificate: types.StringValue("base64-ca-cert"),
		Token:                types.StringValue("test-token"),
	}

	err := ValidateConnection(context.Background(), conn)

	assert.NoError(t, err)
}

func TestValidateConnection_ExecMissingCommand(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:                 types.StringValue("https://test.example.com"),
		ClusterCACertificate: types.StringValue("base64-ca-cert"),
		Exec: &ExecAuthModel{
			APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
			// Command is missing
		},
	}

	err := ValidateConnection(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exec authentication requires 'command'")
}

func TestValidateConnection_ClientCertWithoutKey(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:                 types.StringValue("https://test.example.com"),
		ClusterCACertificate: types.StringValue("base64-ca-cert"),
		ClientCertificate:    types.StringValue("base64-cert"),
		// ClientKey is missing
	}

	err := ValidateConnection(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client_certificate requires client_key")
}

func TestValidateConnection_ClientKeyWithoutCert(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:                 types.StringValue("https://test.example.com"),
		ClusterCACertificate: types.StringValue("base64-ca-cert"),
		ClientKey:            types.StringValue("base64-key"),
		// ClientCertificate is missing
	}

	err := ValidateConnection(context.Background(), conn)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client_key requires client_certificate")
}

func TestValidateConnectionWithUnknowns_SkipsWhenUnknown(t *testing.T) {
	conn := ClusterConnectionModel{
		Host:           types.StringUnknown(),
		KubeconfigFile: types.StringValue("/path/to/kubeconfig"),
	}

	// Should not error because host is unknown
	err := ValidateConnectionWithUnknowns(context.Background(), conn)

	assert.NoError(t, err)
}
