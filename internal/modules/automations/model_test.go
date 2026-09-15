package automations_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gochecknoglobals // Fixed fixture instant shared by every model invariant case.
var modelTestTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func validDomainDefinition(t *testing.T) automations.AutomationDefinition {
	t.Helper()
	return automations.AutomationDefinition{
		Name:    "Office light",
		Enabled: true,
		Triggers: []automations.AutomationTrigger{{
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
		Steps: []automations.AutomationStep{{
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

func validDomainRun(t *testing.T) automations.AutomationRun {
	t.Helper()
	commandID, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	runID, err := automations.NewAutomationRunID()
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
	return automations.AutomationRun{
		ID:                runID,
		AutomationID:      automationID,
		AutomationName:    "Office light",
		Revision:          1,
		Snapshot:          validDomainDefinition(t),
		Source:            automations.RunSourceDeviceFact,
		Fact:              &fact,
		MatchedTriggerIDs: []automations.TriggerID{"occupied_and_warm"},
		ConditionDecision: automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionNotConfigured,
		},
		Status:      automations.RunSucceeded,
		StartedAt:   modelTestTime,
		CompletedAt: &completedAt,
		Steps: []automations.AutomationStepAttempt{{
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
	if err := automations.ValidateAutomationRun(valid); err != nil {
		t.Fatalf("valid run rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(run *automations.AutomationRun)
	}{
		{"manual run with fact", func(run *automations.AutomationRun) {
			run.Source = automations.RunSourceManual
		}},
		{"manual run with matched triggers", func(run *automations.AutomationRun) {
			run.Source = automations.RunSourceManual
			run.Fact = nil
		}},
		{"device fact run without fact", func(run *automations.AutomationRun) {
			run.Fact = nil
		}},
		{"device fact run without matched triggers", func(run *automations.AutomationRun) {
			run.MatchedTriggerIDs = nil
		}},
		{"matched trigger not in snapshot", func(run *automations.AutomationRun) {
			run.MatchedTriggerIDs = []automations.TriggerID{"other"}
		}},
		{"zero revision", func(run *automations.AutomationRun) { run.Revision = 0 }},
		{"zero start time", func(run *automations.AutomationRun) { run.StartedAt = time.Time{} }},
		{"succeeded with failure code", func(run *automations.AutomationRun) {
			code := "core_restarted"
			run.FailureCode = &code
		}},
		{"terminal without completion time", func(run *automations.AutomationRun) { run.CompletedAt = nil }},
		{"failed without failure code", func(run *automations.AutomationRun) { run.Status = automations.RunFailed }},
		{"step count mismatch", func(run *automations.AutomationRun) { run.Steps = nil }},
		{"unknown run status", func(run *automations.AutomationRun) { run.Status = automations.RunStatus("paused") }},
		{"step id mismatch", func(run *automations.AutomationRun) { run.Steps[0].StepID = "other" }},
		{"contradictory snapshot trigger", func(run *automations.AutomationRun) {
			run.Snapshot.Triggers[0].EntityEvent = &automations.EntityEventTrigger{
				EntityID: newEntityID(t), EventName: "single_press",
			}
		}},
		{"not attempted with reserved identity", func(run *automations.AutomationRun) {
			run.Steps[0].Status = automations.StepNotAttempted
		}},
		{"running without reserved identity", func(run *automations.AutomationRun) {
			run.Steps[0].Status = automations.StepRunning
			run.Steps[0].ReservedCommandID = nil
		}},
		{"satisfied without verified command", func(run *automations.AutomationRun) {
			run.Steps[0].VerifiedCommandID = nil
		}},
		{"failed without failure code", func(run *automations.AutomationRun) {
			run.Steps[0].Status = automations.StepFailed
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			run := validDomainRun(t)
			test.mutate(&run)
			if err := automations.ValidateAutomationRun(run); err == nil {
				t.Fatal("impossible run was accepted")
			}
		})
	}
}

func validDomainSkip(t *testing.T) automations.AutomationSkip {
	t.Helper()
	skipID, err := automations.NewAutomationSkipID()
	if err != nil {
		t.Fatal(err)
	}
	automationID, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	fact := newObservationFactSummary(t)
	return automations.AutomationSkip{
		ID:             skipID,
		AutomationID:   automationID,
		AutomationName: "Office light",
		Revision:       3,
		Source:         automations.RunSourceDeviceFact,
		Fact:           &fact,
		MatchedTriggers: []automations.AutomationTrigger{{
			ID:   "occupied_and_warm",
			Kind: automations.TriggerKindObservation,
			Observation: &automations.ObservationTrigger{
				EntityID:     newEntityID(t),
				Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
			},
		}},
		Reason: automations.AutomationSkipBusy,
		ConditionDecision: automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionNotConfigured,
		},
		SkippedAt: modelTestTime,
	}
}

// Retained Skips require Fact evidence, matched Trigger snapshots, a valid reason, and time.
func TestValidateAutomationSkipRejectsImpossibleCombinations(t *testing.T) {
	t.Parallel()
	if err := automations.ValidateAutomationSkip(validDomainSkip(t)); err != nil {
		t.Fatalf("valid skip rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(skip *automations.AutomationSkip)
	}{
		{"no matched triggers", func(skip *automations.AutomationSkip) { skip.MatchedTriggers = nil }},
		{"unknown reason", func(skip *automations.AutomationSkip) { skip.Reason = "later" }},
		{"zero skip time", func(skip *automations.AutomationSkip) { skip.SkippedAt = time.Time{} }},
		{"zero revision", func(skip *automations.AutomationSkip) { skip.Revision = 0 }},
		{"missing fact", func(skip *automations.AutomationSkip) { skip.Fact = nil }},
		{"manual source with fact", func(skip *automations.AutomationSkip) {
			skip.Source = automations.RunSourceManual
		}},
		{"unknown source", func(skip *automations.AutomationSkip) { skip.Source = "later" }},
		{"bypass decision", func(skip *automations.AutomationSkip) {
			skip.ConditionDecision.BypassRequested = true
		}},
		{"condition reason without evaluation", func(skip *automations.AutomationSkip) {
			skip.Reason = automations.AutomationSkipConditionsFalse
		}},
		{"duplicate matched trigger", func(skip *automations.AutomationSkip) {
			skip.MatchedTriggers = append(skip.MatchedTriggers, skip.MatchedTriggers[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			skip := validDomainSkip(t)
			test.mutate(&skip)
			if err := automations.ValidateAutomationSkip(skip); err == nil {
				t.Fatal("impossible skip was accepted")
			}
		})
	}
}

// A Fact must carry exactly the payload its family names.
func TestValidateDeviceFactRejectsMismatchedFamilies(t *testing.T) {
	t.Parallel()
	observationID, observationErr := devices.NewObservationID()
	if observationErr != nil {
		t.Fatal(observationErr)
	}
	factID, factErr := devices.NewDeviceFactID()
	if factErr != nil {
		t.Fatal(factErr)
	}
	observation := &automations.ObservationFact{
		FactID:        factID,
		ObservationID: observationID,
		EntityID:      newEntityID(t),
		Disposition:   devices.DispositionApplied,
		Value:         devices.Value(`true`),
		EmittedAt:     modelTestTime,
	}
	if err := automations.ValidateDeviceFact(automations.DeviceFact{
		Family: automations.DeviceFactObservation, Observation: observation,
	}); err != nil {
		t.Fatalf("valid observation fact rejected: %v", err)
	}
	mismatched := []automations.DeviceFact{
		{Family: automations.DeviceFactObservation},
		{
			Family:      automations.DeviceFactObservation,
			Observation: observation,
			EntityEvent: &automations.EntityEventFact{},
		},
		{Family: automations.DeviceFactEntityEvent, Observation: observation},
		{Family: automations.DeviceFactFamily("unknown")},
	}
	for _, fact := range mismatched {
		if err := automations.ValidateDeviceFact(fact); err == nil {
			t.Fatalf("mismatched fact %+v was accepted", fact)
		}
	}
}
