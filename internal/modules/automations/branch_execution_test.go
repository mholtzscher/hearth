package automations_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func branchTriggerCondition(matched bool) automations.Condition {
	leaf := automations.Condition{
		ID:   "source",
		Body: automations.TriggerCondition{TriggerIDs: []automations.TriggerID{"trigger"}},
	}
	if matched { // Manual Runs have no matches, so negation is true.
		return automations.Condition{ID: "manual", Body: automations.NotCondition{Child: leaf}}
	}
	return leaf
}

func branchIf(
	id automations.StepID,
	condition automations.Condition,
	then, otherwise []automations.Step,
) automations.Step {
	return automations.Step{ID: id, Body: automations.IfStep{Conditions: condition, Then: then, Else: otherwise}}
}

func branchChoose(
	conditionA, conditionB automations.Condition,
	commands []automations.Step,
	fallback bool,
) automations.Step {
	step := automations.Step{ID: "route", Body: automations.ChooseStep{
		Branches: []automations.ChooseBranch{
			{ID: "first", Conditions: conditionA, Steps: commands[:1]},
			{ID: "second", Conditions: conditionB, Steps: commands[1:2]},
		},
	}}
	if fallback {
		chooseBody := step.Body.(automations.ChooseStep)
		chooseBody.Default = commands[2:3]
		step.Body = chooseBody
	}
	return step
}

func startBranchRun(
	t *testing.T,
	service *automations.Service,
	definition automations.Definition,
) automations.Run {
	t.Helper()
	record := createRuntimeAutomation(t, service, definition)
	run, err := service.StartManualRun(
		context.Background(),
		automations.ManualRunInput{AutomationID: record.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	entry := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(entry) == nil {
		t.Fatal("expected retained Run")
	}
	return *runEntry(entry)
}

// A8: selected paths resume siblings and never dispatch unselected commands.
//
//nolint:gocognit // Each routing case checks path order, durable decisions, and unselected attempts together.
func TestBranchRunRouting(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		build     func([]automations.Step) []automations.Step
		outcomes  []automations.BranchOutcome
		positions []int
	}{
		{"if then", func(c []automations.Step) []automations.Step {
			return []automations.Step{branchIf("route", branchTriggerCondition(true), c[:1], c[1:2]), c[2]}
		}, []automations.BranchOutcome{automations.BranchThen}, []int{0, 2}},
		{"if else", func(c []automations.Step) []automations.Step {
			return []automations.Step{branchIf("route", branchTriggerCondition(false), c[:1], c[1:2]), c[2]}
		}, []automations.BranchOutcome{automations.BranchElse}, []int{1, 2}},
		{"if no-op", func(c []automations.Step) []automations.Step {
			return []automations.Step{branchIf("route", branchTriggerCondition(false), c[:1], nil)}
		}, []automations.BranchOutcome{automations.BranchNoMatch}, nil},
		{"choose first true", func(c []automations.Step) []automations.Step {
			return []automations.Step{branchChoose(branchTriggerCondition(true), branchTriggerCondition(true), c, true)}
		}, []automations.BranchOutcome{automations.BranchChosen}, []int{0}},
		{"choose default", func(c []automations.Step) []automations.Step {
			return []automations.Step{branchChoose(branchTriggerCondition(false), branchTriggerCondition(false), c, true)}
		}, []automations.BranchOutcome{automations.BranchDefault}, []int{2}},
		{"choose no-op", func(c []automations.Step) []automations.Step {
			return []automations.Step{branchChoose(branchTriggerCondition(false), branchTriggerCondition(false), c, false)}
		}, []automations.BranchOutcome{automations.BranchNoMatch}, nil},
		{"nested resumes outer", func(c []automations.Step) []automations.Step {
			nested := branchIf("nested", branchTriggerCondition(false), c[:1], c[1:2])
			return []automations.Step{branchIf("route", branchTriggerCondition(true), []automations.Step{nested}, nil), c[2]}
		}, []automations.BranchOutcome{automations.BranchThen, automations.BranchElse}, []int{1, 2}},
		{"command-free selected path", func(c []automations.Step) []automations.Step {
			return []automations.Step{branchIf("route", branchTriggerCondition(true), []automations.Step{branchIf("nested", branchTriggerCondition(false), c[:1], nil)}, nil)}
		}, []automations.BranchOutcome{automations.BranchThen, automations.BranchNoMatch}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scripted := newScriptedDevices()
			service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
			definition := runtimeDefinition(t, 3)
			definition.Steps = test.build(definition.Steps)
			run := startBranchRun(t, service, definition)
			if automations.RunStateStatus(run.State) != automations.RunSucceeded ||
				scripted.executionCount() != len(test.positions) {
				t.Fatalf("Run = %#v; executions = %d", run, scripted.executionCount())
			}
			var outcomes []automations.BranchOutcome
			for position, decision := range run.BranchDecisions {
				outcomes = append(outcomes, decision.Outcome())
				if decision.Position != position {
					t.Fatalf("decision position = %d", decision.Position)
				}
			}
			if !slices.Equal(outcomes, test.outcomes) {
				t.Fatalf("outcomes = %v, want %v", outcomes, test.outcomes)
			}
			leaves := automations.CommandLeaves(run.Snapshot.Steps)
			for index, position := range test.positions {
				if scripted.executions[index].EntityID != leaves[position].Command.EntityID {
					t.Fatalf("command %d did not execute position %d", index, position)
				}
			}
			for position, attempt := range run.Steps {
				if slices.Contains(test.positions, position) {
					if automations.StepAttemptStatus(attempt.State) != automations.StepSatisfied ||
						stepVerified(attempt.State) == nil {
						t.Fatalf("selected attempt = %#v", attempt)
					}
				} else if automations.StepAttemptStatus(attempt.State) != automations.StepNotAttempted || stepVerified(attempt.State) != nil {
					t.Fatalf("unselected attempt = %#v", attempt)
				}
			}
			if len(scripted.snapshotRequests()) != 0 {
				t.Fatal("Trigger-only routing read State")
			}
		})
	}
}

