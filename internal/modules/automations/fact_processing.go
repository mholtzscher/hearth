package automations

import (
	"context"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
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
	if err := ValidateDeviceFact(fact); err != nil {
		return AdmissionOutcome{}, err
	}
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

// admitAutomaticFact reads the State needed by enabled matching definitions
// before opening the admission transaction.
func (service *Service) admitAutomaticFact(
	ctx context.Context,
	fact DeviceFact,
) (AdmissionResult, error) {
	admissionContext, cancel := context.WithTimeout(ctx, AdmissionTimeout)
	defer cancel()
	definitions, err := service.repository.ListEnabledAutomations(admissionContext)
	if err != nil {
		return AdmissionResult{}, err
	}
	required, err := requiredMatchingConditionEntityIDs(fact, definitions)
	if err != nil {
		return AdmissionResult{}, err
	}
	snapshot := emptyEntityStateSnapshot()
	if len(required) > 0 {
		snapshot, err = service.readConditionStateSnapshot(admissionContext, required)
		if err != nil {
			return AdmissionResult{}, err
		}
	}
	return service.repository.AdmitDeviceFact(
		admissionContext, fact, snapshot, service.dependencies.Now(), service.heldStateStartupAt,
	)
}

// requiredMatchingConditionEntityIDs returns the sorted, deduplicated Entity
// union every enabled definition matching fact requires for its Conditions.
func requiredMatchingConditionEntityIDs(
	fact DeviceFact,
	definitions []Record,
) ([]devices.EntityID, error) {
	required := make(map[devices.EntityID]struct{})
	for _, record := range definitions {
		conditions := record.Definition.Conditions
		if conditions == nil {
			continue
		}
		matched, err := MatchTriggers(fact, record.Definition)
		if err != nil {
			return nil, err
		}
		if len(matched) == 0 {
			continue
		}
		entityIDs, err := RequiredConditionEntityIDs(*conditions)
		if err != nil {
			return nil, err
		}
		for _, entityID := range entityIDs {
			required[entityID] = struct{}{}
		}
	}
	ids := make([]devices.EntityID, 0, len(required))
	for entityID := range required {
		ids = append(ids, entityID)
	}
	slices.Sort(ids)
	return ids, nil
}
