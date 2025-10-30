package test

import (
	"context"
	"fmt"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// ExpectFieldOwnershipTransition verifies that the plan shows a field_ownership transition
// This is the CRITICAL test for field_ownership - it must predict ownership changes in the plan
func ExpectFieldOwnershipTransition(resourceAddress, fieldPath, fromOwner, toOwner string) plancheck.PlanCheck {
	return &expectFieldOwnershipTransition{
		resourceAddress: resourceAddress,
		fieldPath:       fieldPath,
		fromOwner:       fromOwner,
		toOwner:         toOwner,
	}
}

type expectFieldOwnershipTransition struct {
	resourceAddress string
	fieldPath       string
	fromOwner       string
	toOwner         string
}

func (e *expectFieldOwnershipTransition) CheckPlan(ctx context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
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

	// Get field_ownership from after
	fieldOwnershipRaw, ok := after["field_ownership"]
	if !ok {
		resp.Error = fmt.Errorf("resource %q after state has no field_ownership attribute", e.resourceAddress)
		return
	}

	// Handle unknown values (shouldn't happen for UPDATE, but just in case)
	if fieldOwnershipRaw == nil {
		resp.Error = fmt.Errorf("resource %q field_ownership is unknown in plan - cannot verify transition", e.resourceAddress)
		return
	}

	fieldOwnership, ok := fieldOwnershipRaw.(map[string]interface{})
	if !ok {
		resp.Error = fmt.Errorf("resource %q field_ownership is not a map, got %T", e.resourceAddress, fieldOwnershipRaw)
		return
	}

	// Check if the field exists in field_ownership and has the expected "toOwner" value
	actualOwner, exists := fieldOwnership[e.fieldPath]
	if !exists {
		resp.Error = fmt.Errorf("resource %q field_ownership missing path %q\nAvailable paths: %v",
			e.resourceAddress, e.fieldPath, getKeys(fieldOwnership))
		return
	}

	actualOwnerStr, ok := actualOwner.(string)
	if !ok {
		resp.Error = fmt.Errorf("resource %q field_ownership[%q] is not a string, got %T",
			e.resourceAddress, e.fieldPath, actualOwner)
		return
	}

	// Verify the transition
	if actualOwnerStr != e.toOwner {
		resp.Error = fmt.Errorf("resource %q field_ownership[%q] expected %q (transition from %q), got %q",
			e.resourceAddress, e.fieldPath, e.toOwner, e.fromOwner, actualOwnerStr)
		return
	}

	// Also verify the before state has the fromOwner
	before, ok := targetResource.Change.Before.(map[string]interface{})
	if ok {
		if beforeFieldOwnership, ok := before["field_ownership"].(map[string]interface{}); ok {
			if beforeOwner, exists := beforeFieldOwnership[e.fieldPath]; exists {
				beforeOwnerStr, ok := beforeOwner.(string)
				if ok && beforeOwnerStr != e.fromOwner {
					resp.Error = fmt.Errorf("resource %q field_ownership[%q] before state expected %q, got %q",
						e.resourceAddress, e.fieldPath, e.fromOwner, beforeOwnerStr)
					return
				}
			}
		}
	}

	fmt.Printf("✅ Plan correctly shows field_ownership transition: %s[%q]: %q → %q\n",
		e.resourceAddress, e.fieldPath, e.fromOwner, e.toOwner)
}

// ExpectFieldOwnershipRemoved verifies that a field is no longer in field_ownership
// This happens when ignore_fields is added for a previously-managed field
func ExpectFieldOwnershipRemoved(resourceAddress, fieldPath string) plancheck.PlanCheck {
	return &expectFieldOwnershipRemoved{
		resourceAddress: resourceAddress,
		fieldPath:       fieldPath,
	}
}

type expectFieldOwnershipRemoved struct {
	resourceAddress string
	fieldPath       string
}

func (e *expectFieldOwnershipRemoved) CheckPlan(ctx context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
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

	// Get field_ownership from after
	fieldOwnershipRaw, ok := after["field_ownership"]
	if !ok {
		resp.Error = fmt.Errorf("resource %q after state has no field_ownership attribute", e.resourceAddress)
		return
	}

	fieldOwnership, ok := fieldOwnershipRaw.(map[string]interface{})
	if !ok {
		resp.Error = fmt.Errorf("resource %q field_ownership is not a map, got %T", e.resourceAddress, fieldOwnershipRaw)
		return
	}

	// Check if the field is NOT in field_ownership (it was removed)
	if _, exists := fieldOwnership[e.fieldPath]; exists {
		resp.Error = fmt.Errorf("resource %q field_ownership still contains path %q, expected it to be removed",
			e.resourceAddress, e.fieldPath)
		return
	}

	fmt.Printf("✅ Plan correctly shows field_ownership removal: %s[%q] no longer tracked\n",
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
