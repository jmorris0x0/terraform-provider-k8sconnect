package helm_release

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"gopkg.in/yaml.v3"

	"helm.sh/helm/v4/pkg/action"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/registry"
	release "helm.sh/helm/v4/pkg/release/v1"
	repo "helm.sh/helm/v4/pkg/repo/v1"
	"helm.sh/helm/v4/pkg/storage/driver"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
)

// Create installs a new Helm release
func (r *helmReleaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// 1. Extract plan data
	var data helmReleaseResourceModel
	diags := req.Plan.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "Creating Helm release", map[string]interface{}{
		"name":      data.Name.ValueString(),
		"namespace": data.Namespace.ValueString(),
		"chart":     data.Chart.ValueString(),
	})

	// 2. Generate resource ID
	data.ID = types.StringValue(common.GenerateID())

	// 3. Get Helm action configuration
	actionConfig, err := r.getActionConfig(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Configure Helm Client",
			fmt.Sprintf("Could not create Helm action configuration: %s", err.Error()),
		)
		return
	}

	// 4. Create Install action
	install := action.NewInstall(actionConfig)
	install.ReleaseName = data.Name.ValueString()
	install.Namespace = data.Namespace.ValueString()
	install.CreateNamespace = data.CreateNamespace.ValueBool()

	// Helm v4: Wait is now WaitStrategy (string), Atomic is now RollbackOnFailure (bool)
	if data.Wait.ValueBool() {
		install.WaitStrategy = "watcher" // Use kstatus-based watcher strategy
	}
	install.WaitForJobs = data.WaitForJobs.ValueBool()
	install.RollbackOnFailure = data.Atomic.ValueBool()
	install.SkipCRDs = data.SkipCRDs.ValueBool()
	install.DisableHooks = data.DisableWebhooks.ValueBool()

	// Enable SSA with force conflicts (ADR-005 pattern, matches k8sconnect_object behavior)
	install.ServerSideApply = true
	install.ForceConflicts = true

	// Parse timeout
	if !data.Timeout.IsNull() {
		timeout, err := time.ParseDuration(data.Timeout.ValueString())
		if err != nil {
			resp.Diagnostics.AddError(
				"Invalid Timeout",
				fmt.Sprintf("Could not parse timeout '%s': %s", data.Timeout.ValueString(), err.Error()),
			)
			return
		}
		install.Timeout = timeout
	}

	// 5. Load chart
	chart, err := r.loadChart(ctx, actionConfig, &data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Load Chart",
			fmt.Sprintf("Could not load Helm chart: %s", err.Error()),
		)
		return
	}

	// 6. Merge values
	values, err := r.mergeValues(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Merge Values",
			fmt.Sprintf("Could not merge Helm values: %s", err.Error()),
		)
		return
	}

	// 7. Run install
	releaser, err := install.Run(chart, values)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Install Helm Release",
			fmt.Sprintf("Could not install Helm release '%s': %s", data.Name.ValueString(), err.Error()),
		)
		return
	}

	// Helm v4: Run() returns release.Releaser interface, need type assertion
	rel, ok := releaser.(*release.Release)
	if !ok {
		resp.Diagnostics.AddError(
			"Internal Error",
			"Failed to convert Helm release to concrete type",
		)
		return
	}

	// 8. Update computed fields from release
	if err := r.updateComputedFields(ctx, &data, rel); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Update State",
			fmt.Sprintf("Could not update computed fields: %s", err.Error()),
		)
		return
	}

	tflog.Info(ctx, "Helm release created successfully", map[string]interface{}{
		"name":     data.Name.ValueString(),
		"revision": rel.Version,
		"status":   rel.Info.Status.String(),
	})

	// 9. Save state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Read retrieves the current state of a Helm release
func (r *helmReleaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// 1. Extract state data
	var data helmReleaseResourceModel
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "Reading Helm release", map[string]interface{}{
		"name":      data.Name.ValueString(),
		"namespace": data.Namespace.ValueString(),
	})

	// 2. Get Helm action configuration
	actionConfig, err := r.getActionConfig(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Configure Helm Client",
			fmt.Sprintf("Could not create Helm action configuration: %s", err.Error()),
		)
		return
	}

	// 3. Get the release
	get := action.NewGet(actionConfig)
	releaser, err := get.Run(data.Name.ValueString())
	if err != nil {
		// Release not found - remove from state
		if err == driver.ErrReleaseNotFound {
			tflog.Warn(ctx, "Helm release not found, removing from state", map[string]interface{}{
				"name":      data.Name.ValueString(),
				"namespace": data.Namespace.ValueString(),
			})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Failed to Read Helm Release",
			fmt.Sprintf("Could not read Helm release '%s': %s", data.Name.ValueString(), err.Error()),
		)
		return
	}

	// Helm v4: Get() returns release.Releaser interface, need type assertion
	rel, ok := releaser.(*release.Release)
	if !ok {
		resp.Diagnostics.AddError(
			"Internal Error",
			"Failed to convert Helm release to concrete type",
		)
		return
	}

	// 4. Detect drift before updating state
	hasDrift, driftReasons := r.detectDrift(ctx, &data, rel)
	if hasDrift {
		tflog.Info(ctx, "Drift detected - state will be updated to match cluster", map[string]interface{}{
			"reasons": strings.Join(driftReasons, "; "),
		})
		// Note: We still update state to reflect current reality
		// Terraform will show the drift in the plan
	}

	// 5. Update computed fields from release
	if err := r.updateComputedFields(ctx, &data, rel); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Update State",
			fmt.Sprintf("Could not update computed fields: %s", err.Error()),
		)
		return
	}

	// 6. Save updated state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update upgrades an existing Helm release
