package automations

import (
	"context"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// Service manages automation definitions, Run admission, and execution.
type Service struct {
	repository   Repository
	devices      AutomationDevices
	dependencies Dependencies

	// admission tracks both admission transactions and Run workers for Drain.
	admission *lifecycle.AdmissionGroup
}

// NewService assembles the automation service from its persistence seam, the
// devices-facing seam, and process-owned collaborators.
func NewService(
	repository Repository,
	automationDevices AutomationDevices,
	dependencies Dependencies,
) *Service {
	return &Service{
		repository:   repository,
		devices:      automationDevices,
		dependencies: dependencies.WithDefaults(),
		admission:    lifecycle.NewAdmissionGroup(),
	}
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
