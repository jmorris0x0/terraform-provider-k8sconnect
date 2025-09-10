// internal/k8sconnect/resource/manifest/wait.go
package manifest

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/util/jsonpath"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/k8sclient"
)

// waitForResource waits for resource to meet configured conditions
func (r *manifestResource) waitForResource(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured, waitConfig waitForModel) error {

	// Determine timeout
	timeout := 10 * time.Minute
	if !waitConfig.Timeout.IsNull() && waitConfig.Timeout.ValueString() != "" {
		if t, err := time.ParseDuration(waitConfig.Timeout.ValueString()); err == nil {
			timeout = t
		} else {
			tflog.Warn(ctx, "Invalid timeout format, using default", map[string]interface{}{
				"provided": waitConfig.Timeout.ValueString(),
				"default":  "10m",
			})
		}
	}

	// Check if we should auto-enable rollout waiting
	if shouldAutoWaitRollout(obj, waitConfig) {
		tflog.Info(ctx, "Auto-enabled rollout waiting", map[string]interface{}{
			"kind": obj.GetKind(),
			"name": obj.GetName(),
		})
		if err := r.waitForRollout(ctx, client, gvr, obj, timeout); err != nil {
			return fmt.Errorf("rollout wait failed: %w", err)
		}
	}

	// Handle explicit rollout=true
	if !waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool() {
		tflog.Info(ctx, "Explicit rollout waiting", map[string]interface{}{
			"kind": obj.GetKind(),
			"name": obj.GetName(),
		})
		if err := r.waitForRollout(ctx, client, gvr, obj, timeout); err != nil {
			return fmt.Errorf("rollout wait failed: %w", err)
		}
		return nil
	}

	// Handle field existence check
	if !waitConfig.Field.IsNull() && waitConfig.Field.ValueString() != "" {
		tflog.Info(ctx, "Waiting for field to exist", map[string]interface{}{
			"field":    waitConfig.Field.ValueString(),
			"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
		})
		return r.waitForField(ctx, client, gvr, obj, waitConfig.Field.ValueString(), timeout)
	}

	// Handle field value check
	if !waitConfig.FieldValue.IsNull() {
		fieldMap := make(map[string]string)
		diags := waitConfig.FieldValue.ElementsAs(ctx, &fieldMap, false)
		if diags.HasError() {
			return fmt.Errorf("failed to parse field_value map")
		}
		tflog.Info(ctx, "Waiting for field values", map[string]interface{}{
			"fields":   fieldMap,
			"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
		})
		return r.waitForFieldValues(ctx, client, gvr, obj, fieldMap, timeout)
	}

	// Handle condition check
	if !waitConfig.Condition.IsNull() && waitConfig.Condition.ValueString() != "" {
		tflog.Info(ctx, "Waiting for condition", map[string]interface{}{
			"condition": waitConfig.Condition.ValueString(),
			"resource":  fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
		})
		return r.waitForCondition(ctx, client, gvr, obj, waitConfig.Condition.ValueString(), timeout)
	}

	return nil
}

// shouldAutoWaitRollout determines if we should automatically wait for rollout
func shouldAutoWaitRollout(obj *unstructured.Unstructured, waitConfig waitForModel) bool {
	// If rollout is explicitly set to false, don't auto-wait
	if !waitConfig.Rollout.IsNull() && !waitConfig.Rollout.ValueBool() {
		return false
	}

	// If any other wait condition is set, don't auto-enable rollout
	if !waitConfig.Field.IsNull() || !waitConfig.FieldValue.IsNull() || !waitConfig.Condition.IsNull() {
		return false
	}

	// If rollout is explicitly true, it will be handled separately
	if !waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool() {
		return false
	}

	// Auto-enable only for known types with no other wait config
	kind := obj.GetKind()
	return kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet"
}