func (r *helmReleaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// 1. Extract plan and state data
	var plan, state helmReleaseResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	diags = req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "Updating Helm release", map[string]interface{}{
		"name":      plan.Name.ValueString(),
		"namespace": plan.Namespace.ValueString(),
	})

	// 2. Get Helm action configuration
	actionConfig, err := r.getActionConfig(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Configure Helm Client",
			fmt.Sprintf("Could not create Helm action configuration: %s", err.Error()),
		)
		return
	}

	// 3. Create Upgrade action
	upgrade := action.NewUpgrade(actionConfig)
	upgrade.Namespace = plan.Namespace.ValueString()

	// Helm v4: Wait is now WaitStrategy (string), Atomic is now RollbackOnFailure (bool)
	if plan.Wait.ValueBool() {
		upgrade.WaitStrategy = "watcher" // Use kstatus-based watcher strategy
	}
	upgrade.WaitForJobs = plan.WaitForJobs.ValueBool()
	upgrade.RollbackOnFailure = plan.Atomic.ValueBool()
	upgrade.SkipCRDs = plan.SkipCRDs.ValueBool()
	upgrade.DisableHooks = plan.DisableWebhooks.ValueBool()
	// Note: RecreatePods is deprecated in Helm v3 and not available in action.Upgrade

	// Enable SSA with force conflicts (ADR-005 pattern, matches k8sconnect_object behavior)
	upgrade.ServerSideApply = "true" // Explicit SSA mode (not "auto")
	upgrade.ForceConflicts = true

	// Parse timeout
	if !plan.Timeout.IsNull() {
		timeout, err := time.ParseDuration(plan.Timeout.ValueString())
		if err != nil {
			resp.Diagnostics.AddError(
				"Invalid Timeout",
				fmt.Sprintf("Could not parse timeout '%s': %s", plan.Timeout.ValueString(), err.Error()),
			)
			return
		}
		upgrade.Timeout = timeout
	}

	// 4. Load chart
	chart, err := r.loadChart(ctx, actionConfig, &plan)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Load Chart",
			fmt.Sprintf("Could not load Helm chart: %s", err.Error()),
		)
		return
	}

	// 5. Merge values
	values, err := r.mergeValues(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Merge Values",
			fmt.Sprintf("Could not merge Helm values: %s", err.Error()),
		)
		return
	}

	// 6. Run upgrade
	releaser, err := upgrade.Run(plan.Name.ValueString(), chart, values)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Upgrade Helm Release",
			fmt.Sprintf("Could not upgrade Helm release '%s': %s", plan.Name.ValueString(), err.Error()),
		)
		return
	}

	// Helm v4: Run() returns release.Releaser interface, need type assertion
	rel, ok := releaser.(*release.Release)
	if !ok {
		resp.Diagnostics.AddError(
			"Internal Error",
			"Failed to convert Helm release to concrete type",
		)
		return
	}

	// 7. Update computed fields from release
	if err := r.updateComputedFields(ctx, &plan, rel); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Update State",
			fmt.Sprintf("Could not update computed fields: %s", err.Error()),
		)
		return
	}

	tflog.Info(ctx, "Helm release upgraded successfully", map[string]interface{}{
		"name":     plan.Name.ValueString(),
		"revision": rel.Version,
		"status":   rel.Info.Status.String(),
	})

	// 8. Save state
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete uninstalls a Helm release
func (r *helmReleaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// 1. Extract state data
	var data helmReleaseResourceModel
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "Deleting Helm release", map[string]interface{}{
		"name":      data.Name.ValueString(),
		"namespace": data.Namespace.ValueString(),
	})

	// 2. Get Helm action configuration
	actionConfig, err := r.getActionConfig(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Configure Helm Client",
			fmt.Sprintf("Could not create Helm action configuration: %s", err.Error()),
		)
		return
	}

	// 3. Create Uninstall action
	uninstall := action.NewUninstall(actionConfig)

	// Helm v4: Use hookOnly strategy for uninstall (default behavior)
	// We don't need to wait for resource deletion, just hooks
	uninstall.WaitStrategy = "hookOnly"

	// Parse timeout
	if !data.Timeout.IsNull() {
		timeout, err := time.ParseDuration(data.Timeout.ValueString())
		if err != nil {
			resp.Diagnostics.AddError(
				"Invalid Timeout",
				fmt.Sprintf("Could not parse timeout '%s': %s", data.Timeout.ValueString(), err.Error()),
			)
			return
		}
		uninstall.Timeout = timeout
	}

	// 4. Run uninstall
	_, err = uninstall.Run(data.Name.ValueString())
	if err != nil {
		// If release not found, that's OK - already deleted
		if err == driver.ErrReleaseNotFound {
			tflog.Info(ctx, "Helm release already deleted")
			return
		}

		// ADR-022: Never remove from state if delete fails (fix #472)
		resp.Diagnostics.AddError(
			"Failed to Delete Helm Release",
			fmt.Sprintf("Could not uninstall Helm release '%s': %s\n\nThe release remains in Terraform state. "+
				"Fix the issue and run 'terraform apply' again to retry deletion.", data.Name.ValueString(), err.Error()),
		)
		return
	}

	tflog.Info(ctx, "Helm release deleted successfully")
	// State is automatically removed on successful Delete
}

