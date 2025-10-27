package auth

import (
	"context"
	"fmt"
)

// ValidateConnection ensures exactly one connection mode is specified and all required fields are present.
func ValidateConnection(ctx context.Context, conn ClusterModel) error {
	// Validate connection modes
	if err := validateConnectionModes(conn); err != nil {
		return err
	}

	// Validate client certificate configuration BEFORE checking inline auth
	// This ensures we catch mismatched cert/key errors first
	if err := validateClientCertificates(conn); err != nil {
		return err
	}

	// Additional validation for inline mode
	if hasInlineMode(conn) {
		if err := validateInlineConnection(conn); err != nil {
			return err
		}
	}

	// Validate exec auth if present
	if conn.Exec != nil && !conn.Exec.APIVersion.IsNull() {
		if err := validateExecAuth(conn.Exec); err != nil {
			return err
		}
	}

	return nil
}

// validateConnectionModes ensures exactly one connection mode is specified
func validateConnectionModes(conn ClusterModel) error {
	modes := countActiveModes(conn)

	if modes == 0 {
		return fmt.Errorf("no connection mode specified\n\n" +
			"Must specify exactly one connection mode:\n" +
			"• Inline: Provide 'host' and either 'cluster_ca_certificate' or 'insecure = true'\n" +
			"• Kubeconfig raw: Provide 'kubeconfig' content")
	}

	if modes > 1 {
		conflictingModes := buildMultipleModeError(conn)
		return fmt.Errorf("multiple connection modes specified\n\n%s", conflictingModes)
	}

	return nil
}

// countActiveModes counts how many connection modes are configured
func countActiveModes(conn ClusterModel) int {
	modes := 0
	if hasInlineMode(conn) {
		modes++
	}
	if !conn.Kubeconfig.IsNull() {
		modes++
	}
	return modes
}

// hasInlineMode checks if inline connection fields are present
func hasInlineMode(conn ClusterModel) bool {
	return !conn.Host.IsNull() || !conn.ClusterCACertificate.IsNull()
}

// buildMultipleModeError creates error message for multiple modes
func buildMultipleModeError(conn ClusterModel) string {
	conflictingModes := []string{}
	if hasInlineMode(conn) {
		conflictingModes = append(conflictingModes, "inline (host + cluster_ca_certificate)")
	}
	if !conn.Kubeconfig.IsNull() {
		conflictingModes = append(conflictingModes, "kubeconfig")
	}

	return fmt.Sprintf("Only one connection mode can be specified. Found: %v\n\n"+
		"Choose ONE of:\n"+
		"• Remove 'kubeconfig' to use inline mode\n"+
		"• Remove inline fields ('host', 'cluster_ca_certificate') to use kubeconfig",
		conflictingModes)
}

// validateInlineConnection validates inline connection requirements
func validateInlineConnection(conn ClusterModel) error {
	// Check host is present
	if conn.Host.IsNull() {
		return fmt.Errorf("inline connection incomplete\n\n" +
			"Inline connections require 'host' to be specified.")
	}

	// Check CA certificate OR insecure flag is present
	if !hasValidTLSConfig(conn) {
		return fmt.Errorf("inline connection incomplete\n\n" +
			"Inline connections require either 'cluster_ca_certificate' or 'insecure = true'.\n\n" +
			"For production use, provide the cluster CA certificate:\n" +
			"• cluster_ca_certificate: Base64-encoded or PEM-format CA certificate\n\n" +
			"For development/testing with self-signed certificates:\n" +
			"• insecure = true (skips TLS verification - NOT recommended for production)")
	}

	// Validate authentication is provided
	if !hasAuthentication(conn) {
		return fmt.Errorf("inline connection missing authentication\n\n" +
			"Inline connections require at least one authentication method:\n" +
			"• Bearer token: Set 'token'\n" +
			"• Client certificates: Set both 'client_certificate' and 'client_key'\n" +
			"• Exec auth: Configure the 'exec' block")
	}

	return nil
}

// hasValidTLSConfig checks if TLS is properly configured (CA cert or insecure flag)
func hasValidTLSConfig(conn ClusterModel) bool {
	// Either have CA certificate OR insecure=true
	hasCA := !conn.ClusterCACertificate.IsNull()
	hasInsecure := !conn.Insecure.IsNull() && conn.Insecure.ValueBool()
	return hasCA || hasInsecure
}

// hasAuthentication checks if any authentication method is configured
func hasAuthentication(conn ClusterModel) bool {
	return !conn.Token.IsNull() ||
		hasClientCertAuth(conn) ||
		hasExecAuth(conn)
}

// hasClientCertAuth checks if client certificate authentication is configured
func hasClientCertAuth(conn ClusterModel) bool {
	return !conn.ClientCertificate.IsNull() && !conn.ClientKey.IsNull()
}

// hasExecAuth checks if exec authentication is configured
func hasExecAuth(conn ClusterModel) bool {
	return conn.Exec != nil && !conn.Exec.APIVersion.IsNull()
}

// validateExecAuth validates exec authentication configuration
func validateExecAuth(exec *ExecAuthModel) error {
	if exec == nil {
		return nil
	}

	// If exec block exists and has API version, validate required fields
	if !exec.APIVersion.IsNull() {
		missingFields := []string{}

		if exec.APIVersion.IsNull() {
			missingFields = append(missingFields, "api_version")
		}
		if exec.Command.IsNull() {
			missingFields = append(missingFields, "command")
		}

		if len(missingFields) > 0 {
			return fmt.Errorf("exec authentication incomplete\n\n"+
				"When using exec authentication, all fields are required. Missing: %v\n\n"+
				"Complete exec configuration requires:\n"+
				"• api_version: Authentication API version (e.g., 'client.authentication.k8s.io/v1')\n"+
				"• command: Executable command (e.g., 'aws', 'gcloud')",
				missingFields)
		}
	}

	return nil
}

// validateClientCertificates ensures client cert and key are provided together
func validateClientCertificates(conn ClusterModel) error {
	hasCert := !conn.ClientCertificate.IsNull()
	hasKey := !conn.ClientKey.IsNull()

	if hasCert != hasKey {
		return fmt.Errorf("client certificate configuration incomplete\n\n" +
			"Both 'client_certificate' and 'client_key' must be provided together for client certificate authentication.")
	}

	return nil
}

// ValidateConnectionWithUnknowns performs validation that's safe when values might be unknown.
// This is used during Terraform plan phase when some values might not be computed yet.
func ValidateConnectionWithUnknowns(ctx context.Context, conn ClusterModel) error {
	// Skip validation if key fields are unknown
	hasUnknownFields := conn.Host.IsUnknown() ||
		conn.ClusterCACertificate.IsUnknown() ||
		conn.Kubeconfig.IsUnknown()

	if hasUnknownFields {
		// Can't validate mode count with unknown values
		return nil
	}

	// Only validate things we can validate with potentially unknown values
	return ValidateConnection(ctx, conn)
}
