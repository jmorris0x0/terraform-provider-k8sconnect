// internal/k8sconnect/common/types.go
package common

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/client"
)

// ConnectionConfig contains the connection resolver and client factory
// that are passed from the provider to resources
type ConnectionConfig struct {
	ConnectionResolver *auth.ConnectionResolver
	ClientFactory      client.ClientFactory
}

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