// ImportState imports an existing Helm release into Terraform state
// Import ID format: context:namespace:release-name or context:release-name (uses "default" namespace)
func (r *helmReleaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Parse import ID: "context:namespace:release-name" or "context:release-name" for default namespace
	parts := strings.Split(req.ID, ":")
	var kubeContext, namespace, releaseName string

	switch len(parts) {
	case 2:
		// context:release-name - use default namespace
		kubeContext = parts[0]
		namespace = "default"
		releaseName = parts[1]
	case 3:
		// context:namespace:release-name
		kubeContext = parts[0]
		namespace = parts[1]
		releaseName = parts[2]
	default:
		resp.Diagnostics.AddError(
			"Invalid Import ID Format",
			fmt.Sprintf("Import ID must be in format 'context:namespace:release-name' or 'context:release-name'.\n\n"+
				"Examples:\n"+
				"  prod:kube-system:cilium\n"+
				"  prod:cert-manager  (uses default namespace)\n\n"+
				"Got: %s", req.ID),
		)
		return
	}

	tflog.Info(ctx, "Importing Helm release", map[string]interface{}{
		"context":   kubeContext,
		"namespace": namespace,
		"name":      releaseName,
	})

	// Load kubeconfig from environment or default location
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		homeDir := os.Getenv("HOME")
		if homeDir == "" {
			resp.Diagnostics.AddError(
				"Import Failed: KUBECONFIG Not Found",
				"KUBECONFIG environment variable is not set and HOME directory could not be determined.\n\n"+
					"Set KUBECONFIG environment variable:\n"+
					"  export KUBECONFIG=~/.kube/config\n"+
					"  terraform import k8sconnect_helm_release.example prod:kube-system:cilium",
			)
			return
		}
		kubeconfigPath = filepath.Join(homeDir, ".kube", "config")
	}

	// Read kubeconfig file
	kubeconfigData, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: Cannot Read Kubeconfig",
			fmt.Sprintf("Failed to read kubeconfig file at %s: %s", kubeconfigPath, err.Error()),
		)
		return
	}

	// Create temporary connection model for import
	tempConn := auth.ClusterModel{
		Kubeconfig: types.StringValue(string(kubeconfigData)),
		Context:    types.StringValue(kubeContext),
	}

	// Create temporary resource model to use getActionConfig
	tempData := helmReleaseResourceModel{
		Name:      types.StringValue(releaseName),
		Namespace: types.StringValue(namespace),
		Cluster:   types.ObjectNull(nil), // Will be populated below
	}

	// Convert connection to object
	clusterObj, diags := types.ObjectValueFrom(ctx, auth.GetConnectionAttributeTypes(), tempConn)
	if diags.HasError() {
		resp.Diagnostics.AddError(
			"Import Failed: Connection Conversion Error",
			fmt.Sprintf("Failed to convert connection model: %s", diags.Errors()[0].Summary()),
		)
		return
	}
	tempData.Cluster = clusterObj

	// Get Helm action configuration
	actionConfig, err := r.getActionConfig(ctx, &tempData)
	if err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: Helm Configuration Error",
			fmt.Sprintf("Could not create Helm action configuration: %s", err.Error()),
		)
		return
	}

	// Get the release from cluster
	get := action.NewGet(actionConfig)
	releaser, err := get.Run(releaseName)
	if err != nil {
		if err == driver.ErrReleaseNotFound {
			resp.Diagnostics.AddError(
				"Import Failed: Release Not Found",
				fmt.Sprintf("Helm release '%s' not found in namespace '%s' (context: %s).\n\n"+
					"Verify the release exists:\n"+
					"  helm list -n %s --kube-context %s",
					releaseName, namespace, kubeContext, namespace, kubeContext),
			)
		} else {
			resp.Diagnostics.AddError(
				"Import Failed",
				fmt.Sprintf("Failed to get Helm release: %s", err.Error()),
			)
		}
		return
	}

	// Helm v4: Get() returns release.Releaser interface, need type assertion
	rel, ok := releaser.(*release.Release)
	if !ok {
		resp.Diagnostics.AddError(
			"Internal Error",
			"Failed to convert Helm release to concrete type",
		)
		return
	}

	// Generate resource ID
	resourceID := common.GenerateID()

	// Populate imported data
	importedData := helmReleaseResourceModel{
		ID:        types.StringValue(resourceID),
		Name:      types.StringValue(releaseName),
		Namespace: types.StringValue(namespace),
		Cluster:   clusterObj,

		// Chart info from release
		Chart:   types.StringValue(rel.Chart.Metadata.Name),
		Version: types.StringValue(rel.Chart.Metadata.Version),

		// Repository is unknown from import - user must specify
		Repository: types.StringNull(),

		// Values are unknown - user must specify
		Values:       types.StringNull(),
		Set:          types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "value": types.StringType}}),
		SetSensitive: types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "value": types.StringType}}),
		SetList:      types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "value": types.StringType}}),
		SetString:    types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "value": types.StringType}}),

		// Repository auth unknown
		RepositoryUsername: types.StringNull(),
		RepositoryPassword: types.StringNull(),
		RepositoryKeyFile:  types.StringNull(),
		RepositoryCertFile: types.StringNull(),
		RepositoryCaFile:   types.StringNull(),

		// Options - use defaults
		CreateNamespace:  types.BoolValue(false),
		DependencyUpdate: types.BoolValue(false),
		SkipCRDs:         types.BoolValue(false),
		Atomic:           types.BoolValue(false),
		Wait:             types.BoolValue(true),
		WaitForJobs:      types.BoolValue(false),
		Timeout:          types.StringValue("300s"),
		DisableWebhooks:  types.BoolValue(false),
		RecreatePods:     types.BoolValue(false),
		ForceDestroy:     types.BoolValue(false),
	}

	// Update computed fields from release
	if err := r.updateComputedFields(ctx, &importedData, rel); err != nil {
		resp.Diagnostics.AddError(
			"Import Failed: State Update Error",
			fmt.Sprintf("Could not update computed fields: %s", err.Error()),
		)
		return
	}

	// Set imported state
	resp.Diagnostics.Append(resp.State.Set(ctx, &importedData)...)

	tflog.Info(ctx, "Helm release imported successfully", map[string]interface{}{
		"id":        resourceID,
		"name":      releaseName,
		"namespace": namespace,
		"chart":     rel.Chart.Metadata.Name,
		"version":   rel.Chart.Metadata.Version,
		"revision":  rel.Version,
	})

	// Add warning about repository and values
	resp.Diagnostics.AddWarning(
		"Import Successful - Configuration Required",
		fmt.Sprintf("Helm release '%s' imported successfully.\n\n"+
			"The following fields were imported:\n"+
			"- chart: %s\n"+
			"- version: %s\n"+
			"- revision: %d\n\n"+
			"You must add to your Terraform configuration:\n"+
			"- repository: The Helm repository URL\n"+
			"- values: Any custom values (check with: helm get values %s -n %s)\n\n"+
			"The cluster connection uses your KUBECONFIG. Replace with your actual connection config.",
			releaseName, rel.Chart.Metadata.Name, rel.Chart.Metadata.Version, rel.Version,
			releaseName, namespace),
	)
}

