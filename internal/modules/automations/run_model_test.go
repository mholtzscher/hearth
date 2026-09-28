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

// Completion boundaries reject nonterminal statuses and contradictory evidence.
func TestCompletionRejectsNonterminalAndContradictoryEvidence(t *testing.T) {
	t.Parallel()
	failure := "command_failed"
	commandID, commandErr := devices.NewCommandID()
	if commandErr != nil {
		t.Fatal(commandErr)
	}
	for _, completion := range []automations.StepCompletion{
		{Status: automations.StepRunning},
		{Status: automations.StepSatisfied},
		{Status: automations.StepDispatched, VerifiedCommandID: &commandID, FailureCode: &failure},
		{Status: automations.StepFailed},
		{Status: automations.StepInterrupted},
	} {
		if err := automations.ValidateStepCompletion(completion); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("step completion %#v: %v, want invalid automation", completion, err)
		}
	}
	for _, completion := range []automations.RunCompletion{
		{Status: automations.RunRunning},
		{Status: automations.RunSucceeded, FailureCode: &failure},
		{Status: automations.RunFailed},
		{Status: automations.RunInterrupted},
	} {
		if err := automations.ValidateRunCompletion(completion); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("run completion %#v: %v, want invalid automation", completion, err)
		}
	}
	if err := automations.ValidateStepCompletion(automations.StepCompletion{
		Status: automations.StepSatisfied, VerifiedCommandID: &commandID,
	}); err != nil {
		t.Fatalf("valid successful step: %v", err)
	}
	if err := automations.ValidateRunCompletion(automations.RunCompletion{
		Status: automations.RunFailed, FailureCode: &failure,
	}); err != nil {
		t.Fatalf("valid failed run: %v", err)
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

// Admission snapshots preserve definition order and isolate the caller's matched IDs.
func TestNewRunSnapshotInitializesOrderedAttempts(t *testing.T) {
	t.Parallel()
	id, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	runID, err := automations.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	definition := validDomainDefinition(t)
	definition.Steps = append(definition.Steps, automations.Step{
		ID: "light_off", EntityID: newEntityID(t), OperationName: devices.OperationNameSet,
		Parameters: devices.CommandParameters(`{"value":false}`),
	})
	matched := []automations.TriggerID{"occupied_and_warm"}
	fact := newObservationFactSummary(t)
	run := automations.NewRunSnapshot(automations.Record{ID: id, Revision: 2, Definition: definition},
		runID, automations.RunSourceDeviceFact, &fact, matched, automations.NotConfiguredDecision(), modelTestTime)
	matched[0] = "changed"
	if run.Source != automations.RunSourceDeviceFact || run.Fact != &fact ||
		len(run.MatchedTriggerIDs) != 1 || run.MatchedTriggerIDs[0] != "occupied_and_warm" ||
		run.Status != automations.RunRunning || run.CompletedAt != nil || len(run.Steps) != 2 {
		t.Fatalf("admitted run = %#v", run)
	}
	for i, want := range []automations.StepID{"light_on", "light_off"} {
		if run.Steps[i].Position != i || run.Steps[i].StepID != want ||
			run.Steps[i].Status != automations.StepNotAttempted || run.Steps[i].StartedAt != nil {
			t.Fatalf("step %d = %#v", i, run.Steps[i])
		}
	}
}
