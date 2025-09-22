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

// isConnectionReady checks if the connection object is ready (not null/unknown)
func (r *manifestResource) isConnectionReady(obj types.Object) bool {
	return !obj.IsNull() && !obj.IsUnknown()
}