// Helper functions that will be implemented

func (r *helmReleaseResource) getActionConfig(ctx context.Context, data *helmReleaseResourceModel) (*action.Configuration, error) {
	// Parse cluster configuration
	var clusterModel auth.ClusterModel
	diags := data.Cluster.As(ctx, &clusterModel, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, fmt.Errorf("failed to parse cluster configuration: %s", diags.Errors()[0].Summary())
	}

	// Create RESTClientGetter bridge
	restClientGetter, err := newRESTClientGetter(ctx, data.Namespace.ValueString(), clusterModel, r.clientFactory)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create Helm action configuration
	actionConfig := new(action.Configuration)
	// Helm v4: Init no longer takes logger function (logging is handled internally)
	if err := actionConfig.Init(restClientGetter, data.Namespace.ValueString(), "secret"); err != nil {
		return nil, fmt.Errorf("failed to initialize Helm action configuration: %w", err)
	}

	return actionConfig, nil
}

// helmLogger adapts Terraform's structured logging to Helm's logger function
func helmLogger(ctx context.Context) func(format string, v ...interface{}) {
	return func(format string, v ...interface{}) {
		tflog.Debug(ctx, fmt.Sprintf(format, v...))
	}
}

func (r *helmReleaseResource) loadChart(ctx context.Context, cfg *action.Configuration, data *helmReleaseResourceModel) (*chart.Chart, error) {
	chartName := data.Chart.ValueString()

	// Case 1: Local chart path
	if isLocalChart(chartName) {
		tflog.Debug(ctx, "Loading local chart", map[string]interface{}{"path": chartName})
		chartPath, err := filepath.Abs(chartName)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve chart path: %w", err)
		}

		// Handle dependency update for local charts BEFORE loading
		if data.DependencyUpdate.ValueBool() {
			if err := r.updateDependencies(ctx, chartPath); err != nil {
				return nil, fmt.Errorf("failed to update chart dependencies: %w", err)
			}
		}

		chart, err := loader.Load(chartPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load local chart: %w", err)
		}

		return chart, nil
	}

	// Case 2: Repository chart (HTTP/HTTPS or OCI)
	repository := data.Repository.ValueString()
	if repository == "" {
		return nil, fmt.Errorf("repository must be specified for remote charts")
	}

	// Setup chart path options for repository authentication
	chartPathOpts := action.ChartPathOptions{
		Version: data.Version.ValueString(),
		RepoURL: repository,
	}

	// Configure repository authentication if provided
	if !data.RepositoryUsername.IsNull() {
		chartPathOpts.Username = data.RepositoryUsername.ValueString()
	}
	if !data.RepositoryPassword.IsNull() {
		chartPathOpts.Password = data.RepositoryPassword.ValueString()
	}
	if !data.RepositoryCertFile.IsNull() {
		chartPathOpts.CertFile = data.RepositoryCertFile.ValueString()
	}
	if !data.RepositoryKeyFile.IsNull() {
		chartPathOpts.KeyFile = data.RepositoryKeyFile.ValueString()
	}
	if !data.RepositoryCaFile.IsNull() {
		chartPathOpts.CaFile = data.RepositoryCaFile.ValueString()
	}

	// Handle OCI registries
	if strings.HasPrefix(repository, "oci://") {
		tflog.Debug(ctx, "Loading chart from OCI registry", map[string]interface{}{
			"registry": repository,
			"chart":    chartName,
		})
		return r.loadOCIChart(ctx, cfg, chartName, &chartPathOpts, data.DependencyUpdate.ValueBool())
	}

	// Handle HTTP/HTTPS repositories
	tflog.Debug(ctx, "Loading chart from repository", map[string]interface{}{
		"repository": repository,
		"chart":      chartName,
	})
	return r.loadRepoChart(ctx, cfg, chartName, &chartPathOpts, data.DependencyUpdate.ValueBool())
}

