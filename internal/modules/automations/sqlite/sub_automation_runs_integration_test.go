//go:build integration

package sqlite_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gocognit // Each scenario tests the complete transactional state and its negative control.
func TestCompleteSubAutomationRejectsUnavailableOrMismatchedChildEvidence(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"missing", "running", "mismatched_parent"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			database := openAutomationDatabase(t)
			repo := newAutomationRepository(t, database)
			child, err := repo.CreateAutomation(ctx, validDomainDefinition(t))
			if err != nil {
				t.Fatal(err)
			}
			definition := validDomainDefinition(t)
			definition.Steps = []automations.Step{
				{ID: "call", Body: automations.SubAutomationStep{AutomationID: child.ID}},
			}
			parent, err := repo.CreateAutomation(ctx, definition)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC()
			admitted, err := repo.AdmitManualRun(
				ctx,
				automations.ManualRunInput{AutomationID: parent.ID},
				devices.EntityStateSnapshot{},
				at,
			)
			if err != nil {
				t.Fatal(err)
			}
			run := admitted.(automations.Run)
			initializeStorageRun(t, repo, run)
			input := automations.SubAutomationStepInput{ParentRunID: run.ID, StepID: "call"}
			linked, err := repo.AdmitSubAutomationRun(ctx, input, devices.EntityStateSnapshot{}, at)
			if err != nil || linked == nil {
				t.Fatalf("admission = %#v, %v", linked, err)
			}
			switch scenario {
			case "missing":
				if err = finishStorageCommandRun(t, repo, *linked, at); err != nil {
					t.Fatal(err)
				}
				mustExec(t, database, `DELETE FROM automation_history WHERE id = ?`, string(linked.ID))
			case "mismatched_parent":
				if err = finishStorageCommandRun(t, repo, *linked, at); err != nil {
					t.Fatal(err)
				}
				other, idErr := automations.NewRunID()
				if idErr != nil {
					t.Fatal(idErr)
				}
				mustExec(
					t,
					database,
					`UPDATE automation_history SET parent_run_id = ? WHERE id = ?`,
					string(other),
					string(linked.ID),
				)
			case "running":
				// An admitted but unfinished child has no established outcome.
			}
			if details, completionErr := repo.CompleteSubAutomationStep(ctx, input, at); completionErr == nil {
				t.Fatalf("completion accepted %s child: %#v", scenario, details)
			}
			entry, err := repo.GetHistoryEntry(ctx, parent.ID, string(run.ID))
			if err != nil {
				t.Fatal(err)
			}
			retained := entry.(automations.Run)
			if automations.RunStateStatus(retained.State) != automations.RunRunning || len(retained.Executions) != 1 {
				t.Fatalf("parent after refused completion = %#v", retained)
			}
			running, ok := retained.Executions[0].Details.(automations.SubAutomationExecutionDetails).State.(automations.SubAutomationRunning)
			if !ok || running.ChildRunID != linked.ID {
				t.Fatalf("parent SubAutomation after refused completion = %#v", retained.Executions)
			}
			if scenario == "running" {
				if err = finishStorageCommandRun(t, repo, *linked, at); err != nil {
					t.Fatal(err)
				}
				completed, completionErr := repo.CompleteSubAutomationStep(ctx, input, at)
				if _, ok = completed.State.(automations.SubAutomationSucceeded); completionErr != nil || !ok {
					t.Fatalf("established child success = %#v, %v", completed, completionErr)
				}
			}
		})
	}
}

