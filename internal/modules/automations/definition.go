package automations

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	// automationDefinitionMaxBytes bounds one encoded definition after
	// normalization.
	automationDefinitionMaxBytes = 64 * 1024
	// automationNameMaxRunes bounds the trimmed definition name.
	automationNameMaxRunes = 200
	// automationTriggerMaxCount bounds the Trigger list.
	automationTriggerMaxCount = 32
	// automationStepMaxCount bounds the ordered Step list.
	automationStepMaxCount = 32
	// automationDispositionMaxCount bounds one Observation Trigger's dispositions.
	automationDispositionMaxCount = 2
	// automationComparisonMaxCount bounds one Observation Trigger's comparisons.
	automationComparisonMaxCount = 8
)

//go:embed automation-definition.schema.json
var automationDefinitionSchema []byte

// automationDefinitionCodec compiles the embedded strict persisted shape once
// per process.
//
//nolint:gochecknoglobals // One immutable compiled schema, never reassigned.
var automationDefinitionCodec = sync.OnceValues(NewAutomationDefinitionCodec)

// AutomationDefinitionIssue is one safe structural explanation at a JSON
// Pointer. It never contains definition payload values.
type AutomationDefinitionIssue struct {
	Path    string
	Message string
}

// AutomationDefinitionError reports one or more structural definition failures.
// It matches ErrInvalidAutomation so transport boundaries map it as a permanent
// input error.
type AutomationDefinitionError struct {
	Issues []AutomationDefinitionIssue
}

// Error reports the fixed class message without echoing definition payloads.
func (*AutomationDefinitionError) Error() string {
	return "automation definition does not satisfy the strict schema"
}

// Is classifies every structural definition failure as ErrInvalidAutomation.
func (*AutomationDefinitionError) Is(target error) bool { return target == ErrInvalidAutomation }

// AutomationDefinitionCodec owns the compiled strict definition schema.
type AutomationDefinitionCodec struct {
	schema *jsonschema.Schema
}

// NewAutomationDefinitionCodec compiles the canonical embedded schema.
func NewAutomationDefinitionCodec() (*AutomationDefinitionCodec, error) {
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
	return &AutomationDefinitionCodec{schema: compiled}, nil
}

// AutomationDefinitionSchema returns owned copies of the embedded strict shape.
func (*AutomationDefinitionCodec) AutomationDefinitionSchema() json.RawMessage {
	return bytes.Clone(automationDefinitionSchema)
}

// DecodeAutomationDefinition strictly validates and normalizes one encoded
// definition: unknown fields, typed-family exclusivity, bounds, slugs, pointers,
// operators, and operands are all rejected before any value is trusted. It never
// consults devices, so verification of current Entity and Operation references
// belongs to [ValidateAutomationDefinition].
func DecodeAutomationDefinition(raw json.RawMessage) (AutomationDefinition, error) {
	if len(raw) == 0 {
		return AutomationDefinition{}, definitionIssue("", "definition is required")
	}
	if len(raw) > automationDefinitionMaxBytes {
		return AutomationDefinition{}, definitionIssue(
			"", fmt.Sprintf("definition exceeds %d bytes", automationDefinitionMaxBytes),
		)
	}
	codec, err := automationDefinitionCodec()
	if err != nil {
		return AutomationDefinition{}, err
	}
	document, err := decodeJSONValue(raw)
	if err != nil {
		return AutomationDefinition{}, definitionIssue("", "definition must be exactly one JSON object")
	}
	if err = codec.schema.Validate(document); err != nil {
		var validation *jsonschema.ValidationError
		if !errors.As(err, &validation) {
			return AutomationDefinition{}, definitionIssue("", "definition does not satisfy the strict schema")
		}
		issues := make([]AutomationDefinitionIssue, 0, len(validation.Causes)+1)
		collectDefinitionIssues(validation, &issues)
		return AutomationDefinition{}, &AutomationDefinitionError{Issues: issues}
	}
	var value automationDefinitionJSON
	if err = json.Unmarshal(raw, &value); err != nil {
		return AutomationDefinition{}, definitionIssue("", "definition cannot be bound")
	}
	return normalizeAutomationDefinition(value)
}

