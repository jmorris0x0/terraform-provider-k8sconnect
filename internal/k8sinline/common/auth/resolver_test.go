// internal/k8sinline/common/auth/resolver_test.go
package auth

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionResolver_ResolveConnection(t *testing.T) {
	tests := []struct {
		name           string
		providerConn   *ClusterConnectionModel
		resourceConn   *ClusterConnectionModel
		expectError    bool
		expectedSource string
	}{
		{
			name: "resource connection takes precedence",
			providerConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/provider/kubeconfig"),
				Context:        types.StringValue("provider-context"),
			},
			resourceConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/resource/kubeconfig"),
				Context:        types.StringValue("resource-context"),
			},
			expectError:    false,
			expectedSource: "resource",
		},
		{
			name: "falls back to provider connection",
			providerConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/provider/kubeconfig"),
				Context:        types.StringValue("provider-context"),
			},
			resourceConn:   nil,
			expectError:    false,
			expectedSource: "provider",
		},
		{
			name:         "no connection available",
			providerConn: nil,
			resourceConn: nil,
			expectError:  true,
		},
		{
			name: "empty resource connection falls back to provider",
			providerConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/provider/kubeconfig"),
			},
			resourceConn: &ClusterConnectionModel{
				// All fields null/empty
				KubeconfigFile: types.StringNull(),
				Host:           types.StringNull(),
			},
			expectError:    false,
			expectedSource: "provider",
		},
		{
			name: "host-based connection at resource level",
			providerConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/provider/kubeconfig"),
			},
			resourceConn: &ClusterConnectionModel{
				Host:                 types.StringValue("https://k8s.example.com"),
				ClusterCACertificate: types.StringValue("ca-cert"),
				Token:                types.StringValue("token"),
			},
			expectError:    false,
			expectedSource: "resource",
		},
		{
			name:         "exec-based connection",
			providerConn: nil,

			resourceConn: &ClusterConnectionModel{
				Exec: &ExecAuthModel{
					APIVersion: types.StringValue("client.authentication.k8s.io/v1beta1"),
					Command:    types.StringValue("aws"),
					Args: []types.String{
						types.StringValue("eks"),
						types.StringValue("get-token"),
					},
				},
			},
			expectError:    false,
			expectedSource: "resource",
		},
		{
			name:         "raw kubeconfig",
			providerConn: nil,
			resourceConn: &ClusterConnectionModel{
				KubeconfigRaw: types.StringValue("apiVersion: v1\nkind: Config\n..."),
				Context:       types.StringValue("my-context"),
			},
			expectError:    false,
			expectedSource: "resource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewConnectionResolver()
			if tt.providerConn != nil {
				resolver.SetProviderConnection(tt.providerConn)
			}

			conn, err := resolver.ResolveConnection(tt.resourceConn)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedSource, resolver.GetConnectionSource(tt.resourceConn))

				// Verify the correct connection was returned
				if tt.expectedSource == "resource" {
					assert.Equal(t, tt.resourceConn.KubeconfigFile, conn.KubeconfigFile)
				} else if tt.expectedSource == "provider" {
					assert.Equal(t, tt.providerConn.KubeconfigFile, conn.KubeconfigFile)
				}
			}
		})
	}
}

