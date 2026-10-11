package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

// The fixture's independent expected leaf order is before, a-then, a-else, b,
// default-then, default-else, after. Branch nodes must never become attempts.
func branchingStorageDefinition(t *testing.T) automations.Definition {
	t.Helper()
	definition := validDomainDefinition(t)
	command := func(id automations.StepID) automations.Step {
		step := definition.Steps[0]
		step.ID = id
		return step
	}
	condition := automations.Condition{
		ID:   "matched",
		Body: automations.TriggerCondition{TriggerIDs: []automations.TriggerID{definition.Triggers[0].ID}},
	}
	ifStep := func(id automations.StepID, thenID, elseID automations.StepID) automations.Step {
		return automations.Step{ID: id, Body: automations.IfStep{
			Conditions: condition, Then: []automations.Step{command(thenID)}, Else: []automations.Step{command(elseID)},
		}}
	}
	definition.Steps = []automations.Step{command("before"), {ID: "route", Body: automations.ChooseStep{
		Branches: []automations.ChooseBranch{
			{ID: "a", Conditions: condition, Steps: []automations.Step{ifStep("inside-a", "a-then", "a-else")}},
			{ID: "b", Conditions: condition, Steps: []automations.Step{command("b")}},
		},
		Default: []automations.Step{ifStep("fallback", "default-then", "default-else")},
	}}, command("after")}
	return definition
}

func storageDecision(stepID automations.StepID, position int, at time.Time) automations.BranchDecision {
	evaluation := automations.ConditionEvaluation{EvaluatedAt: at, Result: automations.ConditionFalse,
		Nodes: []automations.ConditionNodeResult{{ID: "matched",
			Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: []automations.TriggerID{}}}}}
	decision := automations.BranchDecision{
		Position:    position,
		StepID:      stepID,
		EvaluatedAt: at,
		Body: automations.IfDecision{
			Result: automations.IfSelected{Arm: automations.IfElse, Evaluation: evaluation},
		},
	}
	if stepID == "route" {
		a, b := automations.BranchID("a"), automations.BranchID("b")
		decision.Body = automations.ChooseDecision{
			Result: automations.ChooseFallback{
				Arm: automations.ChooseDefault,
				Evaluations: []automations.ChooseEvaluation{
					{BranchID: a, Evaluation: evaluation}, {BranchID: b, Evaluation: evaluation},
				},
			},
		}
	}
	return decision
}

func admitStorageRun(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	definition automations.Definition,
	at time.Time,
) (automations.Record, automations.Run) {
	t.Helper()
	record, err := repository.CreateAutomation(context.Background(), definition)
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.AdmitManualRun(
		context.Background(),
		automations.ManualRunInput{AutomationID: record.ID},
		stateSnapshotWith(),
		at,
	)
	if err != nil || runEntry(result) == nil {
		t.Fatalf("admit = %#v, %v", result, err)
	}
	return record, *runEntry(result)
}

func retainedStorageRun(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	record automations.Record,
	run automations.Run,
) automations.Run {
	t.Helper()
	entry, err := repository.GetHistoryEntry(context.Background(), record.ID, string(run.ID))
	if err != nil || runEntry(entry) == nil {
		t.Fatalf("history = %#v, %v", entry, err)
	}
	return *runEntry(entry)
}

