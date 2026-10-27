//go:build integration

package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func delayStorageDefinition(t *testing.T) automations.Definition {
	t.Helper()
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{
		{ID: "first", Body: automations.DelayStep{DurationMS: 1001}},
		{ID: "second", Body: automations.DelayStep{DurationMS: 86400000}},
	}
	return definition
}

// A1: direct repository create and replace must independently reject malformed
// typed delay definitions, without modifying an already saved revision.
//
//nolint:gocognit // A boundary table covers create and replace independently with shared input cases.
func TestDelayRepositorySaveValidation(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"valid minimum", "valid maximum", "zero", "negative", "above maximum", "overflow", "missing payload", "pointer payload", "typed nil payload", "duplicate ID", "empty sequence", "too many children", "too many nodes", "too deep"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			definition := delayStorageDefinition(t)
			valid := false
			switch name {
			case "valid minimum":
				definition.Steps[0].Body = automations.DelayStep{DurationMS: 1}
				valid = true
			case "valid maximum":
				definition.Steps[0].Body = automations.DelayStep{DurationMS: 86400000}
				valid = true
			case "zero":
				definition.Steps[0].Body = automations.DelayStep{}
			case "negative":
				definition.Steps[0].Body = automations.DelayStep{DurationMS: -1}
			case "above maximum":
				definition.Steps[0].Body = automations.DelayStep{DurationMS: 86400001}
			case "overflow":
				definition.Steps[0].Body = automations.DelayStep{DurationMS: math.MaxInt64}
			case "missing payload":
				definition.Steps[0].Body = nil
			case "pointer payload":
				definition.Steps[0].Body = &automations.DelayStep{DurationMS: 1}
			case "typed nil payload":
				definition.Steps[0].Body = (*automations.DelayStep)(nil)
			case "duplicate ID":
				definition.Steps[1].ID = definition.Steps[0].ID
			case "empty sequence":
				definition.Steps = []automations.Step{}
			case "too many children", "too many nodes", "too deep":
				definition = invalidDelayBoundsDefinition(t, name)
			}
			ctx := context.Background()
			_, err := repository.CreateAutomation(ctx, definition)
			if valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("create = %v", err)
			}
			original, err := repository.CreateAutomation(ctx, delayStorageDefinition(t))
			if err != nil {
				t.Fatal(err)
			}
			replaced, err := repository.ReplaceAutomation(ctx, original.ID, original.Revision, definition)
			if valid {
				if err != nil || replaced.Revision != 2 {
					t.Fatalf("replace = %#v, %v", replaced, err)
				}
			} else {
				if !errors.Is(err, automations.ErrInvalidAutomation) {
					t.Fatalf("replace = %v", err)
				}
				got, readErr := repository.GetAutomation(ctx, original.ID)
				if readErr != nil || !reflect.DeepEqual(got, original) {
					t.Fatalf("invalid replace changed record: %#v, %v", got, readErr)
				}
			}
		})
	}
}

func invalidDelayBoundsDefinition(t *testing.T, name string) automations.Definition {
	t.Helper()
	definition := delayStorageDefinition(t)
	leaf := func(id string) automations.Step {
		return automations.Step{
			ID:   automations.StepID(id),
			Body: automations.DelayStep{DurationMS: 1},
		}
	}
	condition := automations.Condition{
		ID:   "match",
		Body: automations.TriggerCondition{TriggerIDs: []automations.TriggerID{definition.Triggers[0].ID}},
	}
	branch := func(id string, children []automations.Step) automations.Step {
		return automations.Step{
			ID:   automations.StepID(id),
			Body: automations.IfStep{Conditions: condition, Then: children},
		}
	}
	switch name {
	case "too many children":
		definition.Steps = nil
		for index := range 33 {
			definition.Steps = append(definition.Steps, leaf(fmt.Sprintf("wait-%d", index)))
		}
	case "too many nodes":
		definition.Steps = nil
		for _, prefix := range []string{"a", "b", "c"} {
			children := []automations.Step{}
			for index := range 22 {
				children = append(children, leaf(prefix+string(rune('a'+index))))
			}
			definition.Steps = append(definition.Steps, branch(prefix, children))
		}
	case "too deep":
		step := leaf("bottom")
		for index := range 8 {
			step = branch(string(rune('a'+index)), []automations.Step{step})
		}
		definition.Steps = []automations.Step{step}
	}
	return definition
}

