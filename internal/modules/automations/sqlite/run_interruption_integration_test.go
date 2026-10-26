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

// ADR 0026: concrete transactions must roll back all scoped evidence on failure,
// preserve reservations and terminal outcomes, and leave siblings untouched.
func TestInterruptRunAtomicScopedAndIdempotent(t *testing.T) {
	t.Parallel()
	for _, active := range []string{"command", "delay"} {
		t.Run(active, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			at := time.Now().UTC()
			record, run, secondID := prepareScopedInterruptionRun(t, repository, at, active)
			siblingRecord, sibling := admitStorageRun(t, repository, validDomainDefinition(t), at)
			siblingID := reserveScopedStorageCommand(t, repository, sibling.ID, 0)
			before := retainedStorageRun(t, repository, record, run)
			siblingBefore := retainedStorageRun(t, repository, siblingRecord, sibling)
			mustExec(
				t,
				database,
				`CREATE TRIGGER reject_scoped_parent BEFORE UPDATE ON automation_history BEGIN SELECT RAISE(ABORT, 'parent unavailable'); END`,
			)
			completedAt := at.Add(time.Hour)
			if err := repository.InterruptRun(ctx, run.ID, completedAt); err == nil {
				t.Fatal("parent failure was ignored")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatal("failed transaction partially interrupted evidence")
			}
			mustExec(t, database, `DROP TRIGGER reject_scoped_parent`)
			if err := repository.InterruptRun(ctx, run.ID, completedAt); err != nil {
				t.Fatal(err)
			}
			got := retainedStorageRun(t, repository, record, run)
			assertScopedInterruptionEvidence(t, got, before, secondID, completedAt, active)
			if siblingAfter := retainedStorageRun(
				t,
				repository,
				siblingRecord,
				sibling,
			); !reflect.DeepEqual(
				siblingAfter,
				siblingBefore,
			) {
				t.Fatal("scoped cleanup interrupted sibling")
			}
			assertScopedInterruptionIdempotent(t, repository, record, got, completedAt)
			assertScopedTerminalRunPreserved(t, repository, siblingRecord, sibling, siblingID, completedAt)
		})
	}
}

