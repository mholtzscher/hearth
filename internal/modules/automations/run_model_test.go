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
		Triggers: []automations.Trigger{{ID: "occupied_and_warm", Body: automations.ObservationTrigger{
			EntityID:     newEntityID(t),
			Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
			Comparisons: []automations.ObservationComparison{
				{Pointer: "/temperature", Operator: automations.ComparisonGreaterThan, Operand: []byte("20")},
			},
		}}},
		Steps: []automations.Step{
			{
				ID: "light_on",
				Body: automations.CommandStep{
					EntityID:      newEntityID(t),
					OperationName: devices.OperationNameSet,
					Parameters:    devices.CommandParameters(`{"value":true}`),
				},
			},
		},
	}
}

// Completion boundaries reject nil, pointer, and incomplete outcomes.
func TestCompletionRejectsInvalidOutcomeValues(t *testing.T) {
	t.Parallel()
	failure := "command_failed"
	commandID, commandErr := devices.NewCommandID()
	if commandErr != nil {
		t.Fatal(commandErr)
	}
	for _, completion := range []automations.StepCompletion{
		{},
		{Outcome: automations.SatisfiedStep{}},
		{Outcome: &automations.DispatchedStep{VerifiedCommandID: commandID}},
		{Outcome: automations.FailedStep{}},
		{Outcome: automations.InterruptedStep{}},
	} {
		if err := automations.ValidateStepCompletion(completion); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("step completion %#v: %v, want invalid automation", completion, err)
		}
	}
	for _, completion := range []automations.RunCompletion{
		{},
		{Outcome: &automations.SucceededRun{}},
		{Outcome: automations.FailedRun{}},
		{Outcome: automations.InterruptedRun{}},
	} {
		if err := automations.ValidateRunCompletion(completion); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("run completion %#v: %v, want invalid automation", completion, err)
		}
	}
	if err := automations.ValidateStepCompletion(automations.StepCompletion{
		Outcome: automations.SatisfiedStep{VerifiedCommandID: commandID},
	}); err != nil {
		t.Fatalf("valid successful step: %v", err)
	}
	if err := automations.ValidateRunCompletion(automations.RunCompletion{
		Outcome: automations.FailedRun{FailureCode: failure},
	}); err != nil {
		t.Fatalf("valid failed run: %v", err)
	}
}

func newModelObservationFact(t *testing.T) automations.ObservationFact {
	t.Helper()
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return automations.ObservationFact{
		FactID:        factID,
		EntityID:      newEntityID(t),
		Disposition:   devices.DispositionApplied,
		ObservationID: observationID,
		Value:         devices.Value(`true`),
		EmittedAt:     modelTestTime,
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
	definition.Steps = append(
		definition.Steps,
		automations.Step{
			ID: "light_off",
			Body: automations.CommandStep{
				EntityID:      newEntityID(t),
				OperationName: devices.OperationNameSet,
				Parameters:    devices.CommandParameters(`{"value":false}`),
			},
		},
	)
	definition, err = automations.NormalizeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	matched := []automations.TriggerID{"occupied_and_warm"}
	fact := newModelObservationFact(t)
	run := automations.NewRunSnapshot(automations.Record{ID: id, Revision: 2, Definition: definition},
		runID, automations.DeviceFactCause{Fact: fact}, matched, automations.NotConfiguredDecision(), modelTestTime)
	matched[0] = "changed"
	if automations.CauseSource(run.Cause) != automations.RunSourceDeviceFact ||
		len(run.MatchedTriggerIDs) != 1 || run.MatchedTriggerIDs[0] != "occupied_and_warm" ||
		automations.RunStateStatus(run.State) != automations.RunRunning || len(run.Steps) != 2 {
		t.Fatalf("admitted run = %#v", run)
	}
	for i, want := range []automations.StepID{"light_on", "light_off"} {
		if run.Steps[i].Position != i || run.Steps[i].StepID != want ||
			automations.StepAttemptStatus(run.Steps[i].State) != automations.StepNotAttempted {
			t.Fatalf("step %d = %#v", i, run.Steps[i])
		}
	}
}
