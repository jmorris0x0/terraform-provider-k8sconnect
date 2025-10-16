// internal/k8sconnect/resource/manifest/crud_operations.go
package manifest

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	Ctx                        context.Context
	Data                       *manifestResourceModel
	Connection                 auth.ClusterConnectionModel
	Client                     k8sclient.K8sClient
	Object                     *unstructured.Unstructured
	GVR                        schema.GroupVersionResource
	ImportedWithoutAnnotations bool // Private state flag
}

// prepareContext sets up the ResourceContext with all common elements
func (r *manifestResource) prepareContext(
	ctx context.Context,
	data *manifestResourceModel,
	requireConnection bool,
) (*ResourceContext, error) {

	rc := &ResourceContext{
		Ctx:  ctx,
		Data: data,
	}

	// Step 1: Load and validate connection from resource data
	conn, err := r.loadConnectionFromData(ctx, data, requireConnection)
	if err != nil {
		return nil, err
	}
	rc.Connection = conn

	// Step 2: Parse YAML (if present)
	if !data.YAMLBody.IsNull() && data.YAMLBody.ValueString() != "" {
		obj, err := r.parseYAML(data.YAMLBody.ValueString())
		if err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
		rc.Object = obj
	}

	// Step 3: Create client (if we have a connection)
	if !r.isConnectionEmpty(conn) {
		// Try clientFactory first, fall back to clientGetter
		var client k8sclient.K8sClient
		var err error

		if r.clientFactory != nil {
			client, err = r.clientFactory.GetClient(conn)
		} else if r.clientGetter != nil {
			client, err = r.clientGetter(conn)
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
				// During apply phase, if this is a CRD-not-found error, we'll retry later
				// So don't fail here - let the apply retry logic handle it
				if r.isCRDNotFoundError(err) {
					tflog.Debug(ctx, "CRD not found during prepareContext, will retry during apply", map[string]interface{}{
						"kind": rc.Object.GetKind(),
						"name": rc.Object.GetName(),
					})
					// Leave GVR empty - operations that need it will be skipped
					rc.GVR = schema.GroupVersionResource{}
				} else {
					// Other errors should fail immediately
					return nil, fmt.Errorf("failed to determine resource type: %w", err)
				}
			} else {
				rc.GVR = gvr
			}
		}
	}

	return rc, nil
}

// loadConnectionFromData now just gets the connection from the resource data
func (r *manifestResource) loadConnectionFromData(
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
	conn, err := r.convertObjectToConnectionModel(ctx, data.ClusterConnection)
	if err != nil {
		return auth.ClusterConnectionModel{}, fmt.Errorf("invalid connection: %w", err)
	}

	return conn, nil
}

// executeWait handles wait conditions
func (r *manifestResource) executeWait(rc *ResourceContext) error {
	if rc.Data.WaitFor.IsNull() {
		return nil
	}

	var waitConfig waitForModel
	diags := rc.Data.WaitFor.As(rc.Ctx, &waitConfig, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return fmt.Errorf("invalid wait_for configuration: %s", diags.Errors())
	}

	// Check if we have actual conditions
	if !r.hasActiveWaitConditions(waitConfig) {
		tflog.Debug(rc.Ctx, "wait_for configured but no active conditions")
		return nil
	}

	// Execute the wait
	return r.waitForResource(rc.Ctx, rc.Client, rc.GVR, rc.Object, waitConfig)
}

// hasActiveWaitConditions checks if there are real conditions to wait for
func (r *manifestResource) hasActiveWaitConditions(waitConfig waitForModel) bool {
	return (!waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != "") ||
		!waitConfig.FieldValue.IsNull() ||
		(!waitConfig.Condition.IsNull() && waitConfig.Condition.ValueString() != "") ||
		(!waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool())
}

// shouldTrackStatus determines if status field should be populated based on wait_for config
// Only wait_for.field populates status (other wait types don't track status)
func (r *manifestResource) shouldTrackStatus(ctx context.Context, data *manifestResourceModel) bool {
	if data.WaitFor.IsNull() || data.WaitFor.IsUnknown() {
		return false
	}

	var waitConfig waitForModel
	diags := data.WaitFor.As(ctx, &waitConfig, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return false
	}

	// Only field waits populate status
	return !waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != ""
}

