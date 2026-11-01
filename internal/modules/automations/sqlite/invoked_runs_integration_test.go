//go:build integration

package sqlite_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestInvokeAdmissionLinksAtomicallyAndRefusesUnverifiedCompletion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database := openAutomationDatabase(t)
	repo := newAutomationRepository(t, database)
	child, err := repo.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{{ID: "call", Body: automations.InvokeStep{AutomationID: child.ID}}}
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
	input := automations.InvokeStepInput{ParentRunID: run.ID, StepID: "call"}
	linked, err := repo.AdmitInvokedRun(ctx, input, devices.EntityStateSnapshot{}, time.Now().UTC())
	if err != nil || linked == nil {
		t.Fatalf("admission = %#v, %v", linked, err)
	}
	if _, err = repo.AdmitInvokedRun(ctx, input, devices.EntityStateSnapshot{}, time.Now().UTC()); err == nil {
		t.Fatal("duplicate reach admitted")
	}
	if _, err = repo.CompleteInvokeStep(ctx, input, time.Now().UTC()); err == nil {
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
	state, ok := retained.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeRunning)
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
	interrupted, ok := retained.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeInterrupted)
	if !ok || interrupted.ChildRunID != linked.ID || interrupted.ReasonCode != automations.FailureCoreRestarted ||
		automations.RunStateStatus(retained.State) != automations.RunInterrupted {
		t.Fatalf("recovered parent = %#v", retained)
	}
	entry, err = repo.GetHistoryEntry(ctx, child.ID, string(linked.ID))
	if err != nil || automations.RunStateStatus(entry.(automations.Run).State) != automations.RunInterrupted {
		t.Fatalf("recovered child = %#v, %v", entry, err)
	}
}

func TestInvokeAdmissionRollbackOnParentEvidenceFailure(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database := openAutomationDatabase(t)
	repo := newAutomationRepository(t, database)
	child, err := repo.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{{ID: "call", Body: automations.InvokeStep{AutomationID: child.ID}}}
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
		`CREATE TRIGGER fail_invoke BEFORE INSERT ON automation_step_executions WHEN NEW.kind = 'invoke' BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	)
	if _, err = repo.AdmitInvokedRun(
		ctx,
		automations.InvokeStepInput{ParentRunID: run.ID, StepID: "call"},
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
	definition.Steps = []automations.Step{{ID: "call", Body: automations.InvokeStep{AutomationID: child.ID}}}
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
	linked, err := repo.AdmitInvokedRun(
		ctx,
		automations.InvokeStepInput{ParentRunID: run.ID, StepID: "call"},
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
	stopped, ok := retained.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeInterrupted)
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
func TestInvokeConditionSkipAndMissingTarget(t *testing.T) {
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
				{ID: "call", Body: automations.InvokeStep{AutomationID: child.ID}},
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
			admitted, err := repo.AdmitInvokedRun(
				ctx,
				automations.InvokeStepInput{ParentRunID: run.ID, StepID: "call"},
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
			rejected, ok := parentRun.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeRejected)
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
