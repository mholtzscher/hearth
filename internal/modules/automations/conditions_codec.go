package automations

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// automationConditionJSON is the strict persisted, flattened Condition shape
// shared by definitions and retained Condition snapshots.
type automationConditionJSON struct {
	ID            ConditionID               `json:"id"`
	Kind          ConditionKind             `json:"kind"`
	EntityID      devices.EntityID          `json:"entity_id,omitempty"`
	ValuePointer  *string                   `json:"value_pointer,omitempty"`
	LegacyPointer *string                   `json:"pointer,omitempty"`
	Operator      ComparisonOperator        `json:"operator,omitempty"`
	Operand       json.RawMessage           `json:"operand,omitempty"`
	MaxAgeSeconds *int64                    `json:"max_age_seconds,omitempty"`
	Children      []automationConditionJSON `json:"children,omitempty"`
	Child         *automationConditionJSON  `json:"child,omitempty"`
}

// encodeAutomationConditionTree renders one normalized Condition tree in the strict persisted form.
func encodeAutomationConditionTree(condition *Condition) *automationConditionJSON {
	if condition == nil {
		return nil
	}
	encoded := encodeAutomationCondition(*condition)
	return &encoded
}

func encodeAutomationCondition(condition Condition) automationConditionJSON {
	encoded := automationConditionJSON{ID: condition.ID, Kind: condition.Kind}
	switch condition.Kind {
	case ConditionEntityState:
		if condition.EntityState != nil {
			encoded.EntityID = condition.EntityState.EntityID
			pointer := condition.EntityState.Pointer
			encoded.ValuePointer = &pointer
			encoded.Operator = condition.EntityState.Operator
			encoded.Operand = condition.EntityState.Operand
			encoded.MaxAgeSeconds = condition.EntityState.MaxAgeSeconds
		}
	case ConditionAll, ConditionAny:
		encoded.Children = make([]automationConditionJSON, 0, len(condition.Children))
		for _, child := range condition.Children {
			encoded.Children = append(encoded.Children, encodeAutomationCondition(child))
		}
	case ConditionNot:
		if condition.Child != nil {
			child := encodeAutomationCondition(*condition.Child)
			encoded.Child = &child
		}
	default:
	}
	return encoded
}

// automationConditionFromJSON maps a schema-validated persisted node to its domain form.
func automationConditionFromJSON(value automationConditionJSON) Condition {
	condition := Condition{ID: value.ID, Kind: value.Kind}
	switch value.Kind {
	case ConditionEntityState:
		pointer := ""
		if value.LegacyPointer != nil {
			pointer = *value.LegacyPointer
		}
		if value.ValuePointer != nil {
			pointer = *value.ValuePointer
		}
		condition.EntityState = &EntityStateCondition{
			EntityID:      value.EntityID,
			Pointer:       pointer,
			Operator:      value.Operator,
			Operand:       value.Operand,
			MaxAgeSeconds: value.MaxAgeSeconds,
		}
	case ConditionAll, ConditionAny:
		if len(value.Children) > 0 {
			condition.Children = make([]Condition, 0, len(value.Children))
			for _, child := range value.Children {
				condition.Children = append(condition.Children, automationConditionFromJSON(child))
			}
		}
	case ConditionNot:
		if value.Child != nil {
			child := automationConditionFromJSON(*value.Child)
			condition.Child = &child
		}
	default:
	}
	return condition
}

// decodeConditionTree maps an optional persisted Condition tree to an owned domain tree.
func decodeConditionTree(value *automationConditionJSON) *Condition {
	if value == nil {
		return nil
	}
	condition := automationConditionFromJSON(*value)
	return &condition
}
