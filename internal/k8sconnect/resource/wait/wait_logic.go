package wait

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/util/jsonpath"

	"github.com/jmorris0x0/terraform-provider-k8sconnect/internal/k8sconnect/common/k8sclient"
)

// waitForResource waits for resource to meet configured conditions
func (r *waitResource) waitForResource(ctx context.Context, client k8sclient.K8sClient,
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

	// Handle explicit rollout=true
	if !waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool() {
		tflog.Info(ctx, "Explicit rollout waiting", map[string]interface{}{
			"kind": obj.GetKind(),
			"name": obj.GetName(),
		})
		if err := r.waitForRollout(ctx, client, gvr, obj, timeout); err != nil {
			return err
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

	// No wait conditions configured
	return nil
}

// waitForField waits for a field to exist and be non-empty
func (r *waitResource) waitForField(ctx context.Context, client k8sclient.K8sClient,
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
				return r.buildFieldTimeoutError(obj, fieldPath, timeout)
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
func (r *waitResource) pollForField(ctx context.Context, client k8sclient.K8sClient,
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
				return r.buildFieldTimeoutError(obj, fieldPath, timeout)
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
func (r *waitResource) waitForFieldValues(ctx context.Context, client k8sclient.K8sClient,
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
			return r.buildFieldValuesTimeoutError(obj, fieldValues, timeout)
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
func (r *waitResource) pollForFieldValues(ctx context.Context, client k8sclient.K8sClient,
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
				return r.buildFieldValuesTimeoutError(obj, fieldValues, timeout)
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
func (r *waitResource) waitForCondition(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	conditionType string, timeout time.Duration) error {

	// Create condition checker
	checker := r.createConditionChecker(conditionType)

	// Check if already satisfied
	if satisfied, err := r.checkConditionImmediately(ctx, client, gvr, obj, checker, conditionType); err != nil {
		return err
	} else if satisfied {
		return nil
	}

	// Try watching for changes
	if err := r.watchForCondition(ctx, client, gvr, obj, checker, conditionType, timeout); err != nil {
		// Fall back to polling if watch fails
		if r.isWatchError(err) {
			return r.pollForCondition(ctx, client, gvr, obj, checker, conditionType, timeout)
		}
		return err
	}

	return nil
}

// createConditionChecker returns a function that checks if a condition is met
func (r *waitResource) createConditionChecker(conditionType string) func(*unstructured.Unstructured) bool {
	return func(obj *unstructured.Unstructured) bool {
		conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if err != nil || !found {
			return false
		}

		for _, cond := range conditions {
			if r.isConditionMet(cond, conditionType) {
				return true
			}
		}
		return false
	}
}

// isConditionMet checks if a single condition matches and is True
func (r *waitResource) isConditionMet(cond interface{}, conditionType string) bool {
	condMap, ok := cond.(map[string]interface{})
	if !ok {
		return false
	}

	typeVal, typeOk := condMap["type"].(string)
	statusVal, statusOk := condMap["status"].(string)

	return typeOk && typeVal == conditionType && statusOk && statusVal == "True"
}

// checkConditionImmediately checks if condition is already satisfied
func (r *waitResource) checkConditionImmediately(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	checker func(*unstructured.Unstructured) bool, conditionType string) (bool, error) {

	current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err != nil {
		return false, nil // Don't fail, just proceed to watching
	}

	if checker(current) {
		tflog.Info(ctx, "Condition already met", map[string]interface{}{
			"condition": conditionType,
		})
		return true, nil
	}

	return false, nil
}

// watchForCondition sets up a watch for condition changes
func (r *waitResource) watchForCondition(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, obj *unstructured.Unstructured,
	checker func(*unstructured.Unstructured) bool, conditionType string, timeout time.Duration) error {

	// Setup watch options
	watchOpts := r.createWatchOptions(obj)

	// Get current resource for ResourceVersion
	current, err := client.Get(ctx, gvr, obj.GetNamespace(), obj.GetName())
	if err == nil && current != nil {
		watchOpts.ResourceVersion = current.GetResourceVersion()
	}

	// Create watcher
	watcher, err := client.Watch(ctx, gvr, obj.GetNamespace(), watchOpts)
	if err != nil {
		return fmt.Errorf("watch setup failed: %w", err)
	}
	defer watcher.Stop()

	// Watch for events
	return r.processWatchEvents(ctx, client, gvr, obj.GetNamespace(), obj.GetName(), watcher, checker, conditionType, timeout)
}

// createWatchOptions creates watch options for the resource
func (r *waitResource) createWatchOptions(obj *unstructured.Unstructured) metav1.ListOptions {
	return metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", obj.GetName()),
	}
}

// processWatchEvents processes events from the watcher
func (r *waitResource) processWatchEvents(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, namespace, name string, watcher watch.Interface,
	checker func(*unstructured.Unstructured) bool, conditionType string, timeout time.Duration) error {

	timeoutCh := time.After(timeout)
	var lastSeenObj *unstructured.Unstructured

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-timeoutCh:
			return r.buildConditionTimeoutError(ctx, client, gvr, namespace, name, lastSeenObj, conditionType, timeout)

		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch ended unexpectedly")
			}

			if err := r.handleWatchEvent(ctx, event, checker, conditionType); err != nil {
				return err
			}

			// Track last seen object for better timeout diagnostics
			if event.Type == watch.Modified || event.Type == watch.Added {
				if obj, ok := event.Object.(*unstructured.Unstructured); ok {
					lastSeenObj = obj
				}
			}

			if r.isConditionMetByEvent(event, checker) {
				tflog.Info(ctx, "Condition is now met", map[string]interface{}{
					"condition": conditionType,
				})
				return nil
			}
		}
	}
}

// handleWatchEvent handles a single watch event
func (r *waitResource) handleWatchEvent(ctx context.Context, event watch.Event,
	checker func(*unstructured.Unstructured) bool, conditionType string) error {

	if event.Type == watch.Error {
		return fmt.Errorf("watch error occurred")
	}

	return nil
}

// isConditionMetByEvent checks if the event indicates condition is met
func (r *waitResource) isConditionMetByEvent(event watch.Event, checker func(*unstructured.Unstructured) bool) bool {
	if event.Type != watch.Modified && event.Type != watch.Added {
		return false
	}

	current, ok := event.Object.(*unstructured.Unstructured)
	if !ok {
		return false
	}

	return checker(current)
}

// isWatchError determines if an error is watch-related
func (r *waitResource) isWatchError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "watch")
}

