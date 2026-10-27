package automations_test

import (
	"encoding/json"
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
	snapshot, err := automations.NormalizeDefinition(snapshot)
	if err != nil {
		t.Fatal(err)
	}
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
	branch := automations.StepExecution{
		StepID:  "route",
		Details: automations.BranchExecutionDetails{Decision: decision},
	}
	envelope, err := automations.EncodeStepExecutionDetails(branch.Details)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Phase != "succeeded" {
		t.Fatalf("selection phase = %q, want succeeded", envelope.Phase)
	}
	for _, test := range []struct {
		name   string
		status automations.RunStatus
		child  automations.StepExecutionDetails
		valid  bool
	}{
		{name: "selection permits a running prefix", status: automations.RunRunning, valid: true},
		{name: "selection alone cannot succeed the Run", status: automations.RunSucceeded},
		{name: "completed child succeeds the selected path", status: automations.RunSucceeded,
			child: automations.DelayExecutionDetails{StartedAt: at, State: automations.DelaySucceeded{CompletedAt: at}}, valid: true},
		{name: "child interruption preserves successful selection", status: automations.RunInterrupted,
			child: automations.DelayExecutionDetails{StartedAt: at, State: automations.DelayInterrupted{CompletedAt: at, FailureCode: automations.FailureCoreStopping}}, valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			trace := []automations.StepExecution{branch}
			if test.child != nil {
				trace = append(trace, automations.StepExecution{StepID: "wait", Sequence: 1, Details: test.child})
			}
			validationErr := validateExecutionTrace(
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
		if _, _, decodeErr := automations.DecodeStepExecutionDetails("route", obsolete); decodeErr == nil {
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
		Details:  automations.DelayExecutionDetails{StartedAt: at, State: automations.DelaySucceeded{CompletedAt: at}},
	}
	second := automations.StepExecution{
		StepID:   "second",
		Sequence: 1,
		Details:  automations.DelayExecutionDetails{StartedAt: at, State: automations.DelaySucceeded{CompletedAt: at}},
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
		{name: "order cannot skip a Step", status: automations.RunRunning, trace: []automations.StepExecution{{StepID: "second", Details: second.Details}}},
		{name: "order cannot skip a sequence", status: automations.RunRunning, trace: []automations.StepExecution{{StepID: "first", Sequence: 1, Details: first.Details}}},
		{name: "duplicate reach", status: automations.RunSucceeded, trace: []automations.StepExecution{first, {StepID: "first", Sequence: 1, Details: first.Details}}},
		{name: "terminal parent cannot own running Step", status: automations.RunInterrupted, trace: []automations.StepExecution{{StepID: "first", Details: automations.DelayExecutionDetails{StartedAt: at, State: automations.DelayRunning{}}}}},
		{name: "running Step blocks later evidence", status: automations.RunRunning, trace: []automations.StepExecution{{StepID: "first", Details: automations.DelayExecutionDetails{StartedAt: at, State: automations.DelayRunning{}}}, second}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateExecutionTrace(snapshot, nil, test.status, test.trace)
			if (err == nil) != test.valid {
				t.Fatalf("trace validation = %v, want valid=%v", err, test.valid)
			}
		})
	}
}

// These selected-path cases use pre-value-flow evidence. The full Run validator
// still validates arbitrary details; decoding is not a prerequisite in this suite.
func validateExecutionTrace(snapshot automations.Definition, matches []automations.TriggerID,
	status automations.RunStatus, trace []automations.StepExecution,
) error {
	var state automations.RunState = automations.RunningRun{}
	if status != automations.RunRunning {
		var outcome automations.RunOutcome = automations.SucceededRun{}
		if status == automations.RunInterrupted {
			outcome = automations.InterruptedRun{FailureCode: automations.FailureCoreStopping}
		}
		state = automations.CompletedRun{
			CompletedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Outcome: outcome,
		}
	}
	return automations.ValidateRunValueTraceWithPreparedSnapshot(snapshot, matches, state, nil, trace, false)
}

func TestPublicStepExecutionsPreserveGlobalSequenceAndTypedEvidence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	trace := []automations.StepExecution{
		{
			StepID:   "first",
			Sequence: 0,
			Details: automations.DelayExecutionDetails{
				StartedAt: at,
				State:     automations.DelaySucceeded{CompletedAt: at},
			},
		},
		{
			StepID:   "second",
			Sequence: 1,
			Details:  automations.DelayExecutionDetails{StartedAt: at, State: automations.DelayRunning{}},
		},
	}
	for index, execution := range trace {
		raw, err := automations.EncodePublicStepExecution(execution)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if decodeErr := json.Unmarshal(raw, &wire); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		var sequence int
		var kind, phase string
		if decodeErr := json.Unmarshal(wire["sequence"], &sequence); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		_ = json.Unmarshal(wire["kind"], &kind)
		_ = json.Unmarshal(wire["phase"], &phase)
		wantPhase := "running"
		if index == 0 {
			wantPhase = "succeeded"
		}
		if sequence != index || kind != "delay" || phase != wantPhase || wire["evidence"] == nil {
			t.Fatalf("public execution = %s", raw)
		}
	}
}
