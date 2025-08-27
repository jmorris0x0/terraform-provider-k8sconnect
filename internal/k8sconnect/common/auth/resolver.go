// internal/k8sconnect/common/auth/resolver.go
package auth

import (
	"fmt"
)

// ConnectionResolver handles provider and resource level auth resolution
type ConnectionResolver struct {
	providerConfig *ClusterConnectionModel
}

// NewConnectionResolver creates a new ConnectionResolver
func NewConnectionResolver() *ConnectionResolver {
	return &ConnectionResolver{}
}

// SetProviderConnection sets the provider-level connection configuration
func (r *ConnectionResolver) SetProviderConnection(conn *ClusterConnectionModel) {
	r.providerConfig = conn
}

// ResolveConnection determines the effective connection for a resource
// Priority: 1. Resource-level connection, 2. Provider-level connection
func (r *ConnectionResolver) ResolveConnection(resourceConn *ClusterConnectionModel) (ClusterConnectionModel, error) {
	// 1. Resource connection takes precedence if provided
	if resourceConn != nil && r.hasValidConnection(resourceConn) {
		return *resourceConn, nil
	}

	// 2. Fall back to provider connection
	if r.providerConfig != nil && r.hasValidConnection(r.providerConfig) {
		return *r.providerConfig, nil
	}

	// 3. No valid connection available
	return ClusterConnectionModel{}, fmt.Errorf(
		"no cluster connection specified: either configure the provider or set cluster_connection on the resource")
}

// hasValidConnection checks if a connection model has at least one auth method configured
func (r *ConnectionResolver) hasValidConnection(conn *ClusterConnectionModel) bool {
	if conn == nil {
		return false
	}

	// Check if any connection method is configured
	return r.hasHostConnection(conn) ||
		r.hasKubeconfigFile(conn) ||
		r.hasKubeconfigRaw(conn) ||
		r.hasExecConfig(conn)
}

// hasHostConnection checks if host-based connection is configured
func (r *ConnectionResolver) hasHostConnection(conn *ClusterConnectionModel) bool {
	return !conn.Host.IsNull() && !conn.Host.IsUnknown() && conn.Host.ValueString() != ""
}

// hasKubeconfigFile checks if kubeconfig file is configured
func (r *ConnectionResolver) hasKubeconfigFile(conn *ClusterConnectionModel) bool {
	return !conn.KubeconfigFile.IsNull() && !conn.KubeconfigFile.IsUnknown() && conn.KubeconfigFile.ValueString() != ""
}

// hasKubeconfigRaw checks if raw kubeconfig is configured
func (r *ConnectionResolver) hasKubeconfigRaw(conn *ClusterConnectionModel) bool {
	return !conn.KubeconfigRaw.IsNull() && !conn.KubeconfigRaw.IsUnknown() && conn.KubeconfigRaw.ValueString() != ""
}

// hasExecConfig checks if exec configuration is provided
func (r *ConnectionResolver) hasExecConfig(conn *ClusterConnectionModel) bool {
	return conn.Exec != nil && r.hasValidExec(conn.Exec)
}

// hasValidExec validates exec configuration
func (r *ConnectionResolver) hasValidExec(exec *ExecAuthModel) bool {
	return exec != nil &&
		!exec.Command.IsNull() && !exec.Command.IsUnknown() &&
		exec.Command.ValueString() != ""
}

// IsResourceConnectionSpecified checks if a resource has any connection configuration
// This is useful for determining if we should record the effective connection in state
func (r *ConnectionResolver) IsResourceConnectionSpecified(resourceConn *ClusterConnectionModel) bool {
	return resourceConn != nil && r.hasValidConnection(resourceConn)
}

// GetConnectionSource returns a string indicating where the connection came from
// Useful for logging and debugging
func (r *ConnectionResolver) GetConnectionSource(resourceConn *ClusterConnectionModel) string {
	if r.IsResourceConnectionSpecified(resourceConn) {
		return "resource"
	}
	if r.providerConfig != nil && r.hasValidConnection(r.providerConfig) {
		return "provider"
	}
	return "none"
}

// CompareConnections checks if two connections target the same cluster
// Returns true if they appear to be the same cluster, false otherwise
func (r *ConnectionResolver) CompareConnections(conn1, conn2 ClusterConnectionModel) bool {
	// If both have host, compare hosts
	if r.hasHostConnection(&conn1) && r.hasHostConnection(&conn2) {
		return conn1.Host.ValueString() == conn2.Host.ValueString()
	}

	// If both have kubeconfig file, compare file and context
	if r.hasKubeconfigFile(&conn1) && r.hasKubeconfigFile(&conn2) {
		sameFile := conn1.KubeconfigFile.ValueString() == conn2.KubeconfigFile.ValueString()
		sameContext := conn1.Context.ValueString() == conn2.Context.ValueString()
		return sameFile && sameContext
	}

	// If both have raw kubeconfig, compare context (can't easily compare raw content)
	if r.hasKubeconfigRaw(&conn1) && r.hasKubeconfigRaw(&conn2) {
		return conn1.Context.ValueString() == conn2.Context.ValueString()
	}

	// Different connection types or can't determine
	return false
}

