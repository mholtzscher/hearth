package automations_test

import (
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

func runFailure(state automations.RunState) *string {
	_, code := automations.RunStateCompletion(state)
	return code
}