func reserveScopedStorageCommand(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	id automations.RunID,
	position int,
) automations.CommandReservation {
	t.Helper()
	commandID := devices.CommandID(newCommandIDString(t))
	stepID := automations.StepID("light_on")
	if position > 0 {
		stepID = "other_command"
	}
	reservation := automations.CommandReservation{
		CommandID:     commandID,
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}
	if err := recordStorageCommand(t.Context(), repository, id, stepID, reservation, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return reservation
}

func prepareScopedInterruptionRun(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	at time.Time,
	active string,
) (automations.Record, automations.Run, devices.CommandID) {
	t.Helper()
	ctx := t.Context()
	definition := delayStorageDefinition(t)
	command := validDomainDefinition(t).Steps[0]
	other := command
	other.ID = "other_command"
	definition.Steps = []automations.Step{command, definition.Steps[0], other, definition.Steps[1]}
	record, run := admitStorageRun(t, repository, definition, at)
	first := reserveScopedStorageCommand(t, repository, run.ID, 0)
	if err := completeStorageStep(ctx, repository, run.ID, "light_on", automations.CommandFinished{
		CompletedAt: at, Reservation: first, Outcome: automations.SatisfiedStep{VerifiedCommandID: first.CommandID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := recordStorageStep(ctx, repository, run.ID, "first", automations.DelayStarted{StartedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := completeStorageStep(
		ctx,
		repository,
		run.ID,
		"first",
		automations.DelayFinished{CompletedAt: at},
	); err != nil {
		t.Fatal(err)
	}
	second := reserveScopedStorageCommand(t, repository, run.ID, 1)
	if active == "delay" {
		if err := completeStorageStep(ctx, repository, run.ID, "other_command", automations.CommandFinished{
			CompletedAt: at,
			Reservation: second,
			Outcome:     automations.DispatchedStep{VerifiedCommandID: second.CommandID},
		}); err != nil {
			t.Fatal(err)
		}
		if err := recordStorageStep(
			ctx,
			repository,
			run.ID,
			"second",
			automations.DelayStarted{StartedAt: at},
		); err != nil {
			t.Fatal(err)
		}
	}
	return record, run, second.CommandID
}

func assertScopedInterruptionEvidence(
	t *testing.T,
	got, before automations.Run,
	secondID devices.CommandID,
	at time.Time,
	active string,
) {
	t.Helper()
	assertStorageRunInterrupted(t, got, at, automations.FailureExecutorFault)
	if !reflect.DeepEqual(
		requireStorageExecution(t, got, "light_on"),
		requireStorageExecution(t, before, "light_on"),
	) ||
		!reflect.DeepEqual(requireStorageExecution(t, got, "first"), requireStorageExecution(t, before, "first")) {
		t.Fatal("scoped cleanup rewrote terminal evidence")
	}
	if active == "command" {
		completed := requireStorageExecution(t, got, "other_command").Details.(automations.CommandExecutionDetails)
		beforeRunning := requireStorageExecution(t, before, "other_command").Details.(automations.CommandExecutionDetails)
		verified, failure := automations.StepOutcomeEvidence(completed.Outcome)
		if completed.Reservation == nil || completed.Reservation.CommandID != secondID ||
			beforeRunning.Reservation == nil || *completed.Reservation != *beforeRunning.Reservation ||
			verified != nil || failure == nil || *failure != automations.FailureExecutorFault {
			t.Fatalf("uncertain reservation became Command success: %#v", completed)
		}
		return
	}
	assertStorageDelayInterrupted(
		t,
		requireStorageExecution(t, got, "second").Details.(automations.DelayExecutionDetails),
		at,
		automations.FailureExecutorFault,
	)
	if !reflect.DeepEqual(
		requireStorageExecution(t, got, "other_command"),
		requireStorageExecution(t, before, "other_command"),
	) {
		t.Fatal("delay cleanup rewrote completed Command")
	}
}

func assertScopedInterruptionIdempotent(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	record automations.Record,
	got automations.Run,
	at time.Time,
) {
	t.Helper()
	if err := repository.InterruptRun(t.Context(), got.ID, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	absent, err := automations.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.InterruptRun(t.Context(), absent, at); err != nil {
		t.Fatalf("absent Run interruption = %v", err)
	}
	if again := retainedStorageRun(t, repository, record, got); !reflect.DeepEqual(again, got) {
		t.Fatal("repeated cleanup rewrote terminal history")
	}
}

func assertScopedTerminalRunPreserved(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	record automations.Record,
	run automations.Run,
	reservation automations.CommandReservation,
	at time.Time,
) {
	t.Helper()
	if err := completeStorageStep(t.Context(), repository, run.ID, "light_on", automations.CommandFinished{
		CompletedAt: run.StartedAt,
		Reservation: reservation,
		Outcome:     automations.SatisfiedStep{VerifiedCommandID: reservation.CommandID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	terminal := retainedStorageRun(t, repository, record, run)
	if err := repository.InterruptRun(t.Context(), run.ID, at); err != nil {
		t.Fatal(err)
	}
	if after := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(after, terminal) {
		t.Fatal("cleanup overwrote already-successful Run")
	}
}

func TestStartupInterruptionRejectsUnsupportedReasonWithoutChangingEvidence(t *testing.T) {
	t.Parallel()
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{{ID: "wait", Body: automations.DelayStep{DurationMS: 1}}}
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repository, definition, at)
	if err := repository.RecordStep(
		t.Context(),
		automations.StepExecutionRecord{
			RunID:    run.ID,
			StepID:   "wait",
			Evidence: automations.DelayStarted{StartedAt: at},
		},
	); err != nil {
		t.Fatal(err)
	}
	before := historyEntry(t, repository, record.ID, string(run.ID))
	for _, reason := range []string{"", "provider_rejected", "arbitrary_reason"} {
		if err := repository.InterruptActiveRuns(
			t.Context(),
			at,
			reason,
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Fatalf("reason %q = %v", reason, err)
		}
		if after := historyEntry(t, repository, record.ID, string(run.ID)); !reflect.DeepEqual(after, before) {
			t.Fatalf("invalid reason %q changed evidence", reason)
		}
	}
	if err := repository.InterruptActiveRuns(t.Context(), at, automations.FailureCoreRestarted); err != nil {
		t.Fatal(err)
	}
	retained := historyEntry(t, repository, record.ID, string(run.ID)).(automations.Run)
	if automations.RunStateStatus(retained.State) != automations.RunInterrupted {
		t.Fatalf("valid startup interruption = %#v", retained)
	}
}
