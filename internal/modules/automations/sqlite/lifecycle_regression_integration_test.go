//go:build integration

package sqlite_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Wall clocks may regress; retained lifecycle timestamps are evidence, not ordering guarantees.
func TestLifecycleHistoryPreservesRegressedClockTimestamps(t *testing.T) {
	t.Parallel()
	for _, interrupt := range []bool{false, true} {
		name := "completion"
		if interrupt {
			name = "supplied interruption"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertRegressedClockHistory(t, interrupt)
		})
	}
}

func assertRegressedClockHistory(t *testing.T, interrupt bool) {
	t.Helper()
	ctx := context.Background()
	runStarted := time.Date(2026, time.September, 1, 12, 0, 0, 123456789, time.UTC)
	stepStarted := runStarted.Add(time.Minute)
	completedAt := runStarted.Add(-time.Minute)
	now := runStarted
	repository := automationssqlite.NewAutomationRepository(openAutomationDatabase(t), automations.Dependencies{
		Now: func() time.Time { return now },
	})
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), runStarted)
	commandID := devices.CommandID(newCommandIDString(t))
	now = stepStarted
	reservation := automations.CommandReservation{
		CommandID:     commandID,
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}
	if err := recordStorageCommand(ctx, repository, run.ID, "light_on", reservation, stepStarted); err != nil {
		t.Fatal(err)
	}
	if interrupt {
		// The supplied timestamp must win over the repository clock too.
		if err := repository.InterruptActiveRuns(ctx, completedAt, automations.FailureCoreRestarted); err != nil {
			t.Fatal(err)
		}
	} else {
		now = completedAt
		if err := completeStorageStep(ctx, repository, run.ID, "light_on", automations.CommandFinished{
			State: automations.CommandSucceeded{CompletedAt: completedAt, Reservation: reservation,
				Outcome: automations.SatisfiedCommand{VerifiedCommandID: commandID}},
		}); err != nil {
			t.Fatal(err)
		}
		if err := repository.CompleteRun(
			ctx,
			automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
		); err != nil {
			t.Fatal(err)
		}
	}
	retained := retainedStorageRun(t, repository, record, run)
	terminal := retained.State.(automations.CompletedRun)
	step := retained.Executions[0].Details.(automations.CommandExecutionDetails)
	stepCompletedAt, err := automations.CommandStateCompletion(step.State)
	if err != nil {
		t.Fatal(err)
	}
	if retained.ValueInitialization == nil {
		t.Fatal("terminal transition lost value initialization")
	}
	if !retained.StartedAt.Equal(runStarted) || !terminal.CompletedAt.Equal(completedAt) ||
		!step.StartedAt.Equal(stepStarted) || stepCompletedAt == nil || !stepCompletedAt.Equal(completedAt) {
		t.Fatalf("retained timestamps = Run %v/%v, Step %v/%v; want %v/%v, %v/%v",
			retained.StartedAt, terminal.CompletedAt, step.StartedAt, stepCompletedAt,
			runStarted, completedAt, stepStarted, completedAt)
	}
}

// A rejected pre-reservation failure must leave the attempt startable. A failure
// before Command creation remains valid once the reservation has been persisted.
func TestFailedStepRequiresPersistedReservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), time.Now().UTC())
	completion := automations.StepExecutionCompletion{
		RunID:  run.ID,
		StepID: "light_on",
		Evidence: automations.CommandFinished{
			State: automations.CommandFailed{
				CompletedAt: time.Now().UTC(),
				FailureCode: automations.FailureInvalidCommand,
			},
		},
	}
	if err := repository.CompleteStep(ctx, completion); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("unreserved failure = %v, want invalid Automation", err)
	}
	commandID := devices.CommandID(newCommandIDString(t))
	correlationID := devices.CorrelationID(newCorrelationIDString(t))
	reservation := automations.CommandReservation{CommandID: commandID, CorrelationID: correlationID}
	if err := recordStorageCommand(ctx, repository, run.ID, "light_on", reservation, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	beforeWrongReservation := retainedStorageRun(t, repository, record, run)
	wrongReservation := reservation
	wrongReservation.CorrelationID = devices.CorrelationID(newCorrelationIDString(t))
	if err := completeStorageStep(ctx, repository, run.ID, "light_on", automations.CommandFinished{
		State: automations.CommandFailed{CompletedAt: time.Now().UTC(), Reservation: wrongReservation,
			FailureCode: automations.FailureInvalidCommand},
	}); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("wrong-reservation failure = %v, want invalid Automation", err)
	}
	if after := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(after, beforeWrongReservation) {
		t.Fatal("wrong reservation changed retained evidence")
	}
	completion.Evidence = automations.CommandFinished{State: automations.CommandFailed{
		CompletedAt: time.Now().UTC(), Reservation: reservation, FailureCode: automations.FailureInvalidCommand,
	}}
	if err := repository.CompleteStep(ctx, completion); err != nil {
		t.Fatal(err)
	}
	retained := retainedStorageRun(t, repository, record, run)
	step := retained.Executions[0].Details.(automations.CommandExecutionDetails)
	failure, ok := step.State.(automations.CommandFailed)
	if !ok || failure.Reservation.CommandID != commandID ||
		failure.Reservation.CorrelationID != correlationID || failure.VerifiedCommandID != nil ||
		failure.FailureCode != automations.FailureInvalidCommand {
		t.Fatalf("pre-creation failure = %#v / %#v", step, failure)
	}
}

func TestHistoryRejectsFailedStepWithoutReservation(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), time.Now().UTC())
	reservation := automations.CommandReservation{
		CommandID:     devices.CommandID(newCommandIDString(t)),
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}
	if err := recordStorageCommand(
		t.Context(),
		repository,
		run.ID,
		"light_on",
		reservation,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	// Bypass row checks to prove detail reads still reject malformed typed evidence.
	mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
	mustExec(
		t,
		database,
		`UPDATE automation_step_executions SET phase = 'failed', completed_at = started_at, evidence_json = '{}' WHERE run_id = ? AND step_id = 'light_on'`,
		string(run.ID),
	)
	mustExec(t, database, `PRAGMA ignore_check_constraints = OFF`)
	if _, err := repository.GetHistoryEntry(
		t.Context(),
		record.ID,
		string(run.ID),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("unreserved retained failure = %v, want invalid Automation", err)
	}
}