// waitForField waits for a field to exist and be non-empty
func (r *manifestResource) waitForField(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	fieldPath string, timeout time.Duration) error {

	jp := jsonpath.New("wait")
	if err := jp.Parse(fmt.Sprintf("{.%s}", fieldPath)); err != nil {
		return fmt.Errorf("invalid field path %q: %w", fieldPath, err)
	}

	// Check current state first and get ResourceVersion
	current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err == nil {
		results, err := jp.FindResults(current.Object)
		if err == nil && len(results) > 0 && len(results[0]) > 0 {
			if !isEmptyValue(results[0][0].Interface()) {
				tflog.Info(ctx, "Field already exists", map[string]interface{}{
					"field": fieldPath,
				})
				return nil
			}
		}

		// Start watch from current ResourceVersion to avoid race
		opts := metav1.ListOptions{
			FieldSelector:   fmt.Sprintf("metadata.name=%s", obj.GetName()),
			ResourceVersion: current.GetResourceVersion(),
		}

		watcher, err := client.Watch(ctx, gvr, obj.GetNamespace(), opts)
		if err != nil {
			tflog.Warn(ctx, "Watch not supported, falling back to polling", map[string]interface{}{
				"error": err.Error(),
			})
			return r.pollForField(ctx, client, gvr, obj, jp, fieldPath, timeout)
		}
		defer watcher.Stop()

		timeoutCh := time.After(timeout)

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timeoutCh:
				return fmt.Errorf("timeout after %v waiting for field %q to be populated", timeout, fieldPath)
			case event, ok := <-watcher.ResultChan():
				if !ok {
					return fmt.Errorf("watch ended unexpectedly")
				}

				if event.Type == watch.Error {
					tflog.Warn(ctx, "Watch error, falling back to polling", map[string]interface{}{
						"error": fmt.Sprintf("%v", event.Object),
					})
					return r.pollForField(ctx, client, gvr, obj, jp, fieldPath, timeout)
				}

				if event.Type == watch.Modified || event.Type == watch.Added {
					current := event.Object.(*unstructured.Unstructured)
					results, err := jp.FindResults(current.Object)
					if err == nil && len(results) > 0 && len(results[0]) > 0 {
						val := results[0][0].Interface()
						if !isEmptyValue(val) {
							tflog.Info(ctx, "Field is now populated", map[string]interface{}{
								"field": fieldPath,
								"value": fmt.Sprintf("%v", val),
							})
							return nil
						}
					}
				}
			}
		}
	}

	// If we can't get current state, fall back to polling
	return r.pollForField(ctx, client, gvr, obj, jp, fieldPath, timeout)
}

// pollForField falls back to polling when watch is not available
func (r *manifestResource) pollForField(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	jp *jsonpath.JSONPath, fieldPath string, timeout time.Duration) error {

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout after %v waiting for field %q", timeout, fieldPath)
			}

			current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
			if err != nil {
				tflog.Warn(ctx, "Failed to get resource during poll", map[string]interface{}{
					"error": err.Error(),
				})
				continue
			}

			results, err := jp.FindResults(current.Object)
			if err == nil && len(results) > 0 && len(results[0]) > 0 {
				val := results[0][0].Interface()
				if !isEmptyValue(val) {
					tflog.Info(ctx, "Field is now populated (via polling)", map[string]interface{}{
						"field": fieldPath,
						"value": fmt.Sprintf("%v", val),
					})
					return nil
				}
			}
		}
	}
}