// A3/A9/A10: metadata belongs to the original snapshot, empty evidence is an
// array, clock correction is valid, and pruning protects an active wait.
func TestDelayStorageSnapshotHistoryAndRetention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Date(2026, 10, 4, 12, 0, 0, 123456789, time.UTC)
	record, run := admitStorageRun(t, repository, delayStorageDefinition(t), at)
	initial := retainedStorageRun(t, repository, record, run)
	assertNoInitialDelayEvidence(t, initial)
	if err := recordStorageStep(ctx, repository, run.ID, "first", automations.DelayStarted{StartedAt: at}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := repository.DeleteHistoryBefore(ctx, at.Add(time.Hour), 100); err != nil || deleted != 0 {
		t.Fatalf("active prune = %d, %v", deleted, err)
	}
	completedAt := at.Add(-time.Hour)
	if err := completeStorageStep(
		ctx,
		repository,
		run.ID,
		"first",
		automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: completedAt}},
	); err != nil {
		t.Fatal(err)
	}
	before := retainedStorageRun(t, repository, record, run)
	delay := requireStorageExecution(t, before, "first").Details.(automations.DelayExecutionDetails)
	state, ok := delay.State.(automations.DelaySucceeded)
	if !delay.StartedAt.Equal(at) || !ok || !state.CompletedAt.Equal(completedAt) ||
		automations.RunStateStatus(before.State) != automations.RunRunning {
		t.Fatalf("history = %#v", before)
	}
	replacement := delayStorageDefinition(t)
	replacement.Steps[0].Body = automations.DelayStep{DurationMS: 60000}
	replaced, err := repository.ReplaceAutomation(ctx, record.ID, record.Revision, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
		t.Fatal("replacement changed snapshot evidence")
	}
	if err = repository.DeleteAutomation(ctx, record.ID, replaced.Revision); err != nil {
		t.Fatal(err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
		t.Fatal("deletion changed snapshot evidence")
	}
	secondStarted := at.Add(time.Hour)
	if err = recordStorageStep(
		ctx,
		repository,
		run.ID,
		"second",
		automations.DelayStarted{StartedAt: secondStarted},
	); err != nil {
		t.Fatal(err)
	}
	if err = completeStorageStep(
		ctx,
		repository,
		run.ID,
		"second",
		automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: secondStarted.Add(time.Second)}},
	); err != nil {
		t.Fatal(err)
	}
	if err = repository.CompleteRun(
		ctx,
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	if deleted, pruneErr := repository.DeleteHistoryBefore(
		ctx,
		at.Add(time.Hour),
		100,
	); pruneErr != nil ||
		deleted != 1 {
		t.Fatalf("terminal prune = %d, %v", deleted, pruneErr)
	}
	var remaining int
	if err = database.QueryRow(`SELECT count(*) FROM automation_step_executions WHERE run_id = ?`, string(run.ID)).
		Scan(&remaining); err != nil ||
		remaining != 0 {
		t.Fatalf("cascade = %d, %v", remaining, err)
	}
	var redundant int
	if err = database.QueryRow(`SELECT count(*) FROM pragma_table_info('automation_step_executions') WHERE name IN ('duration_ms', 'due_at')`).
		Scan(&redundant); err != nil ||
		redundant != 0 {
		t.Fatalf("redundant columns = %d, %v", redundant, err)
	}
}

func assertNoInitialDelayEvidence(t *testing.T, initial automations.Run) {
	t.Helper()
	if len(initial.Executions) != 0 {
		t.Fatalf("initial = %#v", initial)
	}
}