// A9/A12: commands and nested decisions read fresh State, while alternatives share
// the same snapshot. Unselected nested State never enters the requested batch.
func TestBranchReadsFreshStateOnlyAtReachedConstructs(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	entity := newEntityID(t)
	unselectedEntity := newEntityID(t)
	definition := runtimeDefinition(t, 5)
	commands := definition.Steps
	first := *admissionConditionTree(entity, "10")
	second := *admissionConditionTree(entity, "30")
	nested := branchIf("nested", first, commands[2:3], nil)
	unselected := branchIf("unselected", *admissionConditionTree(unselectedEntity, "10"), commands[3:4], nil)
	choose := branchChoose(first, second, commands, false)
	chooseBody := choose.Body.(automations.ChooseStep)
	chooseBody.Branches[0].Steps = []automations.Step{unselected}
	choose.Body = chooseBody
	chooseBody2 := choose.Body.(automations.ChooseStep)
	chooseBody2.Branches[1].Steps = []automations.Step{commands[1], nested}
	choose.Body = chooseBody2
	definition.Steps = []automations.Step{commands[0], choose, commands[4]}
	scripted.execute = func(_ context.Context, input devices.CommandInput) (devices.CommandResult, error) {
		value := `{"level":20}`
		if input.EntityID == commands[1].Body.(automations.CommandStep).EntityID {
			value = `{"level":5}`
		}
		scripted.setEntityStateSnapshot(admissionSnapshot(admissionState(t, entity, value, runtimeTestNow)))
		scripted.recordCommand(terminalCommand(t, input, devices.CommandStatusSatisfied, nil))
		return devices.CommandResult{CommandID: input.ID, Outcome: devices.OutcomeDispatched}, nil
	}
	scripted.onSnapshotRead = func() {
		// Mutation after the coherent snapshot copy cannot change either alternative.
		scripted.setEntityStateSnapshot(admissionSnapshot(admissionState(t, entity, `{"level":99}`, runtimeTestNow)))
	}
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	run := startBranchRun(t, service, definition)
	if automations.RunStateStatus(run.State) != automations.RunSucceeded || scripted.executionCount() != 4 {
		t.Fatalf("Run = %#v", run)
	}
	requests := scripted.snapshotRequests()
	if len(requests) != 2 || !slices.Equal(requests[0], []devices.EntityID{entity}) ||
		!slices.Equal(requests[1], []devices.EntityID{entity}) {
		t.Fatalf("State requests = %v", requests)
	}
	if len(run.BranchDecisions) != 2 {
		t.Fatalf("decisions = %#v", run.BranchDecisions)
	}
	outer, inner := run.BranchDecisions[0], run.BranchDecisions[1]
	if outer.Outcome() != automations.BranchChosen ||
		outer.Body.(automations.ChooseDecision).Result.(automations.ChooseSelected).BranchID != "second" ||
		len(chooseEvaluations(outer)) != 2 ||
		inner.Outcome() != automations.BranchThen {
		t.Fatalf("decisions = %#v", run.BranchDecisions)
	}
	for _, item := range chooseEvaluations(outer) {
		if !item.Evaluation.EvaluatedAt.Equal(outer.EvaluatedAt) ||
			string(item.Evaluation.Nodes[0].Evidence.(automations.KnownStateEvidence).SelectedValue) != "20" {
			t.Fatalf("incoherent evidence = %#v", item)
		}
	}
	if string(ifEvaluation(inner).Nodes[0].Evidence.(automations.KnownStateEvidence).SelectedValue) != "5" {
		t.Fatal("nested branch did not use fresh State")
	}
	if automations.StepAttemptStatus(run.Steps[1].State) != automations.StepNotAttempted {
		t.Fatalf("unselected nested command = %#v", run.Steps[1])
	}
}

