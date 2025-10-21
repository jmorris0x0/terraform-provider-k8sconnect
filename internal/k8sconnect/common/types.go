package common

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ConvertToAttrValue converts arbitrary Go data (typically from Kubernetes unstructured objects)
// to Terraform framework attr.Value types. This enables proper dot notation access
// in Terraform configurations for dynamic nested structures like Kubernetes status.
func ConvertToAttrValue(ctx context.Context, data interface{}) (attr.Value, error) {
	switch v := data.(type) {
	case map[string]interface{}:
		// Convert map to types.Object
		attrTypes := make(map[string]attr.Type)
		attrValues := make(map[string]attr.Value)

		for key, val := range v {
			// Skip managedFields - it has heterogeneous list elements that violate
			// Terraform's type system (each element has different fieldsV1 structure).
			// Users can still access this via manifest/yaml_body if needed.
			if key == "managedFields" {
				continue
			}

			attrVal, err := ConvertToAttrValue(ctx, val)
			if err != nil {
				return nil, fmt.Errorf("failed to convert key %q: %w", key, err)
			}
			attrValues[key] = attrVal
			attrTypes[key] = attrVal.Type(ctx)
		}

		objValue, diags := types.ObjectValue(attrTypes, attrValues)
		if diags.HasError() {
			return nil, fmt.Errorf("failed to create object: %s", diags.Errors())
		}
		return objValue, nil

	case []interface{}:
		// Convert slice to types.List
		if len(v) == 0 {
			// Empty list - default to string type
			return types.ListNull(types.StringType), nil
		}

		// Convert all elements
		elements := make([]attr.Value, len(v))
		var elemType attr.Type

		for i, elem := range v {
			elemVal, err := ConvertToAttrValue(ctx, elem)
			if err != nil {
				return nil, fmt.Errorf("failed to convert list element %d: %w", i, err)
			}
			elements[i] = elemVal

			// Use first element's type as list element type
			if i == 0 {
				elemType = elemVal.Type(ctx)
			}
		}

		listValue, diags := types.ListValue(elemType, elements)
		if diags.HasError() {
			return nil, fmt.Errorf("failed to create list: %s", diags.Errors())
		}
		return listValue, nil

	case string:
		return types.StringValue(v), nil

	case bool:
		return types.BoolValue(v), nil

	case float64:
		// JSON unmarshaling uses float64 for all numbers
		return types.Float64Value(v), nil

	case int64:
		return types.Int64Value(v), nil

	case int:
		return types.Int64Value(int64(v)), nil

	case nil:
		// Null value - default to string null
		return types.StringNull(), nil

	default:
		// Fallback: convert to string
		return types.StringValue(fmt.Sprintf("%v", v)), nil
	}
}

// FormatValueForDisplay converts a value to string for display in flat maps.
// Used by projection systems to represent complex values as strings for Terraform state.
func FormatValueForDisplay(v interface{}) string {
	if v == nil {
		return "<nil>"
	}

	switch val := v.(type) {
	case string:
		return val
	case int, int32, int64, float32, float64, bool:
		return fmt.Sprintf("%v", val)
	case map[string]interface{}, []interface{}:
		// Complex types - use compact JSON
		bytes, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("<error: %v>", err)
		}
		return string(bytes)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// GenerateID creates a random 12-character hex ID for Terraform resource identification.
// This is used consistently across all resources (object, patch, wait) for state tracking.
// Returns a string like "b61bf80287d8" (6 random bytes encoded as hex).
// Panics if the OS CSPRNG is unavailable (which indicates a critically broken system).
func GenerateID() string {
	bytes := make([]byte, 6) // 6 bytes = 12 hex chars
	if _, err := rand.Read(bytes); err != nil {
		// If crypto/rand fails, the system is fundamentally broken - panic loudly
		panic(fmt.Sprintf("crypto/rand.Read failed (OS CSPRNG unavailable): %v", err))
	}
	return hex.EncodeToString(bytes)
}
