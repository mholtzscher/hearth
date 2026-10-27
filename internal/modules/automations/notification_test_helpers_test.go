package automations_test

import (
	"context"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type notificationSenderFunc func(context.Context, notifications.Attempt) notifications.Evidence

func (send notificationSenderFunc) Send(ctx context.Context, attempt notifications.Attempt) notifications.Evidence {
	return send(ctx, attempt)
}

func notificationRuntimeDefinition(
	t *testing.T,
	destination notifications.DestinationID,
	commands int,
) automations.Definition {
	t.Helper()
	definition := runtimeDefinition(t, commands)
	definition.Steps = append([]automations.Step{{ID: "notify", Body: automations.NotificationStep{
		DestinationID: destination, Title: literalText("Freezer"), Message: literalText(" Too warm.\n"),
	}}}, definition.Steps...)
	return definition
}

func retainedNotificationRun(
	t *testing.T,
	service *automations.Service,
	record automations.Record,
	run automations.Run,
) automations.Run {
	t.Helper()
	return *runEntry(historyEntry(t, service, record.ID, string(run.ID)))
}