// isLocalChart checks if the chart reference is a local path
func isLocalChart(chartRef string) bool {
	// Local if it starts with ./ or ../ or / or is a relative path
	return strings.HasPrefix(chartRef, "./") ||
		strings.HasPrefix(chartRef, "../") ||
		strings.HasPrefix(chartRef, "/") ||
		!strings.Contains(chartRef, "/") && fileExists(chartRef)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (r *helmReleaseResource) updateDependencies(ctx context.Context, chartPath string) error {
	tflog.Debug(ctx, "Updating chart dependencies", map[string]interface{}{"path": chartPath})

	// Load the chart to check if it has dependencies
	c, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart for dependency check: %w", err)
	}

	// If no dependencies, nothing to do
	if c.Metadata.Dependencies == nil || len(c.Metadata.Dependencies) == 0 {
		tflog.Debug(ctx, "Chart has no dependencies, skipping dependency update")
		return nil
	}

	tflog.Info(ctx, "Downloading chart dependencies", map[string]interface{}{
		"chart":        c.Metadata.Name,
		"dependencies": len(c.Metadata.Dependencies),
	})

	// Create a dependency manager
	settings := cli.New()
	man := &downloader.Manager{
		Out:              os.Stdout,
		ChartPath:        chartPath,
		Keyring:          settings.RepositoryConfig,
		SkipUpdate:       false,
		Getters:          getter.All(settings),
		RepositoryConfig: settings.RepositoryConfig,
		RepositoryCache:  settings.RepositoryCache,
		Debug:            false,
	}

	// Download all dependencies
	err = man.Update()
	if err != nil {
		return fmt.Errorf("failed to update chart dependencies: %w", err)
	}

	tflog.Info(ctx, "Successfully updated chart dependencies", map[string]interface{}{
		"chart": c.Metadata.Name,
	})

	return nil
}