//nolint:gocognit // Both competing admissions and their retained outcomes are checked together.
func TestSimultaneousSubAutomationAdmissionsAllowOnlyOneChild(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := newAutomationRepository(t, openAutomationDatabase(t))
	child, err := repo.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{{ID: "call", Body: automations.SubAutomationStep{AutomationID: child.ID}}}
	at := time.Now().UTC()
	var parents [2]automations.Record
	var runs [2]automations.Run
	for i := range parents {
		parents[i], err = repo.CreateAutomation(ctx, definition)
		if err != nil {
			t.Fatal(err)
		}
		admitted, admitErr := repo.AdmitManualRun(
			ctx,
			automations.ManualRunInput{AutomationID: parents[i].ID},
			devices.EntityStateSnapshot{},
			at,
		)
		if admitErr != nil {
			t.Fatal(admitErr)
		}
		runs[i] = admitted.(automations.Run)
		initializeStorageRun(t, repo, runs[i])
	}
	type result struct {
		index int
		run   *automations.Run
		err   error
	}
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for i := range parents {
		workers.Go(func() {
			ready <- struct{}{}
			select {
			case <-start:
			case <-ctx.Done():
				results <- result{index: i, err: context.Cause(ctx)}
				return
			}
			childRun, admitErr := repo.AdmitSubAutomationRun(
				ctx,
				automations.SubAutomationStepInput{ParentRunID: runs[i].ID, StepID: "call"},
				devices.EntityStateSnapshot{},
				at,
			)
			results <- result{index: i, run: childRun, err: admitErr}
		})
	}
	<-ready
	<-ready
	close(start)
	workers.Wait()
	close(results)
	var winner *automations.Run
	winnerIndex := -1
	loser := -1
	for outcome := range results {
		if outcome.err != nil {
			t.Fatalf("parent %d admission error = %v", outcome.index, outcome.err)
		}
		if outcome.run == nil {
			if loser != -1 {
				t.Fatal("both callers rejected")
			}
			loser = outcome.index
		} else {
			if winner != nil {
				t.Fatal("both callers admitted a child")
			}
			winner = outcome.run
			winnerIndex = outcome.index
		}
	}
	if winner == nil || loser < 0 {
		t.Fatalf("winner = %#v, loser = %d", winner, loser)
	}
	entry, err := repo.GetHistoryEntry(ctx, parents[loser].ID, string(runs[loser].ID))
	if err != nil {
		t.Fatal(err)
	}
	failed := entry.(automations.Run)
	if automations.RunStateStatus(failed.State) != automations.RunFailed || len(failed.Executions) != 1 {
		t.Fatalf("losing parent = %#v", failed)
	}
	rejected, ok := failed.Executions[0].Details.(automations.SubAutomationExecutionDetails).State.(automations.SubAutomationRejected)
	if !ok || rejected.ReasonCode != "automation_busy" || rejected.SkipID != nil {
		t.Fatalf("losing parent evidence = %#v", failed.Executions)
	}
	entry, err = repo.GetHistoryEntry(ctx, parents[winnerIndex].ID, string(runs[winnerIndex].ID))
	if err != nil {
		t.Fatal(err)
	}
	active := entry.(automations.Run)
	if automations.RunStateStatus(active.State) != automations.RunRunning || len(active.Executions) != 1 {
		t.Fatalf("winning parent = %#v", active)
	}
	linked, ok := active.Executions[0].Details.(automations.SubAutomationExecutionDetails).State.(automations.SubAutomationRunning)
	if !ok || linked.ChildRunID != winner.ID || automations.RunStateStatus(winner.State) != automations.RunRunning {
		t.Fatalf("winning link = %#v, child = %#v", active.Executions, winner)
	}
	history, err := repo.ListHistory(ctx, automations.ListHistoryParams{AutomationID: child.ID})
	if err != nil || len(history.Items) != 1 || history.Items[0].ID != string(winner.ID) {
		t.Fatalf("target history = %#v, %v", history, err)
	}
}

