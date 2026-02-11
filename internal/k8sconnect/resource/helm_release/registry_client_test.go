package helm_release

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/registry"
)

// TestRegistryClientSurvivesChartPathOptionsOverwrite is a regression test for
// the "missing registry client" bug in loadRepoChart.
//
// Bug: action.NewInstall(cfg) copies cfg.RegistryClient into the embedded
// ChartPathOptions.registryClient field. But our code then overwrites the
// entire embedded struct with `client.ChartPathOptions = *opts`, and since
// registryClient is unexported, the new opts always has registryClient == nil.
// This causes "missing registry client" errors when HTTP repos redirect to OCI
// references (e.g. Bitnami charts post-Nov 2024).
//
// Fix: call client.SetRegistryClient(registryClient) after the overwrite.
func TestRegistryClientSurvivesChartPathOptionsOverwrite(t *testing.T) {
	registryClient, err := registry.NewClient()
	require.NoError(t, err)

	cfg := new(action.Configuration)
	cfg.RegistryClient = registryClient

	client := action.NewInstall(cfg)

	// This is what loadRepoChart does: overwrites ChartPathOptions then restores
	opts := action.ChartPathOptions{
		RepoURL: "https://charts.example.com",
		Version: "1.0.0",
	}
	client.ChartPathOptions = opts
	client.SetRegistryClient(registryClient)

	// LocateChart with an OCI name immediately checks registryClient.
	// If nil, it returns "missing registry client".
	_, err = client.ChartPathOptions.LocateChart("oci://example.com/charts/test", cli.New())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "missing registry client",
		"registryClient must survive ChartPathOptions overwrite; call SetRegistryClient() after assignment")
}