//nolint:gocognit // The matrix checks retained leaf reasons and their distinct routing outcomes.
func TestBranchThreeValuedRoutingAndUnknownReasons(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, value string
		kind        automations.ConditionKind
		triggerTrue bool
		reason      automations.ConditionUnknownReason
		outcome     automations.BranchOutcome
	}{
		{"missing State", "", "", false, automations.ConditionUnknownStateMissing, automations.BranchUnknown},
		{"missing Entity", "", "", false, automations.ConditionUnknownEntityMissing, automations.BranchUnknown},
		{"expired evidence", `{"level":5}`, "", false, automations.ConditionUnknownEvidenceExpired, automations.BranchUnknown},
		{"JSON null", `{"level":null}`, "", false, automations.ConditionUnknownTypeMismatch, automations.BranchUnknown},
		{"missing pointer", `{}`, "", false, automations.ConditionUnknownPointerMissing, automations.BranchUnknown},
		{"incompatible type", `{"level":"low"}`, "", false, automations.ConditionUnknownTypeMismatch, automations.BranchUnknown},
		{"any true unknown", "", automations.ConditionAny, true, automations.ConditionUnknownStateMissing, automations.BranchChosen},
		{"all false unknown", "", automations.ConditionAll, false, automations.ConditionUnknownStateMissing, automations.BranchDefault},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scripted := newScriptedDevices()
			entity := newEntityID(t)
			entry := devices.EntityStateSnapshotEntry{EntityID: entity, Exists: true}
			if test.reason == automations.ConditionUnknownEntityMissing {
				entry.Exists = false
			}
			if test.value != "" {
				entry = admissionState(t, entity, test.value, runtimeTestNow)
			}
			scripted.setEntityStateSnapshot(admissionSnapshot(entry))
			condition := *admissionConditionTree(entity, "10")
			if test.reason == automations.ConditionUnknownEvidenceExpired {
				age := int64(60)
				stateBody := condition.Body.(automations.EntityStateCondition)
				stateBody.MaxAgeSeconds = &age
				condition.Body = stateBody
				entry.State.ObservedAt = runtimeTestNow.Add(-2 * time.Minute)
				scripted.setEntityStateSnapshot(admissionSnapshot(entry))
			}
			if test.kind != "" {
				condition = automations.Condition{
					ID: "group",
					Body: groupBody(
						test.kind,
						[]automations.Condition{branchTriggerCondition(test.triggerTrue), condition},
					),
				}
			}
			definition := runtimeDefinition(t, 3)
			later := branchTriggerCondition(false)
			if test.outcome == automations.BranchChosen {
				later = *admissionConditionTree(entity, "10")
			}
			definition.Steps = []automations.Step{
				branchChoose(condition, later, definition.Steps, true),
			}
			service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
			run := startBranchRun(t, service, definition)
			if len(run.BranchDecisions) != 1 || run.BranchDecisions[0].Outcome() != test.outcome {
				t.Fatalf("decisions = %#v", run.BranchDecisions)
			}
			decision := run.BranchDecisions[0]
			if test.outcome == automations.BranchChosen && len(chooseEvaluations(decision)) != 1 {
				t.Fatal("evaluated unknown alternative after selection")
			}
			nodes := chooseEvaluations(decision)[0].Evaluation.Nodes
			leaf := nodes[len(nodes)-1].Evidence.(automations.UnknownStateEvidence)
			if leaf.Reason != test.reason {
				t.Fatalf("leaf = %#v, want %s", leaf, test.reason)
			}
			if test.outcome == automations.BranchUnknown {
				if len(chooseEvaluations(decision)) != 1 ||
					automations.RunStateStatus(run.State) != automations.RunFailed ||
					scripted.executionCount() != 0 {
					t.Fatalf("unknown fell through: %#v", run)
				}
			} else if automations.RunStateStatus(run.State) != automations.RunSucceeded || scripted.executionCount() != 1 {
				t.Fatalf("Run = %#v", run)
			}
		})
	}
}

