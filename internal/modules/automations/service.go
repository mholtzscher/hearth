package automations

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// Service manages automation definitions, Run admission, and execution.
type Service struct {
	repository   Repository
	devices      AutomationDevices
	dependencies Dependencies

	// admission tracks both admission transactions and Run workers for Drain.
	admission *lifecycle.AdmissionGroup

	heldStateStartupAt time.Time
}

// NewService assembles the automation service from its persistence seam, the
// devices-facing seam, and process-owned collaborators.
func NewService(
	repository Repository,
	automationDevices AutomationDevices,
	dependencies Dependencies,
) *Service {
	dependencies = dependencies.WithDefaults()
	startupAt := dependencies.HeldStateStartupAt
	if startupAt.IsZero() {
		startupAt = dependencies.Now()
	}
	service := &Service{
		repository:         repository,
		devices:            automationDevices,
		dependencies:       dependencies,
		admission:          lifecycle.NewAdmissionGroup(),
		heldStateStartupAt: startupAt.UTC(),
	}
	return service
}

// ResetPendingHeldStates discards pre-restart elapsed time while preserving hold cursors.
func (service *Service) ResetPendingHeldStates(ctx context.Context) error {
	return service.repository.ResetPendingHeldStates(ctx)
}

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
	result, processed, err := service.admitDueHeldStates(ctx, at, limit, deadline)
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

func (service *Service) admitDueHeldStates(
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
		result, processed, err := service.repository.AdmitDueHeldStates(admissionContext, snapshot, at, limit)
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

// StopAdmission rejects new Runs with [ErrAdmissionUnavailable] and lets admitted
// Runs finish their current Command.
func (service *Service) StopAdmission() {
	service.admission.CloseAdmission()
}

// AdmissionOpen reports whether new Runs are allowed; executor faults close admission.
func (service *Service) AdmissionOpen() bool {
	return service.admission.AdmissionOpen()
}

// Drain closes admission and joins admitted Runs without canceling Commands. A
// context error stops waiting, not the Runs.
func (service *Service) Drain(ctx context.Context) error {
	service.StopAdmission()
	return service.admission.Wait(ctx)
}

// InterruptActiveRuns marks running Runs and Steps interrupted on restart.
func (service *Service) InterruptActiveRuns(ctx context.Context, at time.Time) error {
	if err := service.repository.InterruptActiveRuns(ctx, at, FailureCoreRestarted); err != nil {
		return err
	}
	service.dependencies.Logger.WarnContext(
		ctx,
		"automation runs interrupted",
		slog.String("event", "automation.run_interrupted"),
		slog.String("reason", FailureCoreRestarted),
	)
	return nil
}

// latchExecutorFault closes admission until restart when Run progress cannot be persisted.
func (service *Service) latchExecutorFault(ctx context.Context, runID RunID, position int) {
	service.admission.CloseAdmission()
	service.dependencies.Logger.ErrorContext(
		ctx,
		"automation executor fault latched until restart",
		slog.String("event", "automation.executor_fault"),
		slog.String("run_id", string(runID)),
		slog.Int("step_position", position),
		slog.String("error_code", FailureExecutorFault),
	)
}

// CreateAutomation validates every current reference and persists one new definition.
func (service *Service) CreateAutomation(
	ctx context.Context,
	definition Definition,
) (Record, error) {
	validated, err := ValidateDefinition(ctx, service.devices, definition)
	if err != nil {
		return Record{}, err
	}
	record, err := service.repository.CreateAutomation(ctx, validated)
	if err != nil {
		return Record{}, err
	}
	service.logDefinition(ctx, "automation.created", record)
	return record, nil
}

// GetAutomation returns one current definition or ErrAutomationNotFound.
func (service *Service) GetAutomation(ctx context.Context, id AutomationID) (Record, error) {
	return service.repository.GetAutomation(ctx, id)
}

// ListAutomations returns one ID-ascending keyset page of current definitions.
func (service *Service) ListAutomations(
	ctx context.Context,
	params ListAutomationsParams,
) (Page[Record], error) {
	return service.repository.ListAutomations(ctx, params)
}

// ReplaceAutomation validates every current reference and replaces one definition
// when the expected revision is still current.
func (service *Service) ReplaceAutomation(
	ctx context.Context,
	id AutomationID,
	expectedRevision int64,
	definition Definition,
) (Record, error) {
	validated, err := ValidateDefinition(ctx, service.devices, definition)
	if err != nil {
		return Record{}, err
	}
	record, err := service.repository.ReplaceAutomation(ctx, id, expectedRevision, validated)
	if err != nil {
		return Record{}, err
	}
	service.logDefinition(ctx, "automation.replaced", record)
	return record, nil
}

// DeleteAutomation hard-deletes one definition under the expected revision.
func (service *Service) DeleteAutomation(ctx context.Context, id AutomationID, expectedRevision int64) error {
	if err := service.repository.DeleteAutomation(ctx, id, expectedRevision); err != nil {
		return err
	}
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation definition deleted",
		slog.String("event", "automation.deleted"),
		slog.String("automation_id", string(id)),
		slog.Int64("revision", expectedRevision),
	)
	return nil
}

// logDefinition records one definition mutation with identity and revision only.
func (service *Service) logDefinition(ctx context.Context, event string, record Record) {
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation definition changed",
		slog.String("event", event),
		slog.String("automation_id", string(record.ID)),
		slog.Int64("revision", record.Revision),
		slog.Time("updated_at", record.UpdatedAt),
	)
}
