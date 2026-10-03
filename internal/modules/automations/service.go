package automations

import (
	"context"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// AdmissionTimeout bounds one automatic or manual admission, including its
// definition pre-read, Condition State snapshot read, and repository transaction.
const AdmissionTimeout = 2 * time.Second

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

// ResetPendingHeldStates discards pre-restart elapsed time while preserving hold cursors.
func (service *Service) ResetPendingHeldStates(ctx context.Context) error {
	return service.repository.ResetPendingHeldStates(ctx)
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

// latchRunExecutorFault reports Run/control-flow faults without a command position.
func (service *Service) latchRunExecutorFault(ctx context.Context, runID RunID, stepID StepID) {
	service.admission.CloseAdmission()
	service.dependencies.Logger.ErrorContext(ctx, "automation executor fault latched until restart",
		slog.String("event", "automation.executor_fault"),
		slog.String("run_id", string(runID)),
		slog.String("step_id", string(stepID)),
		slog.String("error_code", FailureExecutorFault))
}