// waitForFieldValues waits for fields to have specific values
func (r *manifestResource) waitForFieldValues(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	fieldValues map[string]string, timeout time.Duration) error {

	// Create JSONPath parsers for each field
	parsers := make(map[string]*jsonpath.JSONPath)
	for field := range fieldValues {
		jp := jsonpath.New(field)
		if err := jp.Parse(fmt.Sprintf("{.%s}", field)); err != nil {
			return fmt.Errorf("invalid field path %q: %w", field, err)
		}
		parsers[field] = jp
	}

	// Check function
	checkFields := func(obj *unstructured.Unstructured) bool {
		for field, expectedValue := range fieldValues {
			jp := parsers[field]
			results, err := jp.FindResults(obj.Object)
			if err != nil || len(results) == 0 || len(results[0]) == 0 {
				return false
			}

			actualValue := fmt.Sprintf("%v", results[0][0].Interface())
			if actualValue != expectedValue {
				return false
			}
		}
		return true
	}

	// Check current state first
	current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err == nil && checkFields(current) {
		tflog.Info(ctx, "Field values already match", map[string]interface{}{
			"fields": fieldValues,
		})
		return nil
	}

	// Set up watch with ResourceVersion if we got current state
	opts := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", obj.GetName()),
	}
	if current != nil {
		opts.ResourceVersion = current.GetResourceVersion()
	}

	watcher, err := client.Watch(ctx, gvr, obj.GetNamespace(), opts)
	if err != nil {
		return r.pollForFieldValues(ctx, client, gvr, obj, checkFields, fieldValues, timeout)
	}
	defer watcher.Stop()

	timeoutCh := time.After(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeoutCh:
			return fmt.Errorf("timeout after %v waiting for field values %v", timeout, fieldValues)
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch ended unexpectedly")
			}

			if event.Type == watch.Error {
				return r.pollForFieldValues(ctx, client, gvr, obj, checkFields, fieldValues, timeout)
			}

			if event.Type == watch.Modified || event.Type == watch.Added {
				current := event.Object.(*unstructured.Unstructured)
				if checkFields(current) {
					tflog.Info(ctx, "Field values now match", map[string]interface{}{
						"fields": fieldValues,
					})
					return nil
				}
			}
		}
	}
}

// pollForFieldValues polls for field values when watch is not available
func (r *manifestResource) pollForFieldValues(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	checkFunc func(*unstructured.Unstructured) bool, fieldValues map[string]string, timeout time.Duration) error {

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout after %v waiting for field values %v", timeout, fieldValues)
			}

			current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
			if err != nil {
				continue
			}

			if checkFunc(current) {
				tflog.Info(ctx, "Field values now match (via polling)", map[string]interface{}{
					"fields": fieldValues,
				})
				return nil
			}
		}
	}
}

// waitForCondition waits for a Kubernetes condition to be True
func (r *manifestResource) waitForCondition(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	conditionType string, timeout time.Duration) error {

	// Check function
	checkCondition := func(obj *unstructured.Unstructured) bool {
		conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if err != nil || !found {
			return false
		}

		for _, cond := range conditions {
			condMap, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}

			if typeVal, ok := condMap["type"].(string); ok && typeVal == conditionType {
				if statusVal, ok := condMap["status"].(string); ok && statusVal == "True" {
					return true
				}
			}
		}
		return false
	}

	// Check current state first
	current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err == nil && checkCondition(current) {
		tflog.Info(ctx, "Condition already met", map[string]interface{}{
			"condition": conditionType,
		})
		return nil
	}

	// Set up watch with ResourceVersion
	opts := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", obj.GetName()),
	}
	if current != nil {
		opts.ResourceVersion = current.GetResourceVersion()
	}

	watcher, err := client.Watch(ctx, gvr, obj.GetNamespace(), opts)
	if err != nil {
		return r.pollForCondition(ctx, client, gvr, obj, checkCondition, conditionType, timeout)
	}
	defer watcher.Stop()

	timeoutCh := time.After(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeoutCh:
			return fmt.Errorf("timeout after %v waiting for condition %q", timeout, conditionType)
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch ended unexpectedly")
			}

			if event.Type == watch.Error {
				return r.pollForCondition(ctx, client, gvr, obj, checkCondition, conditionType, timeout)
			}

			if event.Type == watch.Modified || event.Type == watch.Added {
				current := event.Object.(*unstructured.Unstructured)
				if checkCondition(current) {
					tflog.Info(ctx, "Condition is now met", map[string]interface{}{
						"condition": conditionType,
					})
					return nil
				}
			}
		}
	}
}

// pollForCondition polls for condition when watch is not available
func (r *manifestResource) pollForCondition(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	checkFunc func(*unstructured.Unstructured) bool, conditionType string, timeout time.Duration) error {

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout after %v waiting for condition %q", timeout, conditionType)
			}

			current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
			if err != nil {
				continue
			}

			if checkFunc(current) {
				tflog.Info(ctx, "Condition is now met (via polling)", map[string]interface{}{
					"condition": conditionType,
				})
				return nil
			}
		}
	}
}

