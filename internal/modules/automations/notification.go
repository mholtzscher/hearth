package automations

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func normalizeNotificationStep(id StepID, body NotificationStep) (Step, error) {
	if err := notifications.ValidateID(string(body.DestinationID), "ndst_"); err != nil {
		return Step{}, fmt.Errorf("%w: step %q destination: %w", ErrInvalidAutomation, id, err)
	}
	title, err := normalizeValueBinding(body.Title)
	if err != nil {
		return Step{}, err
	}
	message, err := normalizeValueBinding(body.Message)
	if err != nil {
		return Step{}, err
	}
	var values [2]string
	for index, binding := range []ValueBinding{title, message} {
		if literal, ok := binding.Body.(LiteralValue); ok {
			if string(literal.Value) == "null" || json.Unmarshal(literal.Value, &values[index]) != nil {
				return Step{}, invalid("notification literal must be a string")
			}
		} else {
			values[index] = "dynamic"
		}
	}
	if err = notifications.ValidateMessage(notifications.Message{Title: values[0], Text: values[1]}); err != nil {
		return Step{}, invalid("invalid notification message")
	}
	body.Title, body.Message = title, message
	return Step{ID: id, Body: body}, nil
}