// updateStatus updates the status field based on wait results
// Uses private state to handle pending waits (similar to ADR-006 projection recovery)
func (r *manifestResource) updateStatus(
	rc *ResourceContext,
	waited bool,
	privateGetter interface {
		GetKey(context.Context, string) ([]byte, diag.Diagnostics)
	},
	privateSetter interface {
		SetKey(context.Context, string, []byte) diag.Diagnostics
	},
) error {
	// No wait = no status, clear any pending flag
	if !waited {
		rc.Data.Status = types.DynamicNull()
		if privateGetter != nil && privateSetter != nil {
			clearPendingWaitStatusFlag(rc.Ctx, privateSetter)
		}
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
		// Set to null (not unknown - Terraform contract!) and mark pending
		// Plan modifier will set to unknown during next plan to block downstream DAG
		rc.Data.Status = types.DynamicNull()
		if privateSetter != nil {
			setPendingWaitStatusFlag(rc.Ctx, privateSetter)
		}
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
				// Set to null and mark pending (not unknown - Terraform contract!)
				rc.Data.Status = types.DynamicNull()
				if privateSetter != nil {
					setPendingWaitStatusFlag(rc.Ctx, privateSetter)
				}
			} else {
				// Success! Set the actual value and clear any pending flag
				rc.Data.Status = types.DynamicValue(statusValue)
				if privateSetter != nil {
					clearPendingWaitStatusFlag(rc.Ctx, privateSetter)
				}
			}
		} else {
			tflog.Debug(rc.Ctx, "Field not found in status, setting to null with pending flag", map[string]interface{}{
				"field": waitConfig.Field.ValueString(),
			})
			// Field not found YET - set to null and mark pending (not unknown!)
			// Plan modifier will set to unknown during next plan to block downstream
			rc.Data.Status = types.DynamicNull()
			if privateSetter != nil {
				setPendingWaitStatusFlag(rc.Ctx, privateSetter)
			}
		}
	} else {
		// No status in K8s resource YET - set to null and mark pending
		tflog.Debug(rc.Ctx, "No status in K8s resource, setting to null with pending flag")
		rc.Data.Status = types.DynamicNull()
		if privateSetter != nil {
			setPendingWaitStatusFlag(rc.Ctx, privateSetter)
		}
	}

	return nil
}

// updateProjection updates managed state projection and field ownership
func (r *manifestResource) updateProjection(rc *ResourceContext) error {
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

	// Apply ignore_fields filtering if specified
	if ignoreFields := getIgnoreFields(rc.Ctx, rc.Data); ignoreFields != nil {
		paths = filterIgnoredPaths(paths, ignoreFields)
		tflog.Debug(rc.Ctx, "Applied ignore_fields filtering in projection update", map[string]interface{}{
			"ignored_count":  len(ignoreFields),
			"filtered_paths": len(paths),
		})
	}

	// Create projection - always project from the current K8s object
	projection, err := projectFields(currentObj.Object, paths)
	if err != nil {
		return fmt.Errorf("failed to project fields: %w", err)
	}

	// Convert projection to flat map for clean diff display
	projectionMap := flattenProjectionToMap(projection, paths)

	// Convert to types.Map
	mapValue, diags := types.MapValueFrom(rc.Ctx, types.StringType, projectionMap)
	if diags.HasError() {
		tflog.Warn(rc.Ctx, "Failed to convert projection to map", map[string]interface{}{
			"diagnostics": diags,
		})
		// Set empty map on error
		emptyMap, _ := types.MapValueFrom(rc.Ctx, types.StringType, map[string]string{})
		rc.Data.ManagedStateProjection = emptyMap
	} else {
		rc.Data.ManagedStateProjection = mapValue
	}

	tflog.Debug(rc.Ctx, "Updated projection", map[string]interface{}{
		"path_count": len(paths),
		"map_size":   len(projectionMap),
	})

	// Update field ownership (existing code continues...)
	ownership := extractFieldOwnership(currentObj)

	// Convert map[string]FieldOwnership to map[string]string (just manager names)
	// Filter out status fields - they're always owned by controllers and provide no actionable information
	ownershipMap := make(map[string]string, len(ownership))
	for path, owner := range ownership {
		// Skip status fields - they're read-only subresources managed by controllers
		// (similar to how status is filtered in yaml.go during object cleanup)
		if strings.HasPrefix(path, "status.") || path == "status" {
			continue
		}
		ownershipMap[path] = owner.Manager
	}

	// Convert to types.Map
	mapValue, diags = types.MapValueFrom(rc.Ctx, types.StringType, ownershipMap)
	if diags.HasError() {
		tflog.Warn(rc.Ctx, "Failed to convert field ownership to map", map[string]interface{}{
			"diagnostics": diags,
		})
		// Set empty map on error
		emptyMap, _ := types.MapValueFrom(rc.Ctx, types.StringType, map[string]string{})
		rc.Data.FieldOwnership = emptyMap
	} else {
		rc.Data.FieldOwnership = mapValue
	}

	// Clear ImportedWithoutAnnotations after first update (will be handled by Update function)

	return nil
}

// isConnectionEmpty checks if connection is empty
func (r *manifestResource) isConnectionEmpty(conn auth.ClusterConnectionModel) bool {
	return conn.Host.IsNull() &&
		conn.Kubeconfig.IsNull() &&
		conn.Kubeconfig.IsNull() &&
		(conn.Exec == nil || conn.Exec.APIVersion.IsNull())
}
