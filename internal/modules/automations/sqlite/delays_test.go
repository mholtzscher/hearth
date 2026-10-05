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
		{ID: "first", Kind: automations.StepKindDelay, Delay: &automations.DelayStep{DurationMS: 1001}},
		{ID: "second", Kind: automations.StepKindDelay, Delay: &automations.DelayStep{DurationMS: 86400000}},
	}
	return definition
}

func nestedDelayStorageDefinition(t *testing.T) automations.Definition {
	t.Helper()
	definition := delayStorageDefinition(t)
	condition := automations.Condition{ID: "matched", Kind: automations.ConditionTrigger,
		Trigger: &automations.TriggerCondition{TriggerIDs: []automations.TriggerID{definition.Triggers[0].ID}}}
	first, second := definition.Steps[0], definition.Steps[1]
	definition.Steps = []automations.Step{
		{ID: "if", Kind: automations.StepKindIf, If: &automations.IfStep{
			Conditions: condition,
			Then: []automations.Step{
				{ID: "unselected", Kind: automations.StepKindDelay, Delay: &automations.DelayStep{DurationMS: 1}},
			},
			Else: []automations.Step{first},
		}},
		{ID: "choose", Kind: automations.StepKindChoose, Choose: &automations.ChooseStep{
			Branches: []automations.ChooseBranch{
				{
					ID:         "a",
					Conditions: condition,
					Steps: []automations.Step{
						{ID: "unchosen", Kind: automations.StepKindDelay, Delay: &automations.DelayStep{DurationMS: 1}},
					},
				},
			},
			Default: []automations.Step{second},
		}},
	}
	return definition
}

