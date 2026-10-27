package automations

import (
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
	// normalization. The v3 bound accommodates lossless binding-wrapper migration
	// of definitions retained under the previous 66,080-byte limit.
	automationDefinitionMaxBytes = 131072
	// automationNameMaxRunes bounds the trimmed definition name.
	automationNameMaxRunes = 200
	// automationTriggerMaxCount bounds the Trigger list.
	automationTriggerMaxCount = 32
	// automationStepMaxCount bounds direct sequences and combined side-effect leaves.
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
// without consulting devices. Its encoded form must fit the definition byte limit.
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
	steps, err := walk.sequence(definition.Steps, 1, "/steps")
	if err != nil {
		return Definition{}, err
	}
	variables, err := normalizeVariables(definition.Variables, true)
	if err != nil {
		if binding, ok := errors.AsType[*bindingError](err); ok {
			return Definition{}, definitionIssue("/variables/"+binding.path, dataFailureCode(err))
		}
		return Definition{}, err
	}
	if err = validateStaticOutputReferences(steps, variables); err != nil {
		return Definition{}, err
	}
	return Definition{
		Variables:  variables,
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
