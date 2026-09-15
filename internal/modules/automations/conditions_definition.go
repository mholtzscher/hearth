package automations

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// automationConditionJSON is the strict persisted, flattened Condition shape.
// It is shared by an automation definition's optional conditions and by the
// Condition snapshot retained in history, so both use the same leaf form: an
// entity_state leaf carries entity_id, pointer, operator, operand, and optional
// max_age_seconds directly rather than a nested payload object. Family
// inapplicable fields stay absent, and the embedded definition schema closes
// every family with oneOf plus additionalProperties:false.
type automationConditionJSON struct {
	ID            AutomationConditionID     `json:"id"`
	Kind          AutomationConditionKind   `json:"kind"`
	EntityID      devices.EntityID          `json:"entity_id,omitempty"`
	Pointer       *string                   `json:"pointer,omitempty"`
	Operator      ComparisonOperator        `json:"operator,omitempty"`
	Operand       json.RawMessage           `json:"operand,omitempty"`
	MaxAgeSeconds *int64                    `json:"max_age_seconds,omitempty"`
	Children      []automationConditionJSON `json:"children,omitempty"`
	Child         *automationConditionJSON  `json:"child,omitempty"`
}

// encodeAutomationConditionTree renders one normalized Condition tree in the
// strict persisted form. It emits only the fields of the node's own family; the
// empty pointer of an entity_state leaf is emitted explicitly because the schema
// requires it.
func encodeAutomationConditionTree(condition *AutomationCondition) *automationConditionJSON {
	if condition == nil {
		return nil
	}
	encoded := encodeAutomationCondition(*condition)
	return &encoded
}

func encodeAutomationCondition(condition AutomationCondition) automationConditionJSON {
	encoded := automationConditionJSON{ID: condition.ID, Kind: condition.Kind}
	switch condition.Kind {
	case AutomationConditionEntityState:
		if condition.EntityState != nil {
			encoded.EntityID = condition.EntityState.EntityID
			pointer := condition.EntityState.Pointer
			encoded.Pointer = &pointer
			encoded.Operator = condition.EntityState.Operator
			encoded.Operand = condition.EntityState.Operand
			encoded.MaxAgeSeconds = condition.EntityState.MaxAgeSeconds
		}
	case AutomationConditionAll, AutomationConditionAny:
		encoded.Children = make([]automationConditionJSON, 0, len(condition.Children))
		for _, child := range condition.Children {
			encoded.Children = append(encoded.Children, encodeAutomationCondition(child))
		}
	case AutomationConditionNot:
		if condition.Child != nil {
			child := encodeAutomationCondition(*condition.Child)
			encoded.Child = &child
		}
	default:
	}
	return encoded
}

// automationConditionFromJSON maps a schema-validated persisted node to its
// domain form. Structural family closure is enforced by the schema for decoded
// documents and by validation for typed callers.
func automationConditionFromJSON(value automationConditionJSON) AutomationCondition {
	condition := AutomationCondition{ID: value.ID, Kind: value.Kind}
	switch value.Kind {
	case AutomationConditionEntityState:
		pointer := ""
		if value.Pointer != nil {
			pointer = *value.Pointer
		}
		condition.EntityState = &EntityStateCondition{
			EntityID:      value.EntityID,
			Pointer:       pointer,
			Operator:      value.Operator,
			Operand:       value.Operand,
			MaxAgeSeconds: value.MaxAgeSeconds,
		}
	case AutomationConditionAll, AutomationConditionAny:
		if len(value.Children) > 0 {
			condition.Children = make([]AutomationCondition, 0, len(value.Children))
			for _, child := range value.Children {
				condition.Children = append(condition.Children, automationConditionFromJSON(child))
			}
		}
	case AutomationConditionNot:
		if value.Child != nil {
			child := automationConditionFromJSON(*value.Child)
			condition.Child = &child
		}
	default:
	}
	return condition
}

// decodeConditionTree maps an optional persisted Condition tree to an owned
// domain tree.
func decodeConditionTree(value *automationConditionJSON) *AutomationCondition {
	if value == nil {
		return nil
	}
	condition := automationConditionFromJSON(*value)
	return &condition
}