// A5/A7: real admission allocates every leaf without identities, and retained
// evidence and positions do not depend on the replacement or deleted definition.
func TestBranchStorageLeafAttemptsAndImmutableHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repository, branchingStorageDefinition(t), at)
	for _, decision := range []automations.BranchDecision{storageDecision("route", 0, at), storageDecision("fallback", 1, at.Add(time.Second))} {
		if err := repository.RecordBranchDecision(ctx, run.ID, decision); err != nil {
			t.Fatal(err)
		}
	}
	before := retainedStorageRun(t, repository, record, run)
	want := []automations.StepID{"before", "a-then", "a-else", "b", "default-then", "default-else", "after"}
	if len(before.Steps) != len(want) || len(before.BranchDecisions) != 2 ||
		automations.RunStateStatus(before.State) != automations.RunRunning {
		t.Fatalf("retained = %#v", before)
	}
	if before.BranchDecisions[0].StepID != "route" || before.BranchDecisions[0].Position != 0 ||
		before.BranchDecisions[1].StepID != "fallback" || before.BranchDecisions[1].Position != 1 {
		t.Fatalf("decision order = %#v", before.BranchDecisions)
	}
	for position, step := range before.Steps {
		if step.Position != position || step.StepID != want[position] ||
			automations.StepAttemptStatus(step.State) != automations.StepNotAttempted ||
			stepReservedCommand(step.State) != nil ||
			stepReservedCorrelation(step.State) != nil ||
			stepVerified(step.State) != nil {
			t.Fatalf("leaf %d = %#v", position, step)
		}
	}
	replacement, err := repository.ReplaceAutomation(ctx, record.ID, record.Revision, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
		t.Fatal("replacement changed retained Run")
	}
	if err = repository.DeleteAutomation(ctx, record.ID, replacement.Revision); err != nil {
		t.Fatal(err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
		t.Fatal("deletion changed retained Run")
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
		t.Fatalf("prune = %d, %v", deleted, pruneErr)
	}
	var decisions int
	if err = database.QueryRow(`SELECT count(*) FROM automation_run_branch_decisions`).
		Scan(&decisions); err != nil ||
		decisions != 0 {
		t.Fatalf("cascade remaining = %d, %v", decisions, err)
	}
}

// A6: wrong snapshot evidence, gaps, backwards timestamps, and duplicate writes
// cannot mutate existing evidence or fail an otherwise-running parent.
//
//nolint:paralleltest,tparallel // Invalid writes deliberately share one retained Run and append position.
func TestBranchStorageRejectsInvalidAppends(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repository, branchingStorageDefinition(t), at)
	first := storageDecision("route", 0, at.Add(time.Second))
	pristine := retainedStorageRun(t, repository, record, run)
	mustExec(
		t,
		database,
		`CREATE TRIGGER reject_branch_insert BEFORE INSERT ON automation_run_branch_decisions BEGIN SELECT RAISE(ABORT, 'injected decision failure'); END`,
	)
	if err := repository.RecordBranchDecision(ctx, run.ID, first); err == nil {
		t.Fatal("injected decision write succeeded")
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, pristine) {
		t.Fatal("failed decision insert changed Run")
	}
	mustExec(t, database, `DROP TRIGGER reject_branch_insert`)
	if err := repository.RecordBranchDecision(ctx, run.ID, first); err != nil {
		t.Fatal(err)
	}
	original := retainedStorageRun(t, repository, record, run)
	for _, name := range []string{"identical duplicate", "changed duplicate", "duplicate step next position", "position gap", "backwards time", "before admission", "command node", "missing node", "kind mismatch", "missing evaluations", "wrong trigger evidence"} {
		t.Run(name, func(t *testing.T) {
			decision := storageDecision("fallback", 1, at.Add(2*time.Second))
			switch name {
			case "identical duplicate":
				decision = first
			case "changed duplicate":
				decision = storageDecision("route", 0, at.Add(2*time.Second))
			case "duplicate step next position":
				decision = storageDecision("route", 1, at.Add(2*time.Second))
			case "position gap":
				decision.Position = 2
			case "backwards time":
				decision = storageDecision("fallback", 1, at)
			case "before admission":
				decision = storageDecision("fallback", 1, at.Add(-time.Second))
			case "command node":
				decision.StepID = "before"
			case "missing node":
				decision.StepID = "missing"
			case "kind mismatch":
				decision.StepID = "route"
			case "missing evaluations":
				decision.Body = automations.IfDecision{Result: automations.IfSelected{Arm: automations.IfElse}}
			case "wrong trigger evidence":
				evaluation := decision.Body.(automations.IfDecision).Result.(automations.IfSelected).Evaluation
				evaluation.Result = automations.ConditionTrue
				evaluation.Nodes[0].Evidence = automations.TriggerMatchEvidence{
					MatchedTriggerIDs: []automations.TriggerID{run.Snapshot.Triggers[0].ID},
				}
				decision.Body = automations.IfDecision{
					Result: automations.IfSelected{Arm: automations.IfThen, Evaluation: evaluation},
				}
			}
			if err := repository.RecordBranchDecision(ctx, run.ID, decision); err == nil {
				t.Fatal("invalid append succeeded")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, original) {
				t.Fatal("rejected append changed persisted Run")
			}
		})
	}
	if err := repository.CompleteRun(
		ctx,
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	terminal := retainedStorageRun(t, repository, record, run)
	for _, decision := range []automations.BranchDecision{first, storageDecision("fallback", 1, at.Add(2*time.Second))} {
		if err := repository.RecordBranchDecision(
			ctx,
			run.ID,
			decision,
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Fatalf("terminal write = %v", err)
		}
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, terminal) {
		t.Fatal("terminal append changed Run")
	}
}

