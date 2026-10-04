package sqlite_test

import (
	"context"
	"errors"
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
	if err := repository.MarkStepRunning(ctx, automations.StepStart{
		RunID: run.ID, Position: 0, CommandID: commandID,
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}); err != nil {
		t.Fatal(err)
	}
	if interrupt {
		// The supplied timestamp must win over the repository clock too.
		if err := repository.InterruptActiveRuns(ctx, completedAt, automations.FailureCoreRestarted); err != nil {
			t.Fatal(err)
		}
	} else {
		now = completedAt
		if err := repository.CompleteStep(ctx, automations.StepCompletion{
			RunID: run.ID, Position: 0, Outcome: automations.SatisfiedStep{VerifiedCommandID: commandID},
		}); err != nil {
			t.Fatal(err)
		}
		if err := repository.CompleteRun(ctx, automations.RunCompletion{
			RunID: run.ID, Outcome: automations.SucceededRun{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	retained := retainedStorageRun(t, repository, record, run)
	terminal := retained.State.(automations.CompletedRun)
	step := retained.Steps[0].State.(automations.CompletedStep)
	if !retained.StartedAt.Equal(runStarted) || !terminal.CompletedAt.Equal(completedAt) ||
		!step.StartedAt.Equal(stepStarted) || !step.CompletedAt.Equal(completedAt) {
		t.Fatalf("retained timestamps = Run %v/%v, Step %v/%v; want %v/%v, %v/%v",
			retained.StartedAt, terminal.CompletedAt, step.StartedAt, step.CompletedAt,
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
	completion := automations.StepCompletion{
		RunID: run.ID, Position: 0, Outcome: automations.FailedStep{FailureCode: automations.FailureInvalidCommand},
	}
	if err := repository.CompleteStep(ctx, completion); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("unreserved failure = %v, want invalid Automation", err)
	}
	commandID := devices.CommandID(newCommandIDString(t))
	correlationID := devices.CorrelationID(newCorrelationIDString(t))
	if err := repository.MarkStepRunning(ctx, automations.StepStart{
		RunID: run.ID, Position: 0, CommandID: commandID, CorrelationID: correlationID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteStep(ctx, completion); err != nil {
		t.Fatal(err)
	}
	retained := retainedStorageRun(t, repository, record, run)
	step := retained.Steps[0].State.(automations.CompletedStep)
	failure := step.Outcome.(automations.FailedStep)
	if step.Reservation == nil || step.Reservation.CommandID != commandID ||
		step.Reservation.CorrelationID != correlationID || failure.VerifiedCommandID != nil ||
		failure.FailureCode != automations.FailureInvalidCommand {
		t.Fatalf("pre-creation failure = %#v / %#v", step, failure)
	}
}

func TestHistoryRejectsFailedStepWithoutReservation(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), time.Now().UTC())
	// This combination satisfies the baseline SQL constraint but violates the typed lifecycle.
	mustExec(t, database, `UPDATE automation_run_steps SET status = 'failed', failure_code = ?,
		started_at = ?, completed_at = ? WHERE run_id = ? AND position = 0`,
		automations.FailureInvalidCommand, migrationTimestamp, migrationTimestamp, string(run.ID))
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