// A1: direct repository create and replace must independently reject malformed
// typed delay definitions, without modifying an already saved revision.
//
//nolint:gocognit // A boundary table covers create and replace independently with shared input cases.
func TestDelayRepositorySaveValidation(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"valid minimum", "valid maximum", "zero", "negative", "above maximum", "overflow", "missing payload", "mixed family", "Command with delay", "If with delay", "Choose with delay", "duplicate ID", "empty sequence", "too many children", "too many nodes", "too deep"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			definition := delayStorageDefinition(t)
			valid := false
			switch name {
			case "valid minimum":
				definition.Steps[0].Delay.DurationMS = 1
				valid = true
			case "valid maximum":
				definition.Steps[0].Delay.DurationMS = 86400000
				valid = true
			case "zero":
				definition.Steps[0].Delay.DurationMS = 0
			case "negative":
				definition.Steps[0].Delay.DurationMS = -1
			case "above maximum":
				definition.Steps[0].Delay.DurationMS = 86400001
			case "overflow":
				definition.Steps[0].Delay.DurationMS = math.MaxInt64
			case "missing payload":
				definition.Steps[0].Delay = nil
			case "mixed family":
				definition.Steps[0].EntityID = validDomainDefinition(t).Steps[0].EntityID
			case "Command with delay":
				definition = validDomainDefinition(t)
				definition.Steps[0].Delay = &automations.DelayStep{DurationMS: 1}
			case "If with delay":
				definition = nestedDelayStorageDefinition(t)
				definition.Steps[0].Delay = &automations.DelayStep{DurationMS: 1}
			case "Choose with delay":
				definition = nestedDelayStorageDefinition(t)
				definition.Steps[1].Delay = &automations.DelayStep{DurationMS: 1}
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
			ID:    automations.StepID(id),
			Kind:  automations.StepKindDelay,
			Delay: &automations.DelayStep{DurationMS: 1},
		}
	}
	condition := automations.Condition{
		ID:      "match",
		Kind:    automations.ConditionTrigger,
		Trigger: &automations.TriggerCondition{TriggerIDs: []automations.TriggerID{definition.Triggers[0].ID}},
	}
	branch := func(id string, children []automations.Step) automations.Step {
		return automations.Step{
			ID:   automations.StepID(id),
			Kind: automations.StepKindIf,
			If:   &automations.IfStep{Conditions: condition, Then: children},
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
	record, run := admitStorageRun(t, repository, nestedDelayStorageDefinition(t), at)
	initial := retainedStorageRun(t, repository, record, run)
	if initial.Delays == nil || len(initial.Delays) != 0 || len(initial.Steps) != 0 {
		t.Fatalf("initial = %#v", initial)
	}
	if err := repository.RecordDelayStart(
		ctx,
		automations.DelayStart{RunID: run.ID, StepID: "first", Position: 0, StartedAt: at},
	); err != nil {
		t.Fatal(err)
	}
	if deleted, err := repository.DeleteHistoryBefore(ctx, at.Add(time.Hour), 100); err != nil || deleted != 0 {
		t.Fatalf("active prune = %d, %v", deleted, err)
	}
	completedAt := at.Add(-time.Hour)
	if err := repository.CompleteDelay(
		ctx,
		automations.DelayCompletion{
			RunID:       run.ID,
			StepID:      "first",
			Status:      automations.DelayCompleted,
			CompletedAt: completedAt,
		},
	); err != nil {
		t.Fatal(err)
	}
	before := retainedStorageRun(t, repository, record, run)
	delay := before.Delays[0]
	if delay.DurationMS != 1001 || !delay.StartedAt.Equal(at) || !delay.DueAt.Equal(at.Add(1001*time.Millisecond)) ||
		delay.CompletedAt == nil ||
		!delay.CompletedAt.Equal(completedAt) ||
		delay.Status != automations.DelayCompleted ||
		delay.FailureCode != nil ||
		before.Status != automations.RunRunning {
		t.Fatalf("history = %#v", before)
	}
	replacement := delayStorageDefinition(t)
	replacement.Steps[0].Delay.DurationMS = 60000
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
	if err = repository.CompleteRun(
		ctx,
		automations.RunCompletion{RunID: run.ID, Status: automations.RunSucceeded},
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
	if err = database.QueryRow(`SELECT count(*) FROM automation_run_delays`).
		Scan(&remaining); err != nil ||
		remaining != 0 {
		t.Fatalf("cascade = %d, %v", remaining, err)
	}
	var redundant int
	if err = database.QueryRow(`SELECT count(*) FROM pragma_table_info('automation_run_delays') WHERE name IN ('duration_ms', 'due_at')`).
		Scan(&redundant); err != nil ||
		redundant != 0 {
		t.Fatalf("redundant columns = %d, %v", redundant, err)
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
	start := automations.DelayStart{RunID: run.ID, StepID: "first", StartedAt: at}
	pristine := retainedStorageRun(t, repository, record, run)
	for _, name := range []string{"missing parent", "invalid run ID", "invalid Step ID", "missing Step", "Command Step", "negative position", "position gap", "position overflow", "zero time", "non UTC"} {
		t.Run(name, func(t *testing.T) {
			input := start
			switch name {
			case "missing parent":
				input.RunID = automations.RunID(newRunIDString(t))
			case "invalid run ID":
				input.RunID = "bad"
			case "invalid Step ID":
				input.StepID = ""
			case "missing Step":
				input.StepID = "absent"
			case "Command Step":
				input.StepID = "light_on"
			case "negative position":
				input.Position = -1
			case "position gap":
				input.Position = 1
			case "position overflow":
				input.Position = 64
			case "zero time":
				input.StartedAt = time.Time{}
			case "non UTC":
				input.StartedAt = at.In(time.FixedZone("offset", 3600))
			}
			if err := repository.RecordDelayStart(ctx, input); !errors.Is(err, automations.ErrInvalidAutomation) {
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
	start := automations.DelayStart{RunID: run.ID, StepID: "first", StartedAt: at}
	if err := repository.RecordDelayStart(ctx, start); err != nil {
		t.Fatal(err)
	}
	original := retainedStorageRun(t, repository, record, run)
	for _, input := range []automations.DelayStart{start, {RunID: run.ID, StepID: "second", Position: 1, StartedAt: at}} {
		if err := repository.RecordDelayStart(ctx, input); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("duplicate/overlapping start = %v", err)
		}
	}
	for _, status := range []automations.RunStatus{automations.RunSucceeded, automations.RunInterrupted, automations.RunFailed} {
		input := automations.RunCompletion{RunID: run.ID, Status: status}
		if status != automations.RunSucceeded {
			reason := "executor_fault"
			input.FailureCode = &reason
		}
		if err := repository.CompleteRun(ctx, input); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("terminal parent = %v", err)
		}
	}
	for _, name := range []string{"unreached", "running status", "zero time", "non UTC", "completed with failure", "interrupted without reason", "invalid reason"} {
		t.Run(name, func(t *testing.T) {
			input := automations.DelayCompletion{
				RunID:       run.ID,
				StepID:      "first",
				Status:      automations.DelayCompleted,
				CompletedAt: at,
			}
			reason := "unknown"
			switch name {
			case "unreached":
				input.StepID = "second"
			case "running status":
				input.Status = automations.DelayRunning
			case "zero time":
				input.CompletedAt = time.Time{}
			case "non UTC":
				input.CompletedAt = at.In(time.FixedZone("offset", 3600))
			case "completed with failure":
				input.FailureCode = &reason
			case "interrupted without reason":
				input.Status = automations.DelayInterrupted
			case "invalid reason":
				input.Status = automations.DelayInterrupted
				input.FailureCode = &reason
			}
			if err := repository.CompleteDelay(ctx, input); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("completion = %v", err)
			}
		})
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, original) {
		t.Fatal("rejected transitions changed history")
	}
	completion := automations.DelayCompletion{
		RunID:       run.ID,
		StepID:      "first",
		Status:      automations.DelayCompleted,
		CompletedAt: at,
	}
	if err := repository.CompleteDelay(ctx, completion); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteDelay(ctx, completion); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("duplicate completion = %v", err)
	}
	if err := repository.RecordDelayStart(
		ctx,
		automations.DelayStart{RunID: run.ID, StepID: "first", Position: 1, StartedAt: at},
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("repeat Step = %v", err)
	}
	if err := repository.RecordDelayStart(
		ctx,
		automations.DelayStart{RunID: run.ID, StepID: "second", Position: 1, StartedAt: at.Add(-time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	reason := "core_stopping"
	completion.StepID, completion.Status, completion.FailureCode = "second", automations.DelayInterrupted, &reason
	if err := repository.CompleteDelay(ctx, completion); err != nil {
		t.Fatal(err)
	}
	terminal := retainedStorageRun(t, repository, record, run)
	if terminal.Delays[1].DurationMS != 86400000 || !terminal.Delays[1].DueAt.Equal(at.Add(23*time.Hour)) {
		t.Fatalf("maximum-duration diagnostic metadata = %#v", terminal.Delays[1])
	}
	if err := repository.RecordDelayStart(ctx, start); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("terminal start = %v", err)
	}
	if err := repository.CompleteDelay(ctx, completion); !errors.Is(err, automations.ErrInvalidAutomation) {
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
			if err := repository.RecordDelayStart(
				ctx,
				automations.DelayStart{RunID: run.ID, StepID: "first", StartedAt: at},
			); err != nil {
				t.Fatal(err)
			}
			if err := repository.CompleteDelay(
				ctx,
				automations.DelayCompletion{
					RunID:       run.ID,
					StepID:      "first",
					Status:      automations.DelayCompleted,
					CompletedAt: at,
				},
			); err != nil {
				t.Fatal(err)
			}
			if err := repository.RecordDelayStart(
				ctx,
				automations.DelayStart{RunID: run.ID, StepID: "second", Position: 1, StartedAt: at},
			); err != nil {
				t.Fatal(err)
			}
			original := retainedStorageRun(t, repository, record, run)
			var commandRecord automations.Record
			var commandRun, originalCommand automations.Run
			if reason == "core_restarted" {
				commandRecord, commandRun = admitStorageRun(t, repository, validDomainDefinition(t), at)
				if err := repository.MarkStepRunning(ctx, automations.StepStart{
					RunID: commandRun.ID, Position: 0,
					CommandID:     devices.CommandID(newCommandIDString(t)),
					CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
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
				return repository.CompleteDelay(
					ctx,
					automations.DelayCompletion{
						RunID:       run.ID,
						StepID:      "second",
						Status:      automations.DelayInterrupted,
						CompletedAt: completionAt,
						FailureCode: &reason,
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
					`CREATE TRIGGER reject_recovery_step BEFORE UPDATE ON automation_run_steps BEGIN SELECT RAISE(ABORT, 'injected Step recovery failure'); END`,
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
			last := got.Delays[1]
			assertStorageDelayInterrupted(t, last, completionAt, reason)
			if !reflect.DeepEqual(got.Delays[0], original.Delays[0]) {
				t.Fatal("interruption rewrote completed delay")
			}
			if reason == "core_restarted" {
				recovered := retainedStorageRun(t, repository, commandRecord, commandRun)
				assertStorageRunInterrupted(t, recovered, completionAt, reason)
				step := recovered.Steps[0]
				if step.Status != automations.StepInterrupted ||
					step.FailureCode == nil ||
					*step.FailureCode != reason ||
					step.CompletedAt == nil ||
					!step.CompletedAt.Equal(completionAt) {
					t.Fatalf("recovered Command = %#v", recovered)
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
	if run.Status != automations.RunInterrupted || run.CompletedAt == nil ||
		!run.CompletedAt.Equal(at) || run.FailureCode == nil || *run.FailureCode != reason {
		t.Fatalf("interrupted Run = %#v", run)
	}
}

func assertStorageDelayInterrupted(t *testing.T, delay automations.DelayExecution, at time.Time, reason string) {
	t.Helper()
	if delay.Status != automations.DelayInterrupted || delay.CompletedAt == nil ||
		!delay.CompletedAt.Equal(at) || delay.FailureCode == nil || *delay.FailureCode != reason {
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
		`CREATE TRIGGER reject_delay_insert BEFORE INSERT ON automation_run_delays BEGIN SELECT RAISE(ABORT, 'injected start failure'); END`,
	)
	start := automations.DelayStart{RunID: run.ID, StepID: "first", StartedAt: at}
	if err := repository.RecordDelayStart(ctx, start); err == nil {
		t.Fatal("injected insert succeeded")
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, pristine) {
		t.Fatal("failed insert changed Run")
	}
	mustExec(t, database, `DROP TRIGGER reject_delay_insert`)
	if err := repository.RecordDelayStart(ctx, start); err != nil {
		t.Fatal(err)
	}
	original := retainedStorageRun(t, repository, record, run)
	mustExec(
		t,
		database,
		`CREATE TRIGGER reject_delay_update BEFORE UPDATE ON automation_run_delays BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END`,
	)
	if err := repository.CompleteDelay(
		ctx,
		automations.DelayCompletion{
			RunID:       run.ID,
			StepID:      "first",
			Status:      automations.DelayCompleted,
			CompletedAt: at,
		},
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
			if err := repository.RecordDelayStart(
				ctx,
				automations.DelayStart{RunID: run.ID, StepID: "first", StartedAt: at},
			); err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
			statement := ""
			switch name {
			case "missing Step":
				statement = `UPDATE automation_run_delays SET step_id = 'absent'`
			case "invalid Step ID":
				statement = `UPDATE automation_run_delays SET step_id = ''`
			case "non delay Step":
				statement = `UPDATE automation_run_delays SET step_id = 'light_on'`
			case "position gap":
				statement = `UPDATE automation_run_delays SET position = 1`
			case "bad start":
				statement = `UPDATE automation_run_delays SET started_at = 'bad'`
			case "zero start":
				statement = `UPDATE automation_run_delays SET started_at = '0001-01-01T00:00:00.000000000Z'`
			case "offset start":
				statement = `UPDATE automation_run_delays SET started_at = '2026-10-04T00:00:00+01:00'`
			case "bad completion":
				statement = `UPDATE automation_run_delays SET status = 'completed', completed_at = 'bad'`
			case "zero completion":
				statement = `UPDATE automation_run_delays SET status = 'completed', completed_at = '0001-01-01T00:00:00.000000000Z'`
			case "running completion":
				statement = `UPDATE automation_run_delays SET completed_at = started_at`
			case "running failure":
				statement = `UPDATE automation_run_delays SET failure_code = 'executor_fault'`
			case "completed no completion":
				statement = `UPDATE automation_run_delays SET status = 'completed'`
			case "completed failure":
				statement = `UPDATE automation_run_delays SET status = 'completed', completed_at = started_at, failure_code = 'executor_fault'`
			case "interrupted no reason":
				statement = `UPDATE automation_run_delays SET status = 'interrupted', completed_at = started_at`
			case "interrupted bad reason":
				statement = `UPDATE automation_run_delays SET status = 'interrupted', completed_at = started_at, failure_code = 'bad'`
			case "unknown status":
				statement = `UPDATE automation_run_delays SET status = 'bad', completed_at = started_at`
			case "terminal parent":
				statement = `UPDATE automation_history SET run_status = 'succeeded', run_completed_at = run_started_at`
			case "invalid snapshot":
				statement = `UPDATE automation_history SET run_snapshot_json = '{}'`
			case "invalid snapshot duration":
				statement = `UPDATE automation_history SET run_snapshot_json = replace(run_snapshot_json, '"duration_ms":1001', '"duration_ms":0')`
			}
			mustExec(t, database, statement)
			if _, err := repository.GetHistoryEntry(ctx, record.ID, string(run.ID)); err == nil {
				t.Fatal("corrupt history read succeeded")
			}
		})
	}
}