// A6: failure completion uses repository time. A SQLite trigger failing the
// second write proves both the inserted evidence and Run update roll back.
//
//nolint:gocognit // One atomicity scenario checks rollback and the committed failure evidence.
func TestBranchStorageFailureIsAtomic(t *testing.T) {
	t.Parallel()
	for _, outcome := range []automations.BranchOutcome{automations.BranchUnknown, automations.BranchError} {
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openAutomationDatabase(t)
			at := time.Now().UTC()
			completedAt := at.Add(time.Minute)
			repository := automationssqlite.NewAutomationRepository(
				database,
				automations.Dependencies{Now: func() time.Time { return completedAt }},
			)
			definition := branchingStorageDefinition(t)
			chooseBody := definition.Steps[1].Body.(automations.ChooseStep)
			ifBody := chooseBody.Default[0].Body.(automations.IfStep)
			ifBody.Conditions = *conditionLeaf("state", newEntityID(t), automations.ComparisonEqual, "true")
			chooseBody.Default[0].Body = ifBody
			definition.Steps[1].Body = chooseBody
			record, run := admitStorageRun(t, repository, definition, at)
			if err := repository.RecordBranchDecision(ctx, run.ID, storageDecision("route", 0, at)); err != nil {
				t.Fatal(err)
			}
			decision := automations.BranchDecision{
				Position:    1,
				StepID:      "fallback",
				EvaluatedAt: at.Add(time.Second),
			}
			code := "branch_state_read_failed"
			if outcome == automations.BranchUnknown {
				code = "branch_condition_unknown"
				reason := automations.ConditionUnknownStateMissing
				decision.Body = automations.IfDecision{
					Result: automations.IfUnknown{Evaluation: automations.ConditionEvaluation{
						EvaluatedAt: decision.EvaluatedAt,
						Result:      automations.ConditionUnknown,
						Nodes: []automations.ConditionNodeResult{
							{
								ID:       definition.Steps[1].Body.(automations.ChooseStep).Default[0].Body.(automations.IfStep).Conditions.ID,
								Evidence: automations.UnknownStateEvidence{Reason: reason},
							},
						},
					}},
				}
			} else {
				decision.Body = automations.IfDecision{Result: automations.IfError{FailureCode: code}}
			}
			before := retainedStorageRun(t, repository, record, run)
			mustExec(
				t,
				database,
				`CREATE TRIGGER reject_branch_failure BEFORE UPDATE OF run_status ON automation_history BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END`,
			)
			if err := repository.RecordBranchDecision(ctx, run.ID, decision); err == nil {
				t.Fatal("injected update failure succeeded")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatal("failed completion left partial evidence or Run mutation")
			}
			mustExec(t, database, `DROP TRIGGER reject_branch_failure`)
			if err := repository.RecordBranchDecision(ctx, run.ID, decision); err != nil {
				t.Fatal(err)
			}
			got := retainedStorageRun(t, repository, record, run)
			if automations.RunStateStatus(got.State) != automations.RunFailed || runFailure(got.State) == nil ||
				*runFailure(got.State) != code {
				t.Fatalf("failed Run status = %#v", got)
			}
			if runCompleted(got.State) == nil || !runCompleted(got.State).Equal(completedAt) {
				t.Fatalf("completion time = %v, want %v", runCompleted(got.State), completedAt)
			}
			if len(got.BranchDecisions) != 2 || got.BranchDecisions[1].Outcome() != outcome ||
				!reflect.DeepEqual(got.Steps, before.Steps) {
				t.Fatalf("failed Run = %#v", got)
			}
		})
	}
}

