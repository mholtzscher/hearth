//go:build integration

package automations_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// V3: registration/definition edits cannot redirect an admitted send or alter its immutable message.
// The reached Step keeps its active slot until authoritative evidence completes.
func TestNotificationAdmittedSnapshotAndBusySlotSurviveConcurrentEdits(t *testing.T) {
	t.Parallel()
	entered, release := make(chan notifications.Attempt, 1), make(chan struct{})
	var sends atomic.Int32
	fixture := newNotificationFixture(
		t,
		func(ctx context.Context, attempt notifications.Attempt) notifications.Evidence {
			sends.Add(1)
			entered <- attempt
			<-release
			if ctx.Err() != nil {
				t.Error("definition edit canceled admitted send")
			}
			return notifications.Evidence{Outcome: notifications.Accepted}
		},
	)
	defer close(release)
	dependencies := runtimeTestDependencies()
	dependencies.Notifications = fixture.service
	scripted := newScriptedDevices()
	service := automations.NewService(
		automationssqlite.NewAutomationRepository(fixture.database, dependencies),
		scripted,
		dependencies,
	)
	record := createRuntimeAutomation(t, service, notificationRuntimeDefinition(t, fixture.destination.ID, 1))
	run := admitDelayRun(t, service, record)
	var sent notifications.Attempt
	select {
	case sent = <-entered:
	case <-t.Context().Done():
		t.Fatal("send did not start")
	}
	if _, err := service.StartManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
	); !errors.Is(
		err,
		automations.ErrAutomationBusy,
	) {
		t.Fatalf("notification released active slot=%v", err)
	}
	before := retainedNotificationRun(t, service, record, run)
	if before.Notifications[0].Status != automations.NotificationRunning ||
		before.Notifications[0].AttemptID != sent.ID ||
		scripted.executionCount() != 0 {
		t.Fatalf("in-flight evidence=%#v", before)
	}
	registration := fixture.registration
	registration.Name = "Renamed"
	renamed, err := fixture.service.Register(t.Context(), registration)
	if err != nil {
		t.Fatal(err)
	}
	registration.Key, registration.Name = "other", "Other"
	other, err := fixture.service.Register(t.Context(), registration)
	if err != nil {
		t.Fatal(err)
	}
	definition := record.Definition
	body := definition.Steps[0].Body.(automations.NotificationStep)
	body.DestinationID, body.Title, body.Message = other.ID, literalText("Changed"), literalText("Different")
	definition.Steps[0].Body = body
	if _, err = service.ReplaceAutomation(t.Context(), record.ID, record.Revision, definition); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	waitForRuns(t, service)
	got := retainedNotificationRun(t, service, record, run)
	attempt, err := fixture.service.GetAttempt(t.Context(), sent.ID)
	if err != nil || attempt.DestinationID != fixture.destination.ID || attempt.DestinationName != "Home" ||
		attempt.Route != sent.Route ||
		attempt.Message != sent.Message ||
		renamed.ID != fixture.destination.ID ||
		automations.RunStateStatus(got.State) != automations.RunSucceeded ||
		sends.Load() != 1 ||
		scripted.executionCount() != 1 {
		t.Fatalf("admitted snapshot drifted: attempt=%#v Run=%#v err=%v", attempt, got, err)
	}
	if got.Snapshot.Steps[0].Body.(automations.NotificationStep).DestinationID != fixture.destination.ID {
		t.Fatal("definition replacement mutated Run snapshot")
	}
}