// Bypass is admission-only; a branch failure is a retained Run, never a Skip.
func TestManualBypassDoesNotBypassBranchAndBusyGuard(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	entity := newEntityID(t)
	scripted.setEntityStateSnapshot(admissionSnapshot(devices.EntityStateSnapshotEntry{EntityID: entity, Exists: true}))
	definition := runtimeDefinition(t, 1)
	definition.Conditions = admissionConditionTree(newEntityID(t), "10")
	definition.Steps = []automations.Step{
		branchIf("route", *admissionConditionTree(entity, "10"), definition.Steps, nil),
	}
	reading, release := make(chan struct{}), make(chan struct{})
	scripted.onSnapshotRead = func() { close(reading); <-release }
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, definition)
	run, err := service.StartManualRun(
		context.Background(),
		automations.ManualRunInput{AutomationID: record.ID, BypassConditions: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	<-reading
	_, busyErr := service.StartManualRun(
		context.Background(),
		automations.ManualRunInput{AutomationID: record.ID, BypassConditions: true},
	)
	close(release)
	waitForRuns(t, service)
	if !errors.Is(busyErr, automations.ErrAutomationBusy) {
		t.Fatalf("concurrent invocation = %v", busyErr)
	}
	finished := runEntry(historyEntry(t, service, record.ID, string(run.ID)))
	if finished == nil || automations.RunStateStatus(finished.State) != automations.RunFailed ||
		len(finished.BranchDecisions) != 1 ||
		finished.BranchDecisions[0].Outcome() != automations.BranchUnknown {
		t.Fatalf("Run = %#v", finished)
	}
	if requests := scripted.snapshotRequests(); len(requests) != 1 ||
		!slices.Equal(requests[0], []devices.EntityID{entity}) {
		t.Fatalf("bypass read admission references: %v", requests)
	}
}

func TestSelectedBranchCommandFailureDoesNotTryFallback(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	scripted.execute = func(context.Context, devices.CommandInput) (devices.CommandResult, error) {
		return devices.CommandResult{}, devices.ErrInvalidCommand
	}
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	definition := runtimeDefinition(t, 4)
	definition.Steps = []automations.Step{
		branchChoose(branchTriggerCondition(true), branchTriggerCondition(true), definition.Steps, true),
		definition.Steps[3],
	}
	run := startBranchRun(t, service, definition)
	if automations.RunStateStatus(run.State) != automations.RunFailed || scripted.executionCount() != 1 ||
		automations.StepAttemptStatus(run.Steps[0].State) != automations.StepFailed {
		t.Fatalf("Run = %#v", run)
	}
	if len(run.BranchDecisions) != 1 || len(chooseEvaluations(run.BranchDecisions[0])) != 1 {
		t.Fatalf("decision = %#v", run.BranchDecisions)
	}
	for _, attempt := range run.Steps[1:] {
		if automations.StepAttemptStatus(attempt.State) != automations.StepNotAttempted {
			t.Fatalf("fallback attempted: %#v", attempt)
		}
	}
}

// A10/A11: false prefixes survive corruption; failed trees supply no partial evidence.
//
//nolint:gocognit // One failure matrix verifies evidence, prior effects, and untouched later attempts.
func TestBranchFailuresRetainTruthfulEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, value, code string
		readErr           error
		covered, prefix   bool
	}{
		{"absent State", "", "branch_condition_unknown", nil, true, false},
		{"corrupt after false", "{", "branch_state_corrupt", nil, true, true},
		{"incomplete before trigger match", "", "branch_snapshot_incomplete", nil, false, false},
		{"read error", "", "branch_state_read_failed", errors.New("read unavailable"), true, false},
		{"corrupt read", "", "branch_state_corrupt", devices.ErrEntityStateSnapshotCorrupt, true, false},
		{"deadline", "", "branch_state_read_failed", context.DeadlineExceeded, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scripted := newScriptedDevices()
			entity := newEntityID(t)
			if test.covered {
				entry := devices.EntityStateSnapshotEntry{EntityID: entity, Exists: true}
				if test.value != "" {
					entry = admissionState(t, entity, test.value, runtimeTestNow)
				}
				scripted.setEntityStateSnapshot(admissionSnapshot(entry))
			}
			scripted.setEntityStateSnapshotError(test.readErr)
			service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
			definition := runtimeDefinition(t, 4)
			condition := *admissionConditionTree(entity, "30")
			if test.prefix {
				condition = automations.Condition{
					ID: "failed-tree",
					Body: automations.AllCondition{
						Children: []automations.Condition{branchTriggerCondition(true), condition},
					},
				}
			}
			first := condition
			if test.prefix {
				first = branchTriggerCondition(false)
			}
			if !test.covered {
				first = branchTriggerCondition(true)
			}
			definition.Steps = []automations.Step{
				definition.Steps[3],
				branchChoose(first, condition, definition.Steps, true),
			}
			run := startBranchRun(t, service, definition)
			if automations.RunStateStatus(run.State) != automations.RunFailed || runFailure(run.State) == nil ||
				*runFailure(run.State) != test.code {
				t.Fatalf("Run = %#v, want failed/%s", run, test.code)
			}
			if scripted.executionCount() != 1 ||
				automations.StepAttemptStatus(run.Steps[0].State) != automations.StepSatisfied {
				t.Fatal("failure lost earlier effect or dispatched fallback")
			}
			if len(run.BranchDecisions) != 1 {
				t.Fatalf("decisions = %#v", run.BranchDecisions)
			}
			decision := run.BranchDecisions[0]
			wantEvaluations := 0
			if test.prefix || test.code == "branch_condition_unknown" {
				wantEvaluations = 1
			}
			if len(chooseEvaluations(decision)) != wantEvaluations {
				t.Fatalf("evaluations = %#v", chooseEvaluations(decision))
			}
			if test.prefix && chooseEvaluations(decision)[0].Evaluation.Result != automations.ConditionFalse {
				t.Fatal("lost false prefix")
			}
			for _, attempt := range run.Steps[1:] {
				if automations.StepAttemptStatus(attempt.State) != automations.StepNotAttempted {
					t.Fatalf("unexpected attempt = %#v", attempt)
				}
			}
			if !service.AdmissionOpen() {
				t.Fatal("expected evidence failure closed readiness")
			}
		})
	}
}

