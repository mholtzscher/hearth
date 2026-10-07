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
		switch body := node.Body.(type) {
		case TriggerCondition:
			// Trigger leaves request no State.
		case EntityStateCondition:
			ids = append(ids, body.EntityID)
		case AllCondition:
			for _, child := range body.Children {
				collect(child)
			}
		case AnyCondition:
			for _, child := range body.Children {
				collect(child)
			}
		case NotCondition:
			collect(body.Child)
		default:
			panic("invalid normalized Condition body")
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
	switch body := node.Body.(type) {
	case EntityStateCondition:
		if err := validateEntityStateConditionValue(body); err != nil {
			return err
		}
		walk.entityIDs = append(walk.entityIDs, body.EntityID)
		return nil
	case TriggerCondition:
		return walk.visitTrigger(node.ID, body)
	case AllCondition:
		return walk.visitGroup(node.ID, body.Children, depth)
	case AnyCondition:
		return walk.visitGroup(node.ID, body.Children, depth)
	case NotCondition:
		return walk.visit(&body.Child, depth+1)
	default:
		return invalid("condition %q: unsupported body", node.ID)
	}
}

func (walk *conditionTreeWalk) visitGroup(id ConditionID, children []Condition, depth int) error {
	if len(children) == 0 {
		return invalid("condition %q: all/any requires a nonempty children array", id)
	}
	for index := range children {
		if err := walk.visit(&children[index], depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (walk *conditionTreeWalk) visitTrigger(id ConditionID, body TriggerCondition) error {
	if !walk.allowTrigger {
		return invalid("condition %q: trigger Conditions are invalid at admission", id)
	}
	if len(body.TriggerIDs) < 1 || len(body.TriggerIDs) > automationTriggerMaxCount {
		return invalid("condition %q: trigger_ids requires 1 to 32 IDs", id)
	}
	seen := make(map[TriggerID]bool)
	for _, triggerID := range body.TriggerIDs {
		if !subjectSlugPattern.MatchString(string(triggerID)) || seen[triggerID] || !walk.triggerIDs[triggerID] {
			return invalid("condition %q: trigger IDs must be unique references in this definition", id)
		}
		seen[triggerID] = true
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
	cloned := Condition{ID: condition.ID}
	switch body := condition.Body.(type) {
	case TriggerCondition:
		cloned.Body = TriggerCondition{TriggerIDs: slices.Clone(body.TriggerIDs)}
	case EntityStateCondition:
		cloned.Body = cloneEntityStateCondition(body)
	case AllCondition:
		cloned.Body = AllCondition{Children: cloneConditionChildren(body.Children)}
	case AnyCondition:
		cloned.Body = AnyCondition{Children: cloneConditionChildren(body.Children)}
	case NotCondition:
		cloned.Body = NotCondition{Child: cloneAutomationCondition(body.Child)}
	default:
		panic("invalid normalized Condition body")
	}
	return cloned
}

func cloneConditionChildren(children []Condition) []Condition {
	cloned := make([]Condition, 0, len(children))
	for _, child := range children {
		cloned = append(cloned, cloneAutomationCondition(child))
	}
	return cloned
}

func cloneEntityStateCondition(condition EntityStateCondition) EntityStateCondition {
	cloned := condition
	cloned.Operand = append(json.RawMessage(nil), condition.Operand...)
	if condition.MaxAgeSeconds != nil {
		age := *condition.MaxAgeSeconds
		cloned.MaxAgeSeconds = &age
	}
	return cloned
}