// waitForRollout waits for Deployment/StatefulSet/DaemonSet rollout
func (r *manifestResource) waitForRollout(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured, timeout time.Duration) error {

	kind := obj.GetKind()
	switch kind {
	case "Deployment":
		return r.waitForDeploymentRollout(ctx, client, gvr, obj, timeout)
	case "StatefulSet":
		return r.waitForStatefulSetRollout(ctx, client, gvr, obj, timeout)
	case "DaemonSet":
		return r.waitForDaemonSetRollout(ctx, client, gvr, obj, timeout)
	default:
		return nil
	}
}

// waitForDeploymentRollout waits for a Deployment to complete its rollout
func (r *manifestResource) waitForDeploymentRollout(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured, timeout time.Duration) error {

	checkRollout := func(obj *unstructured.Unstructured) (bool, string) {
		// Check if replicas match
		replicas, _, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")
		readyReplicas, _, _ := unstructured.NestedInt64(obj.Object, "status", "readyReplicas")
		updatedReplicas, _, _ := unstructured.NestedInt64(obj.Object, "status", "updatedReplicas")

		// Check generation matches observedGeneration
		generation, _, _ := unstructured.NestedInt64(obj.Object, "metadata", "generation")
		observedGen, _, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")

		if generation != observedGen {
			return false, fmt.Sprintf("generation mismatch: %d != %d", generation, observedGen)
		}

		if replicas == 0 {
			replicas = 1 // Default if not specified
		}

		if readyReplicas == replicas && updatedReplicas == replicas {
			return true, ""
		}

		return false, fmt.Sprintf("replicas not ready: %d/%d ready, %d/%d updated",
			readyReplicas, replicas, updatedReplicas, replicas)
	}

	return r.waitWithCheck(ctx, client, gvr, obj, checkRollout, "deployment rollout", timeout)
}

// waitForStatefulSetRollout waits for a StatefulSet to complete its rollout
func (r *manifestResource) waitForStatefulSetRollout(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured, timeout time.Duration) error {

	checkRollout := func(obj *unstructured.Unstructured) (bool, string) {
		replicas, _, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")
		readyReplicas, _, _ := unstructured.NestedInt64(obj.Object, "status", "readyReplicas")
		currentReplicas, _, _ := unstructured.NestedInt64(obj.Object, "status", "currentReplicas")
		updatedReplicas, _, _ := unstructured.NestedInt64(obj.Object, "status", "updatedReplicas")

		generation, _, _ := unstructured.NestedInt64(obj.Object, "metadata", "generation")
		observedGen, _, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")

		if generation != observedGen {
			return false, fmt.Sprintf("generation mismatch: %d != %d", generation, observedGen)
		}

		if replicas == 0 {
			replicas = 1
		}

		if readyReplicas == replicas && currentReplicas == replicas && updatedReplicas == replicas {
			return true, ""
		}

		return false, fmt.Sprintf("replicas not ready: %d/%d ready, %d/%d current, %d/%d updated",
			readyReplicas, replicas, currentReplicas, replicas, updatedReplicas, replicas)
	}

	return r.waitWithCheck(ctx, client, gvr, obj, checkRollout, "statefulset rollout", timeout)
}

// waitForDaemonSetRollout waits for a DaemonSet to complete its rollout
func (r *manifestResource) waitForDaemonSetRollout(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured, timeout time.Duration) error {

	checkRollout := func(obj *unstructured.Unstructured) (bool, string) {
		desiredNumberScheduled, _, _ := unstructured.NestedInt64(obj.Object, "status", "desiredNumberScheduled")
		numberReady, _, _ := unstructured.NestedInt64(obj.Object, "status", "numberReady")
		updatedNumberScheduled, _, _ := unstructured.NestedInt64(obj.Object, "status", "updatedNumberScheduled")

		generation, _, _ := unstructured.NestedInt64(obj.Object, "metadata", "generation")
		observedGen, _, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")

		if generation != observedGen {
			return false, fmt.Sprintf("generation mismatch: %d != %d", generation, observedGen)
		}

		if numberReady == desiredNumberScheduled && updatedNumberScheduled == desiredNumberScheduled {
			return true, ""
		}

		return false, fmt.Sprintf("pods not ready: %d/%d ready, %d/%d updated",
			numberReady, desiredNumberScheduled, updatedNumberScheduled, desiredNumberScheduled)
	}

	return r.waitWithCheck(ctx, client, gvr, obj, checkRollout, "daemonset rollout", timeout)
}

