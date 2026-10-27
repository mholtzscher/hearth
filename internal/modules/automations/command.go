package automations

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

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
	binding, err := normalizeValueBinding(command.Parameters)
	if err != nil {
		return Step{}, bindingAt(err, "parameters")
	}
	if literal, ok := binding.Body.(LiteralValue); ok {
		if err = validateAutomationStepParameters(devices.CommandParameters(literal.Value)); err != nil {
			return Step{}, invalid("invalid literal Command parameters")
		}
	}
	return Step{
		ID: id,
		Body: CommandStep{
			EntityID:      entityID,
			OperationName: command.OperationName,
			Parameters:    binding,
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