// A3: malformed starts cannot create reached-wait evidence.
//
//nolint:paralleltest,tparallel // Rejected starts deliberately share a pristine Run.
func TestDelayStorageRejectsInvalidStarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Now().UTC()
	definition := delayStorageDefinition(t)
	definition.Steps = append(definition.Steps, validDomainDefinition(t).Steps[0])
	record, run := admitStorageRun(t, repository, definition, at)
	start := automations.DelayStarted{StartedAt: at}
	pristine := retainedStorageRun(t, repository, record, run)
	for _, name := range []string{"missing parent", "invalid run ID", "invalid Step ID", "missing Step", "Command Step", "zero time", "non UTC"} {
		t.Run(name, func(t *testing.T) {
			input := start
			runID, stepID := run.ID, automations.StepID("first")
			switch name {
			case "missing parent":
				runID = automations.RunID(newRunIDString(t))
			case "invalid run ID":
				runID = "bad"
			case "invalid Step ID":
				stepID = ""
			case "missing Step":
				stepID = "absent"
			case "Command Step":
				stepID = "light_on"
			case "zero time":
				input.StartedAt = time.Time{}
			case "non UTC":
				input.StartedAt = at.In(time.FixedZone("offset", 3600))
			}
			if err := recordStorageStep(
				ctx,
				repository,
				runID,
				stepID,
				input,
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("start = %v", err)
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, pristine) {
				t.Fatal("invalid start changed history")
			}
		})
	}
}