func TestSubAutomationAdmissionLinksAtomicallyAndRefusesUnverifiedCompletion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database := openAutomationDatabase(t)
	repo := newAutomationRepository(t, database)
	child, err := repo.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{{ID: "call", Body: automations.SubAutomationStep{AutomationID: child.ID}}}
	parent, err := repo.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := repo.AdmitManualRun(
		ctx,
		automations.ManualRunInput{AutomationID: parent.ID},
		devices.EntityStateSnapshot{},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	run := admitted.(automations.Run)
	initializeStorageRun(t, repo, run)
	input := automations.SubAutomationStepInput{ParentRunID: run.ID, StepID: "call"}
	linked, err := repo.AdmitSubAutomationRun(ctx, input, devices.EntityStateSnapshot{}, time.Now().UTC())
	if err != nil || linked == nil {
		t.Fatalf("admission = %#v, %v", linked, err)
	}
	if _, err = repo.AdmitSubAutomationRun(ctx, input, devices.EntityStateSnapshot{}, time.Now().UTC()); err == nil {
		t.Fatal("duplicate reach admitted")
	}
	if _, err = repo.CompleteSubAutomationStep(ctx, input, time.Now().UTC()); err == nil {
		t.Fatal("running child accepted as success")
	}
	if err = repo.CompleteStep(
		ctx,
		automations.StepExecutionCompletion{
			RunID:    run.ID,
			StepID:   "call",
			Evidence: automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: time.Now().UTC()}},
		},
	); err == nil {
		t.Fatal("fabricated completion accepted")
	}
	entry, err := repo.GetHistoryEntry(ctx, parent.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	retained := entry.(automations.Run)
	state, ok := retained.Executions[0].Details.(automations.SubAutomationExecutionDetails).State.(automations.SubAutomationRunning)
	if !ok || state.ChildRunID != linked.ID || automations.RunStateStatus(retained.State) != automations.RunRunning {
		t.Fatalf("parent = %#v", retained)
	}
	if err = repo.InterruptActiveRuns(ctx, time.Now().UTC(), automations.FailureCoreRestarted); err != nil {
		t.Fatal(err)
	}
	entry, err = repo.GetHistoryEntry(ctx, parent.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	retained = entry.(automations.Run)
	interrupted, ok := retained.Executions[0].Details.(automations.SubAutomationExecutionDetails).State.(automations.SubAutomationInterrupted)
	if !ok || interrupted.ChildRunID != linked.ID || interrupted.ReasonCode != automations.FailureCoreRestarted ||
		automations.RunStateStatus(retained.State) != automations.RunInterrupted {
		t.Fatalf("recovered parent = %#v", retained)
	}
	entry, err = repo.GetHistoryEntry(ctx, child.ID, string(linked.ID))
	if err != nil || automations.RunStateStatus(entry.(automations.Run).State) != automations.RunInterrupted {
		t.Fatalf("recovered child = %#v, %v", entry, err)
	}
}

func TestSubAutomationAdmissionRollbackOnParentEvidenceFailure(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database := openAutomationDatabase(t)
	repo := newAutomationRepository(t, database)
	child, err := repo.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{{ID: "call", Body: automations.SubAutomationStep{AutomationID: child.ID}}}
	parent, err := repo.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	result, err := repo.AdmitManualRun(
		ctx,
		automations.ManualRunInput{AutomationID: parent.ID},
		devices.EntityStateSnapshot{},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	run := result.(automations.Run)
	initializeStorageRun(t, repo, run)
	mustExec(
		t,
		database,
		`CREATE TRIGGER fail_sub_automation BEFORE INSERT ON automation_step_executions WHEN NEW.kind = 'sub_automation' BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	)
	if _, err = repo.AdmitSubAutomationRun(
		ctx,
		automations.SubAutomationStepInput{ParentRunID: run.ID, StepID: "call"},
		devices.EntityStateSnapshot{},
		time.Now().UTC(),
	); err == nil {
		t.Fatal("expected injected failure")
	}
	page, err := repo.ListHistory(ctx, automations.ListHistoryParams{AutomationID: child.ID})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("child history after rollback = %#v, %v", page, err)
	}
	entry, err := repo.GetHistoryEntry(ctx, parent.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	parentRun := entry.(automations.Run)
	if len(parentRun.Executions) != 0 || automations.RunStateStatus(parentRun.State) != automations.RunRunning {
		t.Fatalf("parent after rollback = %#v", parentRun)
	}
}

func TestRecoveryDoesNotContinueCompletedChildInvocation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := newAutomationRepository(t, openAutomationDatabase(t))
	child, err := repo.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{{ID: "call", Body: automations.SubAutomationStep{AutomationID: child.ID}}}
	parent, err := repo.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	result, err := repo.AdmitManualRun(
		ctx,
		automations.ManualRunInput{AutomationID: parent.ID},
		devices.EntityStateSnapshot{},
		at,
	)
	if err != nil {
		t.Fatal(err)
	}
	run := result.(automations.Run)
	initializeStorageRun(t, repo, run)
	linked, err := repo.AdmitSubAutomationRun(
		ctx,
		automations.SubAutomationStepInput{ParentRunID: run.ID, StepID: "call"},
		devices.EntityStateSnapshot{},
		at,
	)
	if err != nil || linked == nil {
		t.Fatalf("child = %#v, %v", linked, err)
	}
	if err = finishStorageCommandRun(t, repo, *linked, at); err != nil {
		t.Fatal(err)
	}
	if err = repo.InterruptActiveRuns(ctx, at, automations.FailureCoreRestarted); err != nil {
		t.Fatal(err)
	}
	entry, err := repo.GetHistoryEntry(ctx, parent.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	retained := entry.(automations.Run)
	stopped, ok := retained.Executions[0].Details.(automations.SubAutomationExecutionDetails).State.(automations.SubAutomationInterrupted)
	if !ok || stopped.ChildRunID != linked.ID || stopped.ReasonCode != automations.FailureCoreRestarted ||
		automations.RunStateStatus(retained.State) != automations.RunInterrupted {
		t.Fatalf("parent = %#v", retained)
	}
	entry, err = repo.GetHistoryEntry(ctx, child.ID, string(linked.ID))
	if err != nil || automations.RunStateStatus(entry.(automations.Run).State) != automations.RunSucceeded {
		t.Fatalf("child = %#v, %v", entry, err)
	}
}

//nolint:gocognit // The table checks linked outcomes for all rejection paths.
func TestSubAutomationConditionSkipAndMissingTarget(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		reason string
	}{
		{"missing", "automation_not_found"}, {"false", "conditions_false"}, {"unknown", "conditions_unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			database := openAutomationDatabase(t)
			repo := newAutomationRepository(t, database)
			entity := newEntityID(t)
			childDefinition := validDomainDefinition(t)
			childDefinition.Conditions = conditionLeaf("gate", entity, automations.ComparisonEqual, "5")
			child, err := repo.CreateAutomation(ctx, childDefinition)
			if err != nil {
				t.Fatal(err)
			}
			parentDefinition := validDomainDefinition(t)
			parentDefinition.Steps = []automations.Step{
				{ID: "call", Body: automations.SubAutomationStep{AutomationID: child.ID}},
			}
			parent, err := repo.CreateAutomation(ctx, parentDefinition)
			if err != nil {
				t.Fatal(err)
			}
			result, err := repo.AdmitManualRun(
				ctx,
				automations.ManualRunInput{AutomationID: parent.ID},
				stateSnapshotWith(),
				admissionNow,
			)
			if err != nil {
				t.Fatal(err)
			}
			run := result.(automations.Run)
			initializeStorageRun(t, repo, run)
			if test.name == "missing" {
				if err = repo.DeleteAutomation(ctx, child.ID, child.Revision); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := stateSnapshotWith(absentEntityEntry(entity))
			if test.name == "false" {
				snapshot = stateSnapshotWith(presentStateEntry(t, entity, `{"level":1}`, admissionNow))
			}
			admitted, err := repo.AdmitSubAutomationRun(
				ctx,
				automations.SubAutomationStepInput{ParentRunID: run.ID, StepID: "call"},
				snapshot,
				admissionNow,
			)
			if err != nil || admitted != nil {
				t.Fatalf("admission = %#v, %v", admitted, err)
			}
			entry, err := repo.GetHistoryEntry(ctx, parent.ID, string(run.ID))
			if err != nil {
				t.Fatal(err)
			}
			parentRun := entry.(automations.Run)
			rejected, ok := parentRun.Executions[0].Details.(automations.SubAutomationExecutionDetails).State.(automations.SubAutomationRejected)
			if !ok || rejected.ReasonCode != test.reason ||
				automations.RunStateStatus(parentRun.State) != automations.RunFailed {
				t.Fatalf("parent = %#v", parentRun)
			}
			if test.name == "missing" {
				if rejected.SkipID != nil {
					t.Fatal("missing target produced Skip")
				}
				return
			}
			if rejected.SkipID == nil {
				t.Fatal("condition rejection lacks Skip")
			}
			entry, err = repo.GetHistoryEntry(ctx, child.ID, string(*rejected.SkipID))
			if err != nil {
				t.Fatal(err)
			}
			skip := entry.(automations.Skip)
			cause, ok := skip.Cause.(automations.InvocationCause)
			if !ok || cause.ParentRunID != run.ID || cause.ParentStepID != "call" ||
				cause.ParentAutomationID != parent.ID {
				t.Fatalf("skip = %#v", skip)
			}
		})
	}
}
