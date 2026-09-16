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
var automationDefinitionCodec = sync.OnceValues(NewDefinitionCodec)

// DefinitionIssue is one safe structural explanation at a JSON
// Pointer. It never contains definition payload values.
type DefinitionIssue struct {
	Path    string
	Message string
}

// DefinitionError reports one or more structural definition failures.
// It matches ErrInvalidAutomation so transport boundaries map it as a permanent
// input error.
type DefinitionError struct {
	Issues []DefinitionIssue
}

// Error reports the fixed class message without echoing definition payloads.
func (*DefinitionError) Error() string {
	return "automation definition does not satisfy the strict schema"
}

// Is classifies every structural definition failure as ErrInvalidAutomation.
func (*DefinitionError) Is(target error) bool { return target == ErrInvalidAutomation }

// DefinitionCodec owns the compiled strict definition schema and its
// reusable Condition subtree, so persisted Condition snapshots are validated by
// exactly the same recursive family rules as a definition document.
type DefinitionCodec struct {
	schema    *jsonschema.Schema
	condition *jsonschema.Schema
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
	condition, err := compiler.Compile(schemaID + "#/$defs/condition")
	if err != nil {
		return nil, fmt.Errorf("automation condition schema compile: %w", err)
	}
	return &DefinitionCodec{schema: compiled, condition: condition}, nil
}

// AutomationDefinitionSchema returns owned copies of the embedded strict shape.
func (*DefinitionCodec) AutomationDefinitionSchema() json.RawMessage {
	return bytes.Clone(automationDefinitionSchema)
}

// ValidateCondition validates one raw flattened Condition node against the same
// strict recursive schema a definition document uses. It rejects an omitted or
// JSON-null required family field, a contradictory family payload, and any
// unknown member, so a persisted decision snapshot can never be more permissive
// than the definition it explains. A selected empty pointer stays valid because
// the schema requires the member, not a nonempty value.
func (codec *DefinitionCodec) ValidateCondition(raw json.RawMessage) error {
	document, err := decodeJSONValue(raw)
	if err != nil {
		return definitionIssue("", "condition must be exactly one JSON value")
	}
	if validationErr := codec.condition.Validate(document); validationErr != nil {
		return definitionIssue("", "condition does not satisfy the strict schema")
	}
	return nil
}

// DecodeDefinition validates and normalizes JSON against the strict
// schema and structural rules. Current device references are checked separately
// by [ValidateDefinition].
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
	return normalizeAutomationDefinition(automationDefinitionFromJSON(value))
}

// EncodeDefinition renders one definition in the strict persisted
// representation. It does not validate; call [NormalizeDefinition]
// before persisting caller-supplied values.
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
// persisted shape as a definition's Triggers, so a retained Skip stays
// explainable after the definition is edited or deleted. It does not validate.
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
	var encoded []automationTriggerJSON
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	triggers := make([]Trigger, 0, len(encoded))
	for _, item := range encoded {
		trigger, err := normalizeAutomationTriggerValue(automationTriggerFromJSON(item))
		if err != nil {
			return nil, err
		}
		triggers = append(triggers, trigger)
	}
	return triggers, nil
}

// NormalizeDefinition returns a structurally validated, canonical copy
// without consulting devices. Its encoded form must fit the 64 KiB limit.
func NormalizeDefinition(definition Definition) (Definition, error) {
	normalized, _, err := normalizeAndEncodeAutomationDefinition(definition)
	return normalized, err
}

// normalizeAndEncodeAutomationDefinition returns the normalized definition and
// persisted bytes in one pass.
func normalizeAndEncodeAutomationDefinition(
	definition Definition,
) (Definition, json.RawMessage, error) {
	normalized, err := normalizeAutomationDefinition(definition)
	if err != nil {
		return Definition{}, nil, err
	}
	raw, err := EncodeDefinition(normalized)
	if err != nil {
		return Definition{}, nil, err
	}
	if len(raw) > automationDefinitionMaxBytes {
		return Definition{}, nil, definitionIssue(
			"", fmt.Sprintf("definition exceeds %d bytes", automationDefinitionMaxBytes),
		)
	}
	return normalized, raw, nil
}

// ValidateDefinition normalizes a definition and validates its Entity,
// event, Operation, and parameter references through devices. It returns normalized
// Step parameters without requiring enablement, availability, or owner health.
func ValidateDefinition(
	ctx context.Context,
	automationDevices AutomationDevices,
	definition Definition,
) (Definition, error) {
	normalized, err := NormalizeDefinition(definition)
	if err != nil {
		return Definition{}, err
	}
	return validateAutomationReferences(ctx, automationDevices, normalized)
}

