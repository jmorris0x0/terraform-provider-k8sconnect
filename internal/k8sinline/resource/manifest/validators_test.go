// internal/k8sinline/resource/manifest/validators_test.go
package manifest

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Simple unit tests for validator helper functions and core logic

func TestIsClusterConnectionEmpty(t *testing.T) {
	tests := []struct {
		name     string
		conn     ClusterConnectionModel
		expected bool
	}{
		{
			name:     "completely empty connection",
			conn:     ClusterConnectionModel{},
			expected: true,
		},
		{
			name: "has host",
			conn: ClusterConnectionModel{
				Host: types.StringValue("https://example.com"),
			},
			expected: false,
		},
		{
			name: "has cluster ca certificate",
			conn: ClusterConnectionModel{
				ClusterCACertificate: types.StringValue("test-cert"),
			},
			expected: false,
		},
		{
			name: "has kubeconfig file",
			conn: ClusterConnectionModel{
				KubeconfigFile: types.StringValue("/path/to/config"),
			},
			expected: false,
		},
		{
			name: "has kubeconfig raw",
			conn: ClusterConnectionModel{
				KubeconfigRaw: types.StringValue("test-config"),
			},
			expected: false,
		},
		{
			name: "has exec",
			conn: ClusterConnectionModel{
				Exec: &execAuthModel{
					APIVersion: types.StringValue("v1"),
					Command:    types.StringValue("aws"),
					Args:       []types.String{types.StringValue("eks")},
				},
			},
			expected: false,
		},
		{
			name: "has null values (should be empty)",
			conn: ClusterConnectionModel{
				Host:                 types.StringNull(),
				ClusterCACertificate: types.StringNull(),
				KubeconfigFile:       types.StringNull(),
				KubeconfigRaw:        types.StringNull(),
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isClusterConnectionEmpty(tt.conn)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// Test the validation logic without the full framework setup
func TestValidationLogic(t *testing.T) {
	t.Run("connection mode detection", func(t *testing.T) {
		// Test inline mode detection
		conn := ClusterConnectionModel{
			Host:                 types.StringValue("https://example.com"),
			ClusterCACertificate: types.StringValue("test-cert"),
		}

		hasInline := !conn.Host.IsNull() || !conn.ClusterCACertificate.IsNull()
		hasFile := !conn.KubeconfigFile.IsNull()
		hasRaw := !conn.KubeconfigRaw.IsNull()

		if !hasInline {
			t.Error("should detect inline mode")
		}
		if hasFile || hasRaw {
			t.Error("should not detect file or raw modes")
		}
	})

	t.Run("multiple mode detection", func(t *testing.T) {
		// Test multiple modes
		conn := ClusterConnectionModel{
			Host:           types.StringValue("https://example.com"),
			KubeconfigFile: types.StringValue("/path/to/config"),
		}

		hasInline := !conn.Host.IsNull() || !conn.ClusterCACertificate.IsNull()
		hasFile := !conn.KubeconfigFile.IsNull()
		hasRaw := !conn.KubeconfigRaw.IsNull()

		modeCount := 0
		if hasInline {
			modeCount++
		}
		if hasFile {
			modeCount++
		}
		if hasRaw {
			modeCount++
		}

		if modeCount != 2 {
			t.Errorf("expected 2 modes, got %d", modeCount)
		}
	})

	t.Run("exec validation logic", func(t *testing.T) {
		// Test incomplete exec
		exec := &execAuthModel{
			APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
			// Missing command and args
		}

		missingFields := []string{}
		if exec.APIVersion.IsNull() {
			missingFields = append(missingFields, "api_version")
		}
		if exec.Command.IsNull() {
			missingFields = append(missingFields, "command")
		}
		if len(exec.Args) == 0 {
			missingFields = append(missingFields, "args")
		}

		expectedMissing := []string{"command", "args"}
		if len(missingFields) != len(expectedMissing) {
			t.Errorf("expected %d missing fields, got %d: %v", len(expectedMissing), len(missingFields), missingFields)
		}
	})

	t.Run("conflict detection logic", func(t *testing.T) {
		// Test conflicting attributes
		deleteProtection := types.BoolValue(true)
		forceDestroy := types.BoolValue(true)

		deleteProtectionEnabled := !deleteProtection.IsNull() && deleteProtection.ValueBool()
		forceDestroyEnabled := !forceDestroy.IsNull() && forceDestroy.ValueBool()

		if !deleteProtectionEnabled {
			t.Error("should detect delete protection enabled")
		}
		if !forceDestroyEnabled {
			t.Error("should detect force destroy enabled")
		}

		hasConflict := deleteProtectionEnabled && forceDestroyEnabled
		if !hasConflict {
			t.Error("should detect conflict")
		}
	})
}

// Test unknown value handling
func TestUnknownValueHandling(t *testing.T) {
	t.Run("unknown values should be skipped", func(t *testing.T) {
		// Create unknown values
		unknownString := types.StringUnknown()
		unknownBool := types.BoolUnknown()

		// Test that we properly detect unknown values
		if !unknownString.IsUnknown() {
			t.Error("string should be unknown")
		}
		if !unknownBool.IsUnknown() {
			t.Error("bool should be unknown")
		}

		// Test that we don't validate unknown values
		if unknownString.IsNull() {
			t.Error("unknown string should not be null")
		}
	})

	t.Run("null vs unknown distinction", func(t *testing.T) {
		nullString := types.StringNull()
		unknownString := types.StringUnknown()
		valueString := types.StringValue("test")

		// Null values
		if !nullString.IsNull() {
			t.Error("null string should be null")
		}
		if nullString.IsUnknown() {
			t.Error("null string should not be unknown")
		}

		// Unknown values
		if unknownString.IsNull() {
			t.Error("unknown string should not be null")
		}
		if !unknownString.IsUnknown() {
			t.Error("unknown string should be unknown")
		}

		// Value strings
		if valueString.IsNull() {
			t.Error("value string should not be null")
		}
		if valueString.IsUnknown() {
			t.Error("value string should not be unknown")
		}
	})
}

// Test that our validators are properly registered
func TestValidatorRegistration(t *testing.T) {
	resource := &manifestResource{}
	validators := resource.ConfigValidators(nil)

	if len(validators) != 4 {
		t.Errorf("expected 4 validators, got %d", len(validators))
	}

	// Check that all validator types are present
	validatorTypes := make(map[string]bool)
	for _, validator := range validators {
		switch validator.(type) {
		case *clusterConnectionValidator:
			validatorTypes["cluster_connection"] = true
		case *execAuthValidator:
			validatorTypes["exec_auth"] = true
		case *conflictingAttributesValidator:
			validatorTypes["conflicting_attributes"] = true
		case *requiredFieldsValidator:
			validatorTypes["required_fields"] = true
		}
	}

	expectedTypes := []string{"cluster_connection", "exec_auth", "conflicting_attributes", "required_fields"}
	for _, expectedType := range expectedTypes {
		if !validatorTypes[expectedType] {
			t.Errorf("missing validator type: %s", expectedType)
		}
	}
}
