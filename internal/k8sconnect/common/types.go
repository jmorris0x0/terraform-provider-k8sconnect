// internal/k8sconnect/common/types.go
package common

import (
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/client"
)

// ConnectionConfig contains the connection resolver and client factory
// that are passed from the provider to resources
type ConnectionConfig struct {
	ConnectionResolver *auth.ConnectionResolver
	ClientFactory      client.ClientFactory
}
