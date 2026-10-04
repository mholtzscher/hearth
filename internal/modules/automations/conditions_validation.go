package automations

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// NormalizeConditions validates one typed Condition tree and returns an owned copy of it.
func NormalizeConditions(root Condition) (Condition, error) {
	if err := validateAutomationConditionTree(root); err != nil {
		return Condition{}, err
	}
	return cloneAutomationCondition(root), nil
}

// RequiredConditionEntityIDs returns the sorted, deduplicated Entity IDs every
// entity_state leaf explicitly requests.
func RequiredConditionEntityIDs(root Condition) ([]devices.EntityID, error) {
	walk := newConditionTreeWalk()
	if err := walk.visit(&root, 1); err != nil {
		return nil, err
	}
	if len(walk.entityIDs) == 0 {
		return nil, nil
	}
	slices.Sort(walk.entityIDs)
	return slices.Compact(walk.entityIDs), nil
}

// requiredValidatedConditionEntityIDs collects references from a freshly
// normalized tree; unlike the public helper it does not validate arbitrary input.
func requiredValidatedConditionEntityIDs(root Condition) []devices.EntityID {
	ids := make([]devices.EntityID, 0)
	var collect func(Condition)
	collect = func(node Condition) {
		switch node.Kind {
		case ConditionTrigger:
			// Trigger leaves request no State.
		case ConditionEntityState:
			ids = append(ids, node.EntityState.EntityID)
		case ConditionAll, ConditionAny:
			for _, child := range node.Children {
				collect(child)
			}
		case ConditionNot:
			collect(*node.Child)
		}
	}
	collect(root)
	slices.Sort(ids)
	return slices.Compact(ids)
}

// conditionTreeWalk validates one typed tree while collecting node IDs, entity
// IDs, and the node count. Depth and node-count bounds keep recursion safe;
// freely constructed Go trees may contain cycles or shared payloads.
type conditionTreeWalk struct {
	allowTrigger bool
	triggerIDs   map[TriggerID]bool
	ids          map[ConditionID]struct{}
	entityIDs    []devices.EntityID
	nodes        int
}

func newConditionTreeWalk() *conditionTreeWalk {
	return &conditionTreeWalk{
		ids: map[ConditionID]struct{}{},
	}
}

// validateAutomationConditionTree validates one typed tree without producing an owned copy.
func validateAutomationConditionTree(root Condition) error {
	return newConditionTreeWalk().visit(&root, 1)
}

// visit validates one node and its descendants. depth is 1 for the root.
func (walk *conditionTreeWalk) visit(node *Condition, depth int) error {
	if depth > automationConditionMaxDepth {
		return invalid("condition %q: tree exceeds the maximum depth", node.ID)
	}
	walk.nodes++
	if walk.nodes > automationConditionMaxNodes {
		return invalid("condition %q: tree exceeds the maximum node count", node.ID)
	}
	if !subjectSlugPattern.MatchString(string(node.ID)) {
		return fmt.Errorf("%w: condition ID %q is not a subject-safe slug", ErrInvalidAutomation, node.ID)
	}
	if _, duplicate := walk.ids[node.ID]; duplicate {
		return invalid("condition %q: condition IDs must be unique within a tree", node.ID)
	}
	walk.ids[node.ID] = struct{}{}
	switch node.Kind {
	case ConditionEntityState:
		return walk.visitEntityState(node)
	case ConditionTrigger:
		return walk.visitTrigger(node)
	case ConditionAll, ConditionAny:
		return walk.visitGroup(node, depth)
	case ConditionNot:
		return walk.visitNot(node, depth)
	default:
		return invalid("condition %q: unknown kind %q", node.ID, node.Kind)
	}
}

func (walk *conditionTreeWalk) visitEntityState(node *Condition) error {
	if node.EntityState == nil || node.Trigger != nil || node.Children != nil || node.Child != nil {
		return invalid("condition %q: entity_state family payload mismatch", node.ID)
	}
	if err := validateEntityStateConditionValue(*node.EntityState); err != nil {
		return err
	}
	walk.entityIDs = append(walk.entityIDs, node.EntityState.EntityID)
	return nil
}

