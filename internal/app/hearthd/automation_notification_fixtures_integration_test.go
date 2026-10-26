//go:build integration

package hearthd //nolint:testpackage // Wraps the real persistence seam to pause a shutdown boundary.

import (
	"context"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type blockedNotificationLinkRepository struct {
	automations.Repository

	linked  chan struct{}
	release chan struct{}
}

func (repo *blockedNotificationLinkRepository) RecordStep(
	ctx context.Context,
	record automations.StepExecutionRecord,
) error {
	if err := repo.Repository.RecordStep(ctx, record); err != nil {
		return err
	}
	if _, isNotification := record.Evidence.(automations.NotificationStarted); !isNotification {
		return nil
	}
	close(repo.linked)
	<-repo.release
	return nil
}

func startReservedNotificationRun(t *testing.T, fixture notificationFixture, reservedID notifications.AttemptID) (
	*blockedNotificationLinkRepository, *automations.Service, *devices.Service, automations.Record, automations.Run,
) {
	t.Helper()
	repo := &blockedNotificationLinkRepository{
		Repository: automationssqlite.NewAutomationRepository(fixture.database, automations.Dependencies{}),
		linked:     make(chan struct{}), release: make(chan struct{}),
	}
	deviceService := devices.NewService(devices.Stores{}, nil, nil, devices.Dependencies{})
	service := automations.NewService(repo, newBlockingAutomationDevices(), automations.Dependencies{
		Notifications:            fixture.service,
		NewNotificationAttemptID: func() (notifications.AttemptID, error) { return reservedID, nil },
	})
	entityID, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	definition := automations.Definition{Name: "Notification drain", Enabled: true,
		Triggers: []automations.Trigger{{ID: "trigger", Body: automations.ObservationTrigger{
			EntityID: entityID, Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
		}}},
		Steps: []automations.Step{{ID: "notify", Body: automations.NotificationStep{
			DestinationID: notifications.DestinationID(fixture.destinationID), Message: "reserved before drain",
		}}},
	}
	record, err := service.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartManualRun(t.Context(), automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	<-repo.linked
	entry, err := service.GetHistoryEntry(t.Context(), record.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	linked := entry.(automations.Run)
	if len(linked.Executions) != 1 || linked.Executions[0].StepID != "notify" {
		t.Fatalf("Step did not reserve durable link: %#v", linked)
	}
	notification, ok := linked.Executions[0].Body.(automations.NotificationExecutionBody)
	if !ok || notification.AttemptID != reservedID || notification.CompletedAt != nil {
		t.Fatalf("Step did not reserve durable link: %#v", linked)
	}
	if _, err = fixture.service.GetAttempt(t.Context(), reservedID); !errors.Is(err, notifications.ErrAttemptNotFound) {
		t.Fatalf("attempt created before Send: %v", err)
	}
	return repo, service, deviceService, record, run
}
