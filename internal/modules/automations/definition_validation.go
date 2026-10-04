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
	// automationStepMaxCount bounds direct sequences and total Command leaves.
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
	normalized, err := prepareDefinition(definition)
	if err != nil {
		return Definition{}, nil, err
	}
	raw, err := encodePreparedDefinition(normalized)
	if err != nil {
		return Definition{}, nil, err
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
	if err := validateSequenceReferences(ctx, automationDevices, definition.Steps); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

// validateSequenceReferences visits all defined arms at save time. Admission
// collectors deliberately continue to read only Definition.Conditions.
func validateSequenceReferences(ctx context.Context, automationDevices AutomationDevices, steps []Step) error {
	for index := range steps {
		if err := validateStepReferences(ctx, automationDevices, &steps[index]); err != nil {
			return err
		}
	}
	return nil
}

func validateStepReferences(ctx context.Context, automationDevices AutomationDevices, step *Step) error {
	switch body := step.Body.(type) {
	case CommandStep:
		parameters, err := automationDevices.ValidateCommand(
			ctx,
			devices.CommandInput{
				EntityID:      body.EntityID,
				OperationName: body.OperationName,
				Parameters:    body.Parameters,
			},
		)
		if err != nil {
			return fmt.Errorf("%w: step %q: %w", ErrInvalidAutomation, step.ID, err)
		}
		body.Parameters = append(devices.CommandParameters(nil), parameters...)
		step.Body = body
	case IfStep:
		if err := validateAutomationConditionReferences(ctx, automationDevices, &body.Conditions); err != nil {
			return err
		}
		if err := validateSequenceReferences(ctx, automationDevices, body.Then); err != nil {
			return err
		}
		return validateSequenceReferences(ctx, automationDevices, body.Else)
	case ChooseStep:
		return validateChooseReferences(ctx, automationDevices, &body)
	default:
		return invalid("step %q: unsupported body", step.ID)
	}
	return nil
}

func validateChooseReferences(ctx context.Context, automationDevices AutomationDevices, choose *ChooseStep) error {
	for _, branch := range choose.Branches {
		if err := validateAutomationConditionReferences(ctx, automationDevices, &branch.Conditions); err != nil {
			return err
		}
		if err := validateSequenceReferences(ctx, automationDevices, branch.Steps); err != nil {
			return err
		}
	}
	return validateSequenceReferences(ctx, automationDevices, choose.Default)
}

func validateAutomationTriggerReference(
	ctx context.Context,
	automationDevices AutomationDevices,
	trigger Trigger,
) error {
	switch body := trigger.Body.(type) {
	case CronTrigger:
		return nil
	case ObservationTrigger:
		pointers := make([]string, len(body.Comparisons))
		for index, comparison := range body.Comparisons {
			pointers[index] = comparison.Pointer
		}
		if err := automationDevices.ValidateObservationTrigger(
			ctx, body.EntityID, pointers,
		); err != nil {
			return fmt.Errorf("%w: trigger %q: %w", ErrInvalidAutomation, trigger.ID, err)
		}
	case EntityEventTrigger:
		err := automationDevices.ValidateEntityEventTrigger(
			ctx, body.EntityID, body.EventName,
		)
		if err != nil {
			return fmt.Errorf("%w: trigger %q: %w", ErrInvalidAutomation, trigger.ID, err)
		}
	case HeldStateTrigger:
		pointers := make([]string, len(body.Comparisons))
		for index, comparison := range body.Comparisons {
			pointers[index] = comparison.Pointer
		}
		if err := automationDevices.ValidateObservationTrigger(ctx, body.EntityID, pointers); err != nil {
			return fmt.Errorf("%w: trigger %q: %w", ErrInvalidAutomation, trigger.ID, err)
		}
	default:
		return fmt.Errorf("%w: trigger %q has unknown kind %q", ErrInvalidAutomation, trigger.ID, trigger.Kind())
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

// prepareDefinition validates typed structure, owns canonical copies, and compiles
// cron clock fields for later matching. Callers own raw or encoded size checks;
// current Devices reference validation is separate.
func prepareDefinition(definition Definition) (Definition, error) {
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
	walk := stepTreePreparation{ids: make(map[StepID]bool), triggerIDs: seenTriggers}
	conditions := definition.Conditions
	if conditions != nil {
		normalized, conditionErr := walk.condition(*conditions, false)
		if conditionErr != nil {
			return Definition{}, conditionErr
		}
		conditions = &normalized
	}
	steps, err := walk.sequence(definition.Steps, 1)
	if err != nil {
		return Definition{}, err
	}
	if walk.commands == 0 {
		return Definition{}, definitionIssue("/steps", "definition needs at least one Command Step")
	}
	return Definition{
		Name:       trimmedName,
		Enabled:    definition.Enabled,
		Triggers:   triggers,
		Conditions: conditions,
		Steps:      steps,
	}, nil
}

// normalizeAutomationTriggerValue validates a concrete value body and owns its
// canonical slices, operand bytes, and compiled cron schedule.
func normalizeAutomationTriggerValue(trigger Trigger) (Trigger, error) {
	if err := validateTriggerFamily(trigger); err != nil {
		return Trigger{}, err
	}
	normalized := Trigger{ID: trigger.ID}
	switch body := trigger.Body.(type) {
	case CronTrigger:
		expression, schedule, err := parseCronExpression(body.Expression)
		if err != nil {
			return Trigger{}, fmt.Errorf("trigger %q expression: %w", trigger.ID, err)
		}
		normalized.Body = CronTrigger{
			Expression: expression,
			schedule: &cronSchedule{
				expression: expression, minute: schedule.Minute, hour: schedule.Hour, weekday: schedule.Dow,
			},
		}
	case ObservationTrigger:
		observation := body
		normalized.Body = ObservationTrigger{
			EntityID:            observation.EntityID,
			Dispositions:        canonicalDispositions(observation.Dispositions),
			PreviousComparisons: cloneObservationComparisons(observation.PreviousComparisons),
			Comparisons:         cloneObservationComparisons(observation.Comparisons),
		}
	case EntityEventTrigger:
		entityEvent := body
		normalized.Body = EntityEventTrigger{
			EntityID:  entityEvent.EntityID,
			EventName: entityEvent.EventName,
		}
	case HeldStateTrigger:
		heldState := body
		normalized.Body = HeldStateTrigger{
			EntityID: heldState.EntityID, Comparisons: cloneObservationComparisons(heldState.Comparisons),
			ForSeconds: heldState.ForSeconds,
		}
	default:
		return Trigger{}, invalid("unsupported Trigger body")
	}
	return normalized, nil
}

func normalizeCommandStepValue(stepID StepID, command CommandStep) (Step, error) {
	id, err := ParseStepID(string(stepID))
	if err != nil {
		return Step{}, err
	}
	entityID, err := devices.ParseEntityID(string(command.EntityID))
	if err != nil {
		return Step{}, fmt.Errorf("%w: step %q entity: %w", ErrInvalidAutomation, id, err)
	}
	if !subjectSlugPattern.MatchString(string(command.OperationName)) {
		return Step{}, fmt.Errorf(
			"%w: step %q operation is not a subject-safe slug", ErrInvalidAutomation, id,
		)
	}
	if err = validateAutomationStepParameters(command.Parameters); err != nil {
		return Step{}, fmt.Errorf("%w: step %q: %w", ErrInvalidAutomation, id, err)
	}
	return Step{
		ID: id,
		Body: CommandStep{
			EntityID:      entityID,
			OperationName: command.OperationName,
			Parameters:    devices.CommandParameters(append(json.RawMessage(nil), command.Parameters...)),
		},
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