// EncodeAutomationDefinition renders one definition in the strict persisted
// representation. It does not validate; call [NormalizeAutomationDefinition]
// before persisting caller-supplied values.
func EncodeAutomationDefinition(definition AutomationDefinition) (json.RawMessage, error) {
	value := automationDefinitionJSON{
		Name:     definition.Name,
		Enabled:  definition.Enabled,
		Triggers: make([]automationTriggerJSON, 0, len(definition.Triggers)),
		Steps:    make([]automationStepJSON, 0, len(definition.Steps)),
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

// NormalizeAutomationDefinition structurally validates and normalizes one
// in-memory definition without consulting devices, so equal definitions always
// have one representation.
func NormalizeAutomationDefinition(definition AutomationDefinition) (AutomationDefinition, error) {
	raw, err := EncodeAutomationDefinition(definition)
	if err != nil {
		return AutomationDefinition{}, err
	}
	return DecodeAutomationDefinition(raw)
}

// ValidateAutomationDefinition normalizes one definition and then verifies every
// current reference through the devices seam: each Trigger's Entity and event
// name and each Step's Entity, Operation, and static parameters. It returns the
// definition with Step parameters replaced by their normalized form. Save-time
// validation proves current references only; it deliberately does not require
// enablement, availability, or owner health.
func ValidateAutomationDefinition(
	ctx context.Context,
	automationDevices AutomationDevices,
	definition AutomationDefinition,
) (AutomationDefinition, error) {
	normalized, err := NormalizeAutomationDefinition(definition)
	if err != nil {
		return AutomationDefinition{}, err
	}
	return validateAutomationReferences(ctx, automationDevices, normalized)
}

func validateAutomationReferences(
	ctx context.Context,
	automationDevices AutomationDevices,
	definition AutomationDefinition,
) (AutomationDefinition, error) {
	if automationDevices == nil {
		return AutomationDefinition{}, fmt.Errorf("%w: device validation is not configured", ErrInvalidAutomation)
	}
	for _, trigger := range definition.Triggers {
		if err := validateAutomationTriggerReference(ctx, automationDevices, trigger); err != nil {
			return AutomationDefinition{}, err
		}
	}
	steps := make([]AutomationStep, len(definition.Steps))
	copy(steps, definition.Steps)
	for index, step := range definition.Steps {
		parameters, err := automationDevices.ValidateCommand(ctx, devices.CommandInput{
			EntityID:      step.EntityID,
			OperationName: step.OperationName,
			Parameters:    step.Parameters,
		})
		if err != nil {
			return AutomationDefinition{}, fmt.Errorf("%w: step %q: %w", ErrInvalidAutomation, step.ID, err)
		}
		steps[index].Parameters = parameters
	}
	definition.Steps = steps
	return definition, nil
}

func validateAutomationTriggerReference(
	ctx context.Context,
	automationDevices AutomationDevices,
	trigger AutomationTrigger,
) error {
	switch trigger.Kind {
	case TriggerKindObservation:
		if err := automationDevices.ValidateObservationTrigger(ctx, trigger.Observation.EntityID); err != nil {
			return fmt.Errorf("%w: trigger %q: %w", ErrInvalidAutomation, trigger.ID, err)
		}
	case TriggerKindEntityEvent:
		err := automationDevices.ValidateEntityEventTrigger(
			ctx, trigger.EntityEvent.EntityID, trigger.EntityEvent.EventName,
		)
		if err != nil {
			return fmt.Errorf("%w: trigger %q: %w", ErrInvalidAutomation, trigger.ID, err)
		}
	default:
		return fmt.Errorf("%w: trigger %q has unknown kind %q", ErrInvalidAutomation, trigger.ID, trigger.Kind)
	}
	return nil
}

type automationDefinitionJSON struct {
	Name     string                  `json:"name"`
	Enabled  bool                    `json:"enabled"`
	Triggers []automationTriggerJSON `json:"triggers"`
	Steps    []automationStepJSON    `json:"steps"`
}

type automationTriggerJSON struct {
	ID           TriggerID                        `json:"id"`
	Kind         TriggerKind                      `json:"kind"`
	EntityID     devices.EntityID                 `json:"entity_id"`
	Dispositions []devices.ObservationDisposition `json:"dispositions,omitempty"`
	Comparisons  []observationComparisonJSON      `json:"comparisons,omitempty"`
	EventName    devices.EntityEventName          `json:"event_name,omitempty"`
}

type observationComparisonJSON struct {
	Pointer  string             `json:"pointer"`
	Operator ComparisonOperator `json:"operator"`
	Operand  json.RawMessage    `json:"operand"`
}

type automationStepJSON struct {
	ID         StepID                `json:"id"`
	EntityID   devices.EntityID      `json:"entity_id"`
	Operation  devices.OperationName `json:"operation"`
	Parameters json.RawMessage       `json:"parameters"`
}

func encodeAutomationTrigger(trigger AutomationTrigger) automationTriggerJSON {
	encoded := automationTriggerJSON{ID: trigger.ID, Kind: trigger.Kind}
	switch trigger.Kind {
	case TriggerKindObservation:
		if trigger.Observation != nil {
			encoded.EntityID = trigger.Observation.EntityID
			encoded.Dispositions = trigger.Observation.Dispositions
			for _, comparison := range trigger.Observation.Comparisons {
				encoded.Comparisons = append(
					encoded.Comparisons, observationComparisonJSON(comparison),
				)
			}
		}
	case TriggerKindEntityEvent:
		if trigger.EntityEvent != nil {
			encoded.EntityID = trigger.EntityEvent.EntityID
			encoded.EventName = trigger.EntityEvent.EventName
		}
	}
	return encoded
}

func normalizeAutomationDefinition(raw automationDefinitionJSON) (AutomationDefinition, error) {
	name := strings.TrimSpace(raw.Name)
	if runeCount := utf8.RuneCountInString(name); runeCount < 1 || runeCount > automationNameMaxRunes {
		return AutomationDefinition{}, definitionIssue(
			"/name", fmt.Sprintf("name must be 1 to %d characters after trimming", automationNameMaxRunes),
		)
	}
	if len(raw.Triggers) < 1 || len(raw.Triggers) > automationTriggerMaxCount {
		return AutomationDefinition{}, definitionIssue(
			"/triggers", fmt.Sprintf("definition needs 1 to %d triggers", automationTriggerMaxCount),
		)
	}
	if len(raw.Steps) < 1 || len(raw.Steps) > automationStepMaxCount {
		return AutomationDefinition{}, definitionIssue(
			"/steps", fmt.Sprintf("definition needs 1 to %d steps", automationStepMaxCount),
		)
	}
	triggers := make([]AutomationTrigger, 0, len(raw.Triggers))
	seenTriggers := make(map[TriggerID]bool, len(raw.Triggers))
	for _, item := range raw.Triggers {
		trigger, err := normalizeAutomationTrigger(item)
		if err != nil {
			return AutomationDefinition{}, err
		}
		if seenTriggers[trigger.ID] {
			return AutomationDefinition{}, definitionIssue("/triggers", "trigger IDs must be unique")
		}
		seenTriggers[trigger.ID] = true
		triggers = append(triggers, trigger)
	}
	steps, err := normalizeAutomationSteps(raw.Steps)
	if err != nil {
		return AutomationDefinition{}, err
	}
	return AutomationDefinition{Name: name, Enabled: raw.Enabled, Triggers: triggers, Steps: steps}, nil
}

func normalizeAutomationTrigger(raw automationTriggerJSON) (AutomationTrigger, error) {
	id, err := ParseTriggerID(string(raw.ID))
	if err != nil {
		return AutomationTrigger{}, err
	}
	switch raw.Kind {
	case TriggerKindObservation:
		observation := &ObservationTrigger{
			EntityID:     raw.EntityID,
			Dispositions: canonicalDispositions(raw.Dispositions),
		}
		for _, comparison := range raw.Comparisons {
			observation.Comparisons = append(observation.Comparisons, ObservationComparison{
				Pointer:  comparison.Pointer,
				Operator: comparison.Operator,
				Operand:  append(json.RawMessage(nil), comparison.Operand...),
			})
		}
		if err = validateObservationTrigger(*observation); err != nil {
			return AutomationTrigger{}, fmt.Errorf("trigger %q: %w", id, err)
		}
		return AutomationTrigger{ID: id, Kind: TriggerKindObservation, Observation: observation}, nil
	case TriggerKindEntityEvent:
		entityEvent := &EntityEventTrigger{EntityID: raw.EntityID, EventName: raw.EventName}
		if err = validateEntityEventTrigger(*entityEvent); err != nil {
			return AutomationTrigger{}, fmt.Errorf("trigger %q: %w", id, err)
		}
		return AutomationTrigger{ID: id, Kind: TriggerKindEntityEvent, EntityEvent: entityEvent}, nil
	default:
		return AutomationTrigger{}, fmt.Errorf("%w: trigger %q has unknown kind %q", ErrInvalidAutomation, id, raw.Kind)
	}
}

func normalizeAutomationSteps(raw []automationStepJSON) ([]AutomationStep, error) {
	steps := make([]AutomationStep, 0, len(raw))
	seen := make(map[StepID]bool, len(raw))
	for _, item := range raw {
		id, err := ParseStepID(string(item.ID))
		if err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, definitionIssue("/steps", "step IDs must be unique")
		}
		seen[id] = true
		if _, err = devices.ParseEntityID(string(item.EntityID)); err != nil {
			return nil, fmt.Errorf("step %q entity: %w", id, err)
		}
		if !subjectSlugPattern.MatchString(string(item.Operation)) {
			return nil, fmt.Errorf("%w: step %q operation is not a subject-safe slug", ErrInvalidAutomation, id)
		}
		steps = append(steps, AutomationStep{
			ID:            id,
			EntityID:      item.EntityID,
			OperationName: item.Operation,
			Parameters:    devices.CommandParameters(append(json.RawMessage(nil), item.Parameters...)),
		})
	}
	return steps, nil
}

// canonicalDispositions orders a valid disposition set so equal semantics always
// encode to equal bytes.
func canonicalDispositions(dispositions []devices.ObservationDisposition) []devices.ObservationDisposition {
	if len(dispositions) <= 1 {
		return dispositions
	}
	canonical := append([]devices.ObservationDisposition(nil), dispositions...)
	slices.Sort(canonical)
	return canonical
}

func definitionIssue(path, message string) error {
	return &AutomationDefinitionError{Issues: []AutomationDefinitionIssue{{Path: path, Message: message}}}
}

func collectDefinitionIssues(validation *jsonschema.ValidationError, issues *[]AutomationDefinitionIssue) {
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
	*issues = append(*issues, AutomationDefinitionIssue{
		Path:    pointer.String(),
		Message: "value does not satisfy the strict schema",
	})
}
