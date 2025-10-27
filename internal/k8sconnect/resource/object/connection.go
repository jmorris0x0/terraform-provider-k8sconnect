package object

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// convertObjectToConnectionModel converts a Terraform object to our connection model
func (r *objectResource) convertObjectToConnectionModel(ctx context.Context, obj basetypes.ObjectValue) (auth.ClusterModel, error) {
	return auth.ObjectToConnectionModel(ctx, obj)
}

// convertConnectionToObject converts our connection model back to a Terraform object.
// This is used when we need to store the connection in state.
func (r *objectResource) convertConnectionToObject(ctx context.Context, conn auth.ClusterModel) (types.Object, error) {
	return auth.ConnectionToObject(ctx, conn)
}

// isConnectionReady checks if the connection has all values known (not unknown)
// This determines if we can attempt to contact the cluster for dry-run.
func (r *objectResource) isConnectionReady(obj types.Object) bool {
	return auth.IsConnectionReady(obj)
}
