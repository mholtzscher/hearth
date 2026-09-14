package automations

import (
	"context"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// Service manages automation definitions, Run admission, and execution.
type Service struct {
	repository   AutomationRepository
	devices      AutomationDevices
	dependencies AutomationDependencies

	// admission tracks both admission transactions and Run workers for WaitRuns.
	admission *lifecycle.AdmissionGroup
}

// NewService assembles the automation service from its persistence seam, the
// devices-facing seam, and process-owned collaborators. Zero-valued dependency
// fields fall back to production defaults.
func NewService(
	repository AutomationRepository,
	automationDevices AutomationDevices,
	dependencies AutomationDependencies,
) *Service {
	return &Service{
		repository:   repository,
		devices:      automationDevices,
		dependencies: dependencies.withDefaults(),
		admission:    lifecycle.NewAdmissionGroup(),
	}
}

// StopAdmission rejects new Runs with [ErrAdmissionUnavailable]. Admitted Runs
// finish their current Command and drain. It is idempotent and does not wait.
func (service *Service) StopAdmission() {
	service.admission.CloseAdmission()
}

// AdmissionOpen reports whether new Runs are allowed; executor faults close admission.
func (service *Service) AdmissionOpen() bool {
	return service.admission.AdmissionOpen()
}

// WaitRuns joins in-flight admissions and Run workers without canceling Commands.
// Close admission first and keep shared dependencies alive until it returns.
func (service *Service) WaitRuns(ctx context.Context) error {
	return service.admission.Wait(ctx)
}

// InterruptActiveRuns marks running Runs and Steps interrupted on restart.
// Call before opening transports; it never replays Commands or infers success.
func (service *Service) InterruptActiveRuns(ctx context.Context, at time.Time) error {
	if err := service.repository.InterruptActiveRuns(ctx, at, AutomationFailureCoreRestarted); err != nil {
		return err
	}
	service.dependencies.Logger.WarnContext(
		ctx,
		"automation runs interrupted",
		slog.String("event", "automation.run_interrupted"),
		slog.String("reason", AutomationFailureCoreRestarted),
	)
	return nil
}

// latchExecutorFault closes admission until restart when Run progress cannot be
// verified or persisted.
func (service *Service) latchExecutorFault(ctx context.Context, runID AutomationRunID, position int) {
	service.admission.CloseAdmission()
	service.dependencies.Logger.ErrorContext(
		ctx,
		"automation executor fault latched until restart",
		slog.String("event", "automation.executor_fault"),
		slog.String("run_id", string(runID)),
		slog.Int("step_position", position),
		slog.String("error_code", AutomationFailureExecutorFault),
	)
}

// CreateAutomation validates every current reference and persists one new
// definition at revision 1.
func (service *Service) CreateAutomation(
	ctx context.Context,
	definition AutomationDefinition,
) (AutomationRecord, error) {
	validated, err := ValidateAutomationDefinition(ctx, service.devices, definition)
	if err != nil {
		return AutomationRecord{}, err
	}
	record, err := service.repository.CreateAutomation(ctx, validated)
	if err != nil {
		return AutomationRecord{}, err
	}
	service.logDefinition(ctx, "automation.created", record)
	return record, nil
}

// GetAutomation returns one current definition or ErrAutomationNotFound.
func (service *Service) GetAutomation(ctx context.Context, id AutomationID) (AutomationRecord, error) {
	return service.repository.GetAutomation(ctx, id)
}

// ListAutomations returns one ID-ascending keyset page of current definitions.
func (service *Service) ListAutomations(
	ctx context.Context,
	params ListAutomationsParams,
) (AutomationPage[AutomationRecord], error) {
	return service.repository.ListAutomations(ctx, params)
}

// ReplaceAutomation validates every current reference and atomically replaces
// one definition when the expected revision is still current.
func (service *Service) ReplaceAutomation(
	ctx context.Context,
	id AutomationID,
	expectedRevision int64,
	definition AutomationDefinition,
) (AutomationRecord, error) {
	validated, err := ValidateAutomationDefinition(ctx, service.devices, definition)
	if err != nil {
		return AutomationRecord{}, err
	}
	record, err := service.repository.ReplaceAutomation(ctx, id, expectedRevision, validated)
	if err != nil {
		return AutomationRecord{}, err
	}
	service.logDefinition(ctx, "automation.replaced", record)
	return record, nil
}

// DeleteAutomation hard-deletes one definition under the expected revision. An
// active Run continues from its snapshot and retained history stays queryable.
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

// logDefinition records one definition mutation using only safe structured
// attributes: identity and revision, never definition JSON or names.
func (service *Service) logDefinition(ctx context.Context, event string, record AutomationRecord) {
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation definition changed",
		slog.String("event", event),
		slog.String("automation_id", string(record.ID)),
		slog.Int64("revision", record.Revision),
		slog.Time("updated_at", record.UpdatedAt),
	)
}