// waitWithCheck is a generic wait function using a check function
func (r *manifestResource) waitWithCheck(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	checkFunc func(*unstructured.Unstructured) (bool, string), waitType string, timeout time.Duration) error {

	// Check current state first
	current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err == nil {
		if ready, _ := checkFunc(current); ready {
			tflog.Info(ctx, "Already ready", map[string]interface{}{
				"type":     waitType,
				"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
			})
			return nil
		}

		// Set up watch with ResourceVersion
		opts := metav1.ListOptions{
			FieldSelector:   fmt.Sprintf("metadata.name=%s", obj.GetName()),
			ResourceVersion: current.GetResourceVersion(),
		}

		watcher, err := client.Watch(ctx, gvr, obj.GetNamespace(), opts)
		if err != nil {
			return r.pollWithCheck(ctx, client, gvr, obj, checkFunc, waitType, timeout)
		}
		defer watcher.Stop()

		timeoutCh := time.After(timeout)

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timeoutCh:
				// Get final status for error message
				current, _ := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
				if current != nil {
					_, reason := checkFunc(current)
					return fmt.Errorf("timeout after %v waiting for %s: %s", timeout, waitType, reason)
				}
				return fmt.Errorf("timeout after %v waiting for %s", timeout, waitType)
			case event, ok := <-watcher.ResultChan():
				if !ok {
					return fmt.Errorf("watch ended unexpectedly")
				}

				if event.Type == watch.Error {
					return r.pollWithCheck(ctx, client, gvr, obj, checkFunc, waitType, timeout)
				}

				if event.Type == watch.Modified || event.Type == watch.Added {
					current := event.Object.(*unstructured.Unstructured)
					if ready, reason := checkFunc(current); ready {
						tflog.Info(ctx, "Now ready", map[string]interface{}{
							"type":     waitType,
							"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
						})
						return nil
					} else {
						tflog.Debug(ctx, "Not ready yet", map[string]interface{}{
							"type":   waitType,
							"reason": reason,
						})
					}
				}
			}
		}
	}

	// If we can't get current state, fall back to polling
	return r.pollWithCheck(ctx, client, gvr, obj, checkFunc, waitType, timeout)
}

// pollWithCheck polls using a check function when watch is not available
func (r *manifestResource) pollWithCheck(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	checkFunc func(*unstructured.Unstructured) (bool, string), waitType string, timeout time.Duration) error {

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				current, _ := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
				if current != nil {
					_, reason := checkFunc(current)
					return fmt.Errorf("timeout after %v waiting for %s: %s", timeout, waitType, reason)
				}
				return fmt.Errorf("timeout after %v waiting for %s", timeout, waitType)
			}

			current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
			if err != nil {
				tflog.Warn(ctx, "Failed to get resource during poll", map[string]interface{}{
					"error": err.Error(),
					"type":  waitType,
				})
				continue
			}

			if ready, reason := checkFunc(current); ready {
				tflog.Info(ctx, "Now ready (via polling)", map[string]interface{}{
					"type":     waitType,
					"resource": fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName()),
				})
				return nil
			} else {
				tflog.Debug(ctx, "Not ready yet (polling)", map[string]interface{}{
					"type":   waitType,
					"reason": reason,
				})
			}
		}
	}
}

// Helper to check if a value is empty
func isEmptyValue(v interface{}) bool {
	switch val := v.(type) {
	case nil:
		return true
	case string:
		return val == ""
	case []interface{}:
		return len(val) == 0
	case map[string]interface{}:
		return len(val) == 0
	case bool:
		return false // bool is never "empty"
	case float64, int64:
		return false // numbers are never "empty"
	default:
		// For unknown types, check if it's nil
		return val == nil
	}
}
