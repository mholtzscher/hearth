package automations

import (
	"context"
	"errors"
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
	admission       *lifecycle.AdmissionGroup
	executionStop   context.Context
	cancelExecution context.CancelCauseFunc

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
	executionStop, cancelExecution := context.WithCancelCause(context.Background())
	startupAt := dependencies.HeldStateStartupAt
	if startupAt.IsZero() {
		startupAt = dependencies.Now()
	}
	service := &Service{
		repository:         repository,
		devices:            automationDevices,
		dependencies:       dependencies,
		admission:          lifecycle.NewAdmissionGroup(),
		executionStop:      executionStop,
		cancelExecution:    cancelExecution,
		heldStateStartupAt: startupAt.UTC(),
	}
	return service
}

// StopAdmission rejects new Runs with [ErrAdmissionUnavailable], wakes pending
// delays, and lets admitted Runs finish their current Command.
func (service *Service) StopAdmission() {
	service.stopExecution(FailureCoreStopping)
}

// stopExecution broadcasts the first stop cause without canceling device Commands.
func (service *Service) stopExecution(reason string) {
	service.cancelExecution(errors.New(reason))
	service.admission.CloseAdmission()
}

func (service *Service) executionStopReason() string {
	if cause := context.Cause(service.executionStop); cause != nil {
		return cause.Error()
	}
	return FailureCoreStopping
}

// AdmissionOpen reports whether lifecycle gates allow new Runs.
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

// interruptStepExecutorFault stops only this Run when progress cannot be established.
func (service *Service) interruptStepExecutorFault(ctx context.Context, runID RunID, position int) {
	service.interruptFaultedRun(ctx, runID, slog.Int("step_position", position))
}

// interruptRunExecutorFault reports Run/control-flow faults without a command position.
func (service *Service) interruptRunExecutorFault(ctx context.Context, runID RunID, stepID StepID) {
	service.interruptFaultedRun(ctx, runID, slog.String("step_id", string(stepID)))
}

// interruptFaultedRun makes one bounded, atomic cleanup attempt. Failure leaves
// this Automation busy until startup recovery, not the module unavailable.
// Callers stop advancing the Run; cleanup never replays external work.
func (service *Service) interruptFaultedRun(ctx context.Context, runID RunID, location slog.Attr) {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.InterruptRun(writeContext, runID, service.dependencies.Now().UTC())
	cancel()
	service.dependencies.Logger.ErrorContext(ctx, "automation run executor fault",
		slog.String("event", "automation.executor_fault"),
		slog.String("run_id", string(runID)), location,
		slog.String("error_code", FailureExecutorFault))
	if err != nil {
		// Database errors can contain payloads; log only stable diagnostic tokens.
		service.dependencies.Logger.ErrorContext(ctx, "automation run interruption failed; startup recovery required",
			slog.String("event", "automation.run_interruption_failed"),
			slog.String("run_id", string(runID)),
			slog.String("error_code", FailureExecutorFault))
	}
}
