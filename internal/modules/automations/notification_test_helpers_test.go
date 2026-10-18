package automations_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationssqlite "github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
)

type notificationSenderFunc func(context.Context, notifications.Attempt) notifications.Evidence

func (send notificationSenderFunc) Send(ctx context.Context, attempt notifications.Attempt) notifications.Evidence {
	return send(ctx, attempt)
}

type notificationFixture struct {
	database     *sql.DB
	repository   *notificationssqlite.Repository
	service      *notifications.Service
	runtimes     *adapterssqlite.AdapterRepository
	registration notifications.Registration
	destination  notifications.Destination
}

func newNotificationFixture(t *testing.T, sender notificationSenderFunc) notificationFixture {
	t.Helper()
	database := openAutomationDatabase(t)
	runtimes := adapterssqlite.NewAdapterRepository(database)
	runtimeID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	registration := notifications.Registration{
		AdapterID: "alerts",
		RuntimeID: "run_" + runtimeID.String(),
		Key:       "home",
		Name:      "Home",
	}
	claimNotificationRuntime(t, runtimes, registration)
	repository := notificationssqlite.NewRepository(database)
	service, err := notifications.NewService(t.Context(), repository, sender)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := service.Register(t.Context(), registration)
	if err != nil {
		t.Fatal(err)
	}
	return notificationFixture{database, repository, service, runtimes, registration, destination}
}

func claimNotificationRuntime(
	t *testing.T,
	runtimes *adapterssqlite.AdapterRepository,
	registration notifications.Registration,
) {
	t.Helper()
	now := time.Now().UTC()
	if err := runtimes.ClaimAdapterRuntime(t.Context(), adapters.ClaimRuntimeWrite{
		AdapterID: registration.AdapterID, RuntimeID: adapters.RuntimeID(registration.RuntimeID),
		SoftwareName: "alerts", SoftwareVersion: "1", ClaimedAt: now, LeaseExpiresAt: now.Add(15 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
}

func notificationRuntimeDefinition(
	t *testing.T,
	destination notifications.DestinationID,
	commands int,
) automations.Definition {
	t.Helper()
	definition := runtimeDefinition(t, commands)
	definition.Steps = append([]automations.Step{{ID: "notify", Body: automations.NotificationStep{
		DestinationID: destination, Title: "Freezer", Message: " Too warm.\n",
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
