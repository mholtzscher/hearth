//go:build integration

package sqlite_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// Sequential Runs cannot reserve a second kind of effect while one is running.
// Rejection preserves all retained evidence; completing the first permits the next.
func TestRunningEvidenceExcludesOtherKindsUntilCompletion(t *testing.T) {
	t.Parallel()
	for _, pair := range []struct{ first, second string }{
		{"delay", "notification"}, {"delay", "command"},
		{"notification", "delay"}, {"notification", "command"},
		{"command", "delay"}, {"command", "notification"},
	} {
		t.Run(pair.first+" then "+pair.second, func(t *testing.T) {
			t.Parallel()
			repository := newAutomationRepository(t, openAutomationDatabase(t))
			definition := notificationStorageDefinition(t)
			steps := map[string]automations.Step{
				"notification": definition.Steps[0],
				"command":      definition.Steps[2],
				"delay":        {ID: "wait", Body: automations.DelayStep{DurationMS: 1}},
			}
			definition.Steps = []automations.Step{steps[pair.first], steps[pair.second]}
			record, run := admitStorageRun(t, repository, definition, time.Now().UTC())
			start, complete := evidenceOperations(t, repository, run, pair.first)
			next, _ := evidenceOperations(t, repository, run, pair.second)
			if err := start(); err != nil {
				t.Fatal(err)
			}
			before := retainedStorageRun(t, repository, record, run)
			if err := next(); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("overlapping %s start = %v", pair.second, err)
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatalf("rejected start changed retained Run: %#v", got)
			}
			if err := complete(); err != nil {
				t.Fatal(err)
			}
			next, _ = evidenceOperations(t, repository, retainedStorageRun(t, repository, record, run), pair.second)
			if err := next(); err != nil {
				t.Fatalf("sequential %s start = %v", pair.second, err)
			}
			assertEvidenceRunning(t, retainedStorageRun(t, repository, record, run), pair.second)
		})
	}
}

func assertEvidenceRunning(t *testing.T, run automations.Run, kind string) {
	t.Helper()
	switch kind {
	case "delay":
		if len(run.Delays) != 1 || run.Delays[0].Status != automations.DelayRunning {
			t.Fatalf("sequential delay was not retained: %#v", run.Delays)
		}
	case "notification":
		if len(run.Notifications) != 1 || run.Notifications[0].Status != automations.NotificationRunning {
			t.Fatalf("sequential notification was not retained: %#v", run.Notifications)
		}
	case "command":
		if automations.StepAttemptStatus(run.Steps[0].State) != automations.StepRunning {
			t.Fatalf("sequential Command reservation was not retained: %#v", run.Steps)
		}
	}
}

func evidenceOperations(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	run automations.Run,
	kind string,
) (func() error, func() error) {
	t.Helper()
	ctx, at := t.Context(), time.Now().UTC()
	switch kind {
	case "delay":
		return func() error {
				return repository.RecordDelayStart(
					ctx,
					automations.DelayStart{RunID: run.ID, StepID: "wait", StartedAt: at},
				)
			},
			func() error {
				return repository.CompleteDelay(
					ctx,
					automations.DelayCompletion{
						RunID:       run.ID,
						StepID:      "wait",
						Status:      automations.DelayCompleted,
						CompletedAt: at,
					},
				)
			}
	case "notification":
		start := notificationStorageStart(t, run)
		outcome := notifications.Accepted
		return func() error { return repository.RecordNotificationStart(ctx, start) },
			func() error {
				return repository.CompleteNotification(
					ctx,
					automations.NotificationCompletion{
						RunID:       run.ID,
						StepID:      start.StepID,
						AttemptID:   start.AttemptID,
						Status:      automations.NotificationAccepted,
						CompletedAt: at,
						Outcome:     &outcome,
					},
				)
			}
	default:
		commandID, err := devices.NewCommandID()
		if err != nil {
			t.Fatal(err)
		}
		correlationID, err := devices.NewCorrelationID()
		if err != nil {
			t.Fatal(err)
		}
		return func() error {
				return repository.MarkStepRunning(
					ctx,
					automations.StepStart{RunID: run.ID, CommandID: commandID, CorrelationID: correlationID,
						Inputs: storageInputs(run, "light_on", `{"parameters":{"value":true}}`)},
				)
			},
			func() error {
				return repository.CompleteStep(
					ctx,
					automations.StepCompletion{
						RunID:   run.ID,
						Outcome: automations.DispatchedStep{VerifiedCommandID: commandID},
					},
				)
			}
	}
}
