// internal/k8sconnect/resource/manifest/crud_pipeline.go
package manifest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/auth"
	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

// ResourceContext contains everything needed for any CRUD operation
type ResourceContext struct {
	Ctx        context.Context
	Data       *manifestResourceModel
	Connection auth.ClusterConnectionModel
	Client     k8sclient.K8sClient
	Object     *unstructured.Unstructured
	GVR        schema.GroupVersionResource
}

// OperationPipeline orchestrates common patterns across CRUD operations
type OperationPipeline struct {
	resource *manifestResource
}

// NewOperationPipeline creates a new pipeline instance
func NewOperationPipeline(r *manifestResource) *OperationPipeline {
	return &OperationPipeline{resource: r}
}

// PrepareContext sets up the ResourceContext with all common elements
func (p *OperationPipeline) PrepareContext(
	ctx context.Context,
	data *manifestResourceModel,
	requireConnection bool,
) (*ResourceContext, error) {

	rc := &ResourceContext{
		Ctx:  ctx,
		Data: data,
	}

	// Step 1: Load and validate connection from resource data
	conn, err := p.loadConnectionFromData(ctx, data, requireConnection)
	if err != nil {
		return nil, err
	}
	rc.Connection = conn

	// Step 2: Parse YAML (if present)
	if !data.YAMLBody.IsNull() && data.YAMLBody.ValueString() != "" {
		obj, err := p.resource.parseYAML(data.YAMLBody.ValueString())
		if err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
		rc.Object = obj
	}

	// Step 3: Create client (if we have a connection)
	if !p.isConnectionEmpty(conn) {
		// Try clientFactory first, fall back to clientGetter
		var client k8sclient.K8sClient
		var err error

		if p.resource.clientFactory != nil {
			client, err = p.resource.clientFactory.GetClient(conn)
		} else if p.resource.clientGetter != nil {
			client, err = p.resource.clientGetter(conn)
		} else {
			return nil, fmt.Errorf("no client factory or getter configured")
		}

		if err != nil {
			return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
		}
		rc.Client = client

		// Step 4: Get GVR (if we have an object)
		if rc.Object != nil {
			gvr, err := client.GetGVR(ctx, rc.Object)
			if err != nil {
				return nil, fmt.Errorf("failed to determine resource type: %w", err)
			}
			rc.GVR = gvr
		}
	}

	return rc, nil
}

// loadConnectionFromData now just gets the connection from the resource data
func (p *OperationPipeline) loadConnectionFromData(
	ctx context.Context,
	data *manifestResourceModel,
	requireConnection bool,
) (auth.ClusterConnectionModel, error) {

	// Connection is always required from the resource now
	if data.ClusterConnection.IsNull() || data.ClusterConnection.IsUnknown() {
		if requireConnection {
			return auth.ClusterConnectionModel{}, fmt.Errorf(
				"cluster_connection is required")
		}
		return auth.ClusterConnectionModel{}, nil
	}

	// Convert the connection object to our model
	conn, err := p.resource.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		return auth.ClusterConnectionModel{}, fmt.Errorf("invalid connection: %w", err)
	}

	return conn, nil
}

// ExecuteWait handles wait conditions
func (p *OperationPipeline) ExecuteWait(rc *ResourceContext) error {
	if rc.Data.WaitFor.IsNull() {
		return nil
	}

	var waitConfig waitForModel
	diags := rc.Data.WaitFor.As(rc.Ctx, &waitConfig, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return fmt.Errorf("invalid wait_for configuration: %s", diags.Errors())
	}

	// Check if we have actual conditions
	if !p.hasActiveWaitConditions(waitConfig) {
		tflog.Debug(rc.Ctx, "wait_for configured but no active conditions")
		return nil
	}

	// Execute the wait
	return p.resource.waitForResource(rc.Ctx, rc.Client, rc.GVR, rc.Object, waitConfig)
}

// hasActiveWaitConditions checks if there are real conditions to wait for
func (p *OperationPipeline) hasActiveWaitConditions(waitConfig waitForModel) bool {
	return (!waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != "") ||
		!waitConfig.FieldValue.IsNull() ||
		(!waitConfig.Condition.IsNull() && waitConfig.Condition.ValueString() != "") ||
		(!waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool())
}