func (r *helmReleaseResource) loadOCIChart(ctx context.Context, cfg *action.Configuration, chartName string, opts *action.ChartPathOptions, updateDeps bool) (*chart.Chart, error) {
	// Setup OCI registry client
	registryClient, err := registry.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create registry client: %w", err)
	}

	// Configure authentication if provided
	if opts.Username != "" && opts.Password != "" {
		if err := registryClient.Login(opts.RepoURL, registry.LoginOptBasicAuth(opts.Username, opts.Password)); err != nil {
			return nil, fmt.Errorf("failed to authenticate with OCI registry: %w", err)
		}
	}

	cfg.RegistryClient = registryClient

	// Pull chart from OCI registry
	// Helm v4: NewPull still uses WithConfig pattern
	pull := action.NewPull(action.WithConfig(cfg))
	pull.Settings = cli.New()
	if opts.Version != "" {
		pull.Version = opts.Version
	}

	// Construct full OCI reference
	chartRef := fmt.Sprintf("%s/%s", strings.TrimPrefix(opts.RepoURL, "oci://"), chartName)

	output, err := pull.Run(chartRef)
	if err != nil {
		return nil, fmt.Errorf("failed to pull OCI chart: %w", err)
	}

	// Update dependencies if requested
	if updateDeps {
		if err := r.updateDependencies(ctx, output); err != nil {
			return nil, err
		}
	}

	// Load the pulled chart (with dependencies if updated)
	return loader.Load(output)
}

func (r *helmReleaseResource) loadRepoChart(ctx context.Context, cfg *action.Configuration, chartName string, opts *action.ChartPathOptions, updateDeps bool) (*chart.Chart, error) {
	// Add repository
	chartRepo, err := repo.NewChartRepository(&repo.Entry{
		Name:     "temp-repo",
		URL:      opts.RepoURL,
		Username: opts.Username,
		Password: opts.Password,
		CertFile: opts.CertFile,
		KeyFile:  opts.KeyFile,
		CAFile:   opts.CaFile,
	}, getter.All(cli.New()))
	if err != nil {
		return nil, fmt.Errorf("failed to create chart repository: %w", err)
	}

	// Download index
	if _, err := chartRepo.DownloadIndexFile(); err != nil {
		return nil, fmt.Errorf("failed to download repository index: %w", err)
	}

	// Use ChartPathOptions to locate and download chart
	client := action.NewInstall(cfg)
	client.ChartPathOptions = *opts

	chartPath, err := client.ChartPathOptions.LocateChart(chartName, cli.New())
	if err != nil {
		return nil, fmt.Errorf("failed to locate chart: %w", err)
	}

	// Update dependencies if requested
	if updateDeps {
		if err := r.updateDependencies(ctx, chartPath); err != nil {
			return nil, err
		}
	}

	// Load the downloaded chart (now with updated dependencies if requested)
	chart, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart: %w", err)
	}

	return chart, nil
}

