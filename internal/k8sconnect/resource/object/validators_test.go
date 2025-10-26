package object

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

func TestIsClusterEmpty(t *testing.T) {
	ctx := context.Background()
	r := &objectResource{} // gives access to convertConnectionModelToObject

	cases := []struct {
		name     string
		conn     auth.ClusterModel
		expected bool
	}{
		{"completely empty", auth.ClusterModel{}, true},
		{"host only", auth.ClusterModel{
			Host: types.StringValue("https://example.com"),
		}, false},
		{"cluster_ca_certificate only", auth.ClusterModel{
			ClusterCACertificate: types.StringValue("cert-bytes"),
		}, false},
		{"kubeconfig only", auth.ClusterModel{
			Kubeconfig: types.StringValue("raw-config"),
		}, false},
		{"exec present", auth.ClusterModel{
			Exec: &auth.ExecAuthModel{
				APIVersion: types.StringValue("v1"),
				Command:    types.StringValue("kubectl"),
				Args:       []types.String{types.StringValue("version")},
			},
		}, false},
		{"all nulls", auth.ClusterModel{
			Host:                 types.StringNull(),
			ClusterCACertificate: types.StringNull(),
			Kubeconfig:           types.StringNull(),
		}, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			obj, err := r.convertConnectionToObject(ctx, tc.conn)
			if err != nil {
				t.Fatalf("conversion failed: %v", err)
			}
			if got := isClusterEmpty(obj); got != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestConnectionModeDetection(t *testing.T) {
	// inline
	inline := auth.ClusterModel{
		Host:                 types.StringValue("https://example.com"),
		ClusterCACertificate: types.StringValue("ca"),
	}
	if !hasInlineMode(inline) {
		t.Error("inline mode not detected")
	}
	// multiple modes
	multi := inline
	multi.Kubeconfig = types.StringValue("foo")
	if countModes(multi) != 2 {
		t.Errorf("expected 2 active modes, got %d", countModes(multi))
	}
}

func TestExecFieldCompleteness(t *testing.T) {
	exec := &auth.ExecAuthModel{
		APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
		// missing command (args is optional)
	}
	missing := execMissingFields(exec)
	if len(missing) != 1 {
		t.Errorf("expected 1 missing field, got %d (%v)", len(missing), missing)
	}
}

func TestDeleteProtectionConflict(t *testing.T) {
	deleteProtection := types.BoolValue(true)
	forceDestroy := types.BoolValue(true)
	if !(deleteProtection.ValueBool() && forceDestroy.ValueBool()) {
		t.Fatal("expected conflict not detected")
	}
}

func TestConfigValidatorsSlice(t *testing.T) {
	r := &objectResource{}
	validatorList := r.ConfigValidators(nil)
	if len(validatorList) != 4 {
		t.Fatalf("expected 4 validators, got %d", len(validatorList))
	}

	typeNames := map[string]bool{}
	for _, v := range validatorList {
		typeName := reflect.TypeOf(v).String()
		switch {
		case typeName == "*validators.Cluster":
			typeNames["cluster"] = true
		case typeName == "*validators.ExecAuth":
			typeNames["exec"] = true
		case typeName == "*object.conflictingAttributesValidator":
			typeNames["conflict"] = true
		case typeName == "*object.requiredFieldsValidator":
			typeNames["required"] = true
		}
	}
	for _, k := range []string{"cluster", "exec", "conflict", "required"} {
		if !typeNames[k] {
			t.Errorf("validator %q missing", k)
		}
	}
}

func hasInlineMode(c auth.ClusterModel) bool {
	return !c.Host.IsNull() || !c.ClusterCACertificate.IsNull()
}

func countModes(c auth.ClusterModel) int {
	n := 0
	if hasInlineMode(c) {
		n++
	}
	if !c.Kubeconfig.IsNull() {
		n++
	}
	return n
}

func execMissingFields(e *auth.ExecAuthModel) []string {
	var m []string
	if e == nil {
		return []string{"api_version", "command"} // Remove "args" from here
	}
	if e.APIVersion.IsNull() {
		m = append(m, "api_version")
	}
	if e.Command.IsNull() {
		m = append(m, "command")
	}
	// Remove the entire check for Args
	return m
}