func validateAutomationReferences(
	ctx context.Context,
	automationDevices AutomationDevices,
	definition Definition,
) (Definition, error) {
	if automationDevices == nil {
		return Definition{}, fmt.Errorf("%w: device validation is not configured", ErrInvalidAutomation)
	}
	for _, trigger := range definition.Triggers {
		if err := validateAutomationTriggerReference(ctx, automationDevices, trigger); err != nil {
			return Definition{}, err
		}
	}
	if err := validateAutomationConditionReferences(ctx, automationDevices, definition.Conditions); err != nil {
		return Definition{}, err
	}
	steps := make([]Step, len(definition.Steps))
	copy(steps, definition.Steps)
	for index, step := range definition.Steps {
		parameters, err := automationDevices.ValidateCommand(ctx, devices.CommandInput{
			EntityID:      step.EntityID,
			OperationName: step.OperationName,
			Parameters:    step.Parameters,
		})
		if err != nil {
			return Definition{}, fmt.Errorf("%w: step %q: %w", ErrInvalidAutomation, step.ID, err)
		}
		steps[index].Parameters = parameters
	}
	definition.Steps = steps
	return definition, nil
}

func validateAutomationTriggerReference(
	ctx context.Context,
	automationDevices AutomationDevices,
	trigger Trigger,
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

// validateAutomationConditionReferences checks that every Entity a Condition tree
// explicitly references currently exists and is stateful, reusing devices'
// condition reference rule. Save-time validation deliberately does not require a
// present State, a compatible selected value, availability, enablement, or a
// healthy owner: those are evaluation results, not definition errors.
func validateAutomationConditionReferences(
	ctx context.Context,
	automationDevices AutomationDevices,
	conditions *Condition,
) error {
	if conditions == nil {
		return nil
	}
	entityIDs, err := RequiredConditionEntityIDs(*conditions)
	if err != nil {
		return err
	}
	for _, entityID := range entityIDs {
		if validationErr := automationDevices.ValidateConditionEntity(ctx, entityID); validationErr != nil {
			return fmt.Errorf("%w: conditions: %w", ErrInvalidAutomation, validationErr)
		}
	}
	return nil
}

type automationDefinitionJSON struct {
	Name       string                   `json:"name"`
	Enabled    bool                     `json:"enabled"`
	Triggers   []automationTriggerJSON  `json:"triggers"`
	Conditions *automationConditionJSON `json:"conditions,omitempty"`
	Steps      []automationStepJSON     `json:"steps"`
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

func encodeAutomationTrigger(trigger Trigger) automationTriggerJSON {
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

// normalizeAutomationDefinition validates typed fields and returns an owned copy.
// DecodeDefinition checks JSON field presence and unknown fields;
// normalizeAndEncodeAutomationDefinition checks the encoded size.
func normalizeAutomationDefinition(definition Definition) (Definition, error) {
	trimmedName := strings.TrimSpace(definition.Name)
	trimmedNameRunes := utf8.RuneCountInString(trimmedName)
	rawNameRunes := utf8.RuneCountInString(definition.Name)
	if rawNameRunes > automationNameMaxRunes || trimmedNameRunes < 1 || trimmedNameRunes > automationNameMaxRunes {
		return Definition{}, definitionIssue(
			"/name", fmt.Sprintf("name must be 1 to %d characters after trimming", automationNameMaxRunes),
		)
	}
	if len(definition.Triggers) < 1 || len(definition.Triggers) > automationTriggerMaxCount {
		return Definition{}, definitionIssue(
			"/triggers", fmt.Sprintf("definition needs 1 to %d triggers", automationTriggerMaxCount),
		)
	}
	if len(definition.Steps) < 1 || len(definition.Steps) > automationStepMaxCount {
		return Definition{}, definitionIssue(
			"/steps", fmt.Sprintf("definition needs 1 to %d steps", automationStepMaxCount),
		)
	}
	triggers := make([]Trigger, 0, len(definition.Triggers))
	seenTriggers := make(map[TriggerID]bool, len(definition.Triggers))
	for _, item := range definition.Triggers {
		trigger, err := normalizeAutomationTriggerValue(item)
		if err != nil {
			return Definition{}, err
		}
		if seenTriggers[trigger.ID] {
			return Definition{}, definitionIssue("/triggers", "trigger IDs must be unique")
		}
		seenTriggers[trigger.ID] = true
		triggers = append(triggers, trigger)
	}
	steps, err := normalizeAutomationStepValues(definition.Steps)
	if err != nil {
		return Definition{}, err
	}
	conditions := definition.Conditions
	if conditions != nil {
		normalized, conditionErr := NormalizeConditions(*conditions)
		if conditionErr != nil {
			return Definition{}, conditionErr
		}
		conditions = &normalized
	}
	return Definition{
		Name:       trimmedName,
		Enabled:    definition.Enabled,
		Triggers:   triggers,
		Conditions: conditions,
		Steps:      steps,
	}, nil
}

// normalizeAutomationTriggerValue returns a canonical copy, rejecting contradictory
// family payloads before encoding could silently discard one.
func normalizeAutomationTriggerValue(trigger Trigger) (Trigger, error) {
	if err := ValidateTrigger(trigger); err != nil {
		return Trigger{}, err
	}
	normalized := Trigger{ID: trigger.ID, Kind: trigger.Kind}
	switch trigger.Kind {
	case TriggerKindObservation:
		observation := trigger.Observation
		normalized.Observation = &ObservationTrigger{
			EntityID:     observation.EntityID,
			Dispositions: canonicalDispositions(observation.Dispositions),
			Comparisons:  cloneObservationComparisons(observation.Comparisons),
		}
	case TriggerKindEntityEvent:
		entityEvent := trigger.EntityEvent
		normalized.EntityEvent = &EntityEventTrigger{
			EntityID:  entityEvent.EntityID,
			EventName: entityEvent.EventName,
		}
	}
	return normalized, nil
}

func normalizeAutomationStepValues(steps []Step) ([]Step, error) {
	normalized := make([]Step, 0, len(steps))
	seen := make(map[StepID]bool, len(steps))
	for _, item := range steps {
		step, err := normalizeAutomationStepValue(item)
		if err != nil {
			return nil, err
		}
		if seen[step.ID] {
			return nil, definitionIssue("/steps", "step IDs must be unique")
		}
		seen[step.ID] = true
		normalized = append(normalized, step)
	}
	return normalized, nil
}

func normalizeAutomationStepValue(step Step) (Step, error) {
	id, err := ParseStepID(string(step.ID))
	if err != nil {
		return Step{}, err
	}
	entityID, err := devices.ParseEntityID(string(step.EntityID))
	if err != nil {
		return Step{}, fmt.Errorf("%w: step %q entity: %w", ErrInvalidAutomation, id, err)
	}
	if !subjectSlugPattern.MatchString(string(step.OperationName)) {
		return Step{}, fmt.Errorf(
			"%w: step %q operation is not a subject-safe slug", ErrInvalidAutomation, id,
		)
	}
	if err = validateAutomationStepParameters(step.Parameters); err != nil {
		return Step{}, fmt.Errorf("%w: step %q: %w", ErrInvalidAutomation, id, err)
	}
	return Step{
		ID:            id,
		EntityID:      entityID,
		OperationName: step.OperationName,
		Parameters:    devices.CommandParameters(append(json.RawMessage(nil), step.Parameters...)),
	}, nil
}

// validateAutomationStepParameters requires exactly one JSON object, matching
// the definition schema and devices Command contract.
func validateAutomationStepParameters(parameters devices.CommandParameters) error {
	value, err := decodeJSONValue(json.RawMessage(parameters))
	if err != nil {
		return errors.New("parameters must contain exactly one JSON value")
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("parameters must be a JSON object")
	}
	return nil
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
	case TriggerKindObservation:
		observation := &ObservationTrigger{EntityID: item.EntityID, Dispositions: item.Dispositions}
		for _, comparison := range item.Comparisons {
			observation.Comparisons = append(observation.Comparisons, ObservationComparison(comparison))
		}
		trigger.Observation = observation
	case TriggerKindEntityEvent:
		trigger.EntityEvent = &EntityEventTrigger{EntityID: item.EntityID, EventName: item.EventName}
	}
	return trigger
}

// canonicalDispositions copies a valid disposition set into canonical order.
func canonicalDispositions(dispositions []devices.ObservationDisposition) []devices.ObservationDisposition {
	canonical := append([]devices.ObservationDisposition(nil), dispositions...)
	if len(canonical) > 1 {
		slices.Sort(canonical)
	}
	return canonical
}

// cloneObservationComparisons copies comparisons and operand bytes without
// aliasing caller memory. Empty sets canonicalize to nil.
func cloneObservationComparisons(comparisons []ObservationComparison) []ObservationComparison {
	if len(comparisons) == 0 {
		return nil
	}
	cloned := make([]ObservationComparison, 0, len(comparisons))
	for _, comparison := range comparisons {
		cloned = append(cloned, ObservationComparison{
			Pointer:  comparison.Pointer,
			Operator: comparison.Operator,
			Operand:  append(json.RawMessage(nil), comparison.Operand...),
		})
	}
	return cloned
}

func definitionIssue(path, message string) error {
	return &DefinitionError{Issues: []DefinitionIssue{{Path: path, Message: message}}}
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