// Retained corruption must fail detail reads rather than remap commands or trust
// payload identity at odds with the ordered SQL columns.
func TestBranchHistoryRejectsCorruptRows(t *testing.T) {
	t.Parallel()
	for _, corruption := range []string{"payload position", "payload step", "column step", "decision gap", "decision chronology", "condition membership", "leaf position", "leaf membership", "missing attempt"} {
		t.Run(corruption, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			at := time.Now().UTC()
			record, run := admitStorageRun(t, repository, branchingStorageDefinition(t), at)
			if err := repository.RecordBranchDecision(ctx, run.ID, storageDecision("route", 0, at)); err != nil {
				t.Fatal(err)
			}
			var statement string
			switch corruption {
			case "payload position":
				statement = `UPDATE automation_run_branch_decisions SET decision_json = json_set(decision_json, '$.position', 1) WHERE run_id = ?`
			case "payload step":
				statement = `UPDATE automation_run_branch_decisions SET decision_json = json_set(decision_json, '$.step_id', 'missing') WHERE run_id = ?`
			case "column step":
				statement = `UPDATE automation_run_branch_decisions SET step_id = 'missing' WHERE run_id = ?`
			case "decision gap":
				statement = `UPDATE automation_run_branch_decisions SET position = 1, decision_json = json_set(decision_json, '$.position', 1) WHERE run_id = ?`
			case "decision chronology":
				statement = `UPDATE automation_run_branch_decisions SET decision_json = json_set(decision_json, '$.evaluated_at', '2020-01-01T00:00:00Z', '$.evaluations[0].evaluation.evaluated_at', '2020-01-01T00:00:00Z', '$.evaluations[1].evaluation.evaluated_at', '2020-01-01T00:00:00Z') WHERE run_id = ?`
			case "condition membership":
				statement = `UPDATE automation_run_branch_decisions SET decision_json = json_set(decision_json, '$.evaluations[0].evaluation.nodes[0].id', 'missing') WHERE run_id = ?`
			case "leaf position":
				statement = `UPDATE automation_run_steps SET position = 31 WHERE run_id = ? AND position = 0`
			case "leaf membership":
				statement = `UPDATE automation_run_steps SET step_id = 'route' WHERE run_id = ? AND position = 0`
			case "missing attempt":
				statement = `DELETE FROM automation_run_steps WHERE run_id = ? AND position = 0`
			}
			mustExec(t, database, statement, string(run.ID))
			if _, err := repository.GetHistoryEntry(
				ctx,
				record.ID,
				string(run.ID),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("corrupt read = %v", err)
			}
		})
	}
}

func assertBranchOrphanRejected(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO automation_run_branch_decisions (run_id, step_id, position, decision_json) VALUES (?, 'route', 0, '{}')`,
		newRunIDString(t),
	); err == nil {
		t.Fatal("decision orphan accepted")
	}
}
