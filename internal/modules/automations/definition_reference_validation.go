package automations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// ValidateDefinition normalizes a definition and validates its Entity, event,
// Operation, and parameter references through devices. Notification and Function
// Steps require their dependencies at the Service create/replace boundary.
// Missing or unsupported Devices references match ErrInvalidAutomation; other
// dependency failures retain their identities without acquiring that class.
func ValidateDefinition(
	ctx context.Context,
	automationDevices AutomationDevices,
	definition Definition,
) (Definition, error) {
	return validateDefinition(ctx, automationDevices, nil, nil, definition)
}

func validateDefinition(
	ctx context.Context,
	automationDevices AutomationDevices,
	automationNotifications AutomationNotifications,
	automationFunctions AutomationFunctions,
	definition Definition,
) (Definition, error) {
	normalized, err := NormalizeDefinition(definition)
	if err != nil {
		return Definition{}, err
	}
	return validateAutomationReferences(
		ctx,
		automationDevices,
		automationNotifications,
		automationFunctions,
		normalized,
	)
}

func validateAutomationReferences(
	ctx context.Context,
	automationDevices AutomationDevices,
	automationNotifications AutomationNotifications,
	automationFunctions AutomationFunctions,
	definition Definition,
) (Definition, error) {
	if automationDevices == nil {
		return Definition{}, errors.New("automation device validation is not configured")
	}
	for _, trigger := range definition.Triggers {
		if err := validateAutomationTriggerReference(ctx, automationDevices, trigger); err != nil {
			return Definition{}, err
		}
	}
	if err := validateAutomationConditionReferences(ctx, automationDevices, definition.Conditions); err != nil {
		return Definition{}, err
	}
	if err := validateSequenceReferences(
		ctx,
		automationDevices,
		automationNotifications,
		automationFunctions,
		definition.Steps,
	); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

// validateSequenceReferences visits all defined arms at save time. Admission
// collectors deliberately continue to read only Definition.Conditions.
func validateSequenceReferences(
	ctx context.Context,
	automationDevices AutomationDevices,
	automationNotifications AutomationNotifications,
	automationFunctions AutomationFunctions,
	steps []Step,
) error {
	for index := range steps {
		if err := validateStepReferences(
			ctx,
			automationDevices,
			automationNotifications,
			automationFunctions,
			&steps[index],
		); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gocognit // Current references differ across the closed effect/data Step family.
func validateStepReferences(
	ctx context.Context,
	automationDevices AutomationDevices,
	automationNotifications AutomationNotifications,
	automationFunctions AutomationFunctions,
	step *Step,
) error {
	switch body := step.Body.(type) {
	case FunctionStep:
		return validateFunctionReference(ctx, automationFunctions, step, body)
	case NotificationStep:
		if automationNotifications == nil {
			return fmt.Errorf("step %q: notification validation is not configured", step.ID)
		}
		if _, err := automationNotifications.GetDestination(ctx, body.DestinationID); err != nil {
			if errors.Is(err, notifications.ErrDestinationNotFound) || errors.Is(err, notifications.ErrInvalidInput) {
				return fmt.Errorf("%w: step %q destination: %w", ErrInvalidAutomation, step.ID, err)
			}
			return fmt.Errorf("step %q destination: %w", step.ID, err)
		}
	case CommandStep:
		literal, isLiteral := body.Parameters.Body.(LiteralValue)
		if !isLiteral {
			if err := automationDevices.ValidateCommandReference(ctx, body.EntityID, body.OperationName); err != nil {
				return fmt.Errorf("step %q Command reference: %w", step.ID, deviceReferenceError(err))
			}
			return nil
		}
		parameters, err := automationDevices.ValidateCommand(
			ctx,
			devices.CommandInput{
				EntityID:      body.EntityID,
				OperationName: body.OperationName,
				Parameters:    devices.CommandParameters(literal.Value),
			},
		)
		if err != nil {
			return fmt.Errorf("step %q: %w", step.ID, deviceReferenceError(err))
		}
		body.Parameters = ValueBinding{Body: LiteralValue{Value: append(json.RawMessage(nil), parameters...)}}
		step.Body = body
	case IfStep:
		if err := validateAutomationConditionReferences(ctx, automationDevices, &body.Conditions); err != nil {
			return err
		}
		if err := validateSequenceReferences(
			ctx,
			automationDevices,
			automationNotifications,
			automationFunctions,
			body.Then,
		); err != nil {
			return err
		}
		return validateSequenceReferences(
			ctx,
			automationDevices,
			automationNotifications,
			automationFunctions,
			body.Else,
		)
	case ChooseStep:
		return validateChooseReferences(ctx, automationDevices, automationNotifications, automationFunctions, &body)
	case DelayStep:
		// Fixed elapsed waits have no Entity or Operation references.
	case ReadStateStep:
		if err := automationDevices.ValidateConditionEntity(ctx, body.EntityID); err != nil {
			return fmt.Errorf("step %q State capture reference: %w", step.ID, deviceReferenceError(err))
		}
	case SetVariablesStep:
	default:
		return invalid("step %q: unsupported body", step.ID)
	}
	return nil
}

func validateChooseReferences(
	ctx context.Context,
	automationDevices AutomationDevices,
	automationNotifications AutomationNotifications,
	automationFunctions AutomationFunctions,
	choose *ChooseStep,
) error {
	for _, branch := range choose.Branches {
		if err := validateAutomationConditionReferences(ctx, automationDevices, &branch.Conditions); err != nil {
			return err
		}
		if err := validateSequenceReferences(
			ctx,
			automationDevices,
			automationNotifications,
			automationFunctions,
			branch.Steps,
		); err != nil {
			return err
		}
	}
	return validateSequenceReferences(
		ctx,
		automationDevices,
		automationNotifications,
		automationFunctions,
		choose.Default,
	)
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
			return fmt.Errorf("trigger %q: %w", trigger.ID, deviceReferenceError(err))
		}
	case EntityEventTrigger:
		err := automationDevices.ValidateEntityEventTrigger(
			ctx, body.EntityID, body.EventName,
		)
		if err != nil {
			return fmt.Errorf("trigger %q: %w", trigger.ID, deviceReferenceError(err))
		}
	case HeldStateTrigger:
		pointers := make([]string, len(body.Comparisons))
		for index, comparison := range body.Comparisons {
			pointers[index] = comparison.Pointer
		}
		if err := automationDevices.ValidateObservationTrigger(ctx, body.EntityID, pointers); err != nil {
			return fmt.Errorf("trigger %q: %w", trigger.ID, deviceReferenceError(err))
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
	entityIDs, err := requiredPreparedConditionEntityIDs(*conditions)
	if err != nil {
		return err
	}
	for _, entityID := range entityIDs {
		if validationErr := automationDevices.ValidateConditionEntity(ctx, entityID); validationErr != nil {
			return fmt.Errorf("conditions: %w", deviceReferenceError(validationErr))
		}
	}
	return nil
}

// deviceReferenceError translates only Devices' permanent save-time reference failures.
func deviceReferenceError(err error) error {
	switch {
	case errors.Is(err, devices.ErrEntityNotFound),
		errors.Is(err, devices.ErrInvalidCommand),
		errors.Is(err, devices.ErrAutomationTriggerSource),
		errors.Is(err, devices.ErrAutomationConditionEntity):
		return fmt.Errorf("%w: %w", ErrInvalidAutomation, err)
	default:
		return err
	}
}
