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
	schema     *jsonschema.Schema
	triggers   *jsonschema.Schema
	conditions *jsonschema.Schema
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
	conditions, err := compiler.Compile(schemaID + "#/$defs/condition")
	if err != nil {
		return nil, fmt.Errorf("automation condition schema compile: %w", err)
	}
	return &DefinitionCodec{schema: compiled, triggers: triggers, conditions: conditions}, nil
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
	if err = boundDefinitionJSON(document); err != nil {
		return Definition{}, err
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
	// Normalization can expand legacy aliases, so check normalized bytes too.
	normalized, _, err := NormalizeAndEncodeDefinition(automationDefinitionFromJSON(value))
	return normalized, err
}

// boundDefinitionJSON rejects excessive recursive nesting before schema evaluation
// and recursive typed conversion. Shape checks remain the schema's responsibility.
func boundDefinitionJSON(document any) error {
	object, _ := document.(map[string]any)
	if err := boundConditionJSON(object["conditions"], 1, new(int)); err != nil {
		return err
	}
	return boundStepSequenceJSON(object["steps"], 1, new(int))
}

func boundConditionJSON(value any, depth int, count *int) error {
	if value == nil {
		return nil
	}
	*count++
	if depth > automationConditionMaxDepth || *count > automationConditionMaxNodes {
		return definitionIssue("/conditions", "Condition tree exceeds its depth or node bound")
	}
	object, _ := value.(map[string]any)
	children, _ := object["children"].([]any)
	for _, child := range children {
		if err := boundConditionJSON(child, depth+1, count); err != nil {
			return err
		}
	}
	return boundConditionJSON(object["child"], depth+1, count)
}

func boundStepSequenceJSON(value any, depth int, count *int) error {
	steps, _ := value.([]any)
	for _, value := range steps {
		*count++
		if depth > automationStepMaxDepth || *count > automationAllStepsMaxCount {
			return definitionIssue("/steps", "Step tree exceeds its depth or node bound")
		}
		object, _ := value.(map[string]any)
		if err := boundConditionJSON(object["conditions"], 1, new(int)); err != nil {
			return err
		}
		for _, field := range []string{"then", "else", "default"} {
			if err := boundStepSequenceJSON(object[field], depth+1, count); err != nil {
				return err
			}
		}
		branches, _ := object["branches"].([]any)
		if err := boundChooseBranchesJSON(branches, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

func boundChooseBranchesJSON(branches []any, depth int, count *int) error {
	for _, value := range branches {
		branch, _ := value.(map[string]any)
		if err := boundConditionJSON(branch["conditions"], 1, new(int)); err != nil {
			return err
		}
		if err := boundStepSequenceJSON(branch["steps"], depth, count); err != nil {
			return err
		}
	}
	return nil
}

// EncodeDefinition validates arbitrary domain input before recursive encoding.
// Commands retain their legacy wire shape. Recursive depth/count bounds also
// terminate cyclic Go values before copying or encoding them.
func EncodeDefinition(definition Definition) (json.RawMessage, error) {
	normalized, err := prepareDefinition(definition)
	if err != nil {
		return nil, err
	}
	return encodePreparedDefinition(normalized)
}

// encodePreparedDefinition consumes an unchanged, structurally normalized value.
func encodePreparedDefinition(definition Definition) (json.RawMessage, error) {
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
		value.Steps = append(value.Steps, encodeAutomationStep(step))
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: definition cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	if len(raw) > automationDefinitionMaxBytes {
		return nil, definitionIssue("", "definition exceeds 65536 bytes")
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
	ID         StepID                       `json:"id"`
	Kind       StepKind                     `json:"kind,omitempty"`
	EntityID   devices.EntityID             `json:"entity_id,omitempty"`
	Operation  devices.OperationName        `json:"operation,omitempty"`
	Parameters json.RawMessage              `json:"parameters,omitempty"`
	Conditions *automationConditionJSON     `json:"conditions,omitempty"`
	Then       []automationStepJSON         `json:"then,omitempty"`
	Else       []automationStepJSON         `json:"else,omitempty"`
	Branches   []automationChooseBranchJSON `json:"branches,omitempty"`
	Default    []automationStepJSON         `json:"default,omitempty"`
}

type automationChooseBranchJSON struct {
	ID         BranchID                `json:"id"`
	Conditions automationConditionJSON `json:"conditions"`
	Steps      []automationStepJSON    `json:"steps"`
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
	encoded := automationStepJSON{ID: step.ID}
	switch step.Kind {
	case StepKindCommand:
		encoded.EntityID, encoded.Operation, encoded.Parameters = step.EntityID, step.OperationName, json.RawMessage(
			step.Parameters,
		)
	case StepKindIf:
		encoded.Kind = step.Kind
		encoded.Conditions = encodeAutomationConditionTree(&step.If.Conditions)
		encoded.Then, encoded.Else = encodeAutomationSteps(step.If.Then), encodeAutomationSteps(step.If.Else)
	case StepKindChoose:
		encoded.Kind = step.Kind
		for _, branch := range step.Choose.Branches {
			encoded.Branches = append(encoded.Branches, automationChooseBranchJSON{
				ID:         branch.ID,
				Conditions: encodeAutomationCondition(branch.Conditions),
				Steps:      encodeAutomationSteps(branch.Steps),
			})
		}
		encoded.Default = encodeAutomationSteps(step.Choose.Default)
	}
	return encoded
}

func decodeAutomationSteps(steps []automationStepJSON) []Step {
	if steps == nil {
		return nil
	}
	decoded := make([]Step, 0, len(steps))
	for _, item := range steps {
		step := Step{ID: item.ID, Kind: item.Kind}
		switch item.Kind {
		case "", StepKindCommand:
			step.EntityID, step.OperationName, step.Parameters = item.EntityID, item.Operation, devices.CommandParameters(
				item.Parameters,
			)
		case StepKindIf:
			step.If = &IfStep{
				Conditions: automationConditionFromJSON(*item.Conditions),
				Then:       decodeAutomationSteps(item.Then),
				Else:       decodeAutomationSteps(item.Else),
			}
		case StepKindChoose:
			step.Choose = &ChooseStep{Default: decodeAutomationSteps(item.Default)}
			for _, branch := range item.Branches {
				step.Choose.Branches = append(
					step.Choose.Branches,
					ChooseBranch{
						ID:         branch.ID,
						Conditions: automationConditionFromJSON(branch.Conditions),
						Steps:      decodeAutomationSteps(branch.Steps),
					},
				)
			}
		}
		decoded = append(decoded, step)
	}
	return decoded
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
		Steps:      decodeAutomationSteps(value.Steps),
	}
	for _, item := range value.Triggers {
		definition.Triggers = append(definition.Triggers, automationTriggerFromJSON(item))
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