// UpdateStatus updates the status field based on wait results
func (p *OperationPipeline) UpdateStatus(rc *ResourceContext, waited bool) error {
	// No wait = no status
	if !waited {
		rc.Data.Status = types.DynamicNull()
		return nil
	}

	// Parse wait configuration
	var waitConfig waitForModel
	diags := rc.Data.WaitFor.As(rc.Ctx, &waitConfig, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		rc.Data.Status = types.DynamicNull()
		return nil
	}

	// Only field waits populate status
	if waitConfig.Field.IsNull() || waitConfig.Field.ValueString() == "" {
		rc.Data.Status = types.DynamicNull()
		tflog.Debug(rc.Ctx, "Not populating status - not a field wait")
		return nil
	}

	// Get current state from cluster
	currentObj, err := rc.Client.Get(rc.Ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
	if err != nil {
		tflog.Warn(rc.Ctx, "Failed to read after wait", map[string]interface{}{"error": err.Error()})
		rc.Data.Status = types.DynamicNull()
		return nil
	}

	// Extract and prune status
	if statusRaw, found, _ := unstructured.NestedMap(currentObj.Object, "status"); found && len(statusRaw) > 0 {
		// PRUNE to only the waited-for field
		prunedStatus := pruneStatusToField(statusRaw, waitConfig.Field.ValueString())

		if prunedStatus != nil {
			tflog.Debug(rc.Ctx, "Pruned status to waited field", map[string]interface{}{
				"field": waitConfig.Field.ValueString(),
			})

			statusValue, err := common.ConvertToAttrValue(rc.Ctx, prunedStatus)
			if err != nil {
				tflog.Warn(rc.Ctx, "Failed to convert pruned status", map[string]interface{}{"error": err.Error()})
				rc.Data.Status = types.DynamicNull()
			} else {
				rc.Data.Status = types.DynamicValue(statusValue)
			}
		} else {
			tflog.Warn(rc.Ctx, "Field not found in status", map[string]interface{}{
				"field": waitConfig.Field.ValueString(),
			})
			rc.Data.Status = types.DynamicNull()
		}
	} else {
		// No status in K8s resource
		rc.Data.Status = types.DynamicNull()
	}

	return nil
}

// UpdateProjection updates managed state projection and field ownership
func (p *OperationPipeline) UpdateProjection(rc *ResourceContext) error {
	// Get current state - but we may already have it from two-phase create
	var currentObj *unstructured.Unstructured
	if len(rc.Object.GetManagedFields()) > 0 {
		// We already have managedFields from two-phase create
		currentObj = rc.Object
		tflog.Debug(rc.Ctx, "Using existing object with managedFields for projection")
	} else {
		// Need to fetch it
		var err error
		currentObj, err = rc.Client.Get(rc.Ctx, rc.GVR, rc.Object.GetNamespace(), rc.Object.GetName())
		if err != nil {
			return fmt.Errorf("failed to read for projection: %w", err)
		}
	}

	// Extract paths - use field ownership if flag is enabled
	var paths []string

	if len(currentObj.GetManagedFields()) > 0 {
		tflog.Debug(rc.Ctx, "Using field ownership for projection", map[string]interface{}{
			"managers": len(currentObj.GetManagedFields()),
		})
		paths = extractOwnedPaths(rc.Ctx, currentObj.GetManagedFields(), currentObj.Object)
	} else {
		tflog.Warn(rc.Ctx, "No managedFields available, using all fields from YAML")
		// When no ownership info, extract all fields from object
		paths = extractOwnedPaths(rc.Ctx, []metav1.ManagedFieldsEntry{}, rc.Object.Object)
	}

	// Create projection - always project from the current K8s object
	projection, err := projectFields(currentObj.Object, paths)
	if err != nil {
		return fmt.Errorf("failed to project fields: %w", err)
	}

	// Convert to JSON
	projectionJSON, err := toJSON(projection)
	if err != nil {
		return fmt.Errorf("failed to convert projection: %w", err)
	}

	rc.Data.ManagedStateProjection = types.StringValue(projectionJSON)
	tflog.Debug(rc.Ctx, "Updated projection", map[string]interface{}{
		"path_count": len(paths),
		"size":       len(projectionJSON),
	})

	// Update field ownership (existing code continues...)
	ownership := extractFieldOwnership(currentObj)
	ownershipJSON, err := json.Marshal(ownership)
	if err != nil {
		tflog.Warn(rc.Ctx, "Failed to marshal field ownership", map[string]interface{}{"error": err.Error()})
		rc.Data.FieldOwnership = types.StringValue("{}")
	} else {
		rc.Data.FieldOwnership = types.StringValue(string(ownershipJSON))
	}

	// Clear ImportedWithoutAnnotations after first update (existing code)
	if !rc.Data.ImportedWithoutAnnotations.IsNull() && rc.Data.ImportedWithoutAnnotations.ValueBool() {
		rc.Data.ImportedWithoutAnnotations = types.BoolNull()
	} else if rc.Data.ImportedWithoutAnnotations.IsUnknown() {
		rc.Data.ImportedWithoutAnnotations = types.BoolNull()
	}

	return nil
}

// isConnectionEmpty checks if connection is empty
func (p *OperationPipeline) isConnectionEmpty(conn auth.ClusterConnectionModel) bool {
	return conn.Host.IsNull() &&
		conn.KubeconfigFile.IsNull() &&
		conn.KubeconfigRaw.IsNull() &&
		(conn.Exec == nil || conn.Exec.APIVersion.IsNull())
}