func TestConnectionResolver_CompareConnections(t *testing.T) {
	tests := []struct {
		name     string
		conn1    ClusterConnectionModel
		conn2    ClusterConnectionModel
		expected bool
	}{
		{
			name: "same host connections",
			conn1: ClusterConnectionModel{
				Host: types.StringValue("https://k8s.example.com"),
			},
			conn2: ClusterConnectionModel{
				Host: types.StringValue("https://k8s.example.com"),
			},
			expected: true,
		},
		{
			name: "different host connections",
			conn1: ClusterConnectionModel{
				Host: types.StringValue("https://k8s1.example.com"),
			},
			conn2: ClusterConnectionModel{
				Host: types.StringValue("https://k8s2.example.com"),
			},
			expected: false,
		},
		{
			name: "same kubeconfig file and context",
			conn1: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("~/.kube/config"),
				Context:        types.StringValue("prod"),
			},
			conn2: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("~/.kube/config"),
				Context:        types.StringValue("prod"),
			},
			expected: true,
		},
		{
			name: "same kubeconfig file, different context",
			conn1: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("~/.kube/config"),
				Context:        types.StringValue("prod"),
			},
			conn2: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("~/.kube/config"),
				Context:        types.StringValue("staging"),
			},
			expected: false,
		},
		{
			name: "different connection types",
			conn1: ClusterConnectionModel{
				Host: types.StringValue("https://k8s.example.com"),
			},
			conn2: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("~/.kube/config"),
			},
			expected: false,
		},
		{
			name: "raw kubeconfig same context",
			conn1: ClusterConnectionModel{
				KubeconfigRaw: types.StringValue("config1"),
				Context:       types.StringValue("prod"),
			},
			conn2: ClusterConnectionModel{
				KubeconfigRaw: types.StringValue("config2"),
				Context:       types.StringValue("prod"),
			},
			expected: true, // Can't compare raw content, assume same if context matches
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewConnectionResolver()
			result := resolver.CompareConnections(tt.conn1, tt.conn2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConnectionResolver_ValidateConnectionChange(t *testing.T) {
	tests := []struct {
		name        string
		oldConn     ClusterConnectionModel
		newConn     ClusterConnectionModel
		expectError bool
	}{
		{
			name: "same cluster allowed",
			oldConn: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("~/.kube/config"),
				Context:        types.StringValue("prod"),
			},
			newConn: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("~/.kube/config"),
				Context:        types.StringValue("prod"),
			},
			expectError: false,
		},
		{
			name: "different cluster blocked",
			oldConn: ClusterConnectionModel{
				Host: types.StringValue("https://k8s1.example.com"),
			},
			newConn: ClusterConnectionModel{
				Host: types.StringValue("https://k8s2.example.com"),
			},
			expectError: true,
		},
		{
			name: "changing from kubeconfig to host blocked",
			oldConn: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("~/.kube/config"),
				Context:        types.StringValue("prod"),
			},
			newConn: ClusterConnectionModel{
				Host: types.StringValue("https://k8s.example.com"),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewConnectionResolver()
			err := resolver.ValidateConnectionChange(tt.oldConn, tt.newConn)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "connection change would move resource to a different cluster")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConnectionResolver_MergeConnectionDefaults(t *testing.T) {
	tests := []struct {
		name         string
		providerConn *ClusterConnectionModel
		resourceConn *ClusterConnectionModel
		expected     ClusterConnectionModel
	}{
		{
			name: "resource overrides all provider settings",
			providerConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/provider/kubeconfig"),
				Context:        types.StringValue("provider-context"),
				Insecure:       types.BoolValue(false),
			},
			resourceConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/resource/kubeconfig"),
				Context:        types.StringValue("resource-context"),
				Insecure:       types.BoolValue(true),
			},
			expected: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/resource/kubeconfig"),
				Context:        types.StringValue("resource-context"),
				Insecure:       types.BoolValue(true),
			},
		},
		{
			name: "resource inherits unset fields from provider",
			providerConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/provider/kubeconfig"),
				Context:        types.StringValue("provider-context"),
				Insecure:       types.BoolValue(true),
				ProxyURL:       types.StringValue("http://proxy:8080"),
			},
			resourceConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/resource/kubeconfig"),
				// Context not set - should inherit from provider
				// Insecure not set - should inherit from provider
			},
			expected: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/resource/kubeconfig"),
				Context:        types.StringValue("provider-context"),  // Inherited
				Insecure:       types.BoolValue(true),                  // Inherited
				ProxyURL:       types.StringValue("http://proxy:8080"), // Inherited
			},
		},
		{
			name: "auth methods are not merged",
			providerConn: &ClusterConnectionModel{
				Host:  types.StringValue("https://provider.example.com"),
				Token: types.StringValue("provider-token"),
			},
			resourceConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/resource/kubeconfig"),
			},
			expected: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/resource/kubeconfig"),
				// Host and Token are NOT inherited
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewConnectionResolver()
			if tt.providerConn != nil {
				resolver.SetProviderConnection(tt.providerConn)
			}

			result := resolver.MergeConnectionDefaults(tt.resourceConn)

			// Compare relevant fields
			if !tt.expected.KubeconfigFile.IsNull() {
				assert.Equal(t, tt.expected.KubeconfigFile, result.KubeconfigFile)
			}
			if !tt.expected.Context.IsNull() {
				assert.Equal(t, tt.expected.Context, result.Context)
			}
			if !tt.expected.Insecure.IsNull() {
				assert.Equal(t, tt.expected.Insecure, result.Insecure)
			}
			if !tt.expected.ProxyURL.IsNull() {
				assert.Equal(t, tt.expected.ProxyURL, result.ProxyURL)
			}
		})
	}
}

func TestConnectionResolver_GetConnectionSourceInfo(t *testing.T) {
	tests := []struct {
		name         string
		providerConn *ClusterConnectionModel
		resourceConn *ClusterConnectionModel
		expected     ConnectionSourceInfo
	}{
		{
			name: "successful resource connection",
			providerConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/provider/kubeconfig"),
			},
			resourceConn: &ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/resource/kubeconfig"),
			},
			expected: ConnectionSourceInfo{
				Source:          "resource",
				HasResourceConn: true,
				HasProviderConn: true,
			},
		},
		{
			name:         "no connection available",
			providerConn: nil,
			resourceConn: nil,
			expected: ConnectionSourceInfo{
				Source:          "none",
				HasResourceConn: false,
				HasProviderConn: false,
				ValidationErrors: []string{
					"no cluster connection specified: either configure the provider or set cluster_connection on the resource",
					"no connection configuration found at resource or provider level",
				},
			},
		},
		{
			name:         "invalid resource connection",
			providerConn: nil,
			resourceConn: &ClusterConnectionModel{
				// Empty connection block
			},
			expected: ConnectionSourceInfo{
				Source:          "none",
				HasResourceConn: false,
				HasProviderConn: false,
				ValidationErrors: []string{
					"no cluster connection specified: either configure the provider or set cluster_connection on the resource",
					"no connection configuration found at resource or provider level",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewConnectionResolver()
			if tt.providerConn != nil {
				resolver.SetProviderConnection(tt.providerConn)
			}

			info := resolver.GetConnectionSourceInfo(tt.resourceConn)

			assert.Equal(t, tt.expected.Source, info.Source)
			assert.Equal(t, tt.expected.HasResourceConn, info.HasResourceConn)
			assert.Equal(t, tt.expected.HasProviderConn, info.HasProviderConn)
			assert.Equal(t, len(tt.expected.ValidationErrors), len(info.ValidationErrors))
		})
	}
}