// buildConditionTimeoutError creates a detailed timeout error with current conditions
// Following ADR-015: Actionable Error Messages and Diagnostic Context
func (r *waitResource) buildConditionTimeoutError(ctx context.Context, client k8sclient.K8sClient,
	gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured,
	conditionType string, timeout time.Duration) error {

	// If no object from watch events, fetch current state from API
	if obj == nil {
		fetchedObj, err := client.Get(ctx, gvr, namespace, name)
		if err != nil {
			// Object may have been deleted or inaccessible
			resourceRef := fmt.Sprintf("%s/%s", gvr.Resource, name)
			if namespace != "" {
				resourceRef = fmt.Sprintf("%s/%s/%s", gvr.Resource, namespace, name)
			}
			return fmt.Errorf("timeout after %v waiting for condition %q on %s: unable to fetch current status: %w",
				timeout, conditionType, resourceRef, err)
		}
		obj = fetchedObj
	}

	kind := obj.GetKind()
	objName := obj.GetName()
	objNamespace := obj.GetNamespace()

	resourceRef := fmt.Sprintf("%s/%s", kind, objName)
	if objNamespace != "" {
		resourceRef = fmt.Sprintf("%s/%s/%s", kind, objNamespace, objName)
	}

	// Extract all conditions for diagnostics
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found || len(conditions) == 0 {
		return r.buildNoConditionsError(resourceRef, kind, objName, objNamespace, conditionType, timeout)
	}

	// Parse conditions
	var conditionDetails []string
	var targetCondition map[string]interface{}
	var targetFound bool

	for _, cond := range conditions {
		condMap, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}

		typeVal, _ := condMap["type"].(string)
		statusVal, _ := condMap["status"].(string)
		reason, _ := condMap["reason"].(string)

		condStr := fmt.Sprintf("  • %s = %s", typeVal, statusVal)
		if reason != "" {
			condStr += fmt.Sprintf(" (reason: %s)", reason)
		}

		conditionDetails = append(conditionDetails, condStr)

		if typeVal == conditionType {
			targetCondition = condMap
			targetFound = true
		}
	}

	// Build error message following ADR-015 template
	errMsg := fmt.Sprintf("Wait Timeout: %s\n\n%s did not reach condition %q=True within %v\n\n",
		resourceRef, kind, conditionType, timeout)

	// Current state - show workload-specific details only for known types
	errMsg += "Current status:\n"
	if r.isWorkloadResource(kind) {
		if replicaStatus := r.extractReplicaStatus(obj); replicaStatus != "" {
			errMsg += fmt.Sprintf("  %s\n", replicaStatus)
		}
	}
	errMsg += "  Conditions:\n"
	errMsg += strings.Join(conditionDetails, "\n")

	// Fetch and show pod issues for workload resources (same as rollout waits)
	if r.isWorkloadResource(kind) {
		podIssues := r.fetchPodIssues(ctx, client, obj)
		if len(podIssues) > 0 {
			errMsg += "\n  Pod Issues:\n"
			for _, issue := range podIssues {
				errMsg += fmt.Sprintf("    • %s\n", issue)
			}
		}
	}

	errMsg += "\n\n"

	// WHY section
	if targetFound {
		statusVal, _ := targetCondition["status"].(string)
		reason, _ := targetCondition["reason"].(string)
		message, _ := targetCondition["message"].(string)

		errMsg += fmt.Sprintf("WHY: Condition %q exists but is %s", conditionType, statusVal)
		if reason != "" {
			errMsg += fmt.Sprintf(" (reason: %s)", reason)
		}
		if message != "" {
			errMsg += fmt.Sprintf(". %s", message)
		}
		errMsg += "\n\n"
	} else {
		errMsg += fmt.Sprintf("WHY: Condition %q never appeared in status. ", conditionType)
		errMsg += "The resource controller may not be running or the condition may not exist for this resource type.\n\n"
	}

	// WHAT TO DO section - generic with workload-specific additions
	errMsg += "WHAT TO DO:\n"

	// Add workload-specific guidance only for known workload types
	if r.isWorkloadResource(kind) {
		replicaStatus := r.extractReplicaStatus(obj)
		errMsg += fmt.Sprintf("• Check pod status:\n    kubectl get pods -n %s -l [selector]\n", namespace)
		if strings.Contains(replicaStatus, "0/") {
			errMsg += "• Check why pods aren't starting:\n"
			errMsg += fmt.Sprintf("    kubectl describe %s %s -n %s\n", kind, name, namespace)
			errMsg += fmt.Sprintf("    kubectl get events -n %s --sort-by='.lastTimestamp'\n", namespace)
		}
	}

	// Generic guidance for all resources
	if namespace != "" {
		errMsg += fmt.Sprintf("• View resource status and events:\n    kubectl describe %s %s -n %s\n", kind, name, namespace)
		errMsg += fmt.Sprintf("• View full resource YAML:\n    kubectl get %s %s -n %s -o yaml\n", kind, name, namespace)
	} else {
		errMsg += fmt.Sprintf("• View resource status and events:\n    kubectl describe %s %s\n", kind, name)
		errMsg += fmt.Sprintf("• View full resource YAML:\n    kubectl get %s %s -o yaml\n", kind, name)
	}

	errMsg += fmt.Sprintf("• Increase timeout if needed:\n    wait_for = { condition = %q, timeout = \"10m\" }", conditionType)

	return fmt.Errorf("%s", errMsg)
}

