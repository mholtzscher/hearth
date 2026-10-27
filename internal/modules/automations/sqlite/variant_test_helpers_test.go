package sqlite_test

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func runEntry(entry any) *automations.Run {
	run, ok := entry.(automations.Run)
	if !ok {
		return nil
	}
	return &run
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

func heldEvidence(cause automations.AdmissionCause) *automations.HeldStateEvidence {
	switch cause := cause.(type) {
	case automations.HeldStateCause:
		return &cause.Evidence
	case automations.ManualCause, automations.DeviceFactCause, automations.ScheduleCause:
		return nil
	default:
		return nil
	}
}

func runFailure(state automations.RunState) *string {
	_, code := automations.RunStateCompletion(state)
	return code
}

func runCompleted(state automations.RunState) *time.Time {
	completed, _ := automations.RunStateCompletion(state)
	return completed
}
