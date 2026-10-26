package automations_test

import (
	"testing"
	"time"

	automations "github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestBranchSelectionSuccessDoesNotEstablishChildSuccess(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	snapshot := branchingFixture(t)
	snapshot.Steps = []automations.Step{{ID: "route", Body: automations.IfStep{
		Conditions: triggerPredicate("a"),
		Then:       []automations.Step{{ID: "wait", Body: automations.DelayStep{DurationMS: 1}}},
	}}}
	decision := automations.BranchDecision{StepID: "route", EvaluatedAt: at, Body: automations.IfDecision{
		Result: automations.IfSelected{Arm: automations.IfThen, Evaluation: automations.ConditionEvaluation{
			EvaluatedAt: at,
			Result:      automations.ConditionTrue,
			Nodes: []automations.ConditionNodeResult{
				{
					ID:       "match",
					Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: []automations.TriggerID{"a"}},
				},
			},
		}},
	}}
	branch := automations.StepExecution{StepID: "route", Body: automations.BranchExecutionBody{Decision: decision}}
	envelope, err := automations.EncodeStepExecutionBody(branch.Body)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Phase != "succeeded" {
		t.Fatalf("selection phase = %q, want succeeded", envelope.Phase)
	}
	for _, test := range []struct {
		name   string
		status automations.RunStatus
		child  automations.StepExecutionBody
		valid  bool
	}{
		{name: "selection permits a running prefix", status: automations.RunRunning, valid: true},
		{name: "selection alone cannot succeed the Run", status: automations.RunSucceeded},
		{name: "completed child succeeds the selected path", status: automations.RunSucceeded,
			child: automations.DelayExecutionBody{StartedAt: at, CompletedAt: &at}, valid: true},
		{name: "child interruption preserves successful selection", status: automations.RunInterrupted,
			child: automations.DelayExecutionBody{StartedAt: at, CompletedAt: &at, FailureCode: new(automations.FailureCoreStopping)}, valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			trace := []automations.StepExecution{branch}
			if test.child != nil {
				trace = append(trace, automations.StepExecution{StepID: "wait", Sequence: 1, Body: test.child})
			}
			validationErr := automations.ValidateStepExecutionTrace(
				snapshot,
				[]automations.TriggerID{"a"},
				test.status,
				trace,
			)
			if (validationErr == nil) != test.valid {
				t.Fatalf("trace validation = %v, want valid=%v", validationErr, test.valid)
			}
		})
	}
	t.Run("obsolete phase is rejected", func(t *testing.T) {
		t.Parallel()
		obsolete := envelope
		obsolete.Phase = "decided"
		if _, decodeErr := automations.DecodeStepExecutionBody("route", obsolete); decodeErr == nil {
			t.Fatal("obsolete branch phase was accepted")
		}
	})
}

func TestStepExecutionTraceRequiresCompleteSelectedPath(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	snapshot := automations.Definition{Steps: []automations.Step{
		{ID: "first", Body: automations.DelayStep{DurationMS: 1}},
		{ID: "second", Body: automations.DelayStep{DurationMS: 1}},
	}}
	first := automations.StepExecution{
		StepID:   "first",
		Sequence: 0,
		Body:     automations.DelayExecutionBody{StartedAt: at, CompletedAt: &at},
	}
	second := automations.StepExecution{
		StepID:   "second",
		Sequence: 1,
		Body:     automations.DelayExecutionBody{StartedAt: at, CompletedAt: &at},
	}
	for _, test := range []struct {
		name   string
		status automations.RunStatus
		trace  []automations.StepExecution
		valid  bool
	}{
		{name: "empty running prefix", status: automations.RunRunning, valid: true},
		{name: "empty success is not evidence", status: automations.RunSucceeded},
		{name: "partial success is rejected", status: automations.RunSucceeded, trace: []automations.StepExecution{first}},
		{name: "partial interrupted prefix", status: automations.RunInterrupted, trace: []automations.StepExecution{first}, valid: true},
		{name: "complete success", status: automations.RunSucceeded, trace: []automations.StepExecution{first, second}, valid: true},
		{name: "order cannot skip a Step", status: automations.RunRunning, trace: []automations.StepExecution{{StepID: "second", Body: second.Body}}},
		{name: "order cannot skip a sequence", status: automations.RunRunning, trace: []automations.StepExecution{{StepID: "first", Sequence: 1, Body: first.Body}}},
		{name: "duplicate reach", status: automations.RunSucceeded, trace: []automations.StepExecution{first, {StepID: "first", Sequence: 1, Body: first.Body}}},
		{name: "terminal parent cannot own running Step", status: automations.RunInterrupted, trace: []automations.StepExecution{{StepID: "first", Body: automations.DelayExecutionBody{StartedAt: at}}}},
		{name: "running Step blocks later evidence", status: automations.RunRunning, trace: []automations.StepExecution{{StepID: "first", Body: automations.DelayExecutionBody{StartedAt: at}}, second}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := automations.ValidateStepExecutionTraceWithPreparedSnapshot(snapshot, nil, test.status, test.trace)
			if (err == nil) != test.valid {
				t.Fatalf("trace validation = %v, want valid=%v", err, test.valid)
			}
		})
	}
}

func TestStepExecutionProjectionDerivesDelayConfigurationAndPosition(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	snapshot := automations.Definition{Steps: []automations.Step{
		{ID: "first", Body: automations.DelayStep{DurationMS: 25}},
		{ID: "second", Body: automations.DelayStep{DurationMS: 50}},
	}}
	projection, err := automations.ProjectStepExecutions(snapshot, []automations.StepExecution{
		{StepID: "first", Sequence: 0, Body: automations.DelayExecutionBody{StartedAt: at, CompletedAt: &at}},
		{StepID: "second", Sequence: 1, Body: automations.DelayExecutionBody{StartedAt: at}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Steps) != 0 || len(projection.Delays) != 2 {
		t.Fatalf("projection = %#v", projection)
	}
	for index, duration := range []int64{25, 50} {
		delay := projection.Delays[index]
		if delay.Position != index || delay.DurationMS != duration ||
			!delay.DueAt.Equal(at.Add(time.Duration(duration)*time.Millisecond)) {
			t.Fatalf("projected delay %d = %#v", index, delay)
		}
	}
	if projection.Delays[0].Status != automations.DelayCompleted ||
		projection.Delays[1].Status != automations.DelayRunning {
		t.Fatalf("projected states = %#v", projection.Delays)
	}
}
