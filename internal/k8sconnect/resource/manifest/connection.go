// internal/k8sconnect/resource/manifest/connection.go
package manifest

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/k8sclient"
)

// createK8sClient creates a Kubernetes client from connection configuration.
// This is a thin wrapper around the common auth package.
func (r *manifestResource) createK8sClient(conn auth.ClusterConnectionModel) (k8sclient.K8sClient, error) {
	config, err := auth.CreateRESTConfig(context.Background(), conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client config: %w", err)
	}

	return k8sclient.NewDynamicK8sClient(config)
}

// convertObjectToConnectionModel converts a Terraform object to our connection model
func (r *manifestResource) convertObjectToConnectionModel(ctx context.Context, obj basetypes.ObjectValue) (auth.ClusterConnectionModel, error) {
	var conn auth.ClusterConnectionModel

	if obj.IsNull() || obj.IsUnknown() {
		return conn, fmt.Errorf("cluster_connection is required")
	}

	attrs := obj.Attributes()

	// Basic fields
	conn.Host = attrs["host"].(types.String)
	conn.ClusterCACertificate = attrs["cluster_ca_certificate"].(types.String)
	conn.KubeconfigFile = attrs["kubeconfig_file"].(types.String)
	conn.KubeconfigRaw = attrs["kubeconfig_raw"].(types.String)
	conn.Context = attrs["context"].(types.String)
	conn.Token = attrs["token"].(types.String)
	conn.ClientCertificate = attrs["client_certificate"].(types.String)
	conn.ClientKey = attrs["client_key"].(types.String)
	conn.Insecure = attrs["insecure"].(types.Bool)
	conn.ProxyURL = attrs["proxy_url"].(types.String)

	// Handle exec if present
	if execObj, ok := attrs["exec"].(types.Object); ok && !execObj.IsNull() {
		execAttrs := execObj.Attributes()
		conn.Exec = &auth.ExecAuthModel{
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

// convertConnectionToObject converts our connection model back to a Terraform object.
// This is used when we need to store the connection in state.
func (r *manifestResource) convertConnectionToObject(ctx context.Context, conn auth.ClusterConnectionModel) (types.Object, error) {
	// Define the attribute types for the connection object
	attrTypes := map[string]attr.Type{
		"host":                   types.StringType,
		"cluster_ca_certificate": types.StringType,
		"kubeconfig_file":        types.StringType,
		"kubeconfig_raw":         types.StringType,
		"context":                types.StringType,
		"token":                  types.StringType,
		"client_certificate":     types.StringType,
		"client_key":             types.StringType,
		"insecure":               types.BoolType,
		"proxy_url":              types.StringType,
		"exec": types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"api_version": types.StringType,
				"command":     types.StringType,
				"args":        types.ListType{ElemType: types.StringType},
				"env":         types.MapType{ElemType: types.StringType},
			},
		},
	}

	// Build the attribute values
	attrs := map[string]attr.Value{
		"host":                   conn.Host,
		"cluster_ca_certificate": conn.ClusterCACertificate,
		"kubeconfig_file":        conn.KubeconfigFile,
		"kubeconfig_raw":         conn.KubeconfigRaw,
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
			map[string]attr.Type{
				"api_version": types.StringType,
				"command":     types.StringType,
				"args":        types.ListType{ElemType: types.StringType},
				"env":         types.MapType{ElemType: types.StringType},
			},
			map[string]attr.Value{
				"api_version": conn.Exec.APIVersion,
				"command":     conn.Exec.Command,
				"args":        argsValue,
				"env":         envValue,
			},
		)
		attrs["exec"] = execValue
	} else {
		attrs["exec"] = types.ObjectNull(map[string]attr.Type{
			"api_version": types.StringType,
			"command":     types.StringType,
			"args":        types.ListType{ElemType: types.StringType},
			"env":         types.MapType{ElemType: types.StringType},
		})
	}

	objValue, diags := types.ObjectValue(attrTypes, attrs)
	if diags.HasError() {
		return types.ObjectNull(attrTypes), fmt.Errorf("failed to create connection object: %v", diags)
	}
	return objValue, nil
}

// isConnectionReady checks if the connection object is ready (not null/unknown)
func (r *manifestResource) isConnectionReady(obj types.Object) bool {
	return !obj.IsNull() && !obj.IsUnknown()
}
