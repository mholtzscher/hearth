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
			if err := next(); err != nil {
				t.Fatalf("sequential %s start = %v", pair.second, err)
			}
			assertEvidenceRunning(t, retainedStorageRun(t, repository, record, run), pair.second)
		})
	}
}

func assertEvidenceRunning(t *testing.T, run automations.Run, kind string) {
	t.Helper()
	projection, err := automations.ProjectStepExecutions(run.Snapshot, run.Executions)
	if err != nil {
		t.Fatal(err)
	}
	switch kind {
	case "delay":
		if len(projection.Delays) != 1 || projection.Delays[0].Status != automations.DelayRunning {
			t.Fatalf("sequential delay was not retained: %#v", projection.Delays)
		}
	case "notification":
		if len(projection.Notifications) != 1 || projection.Notifications[0].Status != automations.NotificationRunning {
			t.Fatalf("sequential notification was not retained: %#v", projection.Notifications)
		}
	case "command":
		if len(projection.Steps) == 0 ||
			automations.StepAttemptStatus(projection.Steps[0].State) != automations.StepRunning {
			t.Fatalf("sequential Command reservation was not retained: %#v", projection.Steps)
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
	stepID := automations.StepID("wait")
	if kind == "command" {
		stepID = automations.CommandLeaves(run.Snapshot.Steps)[0].ID
	}
	switch kind {
	case "delay":
		return func() error {
				return recordStorageStep(ctx, repository, run.ID, stepID, automations.DelayStarted{StartedAt: at})
			},
			func() error {
				return completeStorageStep(ctx, repository, run.ID, stepID, automations.DelayFinished{CompletedAt: at})
			}
	case "notification":
		start := notificationStorageStart(t, run)
		outcome := notifications.Accepted
		return func() error {
				return recordStorageStep(
					ctx,
					repository,
					run.ID,
					start.StepID,
					automations.NotificationStarted{StartedAt: start.StartedAt, AttemptID: start.AttemptID},
				)
			},
			func() error {
				return completeStorageStep(
					ctx,
					repository,
					run.ID,
					start.StepID,
					automations.NotificationFinished{CompletedAt: at, AttemptID: start.AttemptID, Outcome: &outcome},
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
		reservation := automations.CommandReservation{CommandID: commandID, CorrelationID: correlationID}
		return func() error { return recordStorageCommand(ctx, repository, run.ID, stepID, reservation, at) },
			func() error {
				return completeStorageStep(
					ctx,
					repository,
					run.ID,
					stepID,
					automations.CommandFinished{
						CompletedAt: at,
						Reservation: reservation,
						Outcome:     automations.DispatchedStep{VerifiedCommandID: commandID},
					},
				)
			}
	}
}
