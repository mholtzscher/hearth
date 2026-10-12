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

// Notification-owned persistence failures stop the consuming Run without invented acceptance or sticky module failure.
//
//nolint:gocognit // One storage fault timeline asserts evidence, no replay, and later successful work.
func TestNotificationAttemptWriteFailureStopsRunAndAllowsLaterSend(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"admission", "completion"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			fixture := newNotificationFixture(t, func(context.Context, notifications.Attempt) notifications.Evidence {
				calls.Add(1)
				return notifications.Evidence{Outcome: notifications.Accepted}
			})
			dependencies := runtimeTestDependencies()
			dependencies.Notifications = fixture.service
			scripted := newScriptedDevices()
			service := automations.NewService(
				automationssqlite.NewAutomationRepository(fixture.database, dependencies),
				scripted,
				dependencies,
			)
			record := createRuntimeAutomation(t, service, notificationRuntimeDefinition(t, fixture.destination.ID, 1))
			statement := `CREATE TRIGGER fail_attempt BEFORE INSERT ON notification_attempts BEGIN SELECT RAISE(ABORT, 'fault'); END`
			if boundary == "completion" {
				statement = `CREATE TRIGGER fail_attempt BEFORE UPDATE ON notification_attempts BEGIN SELECT RAISE(ABORT, 'fault'); END`
			}
			if _, err := fixture.database.Exec(statement); err != nil {
				t.Fatal(err)
			}
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			got := retainedNotificationRun(t, service, record, run)
			wantCalls := int32(0)
			if boundary == "completion" {
				wantCalls = 1
			}
			if automations.RunStateStatus(got.State) != automations.RunInterrupted ||
				got.Notifications[0].Status != automations.NotificationInterrupted ||
				got.Notifications[0].Outcome != nil ||
				calls.Load() != wantCalls ||
				scripted.executionCount() != 0 ||
				!service.AdmissionOpen() {
				t.Fatalf("fault Run=%#v calls=%d", got, calls.Load())
			}
			attempt, err := fixture.service.GetAttempt(t.Context(), got.Notifications[0].AttemptID)
			if boundary == "admission" && !errors.Is(err, notifications.ErrAttemptNotFound) {
				t.Fatalf("fabricated attempt=%#v %v", attempt, err)
			}
			if boundary == "completion" && (err != nil || attempt.CompletedAt != nil || attempt.Outcome != "") {
				t.Fatalf("invented attempt outcome=%#v %v", attempt, err)
			}
			if _, err = fixture.database.Exec(`DROP TRIGGER fail_attempt`); err != nil {
				t.Fatal(err)
			}
			later := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			assertFaultSiblingSucceeded(t, service, record, later)
			if calls.Load() != wantCalls+1 || scripted.executionCount() != 1 {
				t.Fatal("later send was refused or fault was resent")
			}
			if boundary == "completion" {
				recovered, recoveryErr := notifications.NewService(t.Context(), fixture.repository,
					notificationSenderFunc(func(context.Context, notifications.Attempt) notifications.Evidence {
						t.Error("recovery replayed the already-dispatched attempt")
						return notifications.Evidence{Outcome: notifications.Accepted}
					}))
				if recoveryErr != nil {
					t.Fatal(recoveryErr)
				}
				attempt, err = recovered.GetAttempt(t.Context(), got.Notifications[0].AttemptID)
				if err != nil || attempt.Outcome != notifications.Unknown ||
					attempt.ReasonCode != automations.FailureCoreRestarted {
					t.Fatalf("dispatched attempt recovery=%#v %v", attempt, err)
				}
				retained := retainedNotificationRun(t, service, record, run)
				if retained.Notifications[0].Outcome != nil ||
					retained.Notifications[0].Status != automations.NotificationInterrupted ||
					calls.Load() != wantCalls+1 {
					t.Fatal("attempt recovery rewrote interrupted Step or resent external work")
				}
			}
		})
	}
}

// A real reserved-ID collision cannot adopt unrelated accepted history, even after Send rejects admission.
func TestNotificationAttemptCollisionDoesNotAdoptUnrelatedHistory(t *testing.T) {
	t.Parallel()
	for _, collision := range []struct {
		name      string
		identical bool
		closed    bool
	}{
		{name: "different message"},
		{name: "identical message", identical: true},
		{name: "closed admission identical message", identical: true, closed: true},
	} {
		t.Run(collision.name, func(t *testing.T) {
			t.Parallel()
			runNotificationCollision(t, collision.identical, collision.closed)
		})
	}
}

func runNotificationCollision(t *testing.T, identical, closed bool) {
	t.Helper()
	var sends atomic.Int32
	fixture := newNotificationFixture(t, func(context.Context, notifications.Attempt) notifications.Evidence {
		sends.Add(1)
		return notifications.Evidence{Outcome: notifications.Accepted}
	})
	id, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	message := notifications.Message{Title: "Other", Text: "Not this Step"}
	if identical {
		message = notifications.Message{Title: "Freezer", Text: " Too warm.\n"}
	}
	prior, err := fixture.service.Send(
		t.Context(),
		notifications.SendInput{AttemptID: id, DestinationID: fixture.destination.ID, Message: message},
	)
	if err != nil {
		t.Fatal(err)
	}
	if closed {
		if err = fixture.service.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	dependencies := runtimeTestDependencies()
	dependencies.Notifications = fixture.service
	dependencies.NewNotificationAttemptID = func() (notifications.AttemptID, error) { return id, nil }
	scripted := newScriptedDevices()
	service := automations.NewService(
		automationssqlite.NewAutomationRepository(fixture.database, dependencies),
		scripted,
		dependencies,
	)
	record := createRuntimeAutomation(t, service, notificationRuntimeDefinition(t, fixture.destination.ID, 1))
	run := admitDelayRun(t, service, record)
	waitForRuns(t, service)
	got := retainedNotificationRun(t, service, record, run)
	if automations.RunStateStatus(got.State) != automations.RunInterrupted || got.Notifications[0].Outcome != nil ||
		sends.Load() != 1 ||
		scripted.executionCount() != 0 {
		t.Fatalf("collision adopted prior history=%#v", got)
	}
	retained, err := fixture.service.GetAttempt(t.Context(), id)
	if err != nil || retained.Message != prior.Message || retained.Outcome != notifications.Accepted {
		t.Fatalf("collision mutated original=%#v %v", retained, err)
	}
}
