package automations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gochecknoglobals // Fixed fixture instant shared by every model invariant case.
var modelTestTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func validDomainDefinition(t *testing.T) automations.Definition {
	t.Helper()
	return automations.Definition{
		Name:    "Office light",
		Enabled: true,
		Triggers: []automations.Trigger{{
			ID:   "occupied_and_warm",
			Kind: automations.TriggerKindObservation,
			Observation: &automations.ObservationTrigger{
				EntityID:     newEntityID(t),
				Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
				Comparisons: []automations.ObservationComparison{
					comparison("/temperature", automations.ComparisonGreaterThan, "20"),
				},
			},
		}},
		Steps: []automations.Step{{
			ID:            "light_on",
			EntityID:      newEntityID(t),
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(`{"value":true}`),
		}},
	}
}

func newObservationFactSummary(t *testing.T) automations.DeviceFactSummary {
	t.Helper()
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return automations.DeviceFactSummary{
		FactID:           factID,
		Family:           automations.DeviceFactObservation,
		EntityID:         newEntityID(t),
		Variant:          string(devices.DispositionApplied),
		CausationID:      string(observationID),
		ObservationValue: devices.Value(`true`),
		EmittedAt:        modelTestTime,
	}
}

func validDomainRun(t *testing.T) automations.Run {
	t.Helper()
	commandID, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	runID, err := automations.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	automationID, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	fact := newObservationFactSummary(t)
	completedAt := modelTestTime.Add(time.Second)
	startedAt := modelTestTime
	return automations.Run{
		ID:                runID,
		AutomationID:      automationID,
		AutomationName:    "Office light",
		Revision:          1,
		Snapshot:          validDomainDefinition(t),
		Source:            automations.RunSourceDeviceFact,
		Fact:              &fact,
		MatchedTriggerIDs: []automations.TriggerID{"occupied_and_warm"},
		ConditionDecision: automations.NotConfiguredDecision(),
		Status:            automations.RunSucceeded,
		StartedAt:         modelTestTime,
		CompletedAt:       &completedAt,
		Steps: []automations.StepAttempt{{
			Position:              0,
			StepID:                "light_on",
			Status:                automations.StepSatisfied,
			ReservedCommandID:     &commandID,
			ReservedCorrelationID: &correlationID,
			VerifiedCommandID:     &commandID,
			StartedAt:             &startedAt,
			CompletedAt:           &completedAt,
		}},
	}
}

// Retained Runs must have consistent provenance, snapshots, timestamps, and Step evidence.
func TestValidateAutomationRunRejectsImpossibleCombinations(t *testing.T) {
	t.Parallel()
	valid := validDomainRun(t)
	if err := automations.ValidateRun(valid); err != nil {
		t.Fatalf("valid run rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(run *automations.Run)
	}{
		{"manual run with fact", func(run *automations.Run) {
			run.Source = automations.RunSourceManual
		}},
		{"manual run with matched triggers", func(run *automations.Run) {
			run.Source = automations.RunSourceManual
			run.Fact = nil
		}},
		{"device fact run without fact", func(run *automations.Run) {
			run.Fact = nil
		}},
		{"device fact run without matched triggers", func(run *automations.Run) {
			run.MatchedTriggerIDs = nil
		}},
		{"matched trigger not in snapshot", func(run *automations.Run) {
			run.MatchedTriggerIDs = []automations.TriggerID{"other"}
		}},
		{"zero revision", func(run *automations.Run) { run.Revision = 0 }},
		{"zero start time", func(run *automations.Run) { run.StartedAt = time.Time{} }},
		{"succeeded with failure code", func(run *automations.Run) {
			code := "core_restarted"
			run.FailureCode = &code
		}},
		{"terminal without completion time", func(run *automations.Run) { run.CompletedAt = nil }},
		{"failed without failure code", func(run *automations.Run) { run.Status = automations.RunFailed }},
		{"step count mismatch", func(run *automations.Run) { run.Steps = nil }},
		{"unknown run status", func(run *automations.Run) { run.Status = automations.RunStatus("paused") }},
		{"step id mismatch", func(run *automations.Run) { run.Steps[0].StepID = "other" }},
		{"contradictory snapshot trigger", func(run *automations.Run) {
			run.Snapshot.Triggers[0].EntityEvent = &automations.EntityEventTrigger{
				EntityID: newEntityID(t), EventName: "single_press",
			}
		}},
		{"not attempted with reserved identity", func(run *automations.Run) {
			run.Steps[0].Status = automations.StepNotAttempted
		}},
		{"running without reserved identity", func(run *automations.Run) {
			run.Steps[0].Status = automations.StepRunning
			run.Steps[0].ReservedCommandID = nil
		}},
		{"satisfied without verified command", func(run *automations.Run) {
			run.Steps[0].VerifiedCommandID = nil
		}},
		{"failed without failure code", func(run *automations.Run) {
			run.Steps[0].Status = automations.StepFailed
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			run := validDomainRun(t)
			test.mutate(&run)
			if err := automations.ValidateRun(run); err == nil {
				t.Fatal("impossible run was accepted")
			}
		})
	}
}

// Run decisions never record not_evaluated, bypass only a manual Run, and
// admit on a true root. Envelope-incoherent decisions are unrepresentable, so
// only the record-kind rules remain testable.
func TestValidateRunConditionDecision(t *testing.T) {
	t.Parallel()
	trueTree, trueEvaluation := conditionTreeAndEvaluation(
		t, automations.ConditionTrue, automations.ConditionTrue,
	)
	falseTree, falseEvaluation := conditionTreeAndEvaluation(
		t, automations.ConditionTrue, automations.ConditionFalse,
	)
	run := func(
		conditions *automations.Condition,
		source automations.RunSource,
		decision automations.ConditionDecision,
	) automations.Run {
		return automations.Run{
			Source:            source,
			Snapshot:          automations.Definition{Conditions: conditions},
			ConditionDecision: decision,
		}
	}
	valid := run(&trueTree, automations.RunSourceManual, automations.EvaluatedDecision(
		trueTree, trueEvaluation,
	))
	if err := automations.ValidateRunConditionDecision(valid); err != nil {
		t.Fatalf("valid evaluated Run: %v", err)
	}
	bypassed := run(&trueTree, automations.RunSourceManual, automations.BypassedDecision(trueTree))
	if err := automations.ValidateRunConditionDecision(bypassed); err != nil {
		t.Fatalf("valid bypassed Run: %v", err)
	}
	unconditioned := run(nil, automations.RunSourceDeviceFact, automations.NotConfiguredDecision())
	if err := automations.ValidateRunConditionDecision(unconditioned); err != nil {
		t.Fatalf("valid automatic unconditioned Run: %v", err)
	}
	cases := []struct {
		name string
		run  automations.Run
	}{
		{"not evaluated", run(&trueTree, automations.RunSourceManual, automations.NotEvaluatedDecision(trueTree))},
		{"automatic bypass", run(&trueTree, automations.RunSourceDeviceFact, automations.BypassedDecision(trueTree))},
		{"evaluated false root", run(&falseTree, automations.RunSourceManual, automations.EvaluatedDecision(
			falseTree, falseEvaluation,
		))},
	}
	for _, testCase := range cases {
		if err := automations.ValidateRunConditionDecision(testCase.run); !errors.Is(
			err, automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
}