// A3: rejected append/completion/parent transitions leave all evidence unchanged.
//
//nolint:gocognit,paralleltest,tparallel // One ordered transition sequence deliberately shares a Run.
func TestDelayStorageRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repository, delayStorageDefinition(t), at)
	start := automations.DelayStarted{StartedAt: at}
	if err := recordStorageStep(ctx, repository, run.ID, "first", start); err != nil {
		t.Fatal(err)
	}
	original := retainedStorageRun(t, repository, record, run)
	for _, stepID := range []automations.StepID{"first", "second"} {
		if err := recordStorageStep(
			ctx,
			repository,
			run.ID,
			stepID,
			start,
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Fatalf("duplicate/overlapping start = %v", err)
		}
	}
	for _, outcome := range []automations.RunOutcome{
		automations.SucceededRun{}, automations.InterruptedRun{FailureCode: "executor_fault"},
		automations.FailedRun{FailureCode: "executor_fault"},
	} {
		input := automations.RunCompletion{RunID: run.ID, Outcome: outcome}
		if err := repository.CompleteRun(ctx, input); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("terminal parent = %v", err)
		}
	}
	for _, name := range []string{"unreached", "wrong variant", "zero time", "non UTC", "invalid reason"} {
		t.Run(name, func(t *testing.T) {
			stepID := automations.StepID("first")
			var finished automations.DelayFinished
			switch name {
			case "unreached":
				stepID = "second"
			case "wrong variant":
			case "zero time":
				finished.State = automations.DelaySucceeded{CompletedAt: time.Time{}}
			case "non UTC":
				finished.State = automations.DelaySucceeded{CompletedAt: at.In(time.FixedZone("offset", 3600))}
			case "invalid reason":
				finished.State = automations.DelayInterrupted{CompletedAt: at, FailureCode: "unknown"}
			}
			var err error
			if name == "wrong variant" {
				err = completeStorageStep(ctx, repository, run.ID, stepID, automations.CommandFinished{})
			} else {
				err = completeStorageStep(ctx, repository, run.ID, stepID, finished)
			}
			if !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("completion = %v", err)
			}
		})
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, original) {
		t.Fatal("rejected transitions changed history")
	}
	completion := automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: at}}
	if err := completeStorageStep(ctx, repository, run.ID, "first", completion); err != nil {
		t.Fatal(err)
	}
	if err := completeStorageStep(
		ctx,
		repository,
		run.ID,
		"first",
		completion,
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("duplicate completion = %v", err)
	}
	if err := recordStorageStep(ctx, repository, run.ID, "first", start); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("repeat Step = %v", err)
	}
	if err := recordStorageStep(
		ctx,
		repository,
		run.ID,
		"second",
		automations.DelayStarted{StartedAt: at.Add(-time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	reason := "core_stopping"
	completion = automations.DelayFinished{State: automations.DelayInterrupted{CompletedAt: at, FailureCode: reason}}
	if err := completeStorageStep(ctx, repository, run.ID, "second", completion); err != nil {
		t.Fatal(err)
	}
	terminal := retainedStorageRun(t, repository, record, run)
	second := requireStorageExecution(t, terminal, "second").Details.(automations.DelayExecutionDetails)
	interrupted, ok := second.State.(automations.DelayInterrupted)
	if !ok || interrupted.FailureCode != reason || !interrupted.CompletedAt.Equal(at) {
		t.Fatalf("interrupted Delay execution = %#v", second)
	}
	if err := recordStorageStep(
		ctx,
		repository,
		run.ID,
		"first",
		start,
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("terminal start = %v", err)
	}
	if err := completeStorageStep(
		ctx,
		repository,
		run.ID,
		"second",
		completion,
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("terminal completion = %v", err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, terminal) {
		t.Fatal("terminal write changed history")
	}
}

// A3/A6: an injected second-write failure proves both atomic interruption and
// startup recovery roll back, including in-flight Command evidence.
//
//nolint:gocognit // The same real transaction scenario checks rollback and terminal preservation for each reason.
func TestDelayStorageAtomicInterruptionAndRecovery(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"core_stopping", "executor_fault", "core_restarted"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			at := time.Now().UTC()
			record, run := admitStorageRun(t, repository, delayStorageDefinition(t), at)
			if err := recordStorageStep(
				ctx,
				repository,
				run.ID,
				"first",
				automations.DelayStarted{StartedAt: at},
			); err != nil {
				t.Fatal(err)
			}
			if err := completeStorageStep(
				ctx,
				repository,
				run.ID,
				"first",
				automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: at}},
			); err != nil {
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
			original := retainedStorageRun(t, repository, record, run)
			var commandRecord automations.Record
			var commandRun automations.Run
			var originalCommand automations.Run
			if reason == "core_restarted" {
				commandRecord, commandRun = admitStorageRun(t, repository, validDomainDefinition(t), at)
				reservation := automations.CommandReservation{
					CommandID:     devices.CommandID(newCommandIDString(t)),
					CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
				}
				if err := recordStorageStep(ctx, repository, commandRun.ID, "light_on", automations.CommandStarted{
					StartedAt: at, Reservation: reservation,
				}); err != nil {
					t.Fatal(err)
				}
				originalCommand = retainedStorageRun(t, repository, commandRecord, commandRun)
			}
			mustExec(
				t,
				database,
				`CREATE TRIGGER reject_parent BEFORE UPDATE ON automation_history BEGIN SELECT RAISE(ABORT, 'injected parent failure'); END`,
			)
			completionAt := at.Add(48 * time.Hour)
			interrupt := func() error {
				if reason == "core_restarted" {
					return repository.InterruptActiveRuns(ctx, completionAt, reason)
				}
				return completeStorageStep(
					ctx,
					repository,
					run.ID,
					"second",
					automations.DelayFinished{
						State: automations.DelayInterrupted{CompletedAt: completionAt, FailureCode: reason},
					},
				)
			}
			if err := interrupt(); err == nil {
				t.Fatal("injected parent failure succeeded")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, original) {
				t.Fatal("failed interruption changed evidence")
			}
			mustExec(t, database, `DROP TRIGGER reject_parent`)
			if reason == "core_restarted" {
				mustExec(
					t,
					database,
					`CREATE TRIGGER reject_recovery_step BEFORE UPDATE ON automation_step_executions BEGIN SELECT RAISE(ABORT, 'injected Step recovery failure'); END`,
				)
				if err := interrupt(); err == nil {
					t.Fatal("injected recovery Step failure succeeded")
				}
				if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, original) {
					t.Fatal("failed Step recovery changed wait or parent")
				}
				if got := retainedStorageRun(
					t,
					repository,
					commandRecord,
					commandRun,
				); !reflect.DeepEqual(
					got,
					originalCommand,
				) {
					t.Fatal("failed Step recovery changed Command or parent")
				}
				mustExec(t, database, `DROP TRIGGER reject_recovery_step`)
			}
			if err := interrupt(); err != nil {
				t.Fatal(err)
			}
			got := retainedStorageRun(t, repository, record, run)
			assertStorageRunInterrupted(t, got, completionAt, reason)
			last := requireStorageExecution(t, got, "second").Details.(automations.DelayExecutionDetails)
			assertStorageDelayInterrupted(t, last, completionAt, reason)
			if !reflect.DeepEqual(got.Executions[0], original.Executions[0]) {
				t.Fatal("interruption rewrote completed delay")
			}
			if reason == "core_restarted" {
				recovered := retainedStorageRun(t, repository, commandRecord, commandRun)
				assertStorageRunInterrupted(t, recovered, completionAt, reason)
				step := requireStorageExecution(t, recovered, "light_on")
				command := step.Details.(automations.CommandExecutionDetails)
				interrupted, ok := command.State.(automations.CommandInterrupted)
				if !ok || interrupted.FailureCode != reason || !interrupted.CompletedAt.Equal(completionAt) {
					t.Fatalf("recovered Command execution = %#v", recovered)
				}
			}
			if err := repository.InterruptActiveRuns(ctx, completionAt.Add(time.Hour), "core_restarted"); err != nil {
				t.Fatal(err)
			}
			if again := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(again, got) {
				t.Fatal("recovery rewrote terminal evidence")
			}
		})
	}
}