func (r *helmReleaseResource) mergeValues(ctx context.Context, data *helmReleaseResourceModel) (map[string]interface{}, error) {
	// Start with empty values map
	// Chart defaults are handled by Helm itself during install/upgrade
	values := make(map[string]interface{})

	// 1. Merge values from YAML string (lowest precedence for user-provided values)
	if !data.Values.IsNull() && data.Values.ValueString() != "" {
		var yamlValues map[string]interface{}
		if err := yaml.Unmarshal([]byte(data.Values.ValueString()), &yamlValues); err != nil {
			return nil, fmt.Errorf("failed to parse values YAML: %w", err)
		}
		values = mergeMaps(values, yamlValues)
	}

	// 2. Merge set values (standard key=value pairs)
	if !data.Set.IsNull() {
		var setValues []setValueModel
		diags := data.Set.ElementsAs(ctx, &setValues, false)
		if diags.HasError() {
			return nil, fmt.Errorf("failed to parse set values: %s", diags.Errors()[0].Summary())
		}
		for _, sv := range setValues {
			if err := setNestedValue(values, sv.Name.ValueString(), sv.Value.ValueString()); err != nil {
				return nil, fmt.Errorf("failed to set value '%s': %w", sv.Name.ValueString(), err)
			}
		}
	}

	// 3. Merge set_list values
	if !data.SetList.IsNull() {
		var setListValues []setValueModel
		diags := data.SetList.ElementsAs(ctx, &setListValues, false)
		if diags.HasError() {
			return nil, fmt.Errorf("failed to parse set_list values: %s", diags.Errors()[0].Summary())
		}
		for _, sv := range setListValues {
			// set_list values are comma-separated
			listItems := strings.Split(sv.Value.ValueString(), ",")
			if err := setNestedValue(values, sv.Name.ValueString(), listItems); err != nil {
				return nil, fmt.Errorf("failed to set list value '%s': %w", sv.Name.ValueString(), err)
			}
		}
	}

	// 4. Merge set_string values (forces string type, no type inference)
	if !data.SetString.IsNull() {
		var setStringValues []setValueModel
		diags := data.SetString.ElementsAs(ctx, &setStringValues, false)
		if diags.HasError() {
			return nil, fmt.Errorf("failed to parse set_string values: %s", diags.Errors()[0].Summary())
		}
		for _, sv := range setStringValues {
			// Force as string, don't parse as number/bool
			if err := setNestedValue(values, sv.Name.ValueString(), sv.Value.ValueString()); err != nil {
				return nil, fmt.Errorf("failed to set string value '%s': %w", sv.Name.ValueString(), err)
			}
		}
	}

	// 5. Merge set_sensitive values (highest precedence)
	if !data.SetSensitive.IsNull() {
		var setSensitiveValues []setValueModel
		diags := data.SetSensitive.ElementsAs(ctx, &setSensitiveValues, false)
		if diags.HasError() {
			return nil, fmt.Errorf("failed to parse set_sensitive values: %s", diags.Errors()[0].Summary())
		}
		for _, sv := range setSensitiveValues {
			if err := setNestedValue(values, sv.Name.ValueString(), sv.Value.ValueString()); err != nil {
				return nil, fmt.Errorf("failed to set sensitive value '%s': %w", sv.Name.ValueString(), err)
			}
		}
	}

	return values, nil
}

// mergeMaps merges source map into destination map
func mergeMaps(dst, src map[string]interface{}) map[string]interface{} {
	for key, srcVal := range src {
		if dstVal, exists := dst[key]; exists {
			// Both are maps, merge recursively
			srcMap, srcIsMap := srcVal.(map[string]interface{})
			dstMap, dstIsMap := dstVal.(map[string]interface{})
			if srcIsMap && dstIsMap {
				dst[key] = mergeMaps(dstMap, srcMap)
				continue
			}
		}
		// Otherwise, source value overwrites destination
		dst[key] = srcVal
	}
	return dst
}

// setNestedValue sets a value in a nested map using dot notation (e.g., "image.tag")
func setNestedValue(values map[string]interface{}, key string, value interface{}) error {
	// Split key by dots to handle nesting
	parts := strings.Split(key, ".")
	current := values

	// Navigate to the parent of the final key
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if existing, exists := current[part]; exists {
			// Ensure existing value is a map
			if existingMap, ok := existing.(map[string]interface{}); ok {
				current = existingMap
			} else {
				return fmt.Errorf("cannot set nested value: '%s' is not a map", part)
			}
		} else {
			// Create new map for this level
			newMap := make(map[string]interface{})
			current[part] = newMap
			current = newMap
		}
	}

	// Set the final value
	finalKey := parts[len(parts)-1]
	current[finalKey] = value

	return nil
}

func (r *helmReleaseResource) updateComputedFields(ctx context.Context, data *helmReleaseResourceModel, rel *release.Release) error {
	// Update manifest (rendered YAML)
	data.Manifest = types.StringValue(rel.Manifest)

	// Update status
	data.Status = types.StringValue(rel.Info.Status.String())

	// Update revision
	data.Revision = types.Int64Value(int64(rel.Version))

	// Update metadata
	metadata := make(map[string]string)
	if rel.Chart != nil && rel.Chart.Metadata != nil {
		metadata["chart_name"] = rel.Chart.Metadata.Name
		metadata["chart_version"] = rel.Chart.Metadata.Version
		if rel.Chart.Metadata.AppVersion != "" {
			metadata["app_version"] = rel.Chart.Metadata.AppVersion
		}
		if rel.Chart.Metadata.Description != "" {
			metadata["description"] = rel.Chart.Metadata.Description
		}
	}

	if rel.Info != nil {
		if !rel.Info.FirstDeployed.IsZero() {
			metadata["first_deployed"] = rel.Info.FirstDeployed.Format(time.RFC3339)
		}
		if !rel.Info.LastDeployed.IsZero() {
			metadata["last_deployed"] = rel.Info.LastDeployed.Format(time.RFC3339)
		}
		if rel.Info.Notes != "" {
			metadata["notes"] = rel.Info.Notes
		}
	}

	metadataValue, diags := types.MapValueFrom(ctx, types.StringType, metadata)
	if diags.HasError() {
		return fmt.Errorf("failed to convert metadata to map: %s", diags.Errors()[0].Summary())
	}
	data.Metadata = metadataValue

	return nil
}

