package factory

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

// SetupClient creates a K8s client from a cluster configuration object
// This is a common helper that handles the conversion from types.Object to auth.ClusterModel
// and then creates the client using the provided clientGetter
func SetupClient(ctx context.Context, cluster types.Object, clientGetter func(auth.ClusterModel) (k8sclient.K8sClient, error)) (k8sclient.K8sClient, error) {
	// Convert cluster object to connection model
	conn, err := auth.ObjectToConnectionModel(ctx, cluster)
	if err != nil {
		return nil, err
	}

	// Create and return client
	client, err := clientGetter(conn)
	if err != nil {
		return nil, err
	}

	return client, nil
}
