package automations_test

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type notificationReadOverride struct {
	automations.AutomationNotifications

	read func(context.Context, notifications.AttemptID) (notifications.Attempt, error)
	send func(context.Context, notifications.SendInput) (notifications.Attempt, error)
}

func (seam notificationReadOverride) GetAttempt(
	ctx context.Context,
	id notifications.AttemptID,
) (notifications.Attempt, error) {
	if seam.read != nil {
		return seam.read(ctx, id)
	}
	return seam.AutomationNotifications.GetAttempt(ctx, id)
}

func (seam notificationReadOverride) Send(
	ctx context.Context,
	input notifications.SendInput,
) (notifications.Attempt, error) {
	if seam.send != nil {
		return seam.send(ctx, input)
	}
	return seam.AutomationNotifications.Send(ctx, input)
}
