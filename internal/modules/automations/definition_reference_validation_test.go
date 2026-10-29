package automations_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestDefinitionReferenceFailureClassification(t *testing.T) {
	t.Parallel()
	for _, reference := range []string{
		"observation", "held_state", "entity_event", "literal_command", "expression_command", "condition", "read_state",
	} {
		for _, failure := range []struct {
			name    string
			cause   error
			invalid bool
		}{
			{"missing", devices.ErrEntityNotFound, true},
			{"unsupported", invalidReferenceClass(reference), true},
			{"read failed", errors.New("private dependency failure"), false},
			{"canceled", context.Canceled, false},
			{"deadline", context.DeadlineExceeded, false},
		} {
			t.Run(reference+"/"+failure.name, func(t *testing.T) {
				t.Parallel()
				checkDefinitionReferenceFailure(t, reference, failure.cause, failure.invalid)
			})
		}
	}
}

func checkDefinitionReferenceFailure(t *testing.T, reference string, cause error, invalid bool) {
	t.Helper()
	for _, operation := range []string{"create", "replace"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			definition, stub := failingReferenceDefinition(t, reference, fmt.Errorf("dependency: %w", cause))
			repository := &referenceValidationWrites{}
			service := automations.NewService(repository, stub, automations.Dependencies{})
			var err error
			if operation == "create" {
				_, err = service.CreateAutomation(t.Context(), definition)
			} else {
				_, err = service.ReplaceAutomation(
					t.Context(),
					"aut_01980000-0000-7000-8000-000000000001",
					1,
					definition,
				)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("lost dependency error: %v", err)
			}
			if errors.Is(err, automations.ErrInvalidAutomation) != invalid {
				t.Fatalf(
					"invalid classification = %t, want %t: %v",
					errors.Is(err, automations.ErrInvalidAutomation),
					invalid,
					err,
				)
			}
			if repository.writes != 0 {
				t.Fatalf("failed validation reached persistence: %d writes", repository.writes)
			}
		})
	}
}

func invalidReferenceClass(reference string) error {
	switch reference {
	case "literal_command", "expression_command":
		return devices.ErrInvalidCommand
	case "condition", "read_state":
		return devices.ErrAutomationConditionEntity
	default:
		return devices.ErrAutomationTriggerSource
	}
}

func failingReferenceDefinition(
	t *testing.T,
	reference string,
	cause error,
) (automations.Definition, *stubAutomationDevices) {
	t.Helper()
	definition := validDomainDefinition(t)
	stub := &stubAutomationDevices{}
	entityID := newEntityID(t)
	switch reference {
	case "observation":
		stub.observationError = cause
	case "held_state":
		definition.Triggers[0].Body = automations.HeldStateTrigger{
			EntityID:   entityID,
			ForSeconds: 1,
			Comparisons: []automations.ObservationComparison{
				{Operator: automations.ComparisonEqual, Operand: []byte("true")},
			},
		}
		stub.observationError = cause
	case "entity_event":
		definition.Triggers[0].Body = automations.EntityEventTrigger{EntityID: entityID, EventName: "pressed"}
		stub.entityEventError = cause
	case "literal_command":
		stub.commandError = cause
	case "expression_command":
		body := definition.Steps[0].Body.(automations.CommandStep)
		body.Parameters = automations.ValueBinding{Body: automations.ExpressionValue{Source: `{"value": true}`}}
		definition.Steps[0].Body = body
		stub.commandError = cause
	case "condition":
		definition.Conditions = &automations.Condition{ID: "ready", Body: automations.EntityStateCondition{
			EntityID: entityID, Operator: automations.ComparisonEqual, Operand: []byte("true"),
		}}
		stub.conditionError = cause
	case "read_state":
		definition.Steps[0].Body = automations.ReadStateStep{EntityID: entityID}
		stub.conditionError = cause
	default:
		t.Fatalf("unknown reference fixture %q", reference)
	}
	return definition, stub
}

type referenceValidationWrites struct {
	automations.Repository

	writes int
}

func (repository *referenceValidationWrites) CreateAutomation(
	context.Context,
	automations.Definition,
) (automations.Record, error) {
	repository.writes++
	return automations.Record{}, errors.New("unexpected definition write")
}

func (repository *referenceValidationWrites) ReplaceAutomation(
	context.Context,
	automations.AutomationID,
	int64,
	automations.Definition,
) (automations.Record, error) {
	repository.writes++
	return automations.Record{}, errors.New("unexpected definition write")
}
