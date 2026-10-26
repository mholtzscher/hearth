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
	switch kind {
	case "delay":
		if len(run.Executions) == 0 {
			t.Fatal("sequential delay was not retained")
		}
		delay, ok := run.Executions[len(run.Executions)-1].Details.(automations.DelayExecutionDetails)
		_, running := delay.State.(automations.DelayRunning)
		if !ok || !running {
			t.Fatalf("sequential delay was not running: %#v", run.Executions)
		}
	case "notification":
		if len(run.Executions) == 0 {
			t.Fatal("sequential notification was not retained")
		}
		notification, ok := run.Executions[len(run.Executions)-1].Details.(automations.NotificationExecutionDetails)
		_, running := notification.State.(automations.NotificationRunning)
		if !ok || !running {
			t.Fatalf("sequential notification was not running: %#v", run.Executions)
		}
	case "command":
		if len(run.Executions) == 0 {
			t.Fatal("sequential Command reservation was not retained")
		}
		command, ok := run.Executions[len(run.Executions)-1].Details.(automations.CommandExecutionDetails)
		_, running := command.State.(automations.CommandRunning)
		if !ok || !running {
			t.Fatalf("sequential Command reservation was not running: %#v", run.Executions)
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
				return completeStorageStep(
					ctx,
					repository,
					run.ID,
					stepID,
					automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: at}},
				)
			}
	case "notification":
		start := notificationStorageStart(t)
		return func() error {
				return recordStorageStep(
					ctx,
					repository,
					run.ID,
					"first",
					automations.NotificationStarted{StartedAt: start.StartedAt, AttemptID: start.AttemptID},
				)
			},
			func() error {
				return completeStorageStep(
					ctx,
					repository,
					run.ID,
					"first",
					automations.NotificationFinished{
						AttemptID: start.AttemptID,
						State:     automations.NotificationSucceeded{CompletedAt: at},
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
		reservation := automations.CommandReservation{CommandID: commandID, CorrelationID: correlationID}
		return func() error { return recordStorageCommand(ctx, repository, run.ID, stepID, reservation, at) },
			func() error {
				return completeStorageStep(
					ctx,
					repository,
					run.ID,
					stepID,
					automations.CommandFinished{
						State: automations.CommandSucceeded{
							CompletedAt: at,
							Reservation: reservation,
							Outcome:     automations.DispatchedCommand{VerifiedCommandID: commandID},
						},
					},
				)
			}
	}
}