// isWorkloadResource checks if the resource is a workload type with pods
func (r *waitResource) isWorkloadResource(kind string) bool {
	return kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet" || kind == "ReplicaSet"
}

// extractReplicaStatus extracts replica information for workload resources
// Returns empty string if fields don't exist (not an error)
func (r *waitResource) extractReplicaStatus(obj *unstructured.Unstructured) string {
	kind := obj.GetKind()

	// DaemonSet uses different field names
	if kind == "DaemonSet" {
		desired, _, _ := unstructured.NestedInt64(obj.Object, "status", "desiredNumberScheduled")
		ready, _, _ := unstructured.NestedInt64(obj.Object, "status", "numberReady")
		updated, _, _ := unstructured.NestedInt64(obj.Object, "status", "updatedNumberScheduled")

		if desired == 0 {
			return "" // No status yet
		}

		return fmt.Sprintf("Replicas: %d/%d ready, %d/%d updated",
			ready, desired, updated, desired)
	}

	// Deployment, StatefulSet, ReplicaSet use standard fields
	replicas, hasReplicas, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	readyReplicas, _, _ := unstructured.NestedInt64(obj.Object, "status", "readyReplicas")
	availableReplicas, _, _ := unstructured.NestedInt64(obj.Object, "status", "availableReplicas")
	unavailableReplicas, _, _ := unstructured.NestedInt64(obj.Object, "status", "unavailableReplicas")

	// If spec.replicas doesn't exist, this isn't a workload resource
	if !hasReplicas {
		return ""
	}

	if replicas == 0 {
		replicas = 1 // Default for some resources
	}

	return fmt.Sprintf("Replicas: %d/%d ready, %d available, %d unavailable",
		readyReplicas, replicas, availableReplicas, unavailableReplicas)
}