// ValidateConnectionChange checks if changing from one connection to another is safe
// Returns an error if the change appears to target a different cluster
func (r *ConnectionResolver) ValidateConnectionChange(oldConn, newConn ClusterConnectionModel) error {
	// If connections appear to be the same cluster, allow the change
	if r.CompareConnections(oldConn, newConn) {
		return nil
	}

	// Build helpful error message
	oldSource := r.getConnectionDescription(&oldConn)
	newSource := r.getConnectionDescription(&newConn)

	return fmt.Errorf(
		"connection change would move resource to a different cluster\n"+
			"  Current: %s\n"+
			"  New: %s\n"+
			"Resources cannot be moved between clusters. "+
			"Delete and recreate the resource if you need to change clusters.",
		oldSource, newSource)
}

// getConnectionDescription returns a human-readable description of a connection
func (r *ConnectionResolver) getConnectionDescription(conn *ClusterConnectionModel) string {
	if r.hasHostConnection(conn) {
		return fmt.Sprintf("host=%s", conn.Host.ValueString())
	}
	if r.hasKubeconfigFile(conn) {
		ctx := conn.Context.ValueString()
		if ctx == "" {
			ctx = "current-context"
		}
		return fmt.Sprintf("kubeconfig=%s, context=%s", conn.KubeconfigFile.ValueString(), ctx)
	}
	if r.hasKubeconfigRaw(conn) {
		ctx := conn.Context.ValueString()
		if ctx == "" {
			ctx = "current-context"
		}
		return fmt.Sprintf("raw kubeconfig, context=%s", ctx)
	}
	if r.hasExecConfig(conn) {
		return fmt.Sprintf("exec command=%s", conn.Exec.Command.ValueString())
	}
	return "no connection"
}

// MergeConnectionDefaults applies provider defaults to a resource connection
// This allows resources to override specific fields while inheriting others
func (r *ConnectionResolver) MergeConnectionDefaults(resourceConn *ClusterConnectionModel) ClusterConnectionModel {
	if r.providerConfig == nil || resourceConn == nil {
		if resourceConn != nil {
			return *resourceConn
		}
		if r.providerConfig != nil {
			return *r.providerConfig
		}
		return ClusterConnectionModel{}
	}

	// Start with a copy of the resource connection
	merged := *resourceConn

	// Apply provider defaults for unset fields
	if merged.KubeconfigFile.IsNull() && !r.providerConfig.KubeconfigFile.IsNull() {
		merged.KubeconfigFile = r.providerConfig.KubeconfigFile
	}

	if merged.Context.IsNull() && !r.providerConfig.Context.IsNull() {
		merged.Context = r.providerConfig.Context
	}

	if merged.Insecure.IsNull() && !r.providerConfig.Insecure.IsNull() {
		merged.Insecure = r.providerConfig.Insecure
	}

	if merged.ProxyURL.IsNull() && !r.providerConfig.ProxyURL.IsNull() {
		merged.ProxyURL = r.providerConfig.ProxyURL
	}

	// Note: We don't merge host, certs, tokens, or exec as these are complete auth methods

	return merged
}

// ConnectionSourceInfo provides detailed information about connection resolution
type ConnectionSourceInfo struct {
	Source           string // "resource", "provider", or "none"
	HasResourceConn  bool
	HasProviderConn  bool
	EffectiveConn    ClusterConnectionModel
	ValidationErrors []string
}

// GetConnectionSourceInfo provides detailed information about connection resolution
// Useful for debugging and error messages
func (r *ConnectionResolver) GetConnectionSourceInfo(resourceConn *ClusterConnectionModel) ConnectionSourceInfo {
	info := ConnectionSourceInfo{
		HasResourceConn: r.IsResourceConnectionSpecified(resourceConn),
		HasProviderConn: r.providerConfig != nil && r.hasValidConnection(r.providerConfig),
	}

	// Try to resolve connection
	effectiveConn, err := r.ResolveConnection(resourceConn)
	if err == nil {
		info.EffectiveConn = effectiveConn
		info.Source = r.GetConnectionSource(resourceConn)
	} else {
		info.Source = "none"
		info.ValidationErrors = append(info.ValidationErrors, err.Error())
	}

	// Add specific validation errors
	if info.HasResourceConn && !r.hasValidConnection(resourceConn) {
		info.ValidationErrors = append(info.ValidationErrors,
			"resource has cluster_connection block but no valid authentication method is configured")
	}

	if !info.HasResourceConn && !info.HasProviderConn {
		info.ValidationErrors = append(info.ValidationErrors,
			"no connection configuration found at resource or provider level")
	}

	return info
}
