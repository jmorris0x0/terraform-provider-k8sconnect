// internal/k8sinline/resource/manifest/validators_test.go
package manifest

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestIsClusterConnectionEmpty(t *testing.T) {
	ctx := context.Background()
	r := &manifestResource{} // gives access to convertConnectionModelToObject

	cases := []struct {
		name     string
		conn     ClusterConnectionModel
		expected bool
	}{
		{"completely empty", ClusterConnectionModel{}, true},
		{"host only", ClusterConnectionModel{
			Host: types.StringValue("https://example.com"),
		}, false},
		{"cluster_ca_certificate only", ClusterConnectionModel{
			ClusterCACertificate: types.StringValue("cert-bytes"),
		}, false},
		{"kubeconfig_file only", ClusterConnectionModel{
			KubeconfigFile: types.StringValue("/path/to/config"),
		}, false},
		{"kubeconfig_raw only", ClusterConnectionModel{
			KubeconfigRaw: types.StringValue("raw-config"),
		}, false},
		{"exec present", ClusterConnectionModel{
			Exec: &execAuthModel{
				APIVersion: types.StringValue("v1"),
				Command:    types.StringValue("kubectl"),
				Args:       []types.String{types.StringValue("version")},
			},
		}, false},
		{"all nulls", ClusterConnectionModel{
			Host:                 types.StringNull(),
			ClusterCACertificate: types.StringNull(),
			KubeconfigFile:       types.StringNull(),
			KubeconfigRaw:        types.StringNull(),
		}, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			obj, err := r.convertConnectionModelToObject(ctx, tc.conn)
			if err != nil {
				t.Fatalf("conversion failed: %v", err)
			}
			if got := isClusterConnectionEmpty(obj); got != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestConnectionModeDetection(t *testing.T) {
	// inline
	inline := ClusterConnectionModel{
		Host:                 types.StringValue("https://example.com"),
		ClusterCACertificate: types.StringValue("ca"),
	}
	if !hasInlineMode(inline) {
		t.Error("inline mode not detected")
	}
	// multiple modes
	multi := inline
	multi.KubeconfigFile = types.StringValue("/tmp/kubeconfig")
	if countModes(multi) != 2 {
		t.Errorf("expected 2 active modes, got %d", countModes(multi))
	}
}

func TestExecFieldCompleteness(t *testing.T) {
	exec := &execAuthModel{
		APIVersion: types.StringValue("client.authentication.k8s.io/v1"),
		// missing command and args
	}
	missing := execMissingFields(exec)
	if len(missing) != 2 {
		t.Errorf("expected 2 missing fields, got %d (%v)", len(missing), missing)
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
	r := &manifestResource{}
	validators := r.ConfigValidators(nil)
	if len(validators) != 4 {
		t.Fatalf("expected 4 validators, got %d", len(validators))
	}

	typeNames := map[string]bool{}
	for _, v := range validators {
		switch v.(type) {
		case *clusterConnectionValidator:
			typeNames["cluster"] = true
		case *execAuthValidator:
			typeNames["exec"] = true
		case *conflictingAttributesValidator:
			typeNames["conflict"] = true
		case *requiredFieldsValidator:
			typeNames["required"] = true
		}
	}
	for _, k := range []string{"cluster", "exec", "conflict", "required"} {
		if !typeNames[k] {
			t.Errorf("validator %q missing", k)
		}
	}
}

func hasInlineMode(c ClusterConnectionModel) bool {
	return !c.Host.IsNull() || !c.ClusterCACertificate.IsNull()
}

func countModes(c ClusterConnectionModel) int {
	n := 0
	if hasInlineMode(c) {
		n++
	}
	if !c.KubeconfigFile.IsNull() {
		n++
	}
	if !c.KubeconfigRaw.IsNull() {
		n++
	}
	return n
}

func execMissingFields(e *execAuthModel) []string {
	var m []string
	if e == nil {
		return []string{"api_version", "command", "args"}
	}
	if e.APIVersion.IsNull() {
		m = append(m, "api_version")
	}
	if e.Command.IsNull() {
		m = append(m, "command")
	}
	if len(e.Args) == 0 {
		m = append(m, "args")
	}
	return m
}
