package sqlite_test

import (
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
	repository automations.Repository,
	id automations.RunID,
	position int,
) devices.CommandID {
	t.Helper()
	commandID := devices.CommandID(newCommandIDString(t))
	if err := repository.MarkStepRunning(t.Context(), automations.StepStart{
		RunID: id, Position: position, CommandID: commandID,
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}); err != nil {
		t.Fatal(err)
	}
	return commandID
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
	firstID := reserveScopedStorageCommand(t, repository, run.ID, 0)
	if err := repository.CompleteStep(ctx, automations.StepCompletion{
		RunID: run.ID, Position: 0, Outcome: automations.SatisfiedStep{VerifiedCommandID: firstID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordDelayStart(
		ctx,
		automations.DelayStart{RunID: run.ID, StepID: "first", StartedAt: at},
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteDelay(ctx, automations.DelayCompletion{
		RunID: run.ID, StepID: "first", Status: automations.DelayCompleted, CompletedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	secondID := reserveScopedStorageCommand(t, repository, run.ID, 1)
	if active == "delay" {
		if err := repository.CompleteStep(ctx, automations.StepCompletion{
			RunID: run.ID, Position: 1, Outcome: automations.DispatchedStep{VerifiedCommandID: secondID},
		}); err != nil {
			t.Fatal(err)
		}
		if err := repository.RecordDelayStart(ctx, automations.DelayStart{
			RunID: run.ID, StepID: "second", Position: 1, StartedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return record, run, secondID
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
	if !reflect.DeepEqual(got.Steps[0], before.Steps[0]) || !reflect.DeepEqual(got.Delays[0], before.Delays[0]) {
		t.Fatal("scoped cleanup rewrote terminal evidence")
	}
	if active == "command" {
		completed := got.Steps[1].State.(automations.CompletedStep)
		outcome := completed.Outcome.(automations.InterruptedStep)
		if completed.Reservation == nil || completed.Reservation.CommandID != secondID ||
			*completed.Reservation != before.Steps[1].State.(automations.RunningStep).Reservation ||
			outcome.VerifiedCommandID != nil ||
			outcome.FailureCode != automations.FailureExecutorFault {
			t.Fatalf("uncertain reservation became Command success: %#v", completed)
		}
		return
	}
	assertStorageDelayInterrupted(t, got.Delays[1], at, automations.FailureExecutorFault)
	if !reflect.DeepEqual(got.Steps[1], before.Steps[1]) {
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
	commandID devices.CommandID,
	at time.Time,
) {
	t.Helper()
	if err := repository.CompleteStep(
		t.Context(),
		automations.StepCompletion{
			RunID:    run.ID,
			Position: 0,
			Outcome:  automations.SatisfiedStep{VerifiedCommandID: commandID},
		},
	); err != nil {
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
