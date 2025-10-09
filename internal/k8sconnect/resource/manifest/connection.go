// internal/k8sconnect/resource/manifest/connection.go
package manifest

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

// createK8sClient creates a Kubernetes client from connection configuration.
// This is a thin wrapper around the common auth package.
func (r *manifestResource) createK8sClient(conn auth.ClusterConnectionModel) (k8sclient.K8sClient, error) {
	config, err := auth.CreateRESTConfig(context.Background(), conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client config: %w", err)
	}

	return k8sclient.NewDynamicK8sClient(config)
}

// convertObjectToConnectionModel converts a Terraform object to our connection model
func (r *manifestResource) convertObjectToConnectionModel(ctx context.Context, obj basetypes.ObjectValue) (auth.ClusterConnectionModel, error) {
	return auth.ObjectToConnectionModel(ctx, obj)
}

// convertConnectionToObject converts our connection model back to a Terraform object.
// This is used when we need to store the connection in state.
func (r *manifestResource) convertConnectionToObject(ctx context.Context, conn auth.ClusterConnectionModel) (types.Object, error) {
	return auth.ConnectionToObject(ctx, conn)
}

// isConnectionReady checks if the connection has all values known (not unknown)
// This determines if we can attempt to contact the cluster for dry-run.
// Null values are OK (means not using that auth method), but unknown values
// (like "known after apply" during bootstrap) mean we cannot connect yet.
func (r *manifestResource) isConnectionReady(obj types.Object) bool {
	// First check if the object itself is null/unknown
	if obj.IsNull() || obj.IsUnknown() {
		return false
	}

	// Convert to connection model to check individual fields
	conn, err := auth.ObjectToConnectionModel(context.Background(), obj)
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
