package automations

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// ProcessDueHeldStates commits due held-state Runs or Skips and starts committed Run workers.
func (service *Service) ProcessDueHeldStates(ctx context.Context, at time.Time, limit int) (int, error) {
	reservation, admitted := service.admission.TryAcquire()
	if !admitted {
		return 0, ErrAdmissionUnavailable
	}
	defer reservation.Release()
	if service.devices == nil || !service.devices.CommandAdmissionOpen() {
		return 0, ErrAdmissionUnavailable
	}

	deadline := time.Now().Add(AdmissionTimeout)
	result, processed, err := service.processDueHeldStateBatch(ctx, at, limit, deadline)
	if err != nil {
		return 0, err
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
	return processed, nil
}

func (service *Service) processDueHeldStateBatch(
	ctx context.Context,
	at time.Time,
	limit int,
	deadline time.Time,
) (AdmissionResult, int, error) {
	for {
		admissionContext, cancel := context.WithDeadline(ctx, deadline)
		candidates, err := service.repository.ListDueHeldStates(admissionContext, at, limit)
		if err != nil {
			cancel()
			return AdmissionResult{}, 0, err
		}
		required, err := service.dueConditionEntityIDs(admissionContext, candidates)
		if err != nil {
			cancel()
			return AdmissionResult{}, 0, err
		}
		snapshot := emptyEntityStateSnapshot()
		if len(required) > 0 {
			snapshot, err = service.readConditionStateSnapshot(admissionContext, required)
			if err != nil {
				cancel()
				return AdmissionResult{}, 0, err
			}
		}
		result, processed, err := service.repository.AdmitDueHeldStates(
			admissionContext, snapshot, at, service.dependencies.Now(), limit,
		)
		cancel()
		if errors.Is(err, ErrConditionSnapshotRequired) {
			if time.Now().After(deadline) {
				return AdmissionResult{}, 0, context.DeadlineExceeded
			}
			continue
		}
		if err != nil {
			return AdmissionResult{}, 0, err
		}
		return result, processed, nil
	}
}

func (service *Service) dueConditionEntityIDs(
	ctx context.Context,
	candidates []HeldStateCandidate,
) ([]devices.EntityID, error) {
	ids := make(map[devices.EntityID]struct{})
	for _, candidate := range candidates {
		record, err := service.repository.GetAutomation(ctx, candidate.AutomationID)
		if err != nil {
			if errors.Is(err, ErrAutomationNotFound) {
				continue
			}
			return nil, err
		}
		if record.Revision != candidate.Revision || record.Definition.Conditions == nil {
			continue
		}
		conditionIDs, err := RequiredConditionEntityIDs(*record.Definition.Conditions)
		if err != nil {
			return nil, err
		}
		for _, id := range conditionIDs {
			ids[id] = struct{}{}
		}
	}
	result := make([]devices.EntityID, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	slices.Sort(result)
	return result, nil
}
