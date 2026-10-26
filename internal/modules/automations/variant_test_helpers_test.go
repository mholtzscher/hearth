package automations_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// projectedRun is a test-only, read-only view of a canonical Run and its
// supported history projections. The canonical Run retains only Executions.
type projectedRun struct {
	automations.Run
	automations.RunEvidenceProjection
}

func projectRunFor(t testing.TB, run automations.Run) projectedRun {
	t.Helper()
	projection, err := automations.ProjectStepExecutions(run.Snapshot, run.Executions)
	if err != nil {
		t.Fatal(err)
	}
	return projectedRun{Run: run, RunEvidenceProjection: projection}
}

func runEntry(entry any) *projectedRun {
	run, ok := entry.(automations.Run)
	if !ok {
		return nil
	}
	projection, err := automations.ProjectStepExecutions(run.Snapshot, run.Executions)
	if err != nil {
		return nil
	}
	return &projectedRun{Run: run, RunEvidenceProjection: projection}
}

func skipEntry(entry any) *automations.Skip {
	skip, ok := entry.(automations.Skip)
	if !ok {
		return nil
	}
	return &skip
}

func causeFact(cause automations.AdmissionCause) automations.DeviceFact {
	switch cause := cause.(type) {
	case automations.DeviceFactCause:
		return cause.Fact
	case automations.ManualCause, automations.HeldStateCause, automations.ScheduleCause:
		return nil
	default:
		return nil
	}
}

func runFailure(state automations.RunState) *string {
	_, code := automations.RunStateCompletion(state)
	return code
}

func stepOutcome(state automations.StepAttemptState) automations.StepOutcome {
	switch state := state.(type) {
	case automations.CompletedStep:
		return state.Outcome
	case automations.NotAttemptedStep, automations.RunningStep:
		return nil
	default:
		return nil
	}
}

func stepFailureCode(state automations.StepAttemptState) *string {
	_, failure := automations.StepOutcomeEvidence(stepOutcome(state))
	return failure
}

func stepCompletedAt(state automations.StepAttemptState) *time.Time {
	switch state := state.(type) {
	case automations.CompletedStep:
		return &state.CompletedAt
	case automations.NotAttemptedStep, automations.RunningStep:
		return nil
	default:
		return nil
	}
}

func stepStarted(state automations.StepAttemptState) *time.Time {
	switch state := state.(type) {
	case automations.CompletedStep:
		return &state.StartedAt
	case automations.RunningStep:
		return &state.StartedAt
	case automations.NotAttemptedStep:
		return nil
	default:
		return nil
	}
}

func stepVerified(state automations.StepAttemptState) *devices.CommandID {
	verified, _ := automations.StepOutcomeEvidence(stepOutcome(state))
	return verified
}

func stepReservation(state automations.StepAttemptState) *automations.CommandReservation {
	switch state := state.(type) {
	case automations.CompletedStep:
		return state.Reservation
	case automations.RunningStep:
		return &state.Reservation
	case automations.NotAttemptedStep:
		return nil
	default:
		return nil
	}
}

func stepReservedCommand(state automations.StepAttemptState) *devices.CommandID {
	reservation := stepReservation(state)
	if reservation == nil {
		return nil
	}
	return &reservation.CommandID
}

func stepReservedCorrelation(state automations.StepAttemptState) *devices.CorrelationID {
	reservation := stepReservation(state)
	if reservation == nil {
		return nil
	}
	return &reservation.CorrelationID
}
