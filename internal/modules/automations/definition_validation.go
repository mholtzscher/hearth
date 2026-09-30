package automations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

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

// DefinitionIssue is one safe structural explanation at a JSON Pointer.
type DefinitionIssue struct {
	Path    string
	Message string
}

// DefinitionError reports one or more structural definition failures matching [ErrInvalidAutomation].
type DefinitionError struct {
	Issues []DefinitionIssue
}

// Error reports the fixed class message without echoing definition payloads.
func (*DefinitionError) Error() string {
	return "automation definition does not satisfy the strict schema"
}

// Is classifies every structural definition failure as ErrInvalidAutomation.
func (*DefinitionError) Is(target error) bool { return target == ErrInvalidAutomation }

// NormalizeDefinition returns a structurally validated, canonical copy
// without consulting devices. Its encoded form must fit the 64 KiB limit.
func NormalizeDefinition(definition Definition) (Definition, error) {
	normalized, _, err := NormalizeAndEncodeDefinition(definition)
	return normalized, err
}

// NormalizeAndEncodeDefinition returns an owned, structurally valid canonical
// definition and its size-checked encoding without consulting Devices.
func NormalizeAndEncodeDefinition(
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

// ValidateDefinition normalizes a definition and validates its Entity, event,
// Operation, and parameter references through devices.
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
		pointers := make([]string, len(trigger.Observation.Comparisons))
		for index, comparison := range trigger.Observation.Comparisons {
			pointers[index] = comparison.Pointer
		}
		if err := automationDevices.ValidateObservationTrigger(
			ctx, trigger.Observation.EntityID, pointers,
		); err != nil {
			return fmt.Errorf("%w: trigger %q: %w", ErrInvalidAutomation, trigger.ID, err)
		}
	case TriggerKindEntityEvent:
		err := automationDevices.ValidateEntityEventTrigger(
			ctx, trigger.EntityEvent.EntityID, trigger.EntityEvent.EventName,
		)
		if err != nil {
			return fmt.Errorf("%w: trigger %q: %w", ErrInvalidAutomation, trigger.ID, err)
		}
	case TriggerKindHeldState:
		pointers := make([]string, len(trigger.HeldState.Comparisons))
		for index, comparison := range trigger.HeldState.Comparisons {
			pointers[index] = comparison.Pointer
		}
		if err := automationDevices.ValidateObservationTrigger(ctx, trigger.HeldState.EntityID, pointers); err != nil {
			return fmt.Errorf("%w: trigger %q: %w", ErrInvalidAutomation, trigger.ID, err)
		}
	default:
		return fmt.Errorf("%w: trigger %q has unknown kind %q", ErrInvalidAutomation, trigger.ID, trigger.Kind)
	}
	return nil
}

// validateAutomationConditionReferences checks that every Entity a Condition
// tree explicitly references currently exists and is stateful. Save-time
// validation deliberately does not require a present State.
func validateAutomationConditionReferences(
	ctx context.Context,
	automationDevices AutomationDevices,
	conditions *Condition,
) error {
	if conditions == nil {
		return nil
	}
	// This tree was normalized before reference validation.
	entityIDs := requiredValidatedConditionEntityIDs(*conditions)
	for _, entityID := range entityIDs {
		if validationErr := automationDevices.ValidateConditionEntity(ctx, entityID); validationErr != nil {
			return fmt.Errorf("%w: conditions: %w", ErrInvalidAutomation, validationErr)
		}
	}
	return nil
}

// normalizeAutomationDefinition validates typed fields and returns an owned copy.
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
			EntityID:            observation.EntityID,
			Dispositions:        canonicalDispositions(observation.Dispositions),
			PreviousComparisons: cloneObservationComparisons(observation.PreviousComparisons),
			Comparisons:         cloneObservationComparisons(observation.Comparisons),
		}
	case TriggerKindEntityEvent:
		entityEvent := trigger.EntityEvent
		normalized.EntityEvent = &EntityEventTrigger{
			EntityID:  entityEvent.EntityID,
			EventName: entityEvent.EventName,
		}
	case TriggerKindHeldState:
		heldState := trigger.HeldState
		normalized.HeldState = &HeldStateTrigger{
			EntityID: heldState.EntityID, Comparisons: cloneObservationComparisons(heldState.Comparisons),
			ForSeconds: heldState.ForSeconds,
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

// validateAutomationStepParameters requires exactly one JSON object.
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

// canonicalDispositions copies a valid disposition set into canonical order.
func canonicalDispositions(dispositions []devices.ObservationDisposition) []devices.ObservationDisposition {
	canonical := append([]devices.ObservationDisposition(nil), dispositions...)
	if len(canonical) > 1 {
		slices.Sort(canonical)
	}
	return canonical
}

// cloneObservationComparisons copies comparisons and operand bytes without aliasing caller memory.
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