// A6/A15: the real decision transaction is intercepted only at its return boundary.
type branchWriteRepository struct {
	automations.Repository

	afterCommit  func()
	onWrite      func(context.Context)
	writeErr     error
	commit       bool
	interruptErr error
	writes       int
}

func (repository *branchWriteRepository) RecordBranchDecision(
	ctx context.Context,
	id automations.RunID,
	decision automations.BranchDecision,
) error {
	repository.writes++
	if repository.onWrite != nil {
		repository.onWrite(ctx)
	}
	if repository.commit {
		if err := repository.Repository.RecordBranchDecision(ctx, id, decision); err != nil {
			return err
		}
	}
	if repository.afterCommit != nil {
		repository.afterCommit()
	}
	return repository.writeErr
}

func (repository *branchWriteRepository) CompleteRun(ctx context.Context, completion automations.RunCompletion) error {
	if repository.interruptErr != nil &&
		automations.RunOutcomeStatus(completion.Outcome) == automations.RunInterrupted {
		return repository.interruptErr
	}
	return repository.Repository.CompleteRun(ctx, completion)
}

//nolint:gocognit // The commit/drain matrix checks interruption, retained decisions, and admission closure together.
func TestBranchCommitFaultAndDrainLeaveAttemptsUntouched(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                             string
		commit, fault, interruptionFails bool
	}{
		{"write failed", false, true, false},
		{"ambiguous commit", true, true, false},
		{"committed then drain", true, false, false},
		{"fault interruption failed", false, true, true},
		{"drain interruption failed", true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := &branchWriteRepository{
				Repository: automationssqlite.NewAutomationRepository(database, runtimeTestDependencies()),
				commit:     test.commit,
			}
			if test.fault {
				repository.writeErr = errors.New("ambiguous persistence error")
			}
			if test.interruptionFails {
				repository.interruptErr = errors.New("interruption unavailable")
			}
			scripted := newScriptedDevices()
			service := automations.NewService(repository, scripted, runtimeTestDependencies())
			if !test.fault {
				repository.afterCommit = service.StopAdmission
			}
			definition := runtimeDefinition(t, 2)
			definition.Steps = []automations.Step{
				branchIf("route", branchTriggerCondition(true), definition.Steps[:1], definition.Steps[1:]),
			}
			run := startBranchRun(t, service, definition)
			if repository.writes != 1 || scripted.executionCount() != 0 {
				t.Fatalf("writes = %d, executions = %d", repository.writes, scripted.executionCount())
			}
			for _, attempt := range run.Steps {
				if automations.StepAttemptStatus(attempt.State) != automations.StepNotAttempted ||
					stepVerified(attempt.State) != nil {
					t.Fatalf("branch boundary changed attempt = %#v", attempt)
				}
			}
			wantStatus := automations.RunInterrupted
			if test.interruptionFails {
				wantStatus = automations.RunRunning
			}
			if automations.RunStateStatus(run.State) != wantStatus {
				t.Fatalf("status = %s, want %s", automations.RunStateStatus(run.State), wantStatus)
			}
			wantDecisions := 0
			if test.commit {
				wantDecisions = 1
			}
			if len(run.BranchDecisions) != wantDecisions {
				t.Fatalf("decisions = %#v", run.BranchDecisions)
			}
			if service.AdmissionOpen() {
				t.Fatal("fault/drain left admission open")
			}
			_, err := service.StartManualRun(
				context.Background(),
				automations.ManualRunInput{AutomationID: run.AutomationID},
			)
			if !errors.Is(err, automations.ErrAdmissionUnavailable) {
				t.Fatalf("post-fault admission = %v", err)
			}
			if test.interruptionFails {
				restarted := automations.NewService(repository.Repository, scripted, runtimeTestDependencies())
				if err = restarted.InterruptActiveRuns(context.Background(), runtimeTestNow); err != nil {
					t.Fatal(err)
				}
				recovered := runEntry(historyEntry(t, restarted, run.AutomationID, string(run.ID)))
				if recovered == nil || automations.RunStateStatus(recovered.State) != automations.RunInterrupted ||
					*runFailure(recovered.State) != automations.FailureCoreRestarted ||
					len(recovered.BranchDecisions) != wantDecisions {
					t.Fatalf("recovered Run = %#v", recovered)
				}
				for _, attempt := range recovered.Steps {
					if automations.StepAttemptStatus(attempt.State) != automations.StepNotAttempted {
						t.Fatalf("restart changed unattempted command: %#v", attempt)
					}
				}
				if scripted.executionCount() != 0 {
					t.Fatal("restart replayed selected commands")
				}
			}
		})
	}
}

