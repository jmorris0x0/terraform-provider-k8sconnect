// internal/k8sconnect/common/auth/encoding.go
package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// AutoDecodePEM automatically detects whether input is PEM format or base64-encoded PEM
// and returns the decoded PEM bytes. It handles both direct PEM and base64-encoded PEM.
func AutoDecodePEM(input string, fieldName string) ([]byte, error) {
	// Trim whitespace from input
	input = strings.TrimSpace(input)

	// Empty input
	if input == "" {
		return nil, fmt.Errorf("%s cannot be empty", fieldName)
	}

	// Check if already in PEM format
	if isPEMFormat(input) {
		return []byte(input), nil
	}

	// Try base64 decoding
	decoded, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		// Not valid base64, return clear error
		return nil, fmt.Errorf("%s must be PEM format or base64-encoded PEM", fieldName)
	}

	// Check if decoded content is PEM
	decodedStr := string(decoded)
	if isPEMFormat(decodedStr) {
		return decoded, nil
	}

	// Neither direct PEM nor base64-encoded PEM
	return nil, fmt.Errorf("%s must be PEM format or base64-encoded PEM", fieldName)
}

// isPEMFormat checks if the input string contains PEM headers
func isPEMFormat(input string) bool {
	// Common PEM headers we expect in Kubernetes
	pemHeaders := []string{
		"-----BEGIN CERTIFICATE-----",
		"-----BEGIN RSA PRIVATE KEY-----",
		"-----BEGIN PRIVATE KEY-----",
		"-----BEGIN EC PRIVATE KEY-----",
		"-----BEGIN ENCRYPTED PRIVATE KEY-----",
		"-----BEGIN PUBLIC KEY-----",
		"-----BEGIN RSA PUBLIC KEY-----",
	}

	for _, header := range pemHeaders {
		if strings.Contains(input, header) {
			return true
		}
	}

	return false
}
