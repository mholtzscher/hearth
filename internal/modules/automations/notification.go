package automations

import (
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func normalizeNotificationStep(id StepID, body NotificationStep) (Step, error) {
	if err := notifications.ValidateID(string(body.DestinationID), "ndst_"); err != nil {
		return Step{}, fmt.Errorf("%w: step %q destination: %w", ErrInvalidAutomation, id, err)
	}
	if err := notifications.ValidateMessage(notifications.Message{Title: body.Title, Text: body.Message}); err != nil {
		return Step{}, fmt.Errorf("%w: step %q: %w", ErrInvalidAutomation, id, err)
	}
	return Step{ID: id, Body: body}, nil
}
