package automations

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Private wire wrappers select concrete DTOs rather than optional family fields.
type automationConditionJSON struct{ body conditionJSONBody }

//sumtype:decl
type conditionJSONBody interface{ isConditionJSONBody() }

type stateConditionJSON struct {
	ID            ConditionID        `json:"id"`
	Kind          ConditionKind      `json:"kind"`
	EntityID      devices.EntityID   `json:"entity_id"`
	ValuePointer  string             `json:"value_pointer"`
	Operator      ComparisonOperator `json:"operator"`
	Operand       json.RawMessage    `json:"operand"`
	MaxAgeSeconds *int64             `json:"max_age_seconds,omitempty"`
}
type triggerConditionJSON struct {
	ID         ConditionID   `json:"id"`
	Kind       ConditionKind `json:"kind"`
	TriggerIDs []TriggerID   `json:"trigger_ids"`
}
type allConditionJSON struct {
	ID       ConditionID               `json:"id"`
	Kind     ConditionKind             `json:"kind"`
	Children []automationConditionJSON `json:"children"`
}
type anyConditionJSON struct {
	ID       ConditionID               `json:"id"`
	Kind     ConditionKind             `json:"kind"`
	Children []automationConditionJSON `json:"children"`
}
type notConditionJSON struct {
	ID    ConditionID             `json:"id"`
	Kind  ConditionKind           `json:"kind"`
	Child automationConditionJSON `json:"child"`
}

func (stateConditionJSON) isConditionJSONBody()   {}
func (triggerConditionJSON) isConditionJSONBody() {}
func (allConditionJSON) isConditionJSONBody()     {}
func (anyConditionJSON) isConditionJSONBody()     {}
func (notConditionJSON) isConditionJSONBody()     {}

func (value automationConditionJSON) MarshalJSON() ([]byte, error) { return json.Marshal(value.body) }
func (value *automationConditionJSON) UnmarshalJSON(raw []byte) error {
	var label struct {
		Kind ConditionKind `json:"kind"`
	}
	if err := json.Unmarshal(raw, &label); err != nil {
		return err
	}
	switch label.Kind {
	case ConditionEntityState:
		var body stateConditionJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	case ConditionTrigger:
		var body triggerConditionJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	case ConditionAll:
		var body allConditionJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	case ConditionAny:
		var body anyConditionJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	case ConditionNot:
		var body notConditionJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	default:
		return fmt.Errorf("%w: unsupported Condition kind", ErrInvalidAutomation)
	}
	return nil
}

func encodeAutomationConditionTree(condition *Condition) *automationConditionJSON {
	if condition == nil {
		return nil
	}
	encoded := encodeAutomationCondition(*condition)
	return &encoded
}

func encodeAutomationCondition(condition Condition) automationConditionJSON {
	switch body := condition.Body.(type) {
	case EntityStateCondition:
		return automationConditionJSON{
			body: stateConditionJSON{
				ID:            condition.ID,
				Kind:          ConditionEntityState,
				EntityID:      body.EntityID,
				ValuePointer:  body.Pointer,
				Operator:      body.Operator,
				Operand:       body.Operand,
				MaxAgeSeconds: body.MaxAgeSeconds,
			},
		}
	case TriggerCondition:
		return automationConditionJSON{
			body: triggerConditionJSON{ID: condition.ID, Kind: ConditionTrigger, TriggerIDs: body.TriggerIDs},
		}
	case AllCondition:
		return automationConditionJSON{
			body: allConditionJSON{
				ID:       condition.ID,
				Kind:     ConditionAll,
				Children: encodeConditionChildren(body.Children),
			},
		}
	case AnyCondition:
		return automationConditionJSON{
			body: anyConditionJSON{
				ID:       condition.ID,
				Kind:     ConditionAny,
				Children: encodeConditionChildren(body.Children),
			},
		}
	case NotCondition:
		return automationConditionJSON{
			body: notConditionJSON{ID: condition.ID, Kind: ConditionNot, Child: encodeAutomationCondition(body.Child)},
		}
	default:
		panic("invalid normalized Condition body")
	}
}
func encodeConditionChildren(children []Condition) []automationConditionJSON {
	encoded := make([]automationConditionJSON, 0, len(children))
	for _, child := range children {
		encoded = append(encoded, encodeAutomationCondition(child))
	}
	return encoded
}

func automationConditionFromJSON(value automationConditionJSON) Condition {
	switch body := value.body.(type) {
	case stateConditionJSON:
		return Condition{
			ID: body.ID,
			Body: EntityStateCondition{
				EntityID:      body.EntityID,
				Pointer:       body.ValuePointer,
				Operator:      body.Operator,
				Operand:       body.Operand,
				MaxAgeSeconds: body.MaxAgeSeconds,
			},
		}
	case triggerConditionJSON:
		return Condition{ID: body.ID, Body: TriggerCondition{TriggerIDs: body.TriggerIDs}}
	case allConditionJSON:
		return Condition{ID: body.ID, Body: AllCondition{Children: decodeConditionChildren(body.Children)}}
	case anyConditionJSON:
		return Condition{ID: body.ID, Body: AnyCondition{Children: decodeConditionChildren(body.Children)}}
	case notConditionJSON:
		return Condition{ID: body.ID, Body: NotCondition{Child: automationConditionFromJSON(body.Child)}}
	default:
		panic("invalid validated Condition DTO")
	}
}

func decodeConditionChildren(children []automationConditionJSON) []Condition {
	decoded := make([]Condition, 0, len(children))
	for _, child := range children {
		decoded = append(decoded, automationConditionFromJSON(child))
	}
	return decoded
}

func decodeConditionTree(value *automationConditionJSON) *Condition {
	if value == nil {
		return nil
	}
	condition := automationConditionFromJSON(*value)
	return &condition
}