func TestBranchReadAndWriteAreBoundedAndCommitPrecedesDispatch(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := &branchWriteRepository{
		Repository: automationssqlite.NewAutomationRepository(database, runtimeTestDependencies()),
		commit:     true,
	}
	scripted := newScriptedDevices()
	entity := newEntityID(t)
	assertBound := func(ctx context.Context) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second || time.Until(deadline) <= 0 {
			t.Errorf("operation has no five-second deadline: %v, %v", deadline, ok)
		}
	}
	repository.onWrite = assertBound
	scripted.snapshotRead = func(ctx context.Context, ids []devices.EntityID) (devices.EntityStateSnapshot, error) {
		assertBound(ctx)
		if !slices.Equal(ids, []devices.EntityID{entity}) {
			t.Errorf("requested Entities = %v", ids)
		}
		return admissionSnapshot(admissionState(t, entity, `{"level":5}`, runtimeTestNow)), nil
	}
	scripted.onStart = func(devices.CommandInput) {
		var decisions int
		if err := database.QueryRowContext(context.Background(), "SELECT count(*) FROM automation_run_branch_decisions").
			Scan(&decisions); err != nil {
			t.Error(err)
		}
		if decisions != 1 {
			t.Errorf("dispatch before committed decision: count = %d", decisions)
		}
	}
	service := automations.NewService(repository, scripted, runtimeTestDependencies())
	definition := runtimeDefinition(t, 1)
	definition.Steps = []automations.Step{
		branchIf("route", *admissionConditionTree(entity, "10"), definition.Steps, nil),
	}
	run := startBranchRun(t, service, definition)
	if automations.RunStateStatus(run.State) != automations.RunSucceeded || repository.writes != 1 ||
		scripted.executionCount() != 1 {
		t.Fatalf("Run = %#v", run)
	}
}

