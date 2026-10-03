package automations

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//go:embed automation-definition.schema.json
var automationDefinitionSchema []byte

// automationDefinitionCodec compiles the embedded strict persisted shape once per process.
//
//nolint:gochecknoglobals // One immutable compiled schema, never reassigned.
var automationDefinitionCodec = sync.OnceValues(NewDefinitionCodec)

// DefinitionCodec owns the compiled strict definition schema.
type DefinitionCodec struct {
	schema   *jsonschema.Schema
	triggers *jsonschema.Schema
}

// NewDefinitionCodec compiles the canonical embedded schema.
func NewDefinitionCodec() (*DefinitionCodec, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(automationDefinitionSchema))
	if err != nil {
		return nil, fmt.Errorf("automation definition schema decode: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaID = "urn:hearth:schema:automation-definition:v1"
	if err = compiler.AddResource(schemaID, document); err != nil {
		return nil, fmt.Errorf("automation definition schema resource: %w", err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		return nil, fmt.Errorf("automation definition schema compile: %w", err)
	}
	triggers, err := compiler.Compile(schemaID + "#/properties/triggers/items")
	if err != nil {
		return nil, fmt.Errorf("automation trigger schema compile: %w", err)
	}
	return &DefinitionCodec{schema: compiled, triggers: triggers}, nil
}

// AutomationDefinitionSchema returns owned copies of the embedded strict shape.
func (*DefinitionCodec) AutomationDefinitionSchema() json.RawMessage {
	return bytes.Clone(automationDefinitionSchema)
}

// DecodeDefinition validates and normalizes JSON against the strict schema and structural rules.
func DecodeDefinition(raw json.RawMessage) (Definition, error) {
	if len(raw) == 0 {
		return Definition{}, definitionIssue("", "definition is required")
	}
	if len(raw) > automationDefinitionMaxBytes {
		return Definition{}, definitionIssue(
			"", fmt.Sprintf("definition exceeds %d bytes", automationDefinitionMaxBytes),
		)
	}
	codec, err := automationDefinitionCodec()
	if err != nil {
		return Definition{}, err
	}
	document, err := decodeJSONValue(raw)
	if err != nil {
		return Definition{}, definitionIssue("", "definition must be exactly one JSON object")
	}
	if err = codec.schema.Validate(document); err != nil {
		var validation *jsonschema.ValidationError
		if !errors.As(err, &validation) {
			return Definition{}, definitionIssue("", "definition does not satisfy the strict schema")
		}
		issues := make([]DefinitionIssue, 0, len(validation.Causes)+1)
		collectDefinitionIssues(validation, &issues)
		return Definition{}, &DefinitionError{Issues: issues}
	}
	var value automationDefinitionJSON
	if err = json.Unmarshal(raw, &value); err != nil {
		return Definition{}, definitionIssue("", "definition cannot be bound")
	}
	// The raw document passed the size check above; normalize its typed shape
	// without serializing the whole definition again.
	return prepareDefinition(automationDefinitionFromJSON(value))
}

// EncodeDefinition renders one definition in the strict persisted representation.
// It does not validate.
func EncodeDefinition(definition Definition) (json.RawMessage, error) {
	value := automationDefinitionJSON{
		Name:       definition.Name,
		Enabled:    definition.Enabled,
		Triggers:   make([]automationTriggerJSON, 0, len(definition.Triggers)),
		Conditions: encodeAutomationConditionTree(definition.Conditions),
		Steps:      make([]automationStepJSON, 0, len(definition.Steps)),
	}
	for _, trigger := range definition.Triggers {
		value.Triggers = append(value.Triggers, encodeAutomationTrigger(trigger))
	}
	for _, step := range definition.Steps {
		value.Steps = append(value.Steps, automationStepJSON{
			ID:         step.ID,
			EntityID:   step.EntityID,
			Operation:  step.OperationName,
			Parameters: json.RawMessage(step.Parameters),
		})
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: definition cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

// EncodeMatchedTriggers renders matching Trigger snapshots in the same strict
// persisted shape as a definition's Triggers.
func EncodeMatchedTriggers(triggers []Trigger) (json.RawMessage, error) {
	encoded := make([]automationTriggerJSON, 0, len(triggers))
	for _, trigger := range triggers {
		encoded = append(encoded, encodeAutomationTrigger(trigger))
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: matched triggers cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

// DecodeMatchedTriggers validates and normalizes a persisted Trigger snapshot
// list back into domain values.
func DecodeMatchedTriggers(raw json.RawMessage) ([]Trigger, error) {
	codec, err := automationDefinitionCodec()
	if err != nil {
		return nil, err
	}
	document, err := decodeJSONValue(raw)
	if err != nil {
		return nil, invalid("matched triggers must contain exactly one JSON array")
	}
	items, ok := document.([]any)
	if !ok || len(items) > automationTriggerMaxCount {
		return nil, invalid("matched triggers must be an array of at most 32 triggers")
	}
	for _, item := range items {
		if err = codec.triggers.Validate(item); err != nil {
			return nil, invalid("matched triggers do not satisfy the strict trigger schema")
		}
	}
	var encoded []automationTriggerJSON
	if err = json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	triggers := make([]Trigger, 0, len(encoded))
	for _, item := range encoded {
		trigger, normalizeErr := normalizeAutomationTriggerValue(automationTriggerFromJSON(item))
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		triggers = append(triggers, trigger)
	}
	return triggers, nil
}

type automationDefinitionJSON struct {
	Name       string                   `json:"name"`
	Enabled    bool                     `json:"enabled"`
	Triggers   []automationTriggerJSON  `json:"triggers"`
	Conditions *automationConditionJSON `json:"conditions,omitempty"`
	Steps      []automationStepJSON     `json:"steps"`
}

type automationTriggerJSON struct {
	ID                  TriggerID                        `json:"id"`
	Kind                TriggerKind                      `json:"kind"`
	EntityID            devices.EntityID                 `json:"entity_id,omitempty"`
	Dispositions        []devices.ObservationDisposition `json:"dispositions,omitempty"`
	PreviousComparisons []observationComparisonJSON      `json:"previous_comparisons,omitempty"`
	Comparisons         []observationComparisonJSON      `json:"comparisons,omitempty"`
	EventName           devices.EntityEventName          `json:"event_name,omitempty"`
	ForSeconds          *int64                           `json:"for_seconds,omitempty"`
	Expression          string                           `json:"expression,omitempty"`
}

type observationComparisonJSON struct {
	ValuePointer  *string            `json:"value_pointer,omitempty"`
	LegacyPointer *string            `json:"pointer,omitempty"`
	Operator      ComparisonOperator `json:"operator"`
	Operand       json.RawMessage    `json:"operand"`
}

type automationStepJSON struct {
	ID         StepID                `json:"id"`
	EntityID   devices.EntityID      `json:"entity_id"`
	Operation  devices.OperationName `json:"operation"`
	Parameters json.RawMessage       `json:"parameters"`
}

func encodeAutomationTrigger(trigger Trigger) automationTriggerJSON {
	encoded := automationTriggerJSON{ID: trigger.ID, Kind: trigger.Kind}
	switch trigger.Kind {
	case TriggerKindCron:
		if trigger.Cron != nil {
			encoded.Expression = trigger.Cron.Expression
		}
	case TriggerKindObservation:
		if trigger.Observation != nil {
			encoded.EntityID = trigger.Observation.EntityID
			encoded.Dispositions = trigger.Observation.Dispositions
			for _, comparison := range trigger.Observation.PreviousComparisons {
				valuePointer := comparison.Pointer
				encoded.PreviousComparisons = append(encoded.PreviousComparisons, observationComparisonJSON{
					ValuePointer: &valuePointer,
					Operator:     comparison.Operator,
					Operand:      comparison.Operand,
				})
			}
			for _, comparison := range trigger.Observation.Comparisons {
				valuePointer := comparison.Pointer
				encoded.Comparisons = append(
					encoded.Comparisons, observationComparisonJSON{
						ValuePointer: &valuePointer,
						Operator:     comparison.Operator,
						Operand:      comparison.Operand,
					},
				)
			}
		}
	case TriggerKindEntityEvent:
		if trigger.EntityEvent != nil {
			encoded.EntityID = trigger.EntityEvent.EntityID
			encoded.EventName = trigger.EntityEvent.EventName
		}
	case TriggerKindHeldState:
		if trigger.HeldState != nil {
			encoded.EntityID = trigger.HeldState.EntityID
			encoded.ForSeconds = &trigger.HeldState.ForSeconds
			for _, comparison := range trigger.HeldState.Comparisons {
				valuePointer := comparison.Pointer
				encoded.Comparisons = append(encoded.Comparisons, observationComparisonJSON{
					ValuePointer: &valuePointer, Operator: comparison.Operator, Operand: comparison.Operand,
				})
			}
		}
	}
	return encoded
}

// automationDefinitionFromJSON maps a schema-validated document to domain types.
func automationDefinitionFromJSON(value automationDefinitionJSON) Definition {
	definition := Definition{
		Name:       value.Name,
		Enabled:    value.Enabled,
		Triggers:   make([]Trigger, 0, len(value.Triggers)),
		Conditions: decodeConditionTree(value.Conditions),
		Steps:      make([]Step, 0, len(value.Steps)),
	}
	for _, item := range value.Triggers {
		definition.Triggers = append(definition.Triggers, automationTriggerFromJSON(item))
	}
	for _, item := range value.Steps {
		definition.Steps = append(definition.Steps, Step{
			ID:            item.ID,
			EntityID:      item.EntityID,
			OperationName: item.Operation,
			Parameters:    devices.CommandParameters(item.Parameters),
		})
	}
	return definition
}

func automationTriggerFromJSON(item automationTriggerJSON) Trigger {
	trigger := Trigger{ID: item.ID, Kind: item.Kind}
	switch item.Kind {
	case TriggerKindCron:
		trigger.Cron = &CronTrigger{Expression: item.Expression}
	case TriggerKindObservation:
		observation := &ObservationTrigger{EntityID: item.EntityID, Dispositions: item.Dispositions}
		for _, comparison := range item.PreviousComparisons {
			valuePointer := comparison.LegacyPointer
			if comparison.ValuePointer != nil {
				valuePointer = comparison.ValuePointer
			}
			observation.PreviousComparisons = append(observation.PreviousComparisons, ObservationComparison{
				Pointer: *valuePointer, Operator: comparison.Operator, Operand: comparison.Operand,
			})
		}
		for _, comparison := range item.Comparisons {
			valuePointer := comparison.LegacyPointer
			if comparison.ValuePointer != nil {
				valuePointer = comparison.ValuePointer
			}
			observation.Comparisons = append(observation.Comparisons, ObservationComparison{
				Pointer:  *valuePointer,
				Operator: comparison.Operator,
				Operand:  comparison.Operand,
			})
		}
		trigger.Observation = observation
	case TriggerKindEntityEvent:
		trigger.EntityEvent = &EntityEventTrigger{EntityID: item.EntityID, EventName: item.EventName}
	case TriggerKindHeldState:
		heldState := &HeldStateTrigger{
			EntityID:    item.EntityID,
			Comparisons: make([]ObservationComparison, 0, len(item.Comparisons)),
		}
		if item.ForSeconds != nil {
			heldState.ForSeconds = *item.ForSeconds
		}
		for _, comparison := range item.Comparisons {
			valuePointer := comparison.LegacyPointer
			if comparison.ValuePointer != nil {
				valuePointer = comparison.ValuePointer
			}
			heldState.Comparisons = append(heldState.Comparisons, ObservationComparison{
				Pointer: *valuePointer, Operator: comparison.Operator, Operand: comparison.Operand,
			})
		}
		trigger.HeldState = heldState
	}
	return trigger
}
func collectDefinitionIssues(validation *jsonschema.ValidationError, issues *[]DefinitionIssue) {
	if len(validation.Causes) > 0 {
		for _, cause := range validation.Causes {
			collectDefinitionIssues(cause, issues)
		}
		return
	}
	var pointer strings.Builder
	for _, part := range validation.InstanceLocation {
		pointer.WriteString("/" + strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1"))
	}
	*issues = append(*issues, DefinitionIssue{
		Path:    pointer.String(),
		Message: "value does not satisfy the strict schema",
	})
}