// buildNoConditionsError builds error for resources with no conditions
func (r *waitResource) buildNoConditionsError(resourceRef, kind, name, namespace, conditionType string, timeout time.Duration) error {
	errMsg := fmt.Sprintf("Wait Timeout: %s\n\n", resourceRef)
	errMsg += fmt.Sprintf("%s did not become ready within %v\n\n", kind, timeout)

	// Check if it's a workload resource and include replica status
	if r.isWorkloadResource(kind) {
		// For workload resources, provide more specific guidance
		errMsg += fmt.Sprintf("No %q condition found in status.\n\n", conditionType)
		errMsg += "WHY: The resource may not be creating pods, or the controller may not be running.\n\n"
		errMsg += "WHAT TO DO:\n"
		errMsg += fmt.Sprintf("• Check if pods are being created:\n    kubectl get pods -n %s -l [selector]\n", namespace)
		errMsg += fmt.Sprintf("• Check resource status:\n    kubectl describe %s %s -n %s\n", kind, name, namespace)
		errMsg += fmt.Sprintf("• Check controller events:\n    kubectl get events -n %s --sort-by='.lastTimestamp'\n", namespace)
	} else {
		// For other resources (CRDs, etc), provide generic guidance
		errMsg += fmt.Sprintf("No conditions found in status. The resource may not report conditions, or the controller may not be running.\n\n")
		errMsg += fmt.Sprintf("WHY: Not all Kubernetes resources have conditions. Condition %q may not exist for %s.\n\n", conditionType, kind)
		errMsg += "WHAT TO DO:\n"
		if namespace != "" {
			errMsg += fmt.Sprintf("• Check resource status:\n    kubectl get %s %s -n %s -o yaml\n", kind, name, namespace)
			errMsg += fmt.Sprintf("• Check for errors:\n    kubectl describe %s %s -n %s\n", kind, name, namespace)
		} else {
			errMsg += fmt.Sprintf("• Check resource status:\n    kubectl get %s %s -o yaml\n", kind, name)
			errMsg += fmt.Sprintf("• Check for errors:\n    kubectl describe %s %s\n", kind, name)
		}
		errMsg += "• Verify the resource type supports conditions\n"
		errMsg += fmt.Sprintf("• Consider using wait_for.field or wait_for.field_value instead for %s\n", kind)
	}

	return fmt.Errorf("%s", errMsg)
}

