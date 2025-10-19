// internal/k8sconnect/common/auth/encoding_test.go
package auth

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoDecodePEM_DirectPEM(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Certificate PEM",
			input: `-----BEGIN CERTIFICATE-----
MIIDQTCCAimgAwIBAgITBmyfz5m/jAo54vB4ikPmljZbyjANBgkqhkiG9w0BAQsF
-----END CERTIFICATE-----`,
			expected: `-----BEGIN CERTIFICATE-----
MIIDQTCCAimgAwIBAgITBmyfz5m/jAo54vB4ikPmljZbyjANBgkqhkiG9w0BAQsF
-----END CERTIFICATE-----`,
		},
		{
			name: "RSA Private Key PEM",
			input: `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA0Z3VS5JJcds3xBn/wADdaque0rXpYI8NnH2LOFbjMqWZ7fIr
-----END RSA PRIVATE KEY-----`,
			expected: `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA0Z3VS5JJcds3xBn/wADdaque0rXpYI8NnH2LOFbjMqWZ7fIr
-----END RSA PRIVATE KEY-----`,
		},
		{
			name: "Private Key PEM",
			input: `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQDMX1FJmopwkyMd
-----END PRIVATE KEY-----`,
			expected: `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQDMX1FJmopwkyMd
-----END PRIVATE KEY-----`,
		},
		{
			name: "PEM with leading/trailing whitespace",
			input: `  
		-----BEGIN CERTIFICATE-----
MIIDQTCCAimgAwIBAgITBmyfz5m/jAo54vB4ikPmljZbyjANBgkqhkiG9w0BAQsF
-----END CERTIFICATE-----
		`,
			expected: `-----BEGIN CERTIFICATE-----
MIIDQTCCAimgAwIBAgITBmyfz5m/jAo54vB4ikPmljZbyjANBgkqhkiG9w0BAQsF
-----END CERTIFICATE-----`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := AutoDecodePEM(tc.input, "test_field")
			require.NoError(t, err)
			assert.Equal(t, tc.expected, string(result))
		})
	}
}

func TestAutoDecodePEM_Base64EncodedPEM(t *testing.T) {
	pemCert := `-----BEGIN CERTIFICATE-----
MIIDQTCCAimgAwIBAgITBmyfz5m/jAo54vB4ikPmljZbyjANBgkqhkiG9w0BAQsF
-----END CERTIFICATE-----`

	pemKey := `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQDMX1FJmopwkyMd
-----END PRIVATE KEY-----`

	testCases := []struct {
		name string
		pem  string
	}{
		{
			name: "Base64 encoded certificate",
			pem:  pemCert,
		},
		{
			name: "Base64 encoded private key",
			pem:  pemKey,
		},
		{
			name: "Base64 with whitespace",
			pem:  pemCert,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode to base64
			encoded := base64.StdEncoding.EncodeToString([]byte(tc.pem))

			// Add whitespace for one test case
			if tc.name == "Base64 with whitespace" {
				encoded = "  " + encoded + "\n\t"
			}

			// Decode
			result, err := AutoDecodePEM(encoded, "test_field")
			require.NoError(t, err)
			assert.Equal(t, tc.pem, string(result))
		})
	}
}

func TestAutoDecodePEM_InvalidInput(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		errorContains string
	}{
		{
			name:          "Empty string",
			input:         "",
			errorContains: "cannot be empty",
		},
		{
			name:          "Whitespace only",
			input:         "   \n\t  ",
			errorContains: "cannot be empty",
		},
		{
			name:          "Invalid base64",
			input:         "not-valid-base64!@#$%",
			errorContains: "must be PEM format or base64-encoded PEM",
		},
		{
			name:          "Valid base64 but not PEM",
			input:         base64.StdEncoding.EncodeToString([]byte("hello world")),
			errorContains: "must be PEM format or base64-encoded PEM",
		},
		{
			name:          "Plain text not PEM",
			input:         "just some random text",
			errorContains: "must be PEM format or base64-encoded PEM",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AutoDecodePEM(tc.input, "test_field")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errorContains)
		})
	}
}

func TestIsPEMFormat(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Certificate header",
			input:    "-----BEGIN CERTIFICATE-----",
			expected: true,
		},
		{
			name:     "RSA private key header",
			input:    "-----BEGIN RSA PRIVATE KEY-----",
			expected: true,
		},
		{
			name:     "Private key header",
			input:    "-----BEGIN PRIVATE KEY-----",
			expected: true,
		},
		{
			name:     "EC private key header",
			input:    "-----BEGIN EC PRIVATE KEY-----",
			expected: true,
		},
		{
			name:     "Full certificate",
			input:    "-----BEGIN CERTIFICATE-----\nMIIDQTC...\n-----END CERTIFICATE-----",
			expected: true,
		},
		{
			name:     "No PEM header",
			input:    "MIIDQTCCAimgAwIBAgITBmyfz5m",
			expected: false,
		},
		{
			name:     "Partial header",
			input:    "BEGIN CERTIFICATE",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isPEMFormat(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}
