package automations_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestAutomationConditionTreeBounds(t *testing.T) {
	t.Parallel()
	if _, err := automations.NormalizeConditions(notChain(7)); err != nil {
		t.Fatalf("depth 8 tree: %v", err)
	}
	if _, err := automations.NormalizeConditions(
		notChain(8),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("depth 9 tree error = %v, want ErrInvalidAutomation", err)
	}
	if _, err := automations.NormalizeConditions(wideGroup(63)); err != nil {
		t.Fatalf("64 node tree: %v", err)
	}
	if _, err := automations.NormalizeConditions(
		wideGroup(64),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("65 node tree error = %v, want ErrInvalidAutomation", err)
	}
}

// notChain returns a not chain with count not nodes plus one leaf.
func notChain(count int) automations.Condition {
	node := conditionLeaf(
		fmt.Sprintf("leaf-%d", count),
		conditionEntity(1),
		"",
		automations.ComparisonEqual,
		"true",
		nil,
	)
	for level := count - 1; level >= 0; level-- {
		node = conditionNot(fmt.Sprintf("not-%d", level), node)
	}
	return node
}

// wideGroup returns one all group with count leaf children.
func wideGroup(count int) automations.Condition {
	children := make([]automations.Condition, 0, count)
	for index := range count {
		children = append(children, conditionLeaf(
			fmt.Sprintf("leaf-%d", index), conditionEntity(1), "", automations.ComparisonEqual, "true", nil,
		))
	}
	return automations.Condition{
		ID:       "root",
		Kind:     automations.ConditionAll,
		Children: children,
	}
}

// Typed validation rejects cycles and shared payloads before recursing without
// bound, plus duplicate IDs, contradictory payloads, invalid pointers, operands,
// and age bounds.
func TestAutomationConditionTreeRejectsInvalidTypedTrees(t *testing.T) {
	t.Parallel()
	leaf := conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonEqual, "true", nil)

	selfCycle := automations.Condition{ID: "self", Kind: automations.ConditionNot}
	selfCycle.Child = &selfCycle

	children := make([]automations.Condition, 1)
	sliceCycle := automations.Condition{
		ID:       "root",
		Kind:     automations.ConditionAll,
		Children: children,
	}
	children[0] = sliceCycle

	shared := []automations.Condition{leaf}
	aliased := automations.Condition{
		ID: "root", Kind: automations.ConditionAll,
		Children: []automations.Condition{
			{ID: "left", Kind: automations.ConditionAny, Children: shared},
			{ID: "right", Kind: automations.ConditionAny, Children: shared},
		},
	}

	duplicate := conditionGroup(automations.ConditionAll,
		conditionLeaf("dup", conditionEntity(1), "", automations.ComparisonEqual, "true", nil),
		conditionLeaf("dup", conditionEntity(2), "", automations.ComparisonEqual, "true", nil),
	)
	emptyGroup := conditionGroup(automations.ConditionAny)
	notWithChildren := automations.Condition{
		ID: "root", Kind: automations.ConditionNot,
		Children: []automations.Condition{leaf}, Child: &leaf,
	}
	leafWithChildren := automations.Condition{
		ID: "root", Kind: automations.ConditionEntityState,
		Children: []automations.Condition{leaf},
		EntityState: &automations.EntityStateCondition{
			EntityID: conditionEntity(1), Operator: automations.ComparisonEqual, Operand: json.RawMessage("true"),
		},
	}
	cases := []struct {
		name string
		root automations.Condition
	}{
		{"child cycle", selfCycle},
		{"slice cycle", sliceCycle},
		{"shared children payload", aliased},
		{"duplicate IDs", duplicate},
		{"empty group", emptyGroup},
		{"not with children", notWithChildren},
		{"leaf with children", leafWithChildren},
		{
			"non-slug ID",
			automations.Condition{ID: "Root", Kind: automations.ConditionNot, Child: &leaf},
		},
		{
			"pointer too long",
			conditionLeaf(
				"leaf",
				conditionEntity(1),
				"/"+string(make([]byte, 256)),
				automations.ComparisonEqual,
				"true",
				nil,
			),
		},
		{
			"pointer without slash",
			conditionLeaf("leaf", conditionEntity(1), "a", automations.ComparisonEqual, "true", nil),
		},
		{
			"unknown operator",
			conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonOperator("like"), "true", nil),
		},
		{
			"ordering operand not numeric",
			conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonLessThan, `"1"`, nil),
		},
		{
			"age below minimum",
			conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonEqual, "true", conditionAge(0)),
		},
		{
			"age above maximum",
			conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonEqual, "true", conditionAge(2_592_001)),
		},
		{
			"nil operand",
			automations.Condition{
				ID: "leaf", Kind: automations.ConditionEntityState,
				EntityState: &automations.EntityStateCondition{
					EntityID: conditionEntity(1), Operator: automations.ComparisonEqual,
				},
			},
		},
	}
	for _, testCase := range cases {
		if _, err := automations.NormalizeConditions(
			testCase.root,
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
	// Age boundaries are inclusive on both ends.
	for _, seconds := range []int64{1, 2_592_000} {
		bounded := conditionLeaf(
			"leaf",
			conditionEntity(1),
			"",
			automations.ComparisonEqual,
			"true",
			conditionAge(seconds),
		)
		if _, err := automations.NormalizeConditions(bounded); err != nil {
			t.Errorf("age %d: %v", seconds, err)
		}
	}
}

// Normalization returns owned copies, so neither caller mutation nor pointer or
// byte aliasing can change the normalized tree.
func TestNormalizeAutomationConditionsReturnsOwnedCopy(t *testing.T) {
	t.Parallel()
	operand := json.RawMessage(`{"a":1}`)
	input := automations.Condition{
		ID: "root", Kind: automations.ConditionAll,
		Children: []automations.Condition{
			{
				ID: "leaf", Kind: automations.ConditionEntityState,
				EntityState: &automations.EntityStateCondition{
					EntityID: conditionEntity(1), Pointer: "", Operator: automations.ComparisonEqual,
					Operand: operand, MaxAgeSeconds: conditionAge(60),
				},
			},
		},
	}
	normalized, err := automations.NormalizeConditions(input)
	if err != nil {
		t.Fatal(err)
	}
	operand[0] = '['
	input.Children = nil
	input.EntityState = &automations.EntityStateCondition{EntityID: conditionEntity(2)}
	if string(normalized.Children[0].EntityState.Operand) != `{"a":1}` {
		t.Fatalf("normalized operand changed with caller mutation: %s", normalized.Children[0].EntityState.Operand)
	}
	if normalized.Children[0].EntityState.EntityID != conditionEntity(1) {
		t.Fatalf("normalized entity changed: %s", normalized.Children[0].EntityState.EntityID)
	}
	normalized.Children[0].ID = "mutated"
	*normalized.Children[0].EntityState.MaxAgeSeconds = 10
	if normalized.Children[0].ID != "mutated" {
		t.Fatal("returned tree must be independent of the input")
	}
}

// Fixed groups preserve double negation and child permutation, and
// normalization is idempotent over representative valid trees. Each fixture
// pairs one comparison style per leaf with one snapshot coverage outcome so
// the results span true, false, and unknown without random generation.
