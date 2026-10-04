package automations

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func decodeStrictDTO(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

type observationComparisonJSON struct {
	ValuePointer string             `json:"value_pointer"`
	Operator     ComparisonOperator `json:"operator"`
	Operand      json.RawMessage    `json:"operand"`
}

type automationStepJSON struct{ body stepJSONBody }

//sumtype:decl
type stepJSONBody interface{ isStepJSONBody() }
type commandStepJSON struct {
	ID         StepID                `json:"id"`
	Kind       StepKind              `json:"kind"`
	EntityID   devices.EntityID      `json:"entity_id"`
	Operation  devices.OperationName `json:"operation"`
	Parameters json.RawMessage       `json:"parameters"`
}
type ifStepJSON struct {
	ID         StepID                  `json:"id"`
	Kind       StepKind                `json:"kind"`
	Conditions automationConditionJSON `json:"conditions"`
	Then       []automationStepJSON    `json:"then"`
	Else       []automationStepJSON    `json:"else,omitempty"`
}
type chooseStepJSON struct {
	ID       StepID                       `json:"id"`
	Kind     StepKind                     `json:"kind"`
	Branches []automationChooseBranchJSON `json:"branches"`
	Default  []automationStepJSON         `json:"default,omitempty"`
}
type automationChooseBranchJSON struct {
	ID         BranchID                `json:"id"`
	Conditions automationConditionJSON `json:"conditions"`
	Steps      []automationStepJSON    `json:"steps"`
}

func (commandStepJSON) isStepJSONBody()                       {}
func (ifStepJSON) isStepJSONBody()                            {}
func (chooseStepJSON) isStepJSONBody()                        {}
func (value automationStepJSON) MarshalJSON() ([]byte, error) { return json.Marshal(value.body) }
func (value *automationStepJSON) UnmarshalJSON(raw []byte) error {
	var label struct {
		Kind StepKind `json:"kind"`
	}
	if err := json.Unmarshal(raw, &label); err != nil {
		return err
	}
	switch label.Kind {
	case StepKindCommand:
		var body commandStepJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	case StepKindIf:
		var body ifStepJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	case StepKindChoose:
		var body chooseStepJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	default:
		return fmt.Errorf("%w: unsupported Step kind", ErrInvalidAutomation)
	}
	return nil
}

func encodeAutomationSteps(steps []Step) []automationStepJSON {
	if steps == nil {
		return nil
	}
	encoded := make([]automationStepJSON, 0, len(steps))
	for _, step := range steps {
		encoded = append(encoded, encodeAutomationStep(step))
	}
	return encoded
}
func encodeAutomationStep(step Step) automationStepJSON {
	switch body := step.Body.(type) {
	case CommandStep:
		return automationStepJSON{
			body: commandStepJSON{
				ID:         step.ID,
				Kind:       StepKindCommand,
				EntityID:   body.EntityID,
				Operation:  body.OperationName,
				Parameters: json.RawMessage(body.Parameters),
			},
		}
	case IfStep:
		return automationStepJSON{
			body: ifStepJSON{
				ID:         step.ID,
				Kind:       StepKindIf,
				Conditions: encodeAutomationCondition(body.Conditions),
				Then:       encodeAutomationSteps(body.Then),
				Else:       encodeAutomationSteps(body.Else),
			},
		}
	case ChooseStep:
		branches := make([]automationChooseBranchJSON, 0, len(body.Branches))
		for _, branch := range body.Branches {
			branches = append(
				branches,
				automationChooseBranchJSON{
					ID:         branch.ID,
					Conditions: encodeAutomationCondition(branch.Conditions),
					Steps:      encodeAutomationSteps(branch.Steps),
				},
			)
		}
		return automationStepJSON{
			body: chooseStepJSON{
				ID:       step.ID,
				Kind:     StepKindChoose,
				Branches: branches,
				Default:  encodeAutomationSteps(body.Default),
			},
		}
	default:
		panic("invalid normalized Step body")
	}
}
func decodeAutomationSteps(steps []automationStepJSON) []Step {
	if steps == nil {
		return nil
	}
	decoded := make([]Step, 0, len(steps))
	for _, value := range steps {
		decoded = append(decoded, automationStepFromJSON(value))
	}
	return decoded
}
func automationStepFromJSON(value automationStepJSON) Step {
	switch body := value.body.(type) {
	case commandStepJSON:
		return Step{
			ID: body.ID,
			Body: CommandStep{
				EntityID:      body.EntityID,
				OperationName: body.Operation,
				Parameters:    devices.CommandParameters(body.Parameters),
			},
		}
	case ifStepJSON:
		return Step{
			ID: body.ID,
			Body: IfStep{
				Conditions: automationConditionFromJSON(body.Conditions),
				Then:       decodeAutomationSteps(body.Then),
				Else:       decodeAutomationSteps(body.Else),
			},
		}
	case chooseStepJSON:
		branches := make([]ChooseBranch, 0, len(body.Branches))
		for _, branch := range body.Branches {
			branches = append(
				branches,
				ChooseBranch{
					ID:         branch.ID,
					Conditions: automationConditionFromJSON(branch.Conditions),
					Steps:      decodeAutomationSteps(branch.Steps),
				},
			)
		}
		return Step{ID: body.ID, Body: ChooseStep{Branches: branches, Default: decodeAutomationSteps(body.Default)}}
	default:
		panic("invalid validated Step DTO")
	}
}

type automationTriggerJSON struct{ body triggerJSONBody }

//sumtype:decl
type triggerJSONBody interface{ isTriggerJSONBody() }
type observationTriggerJSON struct {
	ID                  TriggerID                        `json:"id"`
	Kind                TriggerKind                      `json:"kind"`
	EntityID            devices.EntityID                 `json:"entity_id"`
	Dispositions        []devices.ObservationDisposition `json:"dispositions"`
	PreviousComparisons []observationComparisonJSON      `json:"previous_comparisons,omitempty"`
	Comparisons         []observationComparisonJSON      `json:"comparisons"`
}
type entityEventTriggerJSON struct {
	ID        TriggerID               `json:"id"`
	Kind      TriggerKind             `json:"kind"`
	EntityID  devices.EntityID        `json:"entity_id"`
	EventName devices.EntityEventName `json:"event_name"`
}
type heldStateTriggerJSON struct {
	ID          TriggerID                   `json:"id"`
	Kind        TriggerKind                 `json:"kind"`
	EntityID    devices.EntityID            `json:"entity_id"`
	Comparisons []observationComparisonJSON `json:"comparisons"`
	ForSeconds  int64                       `json:"for_seconds"`
}
type cronTriggerJSON struct {
	ID         TriggerID   `json:"id"`
	Kind       TriggerKind `json:"kind"`
	Expression string      `json:"expression"`
}

func (observationTriggerJSON) isTriggerJSONBody()                {}
func (entityEventTriggerJSON) isTriggerJSONBody()                {}
func (heldStateTriggerJSON) isTriggerJSONBody()                  {}
func (cronTriggerJSON) isTriggerJSONBody()                       {}
func (value automationTriggerJSON) MarshalJSON() ([]byte, error) { return json.Marshal(value.body) }
func (value *automationTriggerJSON) UnmarshalJSON(raw []byte) error {
	var label struct {
		Kind TriggerKind `json:"kind"`
	}
	if err := json.Unmarshal(raw, &label); err != nil {
		return err
	}
	switch label.Kind {
	case TriggerKindObservation:
		var body observationTriggerJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	case TriggerKindEntityEvent:
		var body entityEventTriggerJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	case TriggerKindHeldState:
		var body heldStateTriggerJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	case TriggerKindCron:
		var body cronTriggerJSON
		if err := decodeStrictDTO(raw, &body); err != nil {
			return err
		}
		value.body = body
	default:
		return fmt.Errorf("%w: unsupported Trigger kind", ErrInvalidAutomation)
	}
	return nil
}
func encodeComparisons(comparisons []ObservationComparison) []observationComparisonJSON {
	encoded := make([]observationComparisonJSON, 0, len(comparisons))
	for _, comparison := range comparisons {
		encoded = append(
			encoded,
			observationComparisonJSON{
				ValuePointer: comparison.Pointer,
				Operator:     comparison.Operator,
				Operand:      comparison.Operand,
			},
		)
	}
	return encoded
}
func decodeComparisons(comparisons []observationComparisonJSON) []ObservationComparison {
	decoded := make([]ObservationComparison, 0, len(comparisons))
	for _, comparison := range comparisons {
		decoded = append(
			decoded,
			ObservationComparison{
				Pointer:  comparison.ValuePointer,
				Operator: comparison.Operator,
				Operand:  comparison.Operand,
			},
		)
	}
	return decoded
}
func encodeAutomationTrigger(trigger Trigger) automationTriggerJSON {
	switch body := trigger.Body.(type) {
	case ObservationTrigger:
		return automationTriggerJSON{
			body: observationTriggerJSON{
				ID:                  trigger.ID,
				Kind:                TriggerKindObservation,
				EntityID:            body.EntityID,
				Dispositions:        body.Dispositions,
				PreviousComparisons: encodeComparisons(body.PreviousComparisons),
				Comparisons:         encodeComparisons(body.Comparisons),
			},
		}
	case EntityEventTrigger:
		return automationTriggerJSON{
			body: entityEventTriggerJSON{
				ID:        trigger.ID,
				Kind:      TriggerKindEntityEvent,
				EntityID:  body.EntityID,
				EventName: body.EventName,
			},
		}
	case HeldStateTrigger:
		return automationTriggerJSON{
			body: heldStateTriggerJSON{
				ID:          trigger.ID,
				Kind:        TriggerKindHeldState,
				EntityID:    body.EntityID,
				Comparisons: encodeComparisons(body.Comparisons),
				ForSeconds:  body.ForSeconds,
			},
		}
	case CronTrigger:
		return automationTriggerJSON{
			body: cronTriggerJSON{ID: trigger.ID, Kind: TriggerKindCron, Expression: body.Expression},
		}
	default:
		panic("invalid normalized Trigger body")
	}
}
func automationTriggerFromJSON(value automationTriggerJSON) Trigger {
	switch body := value.body.(type) {
	case observationTriggerJSON:
		return Trigger{
			ID: body.ID,
			Body: ObservationTrigger{
				EntityID:            body.EntityID,
				Dispositions:        body.Dispositions,
				PreviousComparisons: decodeComparisons(body.PreviousComparisons),
				Comparisons:         decodeComparisons(body.Comparisons),
			},
		}
	case entityEventTriggerJSON:
		return Trigger{ID: body.ID, Body: EntityEventTrigger{EntityID: body.EntityID, EventName: body.EventName}}
	case heldStateTriggerJSON:
		return Trigger{
			ID: body.ID,
			Body: HeldStateTrigger{
				EntityID:    body.EntityID,
				Comparisons: decodeComparisons(body.Comparisons),
				ForSeconds:  body.ForSeconds,
			},
		}
	case cronTriggerJSON:
		return Trigger{ID: body.ID, Body: CronTrigger{Expression: body.Expression}}
	default:
		panic("invalid validated Trigger DTO")
	}
}