func assertStorageRunInterrupted(t *testing.T, run automations.Run, at time.Time, reason string) {
	t.Helper()
	if automations.RunStateStatus(run.State) != automations.RunInterrupted || runCompleted(run.State) == nil ||
		!runCompleted(run.State).Equal(at) || runFailure(run.State) == nil || *runFailure(run.State) != reason {
		t.Fatalf("interrupted Run = %#v", run)
	}
}

func assertStorageDelayInterrupted(t *testing.T, delay automations.DelayExecutionDetails, at time.Time, reason string) {
	t.Helper()
	state, ok := delay.State.(automations.DelayInterrupted)
	if !ok || !state.CompletedAt.Equal(at) || state.FailureCode != reason {
		t.Fatalf("interrupted delay = %#v", delay)
	}
}

// A3: insert and completion storage errors cannot report durable success.
func TestDelayStorageWriteFailuresRollback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repository, delayStorageDefinition(t), at)
	pristine := retainedStorageRun(t, repository, record, run)
	mustExec(
		t,
		database,
		`CREATE TRIGGER reject_delay_insert BEFORE INSERT ON automation_step_executions BEGIN SELECT RAISE(ABORT, 'injected start failure'); END`,
	)
	start := automations.DelayStarted{StartedAt: at}
	if err := recordStorageStep(ctx, repository, run.ID, "first", start); err == nil {
		t.Fatal("injected insert succeeded")
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, pristine) {
		t.Fatal("failed insert changed Run")
	}
	mustExec(t, database, `DROP TRIGGER reject_delay_insert`)
	if err := recordStorageStep(ctx, repository, run.ID, "first", start); err != nil {
		t.Fatal(err)
	}
	original := retainedStorageRun(t, repository, record, run)
	mustExec(
		t,
		database,
		`CREATE TRIGGER reject_delay_update BEFORE UPDATE ON automation_step_executions BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END`,
	)
	if err := completeStorageStep(
		ctx,
		repository,
		run.ID,
		"first",
		automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: at}},
	); err == nil {
		t.Fatal("injected completion succeeded")
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, original) {
		t.Fatal("failed completion changed Run")
	}
}