func (walk *conditionTreeWalk) visitGroup(node *Condition, depth int) error {
	if node.EntityState != nil || node.Trigger != nil || node.Child != nil {
		return invalid("condition %q: all/any family payload mismatch", node.ID)
	}
	if len(node.Children) == 0 {
		return invalid("condition %q: all/any requires a nonempty children array", node.ID)
	}
	for index := range node.Children {
		if err := walk.visit(&node.Children[index], depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (walk *conditionTreeWalk) visitNot(node *Condition, depth int) error {
	if node.EntityState != nil || node.Trigger != nil || node.Children != nil {
		return invalid("condition %q: not family payload mismatch", node.ID)
	}
	if node.Child == nil {
		return invalid("condition %q: not requires exactly one child", node.ID)
	}
	return walk.visit(node.Child, depth+1)
}

func (walk *conditionTreeWalk) visitTrigger(node *Condition) error {
	if !walk.allowTrigger {
		return invalid("condition %q: trigger Conditions are invalid at admission", node.ID)
	}
	if node.Trigger == nil || node.EntityState != nil || node.Children != nil || node.Child != nil {
		return invalid("condition %q: trigger family payload mismatch", node.ID)
	}
	if len(node.Trigger.TriggerIDs) < 1 || len(node.Trigger.TriggerIDs) > automationTriggerMaxCount {
		return invalid("condition %q: trigger_ids requires 1 to 32 IDs", node.ID)
	}
	seen := make(map[TriggerID]bool)
	for _, id := range node.Trigger.TriggerIDs {
		if !subjectSlugPattern.MatchString(string(id)) || seen[id] || !walk.triggerIDs[id] {
			return invalid("condition %q: trigger IDs must be unique references in this definition", node.ID)
		}
		seen[id] = true
	}
	return nil
}

// validateEntityStateConditionValue checks one leaf's Entity, pointer, operator,
// operand, and evidence age bound.
func validateEntityStateConditionValue(condition EntityStateCondition) error {
	if _, err := devices.ParseEntityID(string(condition.EntityID)); err != nil {
		return fmt.Errorf("%w: condition entity: %w", ErrInvalidAutomation, err)
	}
	if err := validateConditionComparison(condition.Pointer, condition.Operator, condition.Operand); err != nil {
		return err
	}
	if condition.MaxAgeSeconds != nil {
		age := *condition.MaxAgeSeconds
		if age < 1 || age > automationConditionMaxAgeSeconds {
			return fmt.Errorf(
				"%w: max_age_seconds must be between 1 and %d",
				ErrInvalidAutomation, automationConditionMaxAgeSeconds,
			)
		}
	}
	return nil
}

// cloneAutomationCondition returns an owned deep copy so a caller cannot mutate
// a normalized tree through shared slices, pointers, or operand bytes.
func cloneAutomationCondition(condition Condition) Condition {
	cloned := Condition{ID: condition.ID, Kind: condition.Kind}
	switch condition.Kind {
	case ConditionTrigger:
		cloned.Trigger = &TriggerCondition{TriggerIDs: slices.Clone(condition.Trigger.TriggerIDs)}
	case ConditionEntityState:
		if condition.EntityState != nil {
			cloned.EntityState = cloneEntityStateCondition(*condition.EntityState)
		}
	case ConditionAll, ConditionAny:
		if len(condition.Children) > 0 {
			cloned.Children = make([]Condition, 0, len(condition.Children))
			for _, child := range condition.Children {
				cloned.Children = append(cloned.Children, cloneAutomationCondition(child))
			}
		}
	case ConditionNot:
		if condition.Child != nil {
			child := cloneAutomationCondition(*condition.Child)
			cloned.Child = &child
		}
	default:
	}
	return cloned
}

func cloneEntityStateCondition(condition EntityStateCondition) *EntityStateCondition {
	cloned := condition
	cloned.Operand = append(json.RawMessage(nil), condition.Operand...)
	if condition.MaxAgeSeconds != nil {
		age := *condition.MaxAgeSeconds
		cloned.MaxAgeSeconds = &age
	}
	return &cloned
}
