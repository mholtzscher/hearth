package automations_test

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func (repo *executorFaultRepository) RecordNotificationStart(
	ctx context.Context,
	start automations.NotificationStart,
) error {
	return repo.inject(
		start.RunID,
		"notification_start",
		func() error { return repo.Repository.RecordNotificationStart(ctx, start) },
	)
}

func (repo *executorFaultRepository) CompleteNotification(
	ctx context.Context,
	completion automations.NotificationCompletion,
) error {
	return repo.inject(
		completion.RunID,
		"notification_complete",
		func() error { return repo.Repository.CompleteNotification(ctx, completion) },
	)
}