func TestNestedBranchDrainPreservesCompletedChildAndPendingOuterSibling(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := &branchWriteRepository{
		Repository: automationssqlite.NewAutomationRepository(database, runtimeTestDependencies()),
		commit:     true,
	}
	scripted := newScriptedDevices()
	service := automations.NewService(repository, scripted, runtimeTestDependencies())
	repository.afterCommit = func() {
		if repository.writes == 2 {
			service.StopAdmission()
		}
	}
	definition := runtimeDefinition(t, 4)
	commands := definition.Steps
	nested := branchIf("nested", branchTriggerCondition(true), commands[1:2], commands[2:3])
	definition.Steps = []automations.Step{
		branchIf("outer", branchTriggerCondition(true), []automations.Step{commands[0], nested}, nil),
		commands[3],
	}
	run := startBranchRun(t, service, definition)
	if automations.RunStateStatus(run.State) != automations.RunInterrupted || runFailure(run.State) == nil ||
		*runFailure(run.State) != automations.FailureCoreStopping ||
		len(run.BranchDecisions) != 2 ||
		scripted.executionCount() != 1 {
		t.Fatalf("Run = %#v", run)
	}
	if automations.StepAttemptStatus(run.Steps[0].State) != automations.StepSatisfied ||
		stepVerified(run.Steps[0].State) == nil {
		t.Fatalf("completed child = %#v", run.Steps[0])
	}
	for _, attempt := range run.Steps[1:] {
		if automations.StepAttemptStatus(attempt.State) != automations.StepNotAttempted ||
			stepVerified(attempt.State) != nil {
			t.Fatalf("branch drain changed pending attempt = %#v", attempt)
		}
	}
	restarted := automations.NewService(repository.Repository, scripted, runtimeTestDependencies())
	if err := restarted.InterruptActiveRuns(context.Background(), runtimeTestNow); err != nil {
		t.Fatal(err)
	}
	if scripted.executionCount() != 1 {
		t.Fatal("restart replayed a pending child or outer sibling")
	}
}

