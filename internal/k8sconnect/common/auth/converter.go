package auth

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// ObjectToConnectionModel converts a Terraform object to our connection model
func ObjectToConnectionModel(ctx context.Context, obj basetypes.ObjectValue) (ClusterConnectionModel, error) {
	var conn ClusterConnectionModel

	if obj.IsNull() || obj.IsUnknown() {
		return conn, fmt.Errorf("cluster_connection is required")
	}

	attrs := obj.Attributes()

	// Basic fields
	conn.Host = attrs["host"].(types.String)
	conn.ClusterCACertificate = attrs["cluster_ca_certificate"].(types.String)
	conn.Kubeconfig = attrs["kubeconfig"].(types.String)
	conn.Context = attrs["context"].(types.String)
	conn.Token = attrs["token"].(types.String)
	conn.ClientCertificate = attrs["client_certificate"].(types.String)
	conn.ClientKey = attrs["client_key"].(types.String)
	conn.Insecure = attrs["insecure"].(types.Bool)
	conn.ProxyURL = attrs["proxy_url"].(types.String)

	// Handle exec if present
	if execObj, ok := attrs["exec"].(types.Object); ok && !execObj.IsNull() {
		execAttrs := execObj.Attributes()
		conn.Exec = &ExecAuthModel{
			APIVersion: execAttrs["api_version"].(types.String),
			Command:    execAttrs["command"].(types.String),
		}

		// Handle args list
		if argsList, ok := execAttrs["args"].(types.List); ok && !argsList.IsNull() {
			args := make([]types.String, 0, len(argsList.Elements()))
			for _, elem := range argsList.Elements() {
				args = append(args, elem.(types.String))
			}
			conn.Exec.Args = args
		}

		// Handle env map
		if envMap, ok := execAttrs["env"].(types.Map); ok && !envMap.IsNull() {
			env := make(map[string]types.String)
			for k, v := range envMap.Elements() {
				env[k] = v.(types.String)
			}
			conn.Exec.Env = env
		}
	}

	return conn, nil
}

// ConnectionToObject converts our connection model back to a Terraform object
func ConnectionToObject(ctx context.Context, conn ClusterConnectionModel) (types.Object, error) {
	// Define the attribute types for the connection object
	attrTypes := GetConnectionAttributeTypes()

	// Build the attribute values
	attrs := map[string]attr.Value{
		"host":                   conn.Host,
		"cluster_ca_certificate": conn.ClusterCACertificate,
		"kubeconfig":             conn.Kubeconfig,
		"context":                conn.Context,
		"token":                  conn.Token,
		"client_certificate":     conn.ClientCertificate,
		"client_key":             conn.ClientKey,
		"insecure":               conn.Insecure,
		"proxy_url":              conn.ProxyURL,
	}

	// Handle exec
	if conn.Exec != nil {
		// Convert args to list
		var argsList []attr.Value
		for _, arg := range conn.Exec.Args {
			argsList = append(argsList, arg)
		}
		argsValue, _ := types.ListValue(types.StringType, argsList)

		// Convert env to map
		envMap := make(map[string]attr.Value)
		for k, v := range conn.Exec.Env {
			envMap[k] = v
		}
		envValue, _ := types.MapValue(types.StringType, envMap)

		execValue, _ := types.ObjectValue(
			GetExecAttributeTypes(),
			map[string]attr.Value{
				"api_version": conn.Exec.APIVersion,
				"command":     conn.Exec.Command,
				"args":        argsValue,
				"env":         envValue,
			},
		)
		attrs["exec"] = execValue
	} else {
		attrs["exec"] = types.ObjectNull(GetExecAttributeTypes())
	}

	objValue, diags := types.ObjectValue(attrTypes, attrs)
	if diags.HasError() {
		return types.ObjectNull(attrTypes), fmt.Errorf("failed to create connection object: %v", diags)
	}
	return objValue, nil
}

// GetConnectionAttributeTypes returns the attribute types for connection
func GetConnectionAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"host":                   types.StringType,
		"cluster_ca_certificate": types.StringType,
		"kubeconfig":             types.StringType,
		"context":                types.StringType,
		"token":                  types.StringType,
		"client_certificate":     types.StringType,
		"client_key":             types.StringType,
		"insecure":               types.BoolType,
		"proxy_url":              types.StringType,
		"exec":                   types.ObjectType{AttrTypes: GetExecAttributeTypes()},
	}
}

// GetExecAttributeTypes returns the attribute types for exec config
func GetExecAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"api_version": types.StringType,
		"command":     types.StringType,
		"args":        types.ListType{ElemType: types.StringType},
		"env":         types.MapType{ElemType: types.StringType},
	}
}
