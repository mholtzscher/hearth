package automations

import (
	"context"
	"log/slog"
	"time"
)

// StopAdmission atomically closes automation admission. New manual and
// fact-triggered Runs are refused with [ErrAdmissionUnavailable]; already
// admitted Runs finish their current Command and drain. It is idempotent and
// never blocks.
func (service *Service) StopAdmission() {
	service.admission.CloseAdmission()
}

// AdmissionOpen reports whether new Runs may be admitted, including executor
// fault state. Readiness also checks device Command admission.
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

// latchExecutorFault closes admission until restart when execution cannot verify
// or persist progress. The closed gate is the only latched state.
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