// malformedRunRepository injects a broken prepared value after real admission
// commits. The durable snapshot and command attempts remain valid and unchanged.
type malformedRunRepository struct {
	automations.Repository

	malform func(*automations.Step)
}

func (repository malformedRunRepository) AdmitManualRun(
	ctx context.Context,
	input automations.ManualRunInput,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
) (automations.ManualAdmissionResult, error) {
	result, err := repository.Repository.AdmitManualRun(ctx, input, snapshot, at)
	if err == nil && runEntry(result) != nil {
		repository.malform(&runEntry(result).Snapshot.Steps[0])
	}
	return result, err
}

func TestMalformedPreparedBranchFaultLeavesDecisionsAndAttemptsUntouched(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		malform func(*automations.Step)
	}{
		{"nil If payload", func(step *automations.Step) { step.Body = nil }},
		{"nil Choose payload", func(step *automations.Step) { step.Body = nil }},
		{"nil State payload", func(step *automations.Step) {
			ifBody := step.Body.(automations.IfStep)
			ifBody.Conditions = automations.Condition{ID: "broken", Body: nil}
			step.Body = ifBody
		}},
		{"nil Not child", func(step *automations.Step) {
			ifBody2 := step.Body.(automations.IfStep)
			ifBody2.Conditions = automations.Condition{ID: "broken", Body: automations.NotCondition{Child: automations.Condition{}}}
			step.Body = ifBody2
		}},
		{"nil Trigger payload", func(step *automations.Step) {
			ifBody3 := step.Body.(automations.IfStep)
			ifBody3.Conditions = automations.Condition{ID: "broken", Body: nil}
			step.Body = ifBody3
		}},
		{"nested malformed leaf", func(step *automations.Step) {
			ifBody4 := step.Body.(automations.IfStep)
			ifBody4.Conditions = automations.Condition{ID: "broken", Body: automations.AnyCondition{Children: []automations.Condition{{ID: "leaf", Body: nil}}}}
			step.Body = ifBody4
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := malformedRunRepository{
				Repository: automationssqlite.NewAutomationRepository(database, runtimeTestDependencies()),
				malform:    test.malform,
			}
			scripted := newScriptedDevices()
			service := automations.NewService(repository, scripted, runtimeTestDependencies())
			definition := runtimeDefinition(t, 2)
			definition.Steps = []automations.Step{
				branchIf("route", branchTriggerCondition(true), definition.Steps[:1], definition.Steps[1:]),
			}
			run := startBranchRun(t, service, definition)
			if automations.RunStateStatus(run.State) != automations.RunInterrupted || runFailure(run.State) == nil ||
				*runFailure(run.State) != automations.FailureExecutorFault ||
				service.AdmissionOpen() {
				t.Fatalf("malformed snapshot did not latch executor fault: %#v", run)
			}
			if len(run.BranchDecisions) != 0 || scripted.executionCount() != 0 ||
				len(scripted.snapshotRequests()) != 0 {
				t.Fatalf("malformed snapshot invented evidence or effects: %#v", run)
			}
			for _, attempt := range run.Steps {
				if automations.StepAttemptStatus(attempt.State) != automations.StepNotAttempted ||
					stepReservedCommand(attempt.State) != nil ||
					stepReservedCorrelation(attempt.State) != nil ||
					stepVerified(attempt.State) != nil ||
					stepFailureCode(attempt.State) != nil ||
					stepStarted(attempt.State) != nil ||
					stepCompletedAt(attempt.State) != nil {
					t.Fatalf("Run-only fault changed command attempt: %#v", attempt)
				}
			}
		})
	}
}