// pollForCondition polls for condition when watch is not available
func (r *waitResource) pollForCondition(ctx context.Context, client k8sclient.K8sClient,
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
func (r *waitResource) waitForRollout(ctx context.Context, client k8sclient.K8sClient,
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
func (r *waitResource) waitForDeploymentRollout(ctx context.Context, client k8sclient.K8sClient,
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
func (r *waitResource) waitForStatefulSetRollout(ctx context.Context, client k8sclient.K8sClient,
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
func (r *waitResource) waitForDaemonSetRollout(ctx context.Context, client k8sclient.K8sClient,
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
func (r *waitResource) waitWithCheck(ctx context.Context, client k8sclient.K8sClient,
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
				return r.buildRolloutTimeoutError(ctx, client, current, obj, waitType, timeout)
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
func (r *waitResource) pollWithCheck(ctx context.Context, client k8sclient.K8sClient,
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
				return r.buildRolloutTimeoutError(ctx, client, current, obj, waitType, timeout)
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

// buildRolloutTimeoutError creates a clean timeout error for rollout waits
func (r *waitResource) buildRolloutTimeoutError(ctx context.Context, client k8sclient.K8sClient, current, original *unstructured.Unstructured, waitType string, timeout time.Duration) error {
	// Use current object if available, otherwise fall back to original
	obj := current
	if obj == nil {
		obj = original
	}

	kind := obj.GetKind()
	name := obj.GetName()
	namespace := obj.GetNamespace()

	resourceRef := fmt.Sprintf("%s/%s", kind, name)
	if namespace != "" {
		resourceRef = fmt.Sprintf("%s/%s/%s", kind, namespace, name)
	}

	errMsg := fmt.Sprintf("Wait Timeout: %s\n\n", resourceRef)
	errMsg += fmt.Sprintf("%s did not complete rollout within %v\n\n", kind, timeout)

	// Current status section
	errMsg += "Current status:\n"

	// Show replica status if available
	if replicaStatus := r.extractReplicaStatus(obj); replicaStatus != "" {
		errMsg += fmt.Sprintf("  %s\n", replicaStatus)
	}

	// Show conditions if available
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err == nil && found && len(conditions) > 0 {
		errMsg += "  Conditions:\n"
		for _, cond := range conditions {
			condMap, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}

			typeVal, _ := condMap["type"].(string)
			statusVal, _ := condMap["status"].(string)
			reason, _ := condMap["reason"].(string)

			condStr := fmt.Sprintf("    • %s = %s", typeVal, statusVal)
			if reason != "" {
				condStr += fmt.Sprintf(" (reason: %s)", reason)
			}
			errMsg += condStr + "\n"
		}
	}

	// Fetch and show pod issues if any pods are failing
	podIssues := r.fetchPodIssues(ctx, client, obj)
	if len(podIssues) > 0 {
		errMsg += "  Pod Issues:\n"
		for _, issue := range podIssues {
			errMsg += fmt.Sprintf("    • %s\n", issue)
		}
	}

	return fmt.Errorf("%s", errMsg)
}

// fetchPodIssues fetches pods for a workload and extracts failure information
func (r *waitResource) fetchPodIssues(ctx context.Context, client k8sclient.K8sClient, obj *unstructured.Unstructured) []string {
	// Extract label selector from workload
	selector := r.extractLabelSelector(obj)
	if selector == "" {
		return nil
	}

	// Construct GVR for pods (core v1)
	podGVR := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "pods",
	}

	// List pods matching the selector (limit to 10 for efficiency, we only show 3 issues anyway)
	pods, err := client.List(ctx, podGVR, obj.GetNamespace(), metav1.ListOptions{
		LabelSelector: selector,
		Limit:         10,
	})
	if err != nil {
		tflog.Warn(ctx, "Failed to fetch pods for workload", map[string]interface{}{
			"error": err.Error(),
		})
		return nil
	}

	// Extract issues from pod statuses (limit to first 3 most relevant)
	var issues []string
	for _, pod := range pods.Items {
		if len(issues) >= 3 {
			break
		}

		podName := pod.GetName()
		phase, _, _ := unstructured.NestedString(pod.Object, "status", "phase")

		// Check container statuses
		containerStatuses, found, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
		if !found || len(containerStatuses) == 0 {
			// No container statuses yet - pod may be pending
			if phase == "Pending" {
				// Check for pending conditions
				conditions, found, _ := unstructured.NestedSlice(pod.Object, "status", "conditions")
				if found {
					for _, cond := range conditions {
						condMap, ok := cond.(map[string]interface{})
						if !ok {
							continue
						}
						condType, _ := condMap["type"].(string)
						condStatus, _ := condMap["status"].(string)
						reason, _ := condMap["reason"].(string)
						message, _ := condMap["message"].(string)

						if condType == "PodScheduled" && condStatus == "False" {
							issues = append(issues, fmt.Sprintf("%s: %s - %s", podName, reason, message))
							break
						}
					}
				}
			}
			continue
		}

		// Check each container for issues
		for _, cs := range containerStatuses {
			csMap, ok := cs.(map[string]interface{})
			if !ok {
				continue
			}

			containerName, _ := csMap["name"].(string)

			// Check waiting state (ImagePullBackOff, CrashLoopBackOff, etc.)
			if waiting, found, _ := unstructured.NestedMap(csMap, "state", "waiting"); found {
				reason, _ := waiting["reason"].(string)
				message, _ := waiting["message"].(string)

				if reason != "" {
					issue := fmt.Sprintf("%s/%s: %s", podName, containerName, reason)
					if message != "" && len(message) < 100 {
						issue += fmt.Sprintf(" - %s", message)
					}
					issues = append(issues, issue)
					break // One issue per pod is enough
				}
			}

			// Check terminated state (for crash details)
			if terminated, found, _ := unstructured.NestedMap(csMap, "state", "terminated"); found {
				reason, _ := terminated["reason"].(string)
				exitCode, _ := terminated["exitCode"].(int64)
				message, _ := terminated["message"].(string)

				if reason == "Error" || exitCode != 0 {
					issue := fmt.Sprintf("%s/%s: Terminated (exit %d)", podName, containerName, exitCode)
					if message != "" && len(message) < 100 {
						issue += fmt.Sprintf(" - %s", message)
					}
					issues = append(issues, issue)
					break
				}
			}
		}
	}

	return issues
}

// extractLabelSelector extracts the label selector for a workload
func (r *waitResource) extractLabelSelector(obj *unstructured.Unstructured) string {
	kind := obj.GetKind()

	// For Deployment, StatefulSet, DaemonSet, ReplicaSet: use spec.selector.matchLabels
	var matchLabels map[string]interface{}
	var found bool
	var err error

	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		matchLabels, found, err = unstructured.NestedMap(obj.Object, "spec", "selector", "matchLabels")
	default:
		return ""
	}

	if err != nil || !found || len(matchLabels) == 0 {
		return ""
	}

	// Convert matchLabels to selector string format
	var parts []string
	for k, v := range matchLabels {
		if strVal, ok := v.(string); ok {
			parts = append(parts, fmt.Sprintf("%s=%s", k, strVal))
		}
	}

	return strings.Join(parts, ",")
}

// buildFieldTimeoutError creates a helpful timeout error for field existence waits
// Following ADR-015: Actionable Error Messages and Diagnostic Context
func (r *waitResource) buildFieldTimeoutError(obj *unstructured.Unstructured, fieldPath string, timeout time.Duration) error {
	kind := obj.GetKind()
	name := obj.GetName()
	namespace := obj.GetNamespace()

	resourceRef := fmt.Sprintf("%s %q", kind, name)
	if namespace != "" {
		resourceRef += fmt.Sprintf(" in namespace %q", namespace)
	}

	errMsg := fmt.Sprintf("Wait Timeout\n\n")
	errMsg += fmt.Sprintf("%s field %q was not populated within %v\n\n", resourceRef, fieldPath, timeout)

	errMsg += "Common causes:\n"
	errMsg += "• Resource controller may be slow or not running\n"
	errMsg += "• Field may require external dependencies or actions\n"
	errMsg += "• Resource may not have reached the state where this field is populated\n\n"

	errMsg += "Troubleshooting:\n"
	errMsg += fmt.Sprintf("• Increase timeout if the operation is legitimately slow:\n")
	errMsg += fmt.Sprintf("    wait_for = { field = %q, timeout = \"300s\" }\n", fieldPath)

	if namespace != "" {
		errMsg += fmt.Sprintf("• Inspect the resource for errors or pending conditions:\n")
		errMsg += fmt.Sprintf("    kubectl describe %s %s -n %s\n", kind, name, namespace)
		errMsg += fmt.Sprintf("• Check full resource status:\n")
		errMsg += fmt.Sprintf("    kubectl get %s %s -n %s -o yaml\n", kind, name, namespace)
	} else {
		errMsg += fmt.Sprintf("• Inspect the resource for errors or pending conditions:\n")
		errMsg += fmt.Sprintf("    kubectl describe %s %s\n", kind, name)
		errMsg += fmt.Sprintf("• Check full resource status:\n")
		errMsg += fmt.Sprintf("    kubectl get %s %s -o yaml\n", kind, name)
	}

	return fmt.Errorf("%s", errMsg)
}

// buildFieldValuesTimeoutError creates a helpful timeout error for field value waits
// Following ADR-015: Actionable Error Messages and Diagnostic Context
func (r *waitResource) buildFieldValuesTimeoutError(obj *unstructured.Unstructured, fieldValues map[string]string, timeout time.Duration) error {
	kind := obj.GetKind()
	name := obj.GetName()
	namespace := obj.GetNamespace()

	resourceRef := fmt.Sprintf("%s %q", kind, name)
	if namespace != "" {
		resourceRef += fmt.Sprintf(" in namespace %q", namespace)
	}

	errMsg := fmt.Sprintf("Wait Timeout\n\n")
	errMsg += fmt.Sprintf("%s did not reach the expected field values within %v\n\n", resourceRef, timeout)

	errMsg += "Waiting for:\n"
	for field, value := range fieldValues {
		errMsg += fmt.Sprintf("• %s = %q\n", field, value)
	}
	errMsg += "\n"

	errMsg += "Common causes:\n"
	errMsg += "• Resource may not be progressing (check status and events)\n"
	errMsg += "• Resource controller may be slow or encountering errors\n"
	errMsg += "• Expected values may be incorrect\n\n"

	errMsg += "Troubleshooting:\n"
	errMsg += "• Increase timeout if the operation is legitimately slow:\n"
	errMsg += fmt.Sprintf("    wait_for = { field_value = {...}, timeout = \"300s\" }\n")

	if namespace != "" {
		errMsg += fmt.Sprintf("• Inspect the resource for errors:\n")
		errMsg += fmt.Sprintf("    kubectl describe %s %s -n %s\n", kind, name, namespace)
		errMsg += fmt.Sprintf("• Check current field values:\n")
		errMsg += fmt.Sprintf("    kubectl get %s %s -n %s -o yaml\n", kind, name, namespace)
	} else {
		errMsg += fmt.Sprintf("• Inspect the resource for errors:\n")
		errMsg += fmt.Sprintf("    kubectl describe %s %s\n", kind, name)
		errMsg += fmt.Sprintf("• Check current field values:\n")
		errMsg += fmt.Sprintf("    kubectl get %s %s -o yaml\n", kind, name)
	}

	return fmt.Errorf("%s", errMsg)
}
