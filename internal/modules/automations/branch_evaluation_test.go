//nolint:testpackage // Verify the private evaluator's typed-error contract without a test-only public wrapper.
package automations

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The pure helper's operational error retains a decision and its typed error.
// This contract is distinct from the executor's persisted failure assertion.
func TestPreparedBranchEvaluationReturnsTypedErrorAndCompletedPrefix(t *testing.T) {
	t.Parallel()
	entity := devices.EntityID("ent_01950000-0000-7000-8000-000000000001")
	command := Step{
		ID:            "command",
		EntityID:      entity,
		OperationName: devices.OperationNameSet,
		Parameters:    devices.CommandParameters(`{"value":true}`),
	}
	definition, err := NormalizeDefinition(
		Definition{
			Name: "Evaluation",
			Triggers: []Trigger{{ID: "button", Kind: TriggerKindObservation, Observation: &ObservationTrigger{
				EntityID:     entity,
				Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
				Comparisons: []ObservationComparison{
					{Pointer: "", Operator: ComparisonEqual, Operand: []byte("true")},
				},
			}}},
			Steps: []Step{{ID: "route", Kind: StepKindChoose, Choose: &ChooseStep{Branches: []ChooseBranch{
				{
					ID: "first",
					Conditions: Condition{
						ID:      "source",
						Kind:    ConditionTrigger,
						Trigger: &TriggerCondition{TriggerIDs: []TriggerID{"button"}},
					},
					Steps: []Step{command},
				},
				{
					ID: "second",
					Conditions: Condition{
						ID:   "state",
						Kind: ConditionEntityState,
						EntityState: &EntityStateCondition{
							EntityID: entity,
							Pointer:  "",
							Operator: ComparisonEqual,
							Operand:  []byte("true"),
						},
					},
					Steps: []Step{
						{
							ID:            "other",
							EntityID:      entity,
							OperationName: devices.OperationNameSet,
							Parameters:    devices.CommandParameters(`{"value":false}`),
						},
					},
				},
			}}}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := branchRoots(definition.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	ids, err := branchEntityIDs(roots)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, covered := range []bool{false, true} {
		snapshot := devices.EntityStateSnapshot{Entries: make(map[devices.EntityID]devices.EntityStateSnapshotEntry)}
		if covered {
			snapshot.Entries[entity] = devices.EntityStateSnapshotEntry{
				EntityID: entity,
				Exists:   true,
				State:    &devices.State{Value: []byte("{")},
			}
		}
		decision, evaluationErr := evaluateBranch(definition.Steps[0], roots, ids, nil, snapshot, at)
		if decision.Outcome != BranchError || decision.FailureCode == nil || evaluationErr == nil {
			t.Fatalf("decision = %#v, error = %v", decision, evaluationErr)
		}
		if covered {
			if !errors.Is(evaluationErr, devices.ErrEntityStateSnapshotCorrupt) || len(decision.Evaluations) != 1 ||
				decision.Evaluations[0].Evaluation.Result != ConditionFalse {
				t.Fatalf("corrupt result = %#v, %v", decision, evaluationErr)
			}
		} else {
			missing, ok := errors.AsType[*ConditionSnapshotRequiredError](evaluationErr)
			if !ok || len(missing.RequiredEntityIDs) != 1 || missing.RequiredEntityIDs[0] != entity ||
				len(decision.Evaluations) != 0 {
				t.Fatalf("coverage result = %#v, %v", decision, evaluationErr)
			}
		}
	}
}

func TestReachedBranchMalformedStepsReturnNoDecision(t *testing.T) {
	t.Parallel()
	t.Run("missing if payload", func(t *testing.T) {
		t.Parallel()
		service := &Service{}
		decision, err := service.evaluateReachedBranch(
			context.Background(),
			Run{},
			Step{ID: "invalid", Kind: StepKindIf},
		)
		if !errors.Is(err, ErrInvalidAutomation) || decision.StepID != "" || decision.Outcome != "" {
			t.Fatalf("invalid prepared shape = %#v, %v", decision, err)
		}
	})
	for _, root := range []Condition{
		{ID: "state", Kind: ConditionEntityState},
		{ID: "not", Kind: ConditionNot},
		{ID: "trigger", Kind: ConditionTrigger},
		{ID: "empty", Kind: ConditionAny},
		{ID: "unknown", Kind: "invalid"},
		{ID: "nested", Kind: ConditionAll, Children: []Condition{{ID: "state", Kind: ConditionEntityState}}},
	} {
		t.Run(string(root.ID), func(t *testing.T) {
			t.Parallel()
			// Even a later Choose root must be safe before the first root selects.
			command := Step{ID: "first-command", Kind: StepKindCommand,
				EntityID:      "ent_01950000-0000-7000-8000-000000000001",
				OperationName: devices.OperationNameSet, Parameters: devices.CommandParameters(`{"value":true}`)}
			other := command
			other.ID = "second-command"
			step := Step{ID: "route", Kind: StepKindChoose, Choose: &ChooseStep{Branches: []ChooseBranch{
				{
					ID: "first",
					Conditions: Condition{
						ID:      "source",
						Kind:    ConditionTrigger,
						Trigger: &TriggerCondition{TriggerIDs: []TriggerID{"button"}},
					},
					Steps: []Step{command},
				},
				{ID: "malformed", Conditions: root, Steps: []Step{other}},
			}}}
			service := &Service{}
			decision, err := service.evaluateReachedBranch(
				context.Background(),
				Run{MatchedTriggerIDs: []TriggerID{"button"}},
				step,
			)
			if !errors.Is(err, ErrInvalidAutomation) || !reflect.DeepEqual(decision, BranchDecision{}) {
				t.Fatalf("malformed prepared root returned decision %#v, error %v", decision, err)
			}
		})
	}
}
