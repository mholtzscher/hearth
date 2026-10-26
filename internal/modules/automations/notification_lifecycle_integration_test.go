//go:build integration

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

// Operation failure makes exactly one scoped cleanup attempt, never a resend or a sticky readiness/admission failure.
//
//nolint:gocognit // One ordered fault timeline checks sibling, later work, cleanup, and recovery ownership.
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
				if len(got.Executions) != 0 {
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
				assertFaultNotificationEvidence(t, requireNotificationExecution(t, got, "notify"), wantEvidence)
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
				); func() bool {
					entry := requireNotificationExecution(t, got, "notify")
					return entry.CompletedAt == nil || entry.ReasonCode == nil ||
						*entry.ReasonCode != automations.FailureCoreRestarted
				}() {
					t.Fatalf("recovery=%#v", got)
				}
				if sends.Load() != wantSends+1 {
					t.Fatal("startup resent")
				}
			}
		})
	}
}

func assertFaultNotificationEvidence(
	t *testing.T,
	notification automations.NotificationExecutionBody,
	want automations.NotificationStatus,
) {
	t.Helper()
	if want == automations.NotificationAccepted &&
		(notification.Outcome == nil || *notification.Outcome != notifications.Accepted) {
		t.Fatalf("fault evidence=%#v", notification)
	}
	if want == automations.NotificationRunning && notification.CompletedAt != nil {
		t.Fatalf("fault evidence unexpectedly terminal=%#v", notification)
	}
	if want == automations.NotificationInterrupted && (notification.CompletedAt == nil || notification.Outcome != nil) {
		t.Fatalf("fault evidence=%#v", notification)
	}
	if want != automations.NotificationAccepted && notification.Outcome != nil {
		t.Fatal("cleanup invented provider outcome")
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
			if err = repo.RecordStep(
				t.Context(),
				automations.StepExecutionRecord{
					RunID: run.ID, StepID: "notify",
					Evidence: automations.NotificationStarted{AttemptID: id, StartedAt: runtimeTestNow},
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
				if err = repo.CompleteStep(
					t.Context(),
					automations.StepExecutionCompletion{
						RunID: run.ID, StepID: "notify",
						Evidence: automations.NotificationFinished{
							AttemptID: id, Outcome: &outcome, CompletedAt: runtimeTestNow,
						},
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
			entry := requireNotificationExecution(t, got, "notify")
			if boundary == "after Step outcome" {
				if entry.CompletedAt == nil || entry.Outcome == nil ||
					*entry.Outcome != notifications.Accepted {
					t.Fatalf("terminal evidence overwritten=%#v", entry)
				}
			} else if entry.CompletedAt == nil || entry.Outcome != nil || entry.ReasonCode == nil ||
				*entry.ReasonCode != automations.FailureCoreRestarted {
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
