//go:build integration

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
	"github.com/mholtzscher/hearth/internal/modules/devices"
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
	run := *runEntry(result)
	return record, run
}

// A5/A7: sparse reached evidence and its sequence survive definition replacement and deletion.
func TestBranchStorageSparseExecutionsAndImmutableHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repository, branchingStorageDefinition(t), at)
	if err := completeStorageCommand(ctx, t, repository, run.ID, "before", at); err != nil {
		t.Fatal(err)
	}
	for _, decision := range []automations.BranchDecision{storageDecision("route", 0, at), storageDecision("fallback", 1, at.Add(time.Second))} {
		if err := recordStorageBranch(ctx, repository, run.ID, decision); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []automations.StepID{"default-else", "after"} {
		if err := completeStorageCommand(ctx, t, repository, run.ID, id, at.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	before := retainedStorageRun(t, repository, record, run)
	assertBranchStorageExecutions(t, before)
	replacement, err := repository.ReplaceAutomation(ctx, record.ID, record.Revision, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	assertBranchHistoryUnchanged(t, retainedStorageRun(t, repository, record, run), before, "replacement")
	if err = repository.DeleteAutomation(ctx, record.ID, replacement.Revision); err != nil {
		t.Fatal(err)
	}
	assertBranchHistoryUnchanged(t, retainedStorageRun(t, repository, record, run), before, "deletion")
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
	if err = database.QueryRow(`SELECT count(*) FROM automation_step_executions WHERE run_id = ? AND kind IN ('if', 'choose')`, string(run.ID)).
		Scan(&decisions); err != nil ||
		decisions != 0 {
		t.Fatalf("cascade remaining = %d, %v", decisions, err)
	}
}

func assertBranchStorageExecutions(t *testing.T, before automations.Run) {
	t.Helper()
	want := []automations.StepID{"before", "route", "fallback", "default-else", "after"}
	if len(before.Executions) != len(want) ||
		automations.RunStateStatus(before.State) != automations.RunRunning {
		t.Fatalf("retained = %#v", before)
	}
	for position, execution := range before.Executions {
		if execution.Sequence != position || execution.StepID != want[position] {
			t.Fatalf("execution %d = %#v, want step %s", position, execution, want[position])
		}
		if command, isCommand := execution.Details.(automations.CommandExecutionDetails); isCommand {
			success, ok := command.State.(automations.CommandSucceeded)
			satisfied, isSatisfied := success.Outcome.(automations.SatisfiedCommand)
			if !ok || !isSatisfied || satisfied.VerifiedCommandID != success.Reservation.CommandID {
				t.Fatalf("selected Command lost success evidence: %#v", execution)
			}
		}
	}
}

func assertBranchHistoryUnchanged(t *testing.T, got, want automations.Run, operation string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s changed retained Run", operation)
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
	if err := completeStorageCommand(ctx, t, repository, run.ID, "before", at); err != nil {
		t.Fatal(err)
	}
	pristine := retainedStorageRun(t, repository, record, run)
	mustExec(
		t,
		database,
		`CREATE TRIGGER reject_branch_insert BEFORE INSERT ON automation_step_executions BEGIN SELECT RAISE(ABORT, 'injected decision failure'); END`,
	)
	if err := recordStorageBranch(ctx, repository, run.ID, first); err == nil {
		t.Fatal("injected decision write succeeded")
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, pristine) {
		t.Fatal("failed decision insert changed Run")
	}
	mustExec(t, database, `DROP TRIGGER reject_branch_insert`)
	if err := recordStorageBranch(ctx, repository, run.ID, first); err != nil {
		t.Fatal(err)
	}
	original := retainedStorageRun(t, repository, record, run)
	for _, name := range []string{"identical duplicate", "changed duplicate", "duplicate step next position", "zero time", "non UTC", "command node", "missing node", "kind mismatch", "missing evaluations", "wrong trigger evidence"} {
		t.Run(name, func(t *testing.T) {
			decision := storageDecision("fallback", 1, at.Add(2*time.Second))
			decision = invalidStorageBranchDecision(name, first, decision, at, run)
			if err := recordStorageBranch(ctx, repository, run.ID, decision); err == nil {
				t.Fatal("invalid append succeeded")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, original) {
				t.Fatal("rejected append changed persisted Run")
			}
		})
	}
	if err := recordStorageBranch(
		ctx,
		repository,
		run.ID,
		storageDecision("fallback", 1, at.Add(2*time.Second)),
	); err != nil {
		t.Fatal(err)
	}
	for _, id := range []automations.StepID{"default-else", "after"} {
		if err := completeStorageCommand(ctx, t, repository, run.ID, id, at.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.CompleteRun(
		ctx,
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	terminal := retainedStorageRun(t, repository, record, run)
	for _, decision := range []automations.BranchDecision{first, storageDecision("fallback", 1, at.Add(2*time.Second))} {
		if err := recordStorageBranch(ctx, repository, run.ID,
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

func invalidStorageBranchDecision(
	name string,
	first, decision automations.BranchDecision,
	at time.Time,
	run automations.Run,
) automations.BranchDecision {
	switch name {
	case "identical duplicate":
		return first
	case "changed duplicate":
		return storageDecision("route", 0, at.Add(2*time.Second))
	case "duplicate step next position":
		return storageDecision("route", 1, at.Add(2*time.Second))
	case "zero time":
		decision.EvaluatedAt = time.Time{}
	case "non UTC":
		decision.EvaluatedAt = at.In(time.FixedZone("offset", 3600))
	case "command node":
		decision.StepID = "before"
	case "missing node":
		decision.StepID = "missing"
	case "kind mismatch":
		decision.StepID = "route"
	case "missing evaluations":
		decision.Body = automations.IfDecision{Result: automations.IfSelected{Arm: automations.IfElse}}
	case "wrong trigger evidence":
		decision.Body = branchDecisionWithWrongTriggerEvidence(decision, run)
	}
	return decision
}

func branchDecisionWithWrongTriggerEvidence(
	decision automations.BranchDecision,
	run automations.Run,
) automations.IfDecision {
	evaluation := decision.Body.(automations.IfDecision).Result.(automations.IfSelected).Evaluation
	evaluation.Result = automations.ConditionTrue
	evaluation.Nodes[0].Evidence = automations.TriggerMatchEvidence{
		MatchedTriggerIDs: []automations.TriggerID{run.Snapshot.Triggers[0].ID},
	}
	return automations.IfDecision{
		Result: automations.IfSelected{Arm: automations.IfThen, Evaluation: evaluation},
	}
}

// A6: an unknown/error branch and its parent failure commit atomically. Branch
// and Run completion share the instantaneous evaluation timestamp.
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
			if err := completeStorageCommand(ctx, t, repository, run.ID, "before", at); err != nil {
				t.Fatal(err)
			}
			if err := recordStorageBranch(ctx, repository, run.ID, storageDecision("route", 0, at)); err != nil {
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
			if err := recordStorageBranch(ctx, repository, run.ID, decision); err == nil {
				t.Fatal("injected update failure succeeded")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatal("failed completion left partial evidence or Run mutation")
			}
			mustExec(t, database, `DROP TRIGGER reject_branch_failure`)
			if err := recordStorageBranch(ctx, repository, run.ID, decision); err != nil {
				t.Fatal(err)
			}
			got := retainedStorageRun(t, repository, record, run)
			if automations.RunStateStatus(got.State) != automations.RunFailed || runFailure(got.State) == nil ||
				*runFailure(got.State) != code {
				t.Fatalf("failed Run status = %#v", got)
			}
			if runCompleted(got.State) == nil || !runCompleted(got.State).Equal(decision.EvaluatedAt) {
				t.Fatalf("completion time = %v, want evaluation time %v", runCompleted(got.State), decision.EvaluatedAt)
			}
			last := got.Executions[len(got.Executions)-1]
			branch, isBranch := last.Details.(automations.BranchExecutionDetails)
			if !isBranch || branch.Decision.Outcome() != outcome ||
				!reflect.DeepEqual(got.Executions[:len(before.Executions)], before.Executions) {
				t.Fatalf("failed Run = %#v", got)
			}
		})
	}
}

// Retained corruption must fail detail reads rather than remap commands or trust
// payload identity at odds with the ordered SQL columns.
func TestBranchHistoryRejectsCorruptRows(t *testing.T) {
	t.Parallel()
	for _, corruption := range []string{"payload step", "column step", "decision gap", "decision chronology", "condition membership", "leaf sequence", "leaf kind"} {
		t.Run(corruption, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			at := time.Now().UTC()
			record, run := admitStorageRun(t, repository, branchingStorageDefinition(t), at)
			if err := completeStorageCommand(ctx, t, repository, run.ID, "before", at); err != nil {
				t.Fatal(err)
			}
			if err := recordStorageBranch(ctx, repository, run.ID, storageDecision("route", 0, at)); err != nil {
				t.Fatal(err)
			}
			var statement string
			switch corruption {
			case "payload step":
				statement = `UPDATE automation_step_executions SET evidence_json = json_set(evidence_json, '$.step_id', 'missing') WHERE run_id = ? AND step_id = 'route'`
			case "column step":
				statement = `UPDATE automation_step_executions SET step_id = 'missing' WHERE run_id = ? AND step_id = 'route'`
			case "decision gap":
				statement = `UPDATE automation_step_executions SET sequence = 2 WHERE run_id = ? AND step_id = 'route'`
			case "decision chronology":
				statement = `UPDATE automation_step_executions SET evidence_json = json_set(evidence_json, '$.evaluations[0].evaluation.evaluated_at', '2020-01-01T00:00:00Z', '$.evaluations[1].evaluation.evaluated_at', '2020-01-01T00:00:00Z') WHERE run_id = ? AND step_id = 'route'`
			case "condition membership":
				statement = `UPDATE automation_step_executions SET evidence_json = json_set(evidence_json, '$.evaluations[0].evaluation.nodes[0].id', 'missing') WHERE run_id = ? AND step_id = 'route'`
			case "leaf sequence":
				statement = `UPDATE automation_step_executions SET sequence = 31 WHERE run_id = ? AND step_id = 'before'`
			case "leaf kind":
				statement = `UPDATE automation_step_executions SET kind = 'delay' WHERE run_id = ? AND step_id = 'before'`
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

func TestCommandCompletionRequiresMatchingReservationAndFailsParentAtomically(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), at)
	commandID := devices.CommandID(newCommandIDString(t))
	reservation := automations.CommandReservation{
		CommandID:     commandID,
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}
	if err := recordStorageCommand(t.Context(), repository, run.ID, "light_on", reservation, at); err != nil {
		t.Fatal(err)
	}
	before := retainedStorageRun(t, repository, record, run)
	wrong := reservation
	wrong.CorrelationID = devices.CorrelationID(newCorrelationIDString(t))
	if err := completeStorageStep(t.Context(), repository, run.ID, "light_on", automations.CommandFinished{
		State: automations.CommandFailed{CompletedAt: at, Reservation: wrong, FailureCode: "device_failed"},
	}); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("wrong reservation completion = %v", err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
		t.Fatal("wrong reservation changed execution")
	}
	if err := completeStorageStep(t.Context(), repository, run.ID, "light_on", automations.CommandFinished{
		State: automations.CommandFailed{CompletedAt: at, Reservation: reservation, FailureCode: "device_failed"},
	}); err != nil {
		t.Fatal(err)
	}
	failed := retainedStorageRun(t, repository, record, run)
	if automations.RunStateStatus(failed.State) != automations.RunFailed || len(failed.Executions) != 1 {
		t.Fatalf("failed Command did not atomically fail parent: %#v", failed)
	}
}

func assertBranchOrphanRejected(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO automation_step_executions (run_id, step_id, sequence, kind, phase, started_at, completed_at, evidence_json) VALUES (?, 'route', 0, 'choose', 'succeeded', ?, ?, '{}')`,
		newRunIDString(t),
		migrationTimestamp,
		migrationTimestamp,
	); err == nil {
		t.Fatal("decision orphan accepted")
	}
}
