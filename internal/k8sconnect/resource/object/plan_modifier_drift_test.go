package object

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ADR-023 Phase 3: ModifyPlan drift detection with refreshed projection
//
// When Read returns stale state (e.g. expired token), ModifyPlan must use its
// own client.Get() result to create a "refreshed projection" for drift comparison.
// This ensures external changes are detected even when Read can't authenticate.

// TestComputeRefreshedProjection_BasicProjection verifies that
// computeRefreshedProjection extracts field values from the current cluster object.
func TestComputeRefreshedProjection_BasicProjection(t *testing.T) {
	ctx := context.Background()
	r := &objectResource{}

	currentObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-cm",
				"namespace": "default",
			},
			"data": map[string]interface{}{
				"key1": "current-value",
			},
		},
	}

	desiredObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-cm",
				"namespace": "default",
			},
			"data": map[string]interface{}{
				"key1": "desired-value",
			},
		},
	}

	paths := []string{"metadata.name", "metadata.namespace", "data.key1"}

	data := &objectResourceModel{
		IgnoreFields: types.ListNull(types.StringType),
	}

	result := r.computeRefreshedProjection(ctx, currentObj, desiredObj, paths, data)

	if result == nil {
		t.Fatal("Expected non-nil refreshed projection")
	}

	// The projection should contain the CURRENT cluster values (not desired)
	var resultMap map[string]string
	diags := result.ElementsAs(ctx, &resultMap, false)
	if diags.HasError() {
		t.Fatalf("Failed to extract projection map: %v", diags)
	}

	if resultMap["data.key1"] != "current-value" {
		t.Errorf("Expected data.key1='current-value' (from cluster), got %q", resultMap["data.key1"])
	}
}

// TestComputeRefreshedProjection_DetectsDrift verifies that when the cluster
// state has been modified externally (drift), the refreshed projection differs
// from the dry-run projection. This is the key behavior that enables drift
// detection when tokens expire between runs.
func TestComputeRefreshedProjection_DetectsDrift(t *testing.T) {
	ctx := context.Background()
	r := &objectResource{}

	// Current state on cluster — someone changed data.key1 externally
	currentObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-cm",
				"namespace": "default",
			},
			"data": map[string]interface{}{
				"key1": "externally-modified", // Drift!
			},
		},
	}

	desiredObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-cm",
				"namespace": "default",
			},
			"data": map[string]interface{}{
				"key1": "terraform-value",
			},
		},
	}

	paths := []string{"metadata.name", "metadata.namespace", "data.key1"}
	data := &objectResourceModel{
		IgnoreFields: types.ListNull(types.StringType),
	}

	// Compute refreshed projection from cluster (has drift)
	refreshed := r.computeRefreshedProjection(ctx, currentObj, desiredObj, paths, data)
	if refreshed == nil {
		t.Fatal("Expected non-nil refreshed projection")
	}

	// Compute what dry-run would produce (our desired values)
	dryRunProjection := computeProjectionMap(t, ctx, desiredObj, paths)

	// The refreshed and dry-run projections should DIFFER because of drift
	if refreshed.Equal(dryRunProjection) {
		t.Error("Refreshed projection should differ from dry-run projection when drift occurred — drift missed!")
	}
}

// TestComputeRefreshedProjection_NoDrift verifies that when no external changes
// occurred, the refreshed projection matches the dry-run projection. This means
// checkDriftAndPreserveState will correctly suppress unnecessary changes.
func TestComputeRefreshedProjection_NoDrift(t *testing.T) {
	ctx := context.Background()
	r := &objectResource{}

	// Current cluster state matches what we want — no drift
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-cm",
				"namespace": "default",
			},
			"data": map[string]interface{}{
				"key1": "terraform-value",
			},
		},
	}

	paths := []string{"metadata.name", "metadata.namespace", "data.key1"}
	data := &objectResourceModel{
		IgnoreFields: types.ListNull(types.StringType),
	}

	// Compute both projections — should match since no drift
	refreshed := r.computeRefreshedProjection(ctx, obj, obj, paths, data)
	if refreshed == nil {
		t.Fatal("Expected non-nil refreshed projection")
	}

	dryRunProjection := computeProjectionMap(t, ctx, obj, paths)

	// No drift -> projections should match -> changes will be suppressed
	if !refreshed.Equal(dryRunProjection) {
		t.Error("Refreshed projection should match dry-run projection when no drift occurred")
	}
}

// TestComputeRefreshedProjection_WithIgnoreFields verifies that ignored fields
// are excluded from the refreshed projection, matching the dry-run projection behavior.
func TestComputeRefreshedProjection_WithIgnoreFields(t *testing.T) {
	ctx := context.Background()
	r := &objectResource{}

	currentObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-cm",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"controller.io/managed": "true",
				},
			},
			"data": map[string]interface{}{
				"key1": "value1",
			},
		},
	}

	desiredObj := currentObj.DeepCopy()

	paths := []string{
		"metadata.name",
		"metadata.namespace",
		"metadata.annotations.controller.io/managed",
		"data.key1",
	}

	// Create model with ignore_fields for the annotation
	ignoreList, _ := types.ListValueFrom(ctx, types.StringType, []string{"metadata.annotations"})
	data := &objectResourceModel{
		IgnoreFields: ignoreList,
	}

	result := r.computeRefreshedProjection(ctx, currentObj, desiredObj, paths, data)
	if result == nil {
		t.Fatal("Expected non-nil refreshed projection")
	}

	var resultMap map[string]string
	diags := result.ElementsAs(ctx, &resultMap, false)
	if diags.HasError() {
		t.Fatalf("Failed to extract projection: %v", diags)
	}

	// Annotations should be filtered out by ignore_fields
	for key := range resultMap {
		if strings.Contains(key, "annotations") {
			t.Errorf("Ignored field %q should not appear in refreshed projection", key)
		}
	}

	// Non-ignored fields should still be present
	if _, ok := resultMap["data.key1"]; !ok {
		t.Error("data.key1 should be present in refreshed projection")
	}
}

// TestComputeRefreshedProjection_NilCurrentObj verifies graceful handling
// when currentObj is nil (e.g., Get failed during ModifyPlan).
func TestComputeRefreshedProjection_NilCurrentObj(t *testing.T) {
	ctx := context.Background()
	r := &objectResource{}

	paths := []string{"metadata.name"}
	data := &objectResourceModel{
		IgnoreFields: types.ListNull(types.StringType),
	}

	result := r.computeRefreshedProjection(ctx, nil, nil, paths, data)

	if result != nil {
		t.Error("Expected nil result when currentObj is nil")
	}
}

// computeProjectionMap is a test helper that projects fields from an object
// and returns a types.Map, simulating what applyProjection does for dry-run results.
func computeProjectionMap(t *testing.T, ctx context.Context, obj *unstructured.Unstructured, paths []string) types.Map {
	t.Helper()
	projection, err := projectFields(obj.Object, paths)
	if err != nil {
		t.Fatalf("projectFields failed: %v", err)
	}
	projMap := flattenProjectionToMap(projection, paths)
	mapValue, diags := types.MapValueFrom(ctx, types.StringType, projMap)
	if diags.HasError() {
		t.Fatalf("MapValueFrom failed: %v", diags)
	}
	return mapValue
}
