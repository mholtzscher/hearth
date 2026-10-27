//nolint:testpackage // Checks owned Condition preparation and its retained private expression programs.
package automations

import (
	"errors"
	"testing"
)

func TestBranchConditionPreparationOwnsBindingsAndRetainsPrograms(t *testing.T) {
	t.Parallel()
	triggerIDs := []TriggerID{"clock"}
	children := []Condition{
		{ID: "expression", Body: ExpressionCondition{Value: ValueBinding{Body: ExpressionValue{Source: "vars.ready"}}}},
		{ID: "constant", Body: ExpressionCondition{Value: ValueBinding{Body: ExpressionValue{Source: "true"}}}},
		{ID: "trigger", Body: TriggerCondition{TriggerIDs: triggerIDs}},
		{ID: "not", Body: NotCondition{Child: Condition{ID: "any", Body: AnyCondition{Children: []Condition{
			{ID: "nested", Body: ExpressionCondition{Value: ValueBinding{Body: ExpressionValue{Source: "vars.ready"}}}},
		}}}}},
	}
	root := Condition{ID: "all", Body: AllCondition{Children: children}}
	walk := stepTreePreparation{triggerIDs: map[TriggerID]bool{"clock": true}}
	prepared, err := walk.condition(root, true)
	if err != nil {
		t.Fatal(err)
	}
	owned := prepared.Body.(AllCondition).Children
	for _, node := range []Condition{owned[0], owned[3].Body.(NotCondition).Child.Body.(AnyCondition).Children[0]} {
		binding := node.Body.(ExpressionCondition).Value
		if binding.Body.(ExpressionValue).program == nil {
			t.Fatalf("prepared Condition %s has no compiled program", node.ID)
		}
		values, valueErr := newRunValues(flowRun(Definition{}))
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		values.env.Vars["ready"] = true
		result, resolveErr := values.resolve(t.Context(), binding, "/conditions/expr")
		if resolveErr != nil || result.Data != true {
			t.Fatalf("prepared Condition %s: %v, %v", node.ID, result.Data, resolveErr)
		}
	}
	if children[0].Body.(ExpressionCondition).Value.Body.(ExpressionValue).program != nil {
		t.Fatal("preparation mutated the caller's expression binding")
	}
	triggerIDs[0] = "changed"
	children[0].ID = "changed"
	if owned[0].ID != "expression" ||
		owned[1].Body.(ExpressionCondition).Value.Body.(ExpressionValue).Source != "true" ||
		owned[2].Body.(TriggerCondition).TriggerIDs[0] != "clock" {
		t.Fatal("prepared Condition aliases caller-owned payloads")
	}
}

func TestBranchConditionPreparationPreservesNestedBindingErrorPath(t *testing.T) {
	t.Parallel()
	root := Condition{ID: "all", Body: AllCondition{Children: []Condition{
		{ID: "not", Body: NotCondition{Child: Condition{ID: "any", Body: AnyCondition{Children: []Condition{
			{ID: "invalid", Body: ExpressionCondition{Value: ValueBinding{Body: ExpressionValue{Source: "vars."}}}},
		}}}}},
	}}}
	_, err := NormalizeDefinition(Definition{
		Name:     "Invalid nested expression",
		Triggers: []Trigger{{ID: "clock", Body: CronTrigger{Expression: "0 12 * * *"}}},
		Steps: []Step{{ID: "branch", Body: IfStep{
			Conditions: root, Then: []Step{{ID: "wait", Body: DelayStep{DurationMS: 1}}},
		}}},
	})
	failure, ok := errors.AsType[*DefinitionError](err)
	if !ok || len(failure.Issues) != 1 ||
		failure.Issues[0].Path != "/steps/0/conditions/children/0/child/children/0/expr" {
		t.Fatalf("nested binding error lost its path: %#v, %v", failure, err)
	}
}
