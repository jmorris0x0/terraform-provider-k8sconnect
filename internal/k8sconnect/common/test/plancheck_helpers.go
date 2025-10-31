package test

import (
	"context"
	"fmt"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// ExpectManagedFieldsTransition verifies that the plan shows a managed_fields transition
// This is the CRITICAL test for managed_fields - it must predict ownership changes in the plan
func ExpectManagedFieldsTransition(resourceAddress, fieldPath, fromOwner, toOwner string) plancheck.PlanCheck {
	return &expectManagedFieldsTransition{
		resourceAddress: resourceAddress,
		fieldPath:       fieldPath,
		fromOwner:       fromOwner,
		toOwner:         toOwner,
	}
}

type expectManagedFieldsTransition struct {
	resourceAddress string
	fieldPath       string
	fromOwner       string
	toOwner         string
}

func (e *expectManagedFieldsTransition) CheckPlan(ctx context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	// Find the resource in ResourceChanges
	var targetResource *tfjson.ResourceChange
	for _, rc := range req.Plan.ResourceChanges {
		if rc.Address == e.resourceAddress {
			targetResource = rc
			break
		}
	}

	if targetResource == nil {
		resp.Error = fmt.Errorf("resource %q not found in plan", e.resourceAddress)
		return
	}

	if targetResource.Change == nil {
		resp.Error = fmt.Errorf("resource %q has no change object", e.resourceAddress)
		return
	}

	// Get the after object (predicted state after apply)
	after, ok := targetResource.Change.After.(map[string]interface{})
	if !ok {
		resp.Error = fmt.Errorf("resource %q change.after is not a map, got %T", e.resourceAddress, targetResource.Change.After)
		return
	}

	// Get managed_fields from after
	managedFieldsRaw, ok := after["managed_fields"]
	if !ok {
		resp.Error = fmt.Errorf("resource %q after state has no managed_fields attribute", e.resourceAddress)
		return
	}

	// Handle unknown values (shouldn't happen for UPDATE, but just in case)
	if managedFieldsRaw == nil {
		resp.Error = fmt.Errorf("resource %q managed_fields is unknown in plan - cannot verify transition", e.resourceAddress)
		return
	}

	managedFields, ok := managedFieldsRaw.(map[string]interface{})
	if !ok {
		resp.Error = fmt.Errorf("resource %q managed_fields is not a map, got %T", e.resourceAddress, managedFieldsRaw)
		return
	}

	// Check if the field exists in managed_fields and has the expected "toOwner" value
	actualOwner, exists := managedFields[e.fieldPath]
	if !exists {
		resp.Error = fmt.Errorf("resource %q managed_fields missing path %q\nAvailable paths: %v",
			e.resourceAddress, e.fieldPath, getKeys(managedFields))
		return
	}

	actualOwnerStr, ok := actualOwner.(string)
	if !ok {
		resp.Error = fmt.Errorf("resource %q managed_fields[%q] is not a string, got %T",
			e.resourceAddress, e.fieldPath, actualOwner)
		return
	}

	// Verify the transition
	if actualOwnerStr != e.toOwner {
		resp.Error = fmt.Errorf("resource %q managed_fields[%q] expected %q (transition from %q), got %q",
			e.resourceAddress, e.fieldPath, e.toOwner, e.fromOwner, actualOwnerStr)
		return
	}

	// Also verify the before state has the fromOwner
	before, ok := targetResource.Change.Before.(map[string]interface{})
	if ok {
		if beforeManagedFields, ok := before["managed_fields"].(map[string]interface{}); ok {
			if beforeOwner, exists := beforeManagedFields[e.fieldPath]; exists {
				beforeOwnerStr, ok := beforeOwner.(string)
				if ok && beforeOwnerStr != e.fromOwner {
					resp.Error = fmt.Errorf("resource %q managed_fields[%q] before state expected %q, got %q",
						e.resourceAddress, e.fieldPath, e.fromOwner, beforeOwnerStr)
					return
				}
			}
		}
	}

	fmt.Printf("✅ Plan correctly shows managed_fields transition: %s[%q]: %q → %q\n",
		e.resourceAddress, e.fieldPath, e.fromOwner, e.toOwner)
}

// ExpectManagedFieldsRemoved verifies that a field is no longer in managed_fields
// This happens when ignore_fields is added for a previously-managed field
func ExpectManagedFieldsRemoved(resourceAddress, fieldPath string) plancheck.PlanCheck {
	return &expectManagedFieldsRemoved{
		resourceAddress: resourceAddress,
		fieldPath:       fieldPath,
	}
}

type expectManagedFieldsRemoved struct {
	resourceAddress string
	fieldPath       string
}

func (e *expectManagedFieldsRemoved) CheckPlan(ctx context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	// Find the resource in ResourceChanges
	var targetResource *tfjson.ResourceChange
	for _, rc := range req.Plan.ResourceChanges {
		if rc.Address == e.resourceAddress {
			targetResource = rc
			break
		}
	}

	if targetResource == nil {
		resp.Error = fmt.Errorf("resource %q not found in plan", e.resourceAddress)
		return
	}

	if targetResource.Change == nil {
		resp.Error = fmt.Errorf("resource %q has no change object", e.resourceAddress)
		return
	}

	// Get the after object (predicted state after apply)
	after, ok := targetResource.Change.After.(map[string]interface{})
	if !ok {
		resp.Error = fmt.Errorf("resource %q change.after is not a map, got %T", e.resourceAddress, targetResource.Change.After)
		return
	}

	// Get managed_fields from after
	managedFieldsRaw, ok := after["managed_fields"]
	if !ok {
		resp.Error = fmt.Errorf("resource %q after state has no managed_fields attribute", e.resourceAddress)
		return
	}

	managedFields, ok := managedFieldsRaw.(map[string]interface{})
	if !ok {
		resp.Error = fmt.Errorf("resource %q managed_fields is not a map, got %T", e.resourceAddress, managedFieldsRaw)
		return
	}

	// Check if the field is NOT in managed_fields (it was removed)
	if _, exists := managedFields[e.fieldPath]; exists {
		resp.Error = fmt.Errorf("resource %q managed_fields still contains path %q, expected it to be removed",
			e.resourceAddress, e.fieldPath)
		return
	}

	fmt.Printf("✅ Plan correctly shows managed_fields removal: %s[%q] no longer tracked\n",
		e.resourceAddress, e.fieldPath)
}

// Helper function to get keys from a map[string]interface{}
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
