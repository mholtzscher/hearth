package automations_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
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

// Operation failure makes exactly one scoped cleanup attempt, never a resend or a sticky readiness/admission failure.
//
//nolint:gocognit,nestif // One ordered fault timeline checks sibling, later work, cleanup, and recovery ownership.
func TestNotificationPersistenceFaultIsRunScoped(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, boundary       string
		commit, cleanupFails bool
	}{
		{"link write fails", "notification_start", false, false},
		{"link commit ambiguous", "notification_start", true, false},
		{"outcome write fails", "notification_complete", false, false},
		{"outcome commit ambiguous", "notification_complete", true, false},
		{"cleanup fails", "notification_complete", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var sends atomic.Int32
			fixture := newNotificationFixture(t, func(context.Context, notifications.Attempt) notifications.Evidence {
				sends.Add(1)
				return notifications.Evidence{Outcome: notifications.Accepted}
			})
			writer, logger := newAutomationLogSink()
			dependencies := runtimeTestDependencies()
			dependencies.Notifications, dependencies.Logger = fixture.service, logger
			repo := &executorFaultRepository{
				Repository:     automationssqlite.NewAutomationRepository(fixture.database, dependencies),
				boundary:       test.boundary,
				commit:         test.commit,
				cleanupFails:   test.cleanupFails,
				cleanupReached: make(chan struct{}),
			}
			repo.runID.Store(automations.RunID(""))
			scripted := newScriptedDevices()
			service := automations.NewService(repo, scripted, dependencies)
			target := createRuntimeAutomation(t, service, notificationRuntimeDefinition(t, fixture.destination.ID, 1))
			repo.target = target.ID
			sibling := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			blockFaultSiblingCommand(
				t,
				scripted,
				sibling.Definition.Steps[0].Body.(automations.CommandStep).EntityID,
				entered,
				release,
			)
			siblingRun := admitDelayRun(t, service, sibling)
			awaitDelaySignal(t, entered)
			run := admitDelayRun(t, service, target)
			awaitDelaySignal(t, repo.cleanupReached)
			release <- struct{}{}
			waitForRuns(t, service)
			wantSends := int32(1)
			if test.boundary == "notification_start" {
				wantSends = 0
			}
			if sends.Load() != wantSends || scripted.executionCount() != 1 || !service.AdmissionOpen() ||
				repo.cleanupCalls.Load() != 1 {
				t.Fatalf(
					"sends=%d commands=%d cleanup=%d admission=%v",
					sends.Load(),
					scripted.executionCount(),
					repo.cleanupCalls.Load(),
					service.AdmissionOpen(),
				)
			}
			assertFaultSiblingSucceeded(t, service, sibling, siblingRun)
			got := retainedNotificationRun(t, service, target, run)
			wantRun := automations.RunInterrupted
			if test.cleanupFails {
				wantRun = automations.RunRunning
			}
			if automations.RunStateStatus(got.State) != wantRun {
				t.Fatalf("fault Run=%#v", got)
			}
			if test.boundary == "notification_start" && !test.commit {
				if len(got.Notifications) != 0 {
					t.Fatal("failed reservation fabricated a link")
				}
			} else {
				wantEvidence := automations.NotificationInterrupted
				if test.cleanupFails {
					wantEvidence = automations.NotificationRunning
				}
				if test.commit && test.boundary == "notification_complete" {
					wantEvidence = automations.NotificationAccepted
				}
				if len(got.Notifications) != 1 || got.Notifications[0].Status != wantEvidence {
					t.Fatalf("fault evidence=%#v", got.Notifications)
				}
				if wantEvidence != automations.NotificationAccepted && got.Notifications[0].Outcome != nil {
					t.Fatal("cleanup invented provider outcome")
				}
			}
			later := createRuntimeAutomation(t, service, notificationRuntimeDefinition(t, fixture.destination.ID, 0))
			laterRun := admitDelayRun(t, service, later)
			waitForRuns(t, service)
			assertFaultSiblingSucceeded(t, service, later, laterRun)
			if sends.Load() != wantSends+1 || repo.cleanupCalls.Load() != 1 ||
				strings.Contains(writer.output(), errExecutorWrite.Error()) {
				t.Fatal("later work retried fault or exposed database text")
			}
			if test.cleanupFails {
				if _, err := service.StartManualRun(
					t.Context(),
					automations.ManualRunInput{AutomationID: target.ID},
				); !errors.Is(
					err,
					automations.ErrAutomationBusy,
				) {
					t.Fatalf("faulted Run lost busy slot=%v", err)
				}
				if err := service.InterruptActiveRuns(t.Context(), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if got = retainedNotificationRun(
					t,
					service,
					target,
					run,
				); got.Notifications[0].Status != automations.NotificationInterrupted ||
					*got.Notifications[0].ReasonCode != automations.FailureCoreRestarted {
					t.Fatalf("recovery=%#v", got)
				}
				if sends.Load() != wantSends+1 {
					t.Fatal("startup resent")
				}
			}
		})
	}
}