func (r *helmReleaseResource) detectDrift(ctx context.Context, state *helmReleaseResourceModel, current *release.Release) (bool, []string) {
	var driftReasons []string
	hasDrift := false

	// 1. Compare revision numbers (detects manual helm upgrade/rollback)
	if !state.Revision.IsNull() {
		stateRevision := state.Revision.ValueInt64()
		currentRevision := int64(current.Version)
		if stateRevision != currentRevision {
			hasDrift = true
			driftReasons = append(driftReasons, fmt.Sprintf(
				"Release revision changed from %d to %d (manual helm operation detected)",
				stateRevision, currentRevision))
			tflog.Warn(ctx, "Drift detected: revision mismatch", map[string]interface{}{
				"state_revision":   stateRevision,
				"current_revision": currentRevision,
			})
		}
	}

	// 2. Compare chart version (detects chart upgrades and OCI digest changes)
	// Note: Helm's Chart.Metadata.Version doesn't include OCI digests (just semantic version)
	// For digest-based deployments, we store the user's original version string in state
	// Drift is detected if someone manually upgrades to a different version tag
	// Digest-only changes (same tag, different digest) require OCI registry queries to detect
	if !state.Version.IsNull() && current.Chart != nil && current.Chart.Metadata != nil {
		stateVersion := state.Version.ValueString()
		currentVersion := current.Chart.Metadata.Version

		// Strip digest from state version for comparison (Helm doesn't preserve it)
		stateVersionOnly := stateVersion
		if idx := strings.Index(stateVersion, "@sha256:"); idx != -1 {
			stateVersionOnly = stateVersion[:idx]
		}

		// Check for drift
		if stateVersionOnly != currentVersion {
			hasDrift = true

			// Detect if state had a digest reference
			hadDigest := strings.Contains(stateVersion, "@sha256:")

			if hadDigest {
				driftReasons = append(driftReasons, fmt.Sprintf(
					"Chart version changed from %s to %s (digest reference in state but release shows different version)",
					stateVersion, currentVersion))
				tflog.Warn(ctx, "Drift detected: chart version mismatch (digest tracking limited)", map[string]interface{}{
					"state_version":   stateVersion,
					"current_version": currentVersion,
				})
			} else {
				driftReasons = append(driftReasons, fmt.Sprintf(
					"Chart version changed from %s to %s",
					stateVersionOnly, currentVersion))
				tflog.Warn(ctx, "Drift detected: chart version mismatch", map[string]interface{}{
					"state_version":   stateVersionOnly,
					"current_version": currentVersion,
				})
			}
		}
	}

	// 3. Compare chart name (detects complete chart replacement)
	if !state.Chart.IsNull() && current.Chart != nil && current.Chart.Metadata != nil {
		stateChart := state.Chart.ValueString()
		currentChart := current.Chart.Metadata.Name
		if stateChart != currentChart {
			hasDrift = true
			driftReasons = append(driftReasons, fmt.Sprintf(
				"Chart name changed from %s to %s",
				stateChart, currentChart))
			tflog.Warn(ctx, "Drift detected: chart name mismatch", map[string]interface{}{
				"state_chart":   stateChart,
				"current_chart": currentChart,
			})
		}
	}

	// 4. Compare release status (detects failed releases)
	if !state.Status.IsNull() && current.Info != nil {
		stateStatus := state.Status.ValueString()
		currentStatus := current.Info.Status.String()
		// Only report drift if status changed to a problematic state
		if stateStatus != currentStatus {
			// Status changes are expected for deployments, only warn on unexpected states
			if currentStatus == "failed" || currentStatus == "superseded" || currentStatus == "uninstalling" {
				hasDrift = true
				driftReasons = append(driftReasons, fmt.Sprintf(
					"Release status changed from %s to %s",
					stateStatus, currentStatus))
				tflog.Warn(ctx, "Drift detected: release status changed", map[string]interface{}{
					"state_status":   stateStatus,
					"current_status": currentStatus,
				})
			}
		}
	}

	// 5. Check if release was modified by a different manager
	// This would be detected by comparing values, but Helm doesn't expose the exact values used
	// We rely on revision number changes to catch this

	if hasDrift {
		tflog.Warn(ctx, "Drift detected in Helm release", map[string]interface{}{
			"release":       state.Name.ValueString(),
			"namespace":     state.Namespace.ValueString(),
			"drift_reasons": strings.Join(driftReasons, "; "),
		})
	}

	return hasDrift, driftReasons
}
