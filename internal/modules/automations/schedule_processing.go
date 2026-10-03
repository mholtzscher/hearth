package automations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// InitializeSchedules consumes the activation minute without admitting work.
func (service *Service) InitializeSchedules(ctx context.Context, at time.Time) error {
	if service.dependencies.HouseholdLocation == nil {
		return fmt.Errorf("%w: household location is required", ErrInvalidAutomation)
	}
	if at.IsZero() {
		return fmt.Errorf("%w: schedule activation time is required", ErrInvalidAutomation)
	}
	admissionContext, cancel := context.WithTimeout(ctx, AdmissionTimeout)
	defer cancel()
	return service.repository.InitializeScheduleWatermark(admissionContext, at.UTC())
}

// ProcessDueSchedules admits only the sampled current minute and starts workers
// for committed Runs under the process-owned admission reservation.
func (service *Service) ProcessDueSchedules(ctx context.Context) (AdmissionOutcome, error) {
	reservation, admitted := service.admission.TryAcquire()
	if !admitted {
		return AdmissionOutcome{}, ErrAdmissionUnavailable
	}
	defer reservation.Release()
	if service.devices == nil || !service.devices.CommandAdmissionOpen() {
		return AdmissionOutcome{}, ErrAdmissionUnavailable
	}
	if service.dependencies.HouseholdLocation == nil {
		return AdmissionOutcome{}, fmt.Errorf("%w: household location is required", ErrInvalidAutomation)
	}
	admissionContext, cancel := context.WithTimeout(ctx, AdmissionTimeout)
	defer cancel()
	result, err := service.admitSchedules(admissionContext)
	if err != nil {
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

func (service *Service) admitSchedules(ctx context.Context) (AdmissionResult, error) {
	for {
		if err := ctx.Err(); err != nil {
			return AdmissionResult{}, err
		}
		records, err := service.repository.ListEnabledAutomations(ctx)
		if err != nil {
			return AdmissionResult{}, err
		}
		preparationAt := service.dependencies.Now().UTC()
		required, err := requiredScheduledConditionEntityIDs(
			records,
			preparationAt,
			service.dependencies.HouseholdLocation,
		)
		if err != nil {
			return AdmissionResult{}, err
		}
		snapshot := emptyEntityStateSnapshot()
		if len(required) > 0 {
			snapshot, err = service.readConditionStateSnapshot(ctx, required)
			if err != nil {
				return AdmissionResult{}, err
			}
		}
		result, err := service.repository.AdmitDueSchedules(ctx, snapshot, ScheduleTick{
			At: service.dependencies.Now().UTC(), Location: service.dependencies.HouseholdLocation,
		})
		if errors.Is(err, ErrConditionSnapshotRequired) {
			continue
		}
		return result, err
	}
}

func requiredScheduledConditionEntityIDs(
	records []Record,
	at time.Time,
	location *time.Location,
) ([]devices.EntityID, error) {
	minute := at.UTC().Truncate(time.Minute)
	required := make(map[devices.EntityID]struct{})
	for _, record := range records {
		definition := record.Definition
		if !definition.Enabled || definition.Conditions == nil || !minute.After(record.UpdatedAt) {
			continue
		}
		matched, err := MatchScheduledTriggers(definition, minute, location)
		if err != nil {
			return nil, err
		}
		if len(matched) == 0 {
			continue
		}
		ids, err := RequiredConditionEntityIDs(*definition.Conditions)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			required[id] = struct{}{}
		}
	}
	ids := make([]devices.EntityID, 0, len(required))
	for id := range required {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}
