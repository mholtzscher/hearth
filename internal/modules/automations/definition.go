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
	// The raw document passed the size check above; normalize its typed shape
	// without serializing the whole definition again.
	return normalizeAutomationDefinition(automationDefinitionFromJSON(value))
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
// have one representation. It also proves the canonical persisted encoding fits
// the 64 KiB definition bound.
func NormalizeAutomationDefinition(definition AutomationDefinition) (AutomationDefinition, error) {
	normalized, _, err := normalizeAndEncodeAutomationDefinition(definition)
	return normalized, err
}

// normalizeAndEncodeAutomationDefinition is the single write-side canonical
// path: it normalizes the typed definition once and returns the canonical
// persisted bytes, so persistence never normalizes or encodes the same value
// twice.
func normalizeAndEncodeAutomationDefinition(
	definition AutomationDefinition,
) (AutomationDefinition, json.RawMessage, error) {
	normalized, err := normalizeAutomationDefinition(definition)
	if err != nil {
		return AutomationDefinition{}, nil, err
	}
	raw, err := EncodeAutomationDefinition(normalized)
	if err != nil {
		return AutomationDefinition{}, nil, err
	}
	if len(raw) > automationDefinitionMaxBytes {
		return AutomationDefinition{}, nil, definitionIssue(
			"", fmt.Sprintf("definition exceeds %d bytes", automationDefinitionMaxBytes),
		)
	}
	return normalized, raw, nil
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

// normalizeAutomationDefinition structurally validates one already-typed
// definition and returns an owned copy without a whole-definition JSON round
// trip. Raw operands and parameters are still checked as JSON. Required fields
// and unknown fields are checked by [DecodeAutomationDefinition] at the JSON
// boundary; [normalizeAndEncodeAutomationDefinition] checks the encoded size.
func normalizeAutomationDefinition(definition AutomationDefinition) (AutomationDefinition, error) {
	trimmedName := strings.TrimSpace(definition.Name)
	trimmedNameRunes := utf8.RuneCountInString(trimmedName)
	rawNameRunes := utf8.RuneCountInString(definition.Name)
	if rawNameRunes > automationNameMaxRunes || trimmedNameRunes < 1 || trimmedNameRunes > automationNameMaxRunes {
		return AutomationDefinition{}, definitionIssue(
			"/name", fmt.Sprintf("name must be 1 to %d characters after trimming", automationNameMaxRunes),
		)
	}
	if len(definition.Triggers) < 1 || len(definition.Triggers) > automationTriggerMaxCount {
		return AutomationDefinition{}, definitionIssue(
			"/triggers", fmt.Sprintf("definition needs 1 to %d triggers", automationTriggerMaxCount),
		)
	}
	if len(definition.Steps) < 1 || len(definition.Steps) > automationStepMaxCount {
		return AutomationDefinition{}, definitionIssue(
			"/steps", fmt.Sprintf("definition needs 1 to %d steps", automationStepMaxCount),
		)
	}
	triggers := make([]AutomationTrigger, 0, len(definition.Triggers))
	seenTriggers := make(map[TriggerID]bool, len(definition.Triggers))
	for _, item := range definition.Triggers {
		trigger, err := normalizeAutomationTriggerValue(item)
		if err != nil {
			return AutomationDefinition{}, err
		}
		if seenTriggers[trigger.ID] {
			return AutomationDefinition{}, definitionIssue("/triggers", "trigger IDs must be unique")
		}
		seenTriggers[trigger.ID] = true
		triggers = append(triggers, trigger)
	}
	steps, err := normalizeAutomationStepValues(definition.Steps)
	if err != nil {
		return AutomationDefinition{}, err
	}
	return AutomationDefinition{
		Name:     trimmedName,
		Enabled:  definition.Enabled,
		Triggers: triggers,
		Steps:    steps,
	}, nil
}

// normalizeAutomationTriggerValue rejects a contradictory or malformed typed
// Trigger before anything can encode it, so a family payload can never be
// silently discarded, then returns an owned canonical copy.
func normalizeAutomationTriggerValue(trigger AutomationTrigger) (AutomationTrigger, error) {
	if err := ValidateAutomationTrigger(trigger); err != nil {
		return AutomationTrigger{}, err
	}
	normalized := AutomationTrigger{ID: trigger.ID, Kind: trigger.Kind}
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

func normalizeAutomationStepValues(steps []AutomationStep) ([]AutomationStep, error) {
	normalized := make([]AutomationStep, 0, len(steps))
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

func normalizeAutomationStepValue(step AutomationStep) (AutomationStep, error) {
	id, err := ParseStepID(string(step.ID))
	if err != nil {
		return AutomationStep{}, err
	}
	entityID, err := devices.ParseEntityID(string(step.EntityID))
	if err != nil {
		return AutomationStep{}, fmt.Errorf("%w: step %q entity: %w", ErrInvalidAutomation, id, err)
	}
	if !subjectSlugPattern.MatchString(string(step.OperationName)) {
		return AutomationStep{}, fmt.Errorf(
			"%w: step %q operation is not a subject-safe slug", ErrInvalidAutomation, id,
		)
	}
	if err = validateAutomationStepParameters(step.Parameters); err != nil {
		return AutomationStep{}, fmt.Errorf("%w: step %q: %w", ErrInvalidAutomation, id, err)
	}
	return AutomationStep{
		ID:            id,
		EntityID:      entityID,
		OperationName: step.OperationName,
		Parameters:    devices.CommandParameters(append(json.RawMessage(nil), step.Parameters...)),
	}, nil
}

// validateAutomationStepParameters rejects parameters that are not exactly one
// JSON object, mirroring the strict schema's `"parameters": {"type": "object"}`
// and the devices Command contract at the typed boundary.
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

// automationDefinitionFromJSON maps one strictly schema-validated document into
// the typed domain shape. It never invents a family payload, so a document can
// only produce the pointer its discriminator names.
func automationDefinitionFromJSON(value automationDefinitionJSON) AutomationDefinition {
	definition := AutomationDefinition{
		Name:     value.Name,
		Enabled:  value.Enabled,
		Triggers: make([]AutomationTrigger, 0, len(value.Triggers)),
		Steps:    make([]AutomationStep, 0, len(value.Steps)),
	}
	for _, item := range value.Triggers {
		definition.Triggers = append(definition.Triggers, automationTriggerFromJSON(item))
	}
	for _, item := range value.Steps {
		definition.Steps = append(definition.Steps, AutomationStep{
			ID:            item.ID,
			EntityID:      item.EntityID,
			OperationName: item.Operation,
			Parameters:    devices.CommandParameters(item.Parameters),
		})
	}
	return definition
}

func automationTriggerFromJSON(item automationTriggerJSON) AutomationTrigger {
	trigger := AutomationTrigger{ID: item.ID, Kind: item.Kind}
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

// canonicalDispositions returns an owned copy of one valid disposition set in
// canonical order, so equal semantics always encode to equal bytes without
// aliasing the caller's slice.
func canonicalDispositions(dispositions []devices.ObservationDisposition) []devices.ObservationDisposition {
	canonical := append([]devices.ObservationDisposition(nil), dispositions...)
	if len(canonical) > 1 {
		slices.Sort(canonical)
	}
	return canonical
}

// cloneObservationComparisons returns owned comparisons, including owned
// operand bytes, so a normalized definition never aliases caller memory. An
// empty set canonicalizes to nil so equal semantics always encode equally.
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
