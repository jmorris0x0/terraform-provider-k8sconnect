// internal/k8sinline/common/auth/validation.go
package auth

import (
	"context"
	"fmt"
)

// ValidateConnection validates the connection configuration.
// It ensures exactly one connection mode is specified and all required fields are present.
func ValidateConnection(ctx context.Context, conn ClusterConnectionModel) error {
	// Check for inline mode (host-based)
	hasInline := !conn.Host.IsNull()

	// Check for kubeconfig modes
	hasFile := !conn.KubeconfigFile.IsNull()
	hasRaw := !conn.KubeconfigRaw.IsNull()

	// Count active modes
	modeCount := 0
	activeModes := []string{}

	if hasInline {
		modeCount++
		activeModes = append(activeModes, "inline")
	}

	if hasFile {
		modeCount++
		activeModes = append(activeModes, "kubeconfig_file")
	}

	if hasRaw {
		modeCount++
		activeModes = append(activeModes, "kubeconfig_raw")
	}

	// Validate exactly one mode is specified
	if modeCount == 0 {
		return fmt.Errorf("no cluster connection mode specified: provide host (inline), kubeconfig_file, or kubeconfig_raw")
	} else if modeCount > 1 {
		return fmt.Errorf("multiple cluster connection modes specified (%v): use only one", activeModes)
	}

	// Additional validation for inline mode
	if hasInline {
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

	// Validate client certificate configuration
	if err := validateClientCertificates(conn); err != nil {
		return err
	}

	return nil
}

// validateInlineConnection validates inline connection requirements
func validateInlineConnection(conn ClusterConnectionModel) error {
	// Validate CA cert or insecure for inline mode
	if conn.ClusterCACertificate.IsNull() && (conn.Insecure.IsNull() || !conn.Insecure.ValueBool()) {
		return fmt.Errorf("inline connections require either 'cluster_ca_certificate' or 'insecure = true'")
	}

	// Validate authentication is provided
	hasAuth := !conn.Token.IsNull() ||
		(!conn.ClientCertificate.IsNull() && !conn.ClientKey.IsNull()) ||
		(conn.Exec != nil && !conn.Exec.APIVersion.IsNull())

	if !hasAuth {
		return fmt.Errorf("inline connections require at least one authentication method: token, client certificates, or exec")
	}

	return nil
}

// validateExecAuth validates exec authentication configuration
func validateExecAuth(exec *ExecAuthModel) error {
	if exec == nil {
		return nil
	}

	// If exec block exists and has API version, validate required fields
	if !exec.APIVersion.IsNull() {
		if exec.Command.IsNull() {
			return fmt.Errorf("exec authentication requires 'command' to be specified")
		}
	}

	return nil
}

// validateClientCertificates ensures client cert and key are provided together
func validateClientCertificates(conn ClusterConnectionModel) error {
	hasCert := !conn.ClientCertificate.IsNull()
	hasKey := !conn.ClientKey.IsNull()

	if hasCert != hasKey {
		if hasCert {
			return fmt.Errorf("client_certificate requires client_key to also be specified")
		}
		return fmt.Errorf("client_key requires client_certificate to also be specified")
	}

	return nil
}

// ValidateConnectionWithUnknowns performs validation that's safe when values might be unknown.
// This is used during Terraform plan phase when some values might not be computed yet.
func ValidateConnectionWithUnknowns(ctx context.Context, conn ClusterConnectionModel) error {
	// Skip validation if key fields are unknown
	hasUnknownFields := conn.Host.IsUnknown() ||
		conn.ClusterCACertificate.IsUnknown() ||
		conn.KubeconfigFile.IsUnknown() ||
		conn.KubeconfigRaw.IsUnknown()

	if hasUnknownFields {
		// Can't validate mode count with unknown values
		return nil
	}

	// Only validate things we can validate with potentially unknown values
	return ValidateConnection(ctx, conn)
}