// Persist real crash boundaries without starting an executor: startup interrupts, never adopts or resumes external work.
//
//nolint:gocognit // Crash boundaries share setup while retaining distinct independently persisted evidence.
func TestNotificationCrashRecoveryNeverResends(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"before attempt", "after attempt admission", "after provider outcome", "after Step outcome"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			var sends atomic.Int32
			fixture := newNotificationFixture(t, func(context.Context, notifications.Attempt) notifications.Evidence {
				sends.Add(1)
				return notifications.Evidence{Outcome: notifications.Accepted}
			})
			dependencies := runtimeTestDependencies()
			dependencies.Notifications = fixture.service
			repo := automationssqlite.NewAutomationRepository(fixture.database, dependencies)
			definition := notificationRuntimeDefinition(t, fixture.destination.ID, 1)
			record, err := repo.CreateAutomation(t.Context(), definition)
			if err != nil {
				t.Fatal(err)
			}
			result, err := repo.AdmitManualRun(
				t.Context(),
				automations.ManualRunInput{AutomationID: record.ID},
				devices.EntityStateSnapshot{},
				runtimeTestNow,
			)
			if err != nil {
				t.Fatal(err)
			}
			run := *runEntry(result)
			id, err := notifications.NewAttemptID()
			if err != nil {
				t.Fatal(err)
			}
			if err = repo.RecordNotificationStart(
				t.Context(),
				automations.NotificationStart{
					RunID:     run.ID,
					StepID:    "notify",
					AttemptID: id,
					StartedAt: runtimeTestNow,
				},
			); err != nil {
				t.Fatal(err)
			}
			input := notifications.SendInput{
				AttemptID:     id,
				DestinationID: fixture.destination.ID,
				Message:       notifications.Message{Title: "Freezer", Text: " Too warm.\n"},
			}
			persistNotificationCrashBoundary(t, fixture, input, boundary)
			if boundary == "after Step outcome" {
				outcome := notifications.Accepted
				if err = repo.CompleteNotification(
					t.Context(),
					automations.NotificationCompletion{
						RunID:       run.ID,
						StepID:      "notify",
						AttemptID:   id,
						Status:      automations.NotificationAccepted,
						Outcome:     &outcome,
						CompletedAt: runtimeTestNow,
					},
				); err != nil {
					t.Fatal(err)
				}
			}
			// Production startup recovers Notifications first, then Automation evidence.
			recoveredNotifications, err := notifications.NewService(
				t.Context(),
				fixture.repository,
				notificationSenderFunc(func(context.Context, notifications.Attempt) notifications.Evidence {
					t.Error("startup replayed a send")
					return notifications.Evidence{Outcome: notifications.Accepted}
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			dependencies.Notifications = recoveredNotifications
			scripted := newScriptedDevices()
			service := automations.NewService(repo, scripted, dependencies)
			if err = service.InterruptActiveRuns(t.Context(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			got := retainedNotificationRun(t, service, record, run)
			if automations.RunStateStatus(got.State) != automations.RunInterrupted || sends.Load() != 0 ||
				scripted.executionCount() != 0 {
				t.Fatalf("recovered Run=%#v", got)
			}
			entry := got.Notifications[0]
			if boundary == "after Step outcome" {
				if entry.Status != automations.NotificationAccepted || entry.Outcome == nil ||
					*entry.Outcome != notifications.Accepted {
					t.Fatalf("terminal evidence overwritten=%#v", entry)
				}
			} else if entry.Status != automations.NotificationInterrupted || entry.Outcome != nil || *entry.ReasonCode != automations.FailureCoreRestarted {
				t.Fatalf("recovery invented Step outcome=%#v", entry)
			}
			attempt, readErr := recoveredNotifications.GetAttempt(t.Context(), id)
			if boundary == "before attempt" {
				if !errors.Is(readErr, notifications.ErrAttemptNotFound) {
					t.Fatalf("recovery fabricated attempt=%#v %v", attempt, readErr)
				}
			} else if readErr != nil || (boundary == "after attempt admission" && (attempt.Outcome != notifications.Unknown || attempt.ReasonCode != "core_restarted")) || (boundary != "after attempt admission" && attempt.Outcome != notifications.Accepted) {
				t.Fatalf("authoritative recovery=%#v %v", attempt, readErr)
			}
		})
	}
}

func persistNotificationCrashBoundary(
	t *testing.T,
	fixture notificationFixture,
	input notifications.SendInput,
	boundary string,
) {
	t.Helper()
	if boundary == "before attempt" {
		return
	}
	at := time.Now().UTC()
	if _, _, err := fixture.repository.Admit(t.Context(), input, at, at.Add(notifications.SendTimeout)); err != nil {
		t.Fatal(err)
	}
	if boundary != "after attempt admission" {
		if _, err := fixture.repository.Complete(
			t.Context(),
			input.AttemptID,
			notifications.Evidence{Outcome: notifications.Accepted},
			at,
		); err != nil {
			t.Fatal(err)
		}
	}
}