// A3: retained corruption fails reads instead of inventing metadata or success.
func TestDelayStorageRejectsCorruptHistory(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"missing Step", "invalid Step ID", "non delay Step", "position gap", "bad start", "zero start", "offset start", "bad completion", "zero completion", "running completion", "running failure", "completed no completion", "completed failure", "interrupted no reason", "interrupted bad reason", "unknown status", "terminal parent", "invalid snapshot", "invalid snapshot duration"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			at := time.Now().UTC()
			definition := delayStorageDefinition(t)
			definition.Steps = append(definition.Steps, validDomainDefinition(t).Steps[0])
			record, run := admitStorageRun(t, repository, definition, at)
			if err := recordStorageStep(
				ctx,
				repository,
				run.ID,
				"first",
				automations.DelayStarted{StartedAt: at},
			); err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
			statement := ""
			switch name {
			case "missing Step":
				statement = `UPDATE automation_step_executions SET step_id = 'absent' WHERE run_id = ?`
			case "invalid Step ID":
				statement = `UPDATE automation_step_executions SET step_id = '' WHERE run_id = ?`
			case "non delay Step":
				statement = `UPDATE automation_step_executions SET kind = 'command' WHERE run_id = ?`
			case "position gap":
				statement = `UPDATE automation_step_executions SET sequence = 1 WHERE run_id = ?`
			case "bad start":
				statement = `UPDATE automation_step_executions SET started_at = 'bad' WHERE run_id = ?`
			case "zero start":
				statement = `UPDATE automation_step_executions SET started_at = '0001-01-01T00:00:00.000000000Z' WHERE run_id = ?`
			case "offset start":
				statement = `UPDATE automation_step_executions SET started_at = '2026-10-04T00:00:00+01:00' WHERE run_id = ?`
			case "bad completion":
				statement = `UPDATE automation_step_executions SET phase = 'succeeded', completed_at = 'bad' WHERE run_id = ?`
			case "zero completion":
				statement = `UPDATE automation_step_executions SET phase = 'succeeded', completed_at = '0001-01-01T00:00:00.000000000Z' WHERE run_id = ?`
			case "running completion":
				statement = `UPDATE automation_step_executions SET completed_at = started_at WHERE run_id = ?`
			case "running failure":
				statement = `UPDATE automation_step_executions SET evidence_json = '{"reason_code":"executor_fault"}' WHERE run_id = ?`
			case "completed no completion":
				statement = `UPDATE automation_step_executions SET phase = 'succeeded' WHERE run_id = ?`
			case "completed failure":
				statement = `UPDATE automation_step_executions SET phase = 'succeeded', completed_at = started_at, evidence_json = '{"reason_code":"executor_fault"}' WHERE run_id = ?`
			case "interrupted no reason":
				statement = `UPDATE automation_step_executions SET phase = 'interrupted', completed_at = started_at WHERE run_id = ?`
			case "interrupted bad reason":
				statement = `UPDATE automation_step_executions SET phase = 'interrupted', completed_at = started_at, evidence_json = '{"reason_code":"bad"}' WHERE run_id = ?`
			case "unknown status":
				statement = `UPDATE automation_step_executions SET phase = 'bad', completed_at = started_at WHERE run_id = ?`
			case "terminal parent":
				statement = `UPDATE automation_history SET run_status = 'succeeded', run_completed_at = run_started_at`
			case "invalid snapshot":
				allowDefinitionRevisionCorruption(t, database)
				statement = `UPDATE automation_definition_revisions SET definition_json = '{}'`
			case "invalid snapshot duration":
				allowDefinitionRevisionCorruption(t, database)
				statement = `UPDATE automation_definition_revisions SET definition_json = replace(definition_json, '"duration_ms":{"literal":1001}', '"duration_ms":{"literal":0}')`
			}
			mustExec(t, database, statement, string(run.ID))
			if _, err := repository.GetHistoryEntry(ctx, record.ID, string(run.ID)); err == nil {
				t.Fatal("corrupt history read succeeded")
			}
		})
	}
}
