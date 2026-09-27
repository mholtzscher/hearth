package automations

import (
	"context"
	"time"
)

// FactMaximumAge is the fixed freshness limit measured from emitted_at.
// Older matching Facts record stale_fact Skips instead of starting Runs.
const FactMaximumAge = 30 * time.Second

// ReceiveDeviceFact admits one Device Fact against current enabled definitions.
// Run workers start only after the admission transaction commits.
func (service *Service) ReceiveDeviceFact(
	ctx context.Context,
	fact DeviceFact,
) (AdmissionOutcome, error) {
	reservation, admitted := service.admission.TryAcquire()
	if !admitted {
		return AdmissionOutcome{}, ErrAdmissionUnavailable
	}
	defer reservation.Release()
	result, err := service.admitAutomaticFact(ctx, fact)
	if err != nil {
		reservation.Release()
		service.logConditionStateCorrupt(ctx, err)
		return AdmissionOutcome{}, err
	}
	workerContext := context.WithoutCancel(ctx)
	for _, run := range result.StartedRuns {
		reservation.Go(func() { service.executeRun(workerContext, run) })
	}
	reservation.Release()
	for _, run := range result.StartedRuns {
		service.logRunStarted(ctx, run)
	}
	for _, skip := range result.Skips {
		service.logSkipped(ctx, skip)
	}
	return result.Outcome, nil
}
